package azure

import (
	"fmt"
	"strings"

	"github.com/gecburton/cloudcompose/internal/compiler/shared"
	"github.com/gecburton/cloudcompose/internal/models"
)

// privateNetworkingArgs holds the private-networking configuration fields
// that map onto PostgreSQLFlexibleServer/MySQLFlexibleServer fields.
type privateNetworkingArgs struct {
	DelegatedSubnetID          *string
	PrivateDnsZoneID           *string
	PublicNetworkAccessEnabled bool
	DependsOn                  []string
}

// privateNetworkingAzure computes the arguments placing a Flexible Server
// on the environment's private network.
//
// Azure will not create a server on a delegated subnet without a private
// DNS zone (EmptyPrivateDnsZoneArmResourceId), and the zone has to be
// linked to the VNet before the server exists. Environments predating
// those subnets have no delegated subnet to use, so the server falls back
// to public network access rather than failing to compile.
func privateNetworkingAzure(
	resources *models.AzureResources,
	env *models.AzureEnvironment,
	app *models.Application,
	engine string,
	subnetID *string,
	tags map[string]string,
) privateNetworkingArgs {
	if subnetID == nil || *subnetID == "" {
		return privateNetworkingArgs{PublicNetworkAccessEnabled: true}
	}

	zoneKey := engine
	suffix := "mysql"
	if engine == "postgresql" {
		suffix = "postgres"
	}
	zoneName := fmt.Sprintf("%s-%s.%s.database.azure.com", app.Name, engine, suffix)

	resources.PrivateDnsZone[zoneKey] = models.PrivateDnsZone{
		Name:              zoneName,
		ResourceGroupName: env.Name,
		Tags:              tags,
	}
	resources.PrivateDnsZoneVirtualNetworkLink[zoneKey] = models.PrivateDnsZoneVirtualNetworkLink{
		Name:               fmt.Sprintf("%s-%s-link", app.Name, engine),
		ResourceGroupName:  env.Name,
		PrivateDnsZoneName: fmt.Sprintf("${azurerm_private_dns_zone.%s.name}", zoneKey),
		VirtualNetworkID:   env.VnetID,
		Tags:               tags,
	}

	privateDnsZoneID := fmt.Sprintf("${azurerm_private_dns_zone.%s.id}", zoneKey)
	return privateNetworkingArgs{
		DelegatedSubnetID:          subnetID,
		PrivateDnsZoneID:           &privateDnsZoneID,
		PublicNetworkAccessEnabled: false,
		DependsOn:                  []string{fmt.Sprintf("azurerm_private_dns_zone_virtual_network_link.%s", zoneKey)},
	}
}

// redisPrivateLinkSubresource and redisPrivateDnsZoneName are Azure
// Managed Redis's fixed private-link identifiers.
const (
	redisPrivateLinkSubresource = "redisEnterprise"
	redisPrivateDnsZoneName     = "privatelink.redis.azure.net"
)

// privateEndpointRedisAzure attaches a private endpoint to redis (and
// wires the corresponding private DNS zone/link + sets
// public_network_access to "Disabled") when env.RedisSubnetID is set.
//
// azurerm_managed_redis has no networking attributes beyond
// public_network_access, so private connectivity is a separate
// azurerm_private_endpoint resource attached to a plain subnet, unlike
// Flexible Server which takes delegated_subnet_id/private_dns_zone_id
// directly.
//
// Environments with no RedisSubnetID fall back to public network
// access rather than a hard error.
func privateEndpointRedisAzure(
	resources *models.AzureResources,
	env *models.AzureEnvironment,
	app *models.Application,
	service *models.Service,
	cacheKey string,
	redis *models.ManagedRedis,
	getName func(string) string,
	tags map[string]string,
) {
	if env.RedisSubnetID == nil || *env.RedisSubnetID == "" {
		return
	}

	zoneKey := "redis"
	if _, exists := resources.PrivateDnsZone[zoneKey]; !exists {
		resources.PrivateDnsZone[zoneKey] = models.PrivateDnsZone{
			Name:              redisPrivateDnsZoneName,
			ResourceGroupName: env.Name,
			Tags:              tags,
		}
		resources.PrivateDnsZoneVirtualNetworkLink[zoneKey] = models.PrivateDnsZoneVirtualNetworkLink{
			Name:               fmt.Sprintf("%s-redis-link", app.Name),
			ResourceGroupName:  env.Name,
			PrivateDnsZoneName: fmt.Sprintf("${azurerm_private_dns_zone.%s.name}", zoneKey),
			VirtualNetworkID:   env.VnetID,
			Tags:               tags,
		}
	}

	peKey := service.Name + "_redis_pe"
	resources.PrivateEndpoint[peKey] = models.PrivateEndpoint{
		Name:              getName(service.Name + "-redis-pe"),
		ResourceGroupName: env.Name,
		Location:          env.Region,
		SubnetID:          *env.RedisSubnetID,
		PrivateServiceConnection: []models.PrivateServiceConnection{{
			Name:                        getName(service.Name + "-redis-psc"),
			IsManualConnection:          false,
			PrivateConnectionResourceID: fmt.Sprintf("${azurerm_managed_redis.%s.id}", cacheKey),
			SubresourceNames:            []string{redisPrivateLinkSubresource},
		}},
		PrivateDnsZoneGroup: &models.PrivateEndpointDnsZoneGroup{
			Name:              "default",
			PrivateDnsZoneIDs: []string{fmt.Sprintf("${azurerm_private_dns_zone.%s.id}", zoneKey)},
		},
		Tags: tags,
	}

	disabled := "Disabled"
	redis.PublicNetworkAccess = &disabled
}

// isMySQLImage reports whether image should be provisioned onto a MySQL
// Flexible Server rather than PostgreSQL: "mysql" or "mariadb" in the
// image name (and not "postgres") selects MySQL; everything else
// defaults to PostgreSQL. Azure has no dedicated MariaDB product, so a
// MariaDB image is provisioned onto the MySQL Flexible Server.
func isMySQLImage(image string) bool {
	lower := strings.ToLower(image)
	if strings.Contains(lower, "postgres") {
		return false
	}
	return strings.Contains(lower, "mysql") || strings.Contains(lower, "mariadb")
}

// azureDBSkuFor maps a service size to a PostgreSQL/MySQL Flexible
// Server SKU name. B_* (Burstable) is the cheapest tier; GP_* (General
// Purpose) has a dedicated vCPU allocation, used for medium/large.
func azureDBSkuFor(size models.ServiceSize) string {
	switch size {
	case models.ServiceSizeMedium:
		return "GP_Standard_D2s_v3"
	case models.ServiceSizeLarge:
		return "GP_Standard_D4s_v3"
	default:
		return "B_Standard_B1ms"
	}
}

// highAvailabilityAzure returns the high_availability block for a
// Flexible Server, or nil when disabled. Always maps to
// "ZoneRedundant" (not "SameZone"), matching AWS Multi-AZ's guarantee
// of placing the standby in a different Availability Zone.
// standby_availability_zone is left unset; Azure auto-assigns it.
func highAvailabilityAzure(env *models.AzureEnvironment) map[string]string {
	if !env.HighAvailabilityEnabled {
		return nil
	}
	return map[string]string{"mode": "ZoneRedundant"}
}

// largestServiceSize returns the largest ServiceSize declared among
// services, defaulting to small if none is set. Used when multiple
// services share one Flexible Server: the shared server is sized for
// its largest consumer.
func largestServiceSize(services []*models.Service) models.ServiceSize {
	rank := map[models.ServiceSize]int{
		models.ServiceSizeSmall:  0,
		models.ServiceSizeMedium: 1,
		models.ServiceSizeLarge:  2,
	}
	largest := models.ServiceSizeSmall
	for _, s := range services {
		if rank[s.Size] > rank[largest] {
			largest = s.Size
		}
	}
	return largest
}

// inferDatabasesAzure infers PostgreSQL and MySQL Flexible Server
// databases. Returns a mapping of service name to Connection for use in
// wiring.
func inferDatabasesAzure(
	resources *models.AzureResources,
	app *models.Application,
	env *models.AzureEnvironment,
	getName func(string) string,
	tags map[string]string,
) map[string]models.Connection {
	connections := map[string]models.Connection{}

	var pgServices, mysqlServices []*models.Service
	for i := range app.Services {
		s := &app.Services[i]
		if s.Capability != models.CapabilityDatabase {
			continue
		}
		if isMySQLImage(s.Image) {
			mysqlServices = append(mysqlServices, s)
		} else {
			pgServices = append(pgServices, s)
		}
	}

	// Create PostgreSQL server if needed.
	if len(pgServices) > 0 && (env.PostgresqlServerID == nil || *env.PostgresqlServerID == "") {
		serverName := getName("pg")
		adminPasswordKey := "postgres_admin"
		resources.RandomPassword[adminPasswordKey] = models.RandomPassword{Length: 20, Special: false}

		passwordRef := fmt.Sprintf("${random_password.%s.result}", adminPasswordKey)
		server := models.NewPostgreSQLFlexibleServer()
		server.Name = serverName
		server.ResourceGroupName = env.Name
		server.Location = env.Region
		server.AdministratorLogin = shared.DatabaseDefaultUsername
		server.AdministratorPassword = passwordRef
		server.SkuName = azureDBSkuFor(largestServiceSize(pgServices))
		server.BackupRetentionDays = env.BackupRetentionDays
		server.HighAvailability = highAvailabilityAzure(env)
		server.Tags = tags

		networking := privateNetworkingAzure(resources, env, app, "postgresql", env.PostgresqlSubnetID, tags)
		server.DelegatedSubnetID = networking.DelegatedSubnetID
		server.PrivateDnsZoneID = networking.PrivateDnsZoneID
		server.PublicNetworkAccessEnabled = networking.PublicNetworkAccessEnabled
		server.DependsOn = networking.DependsOn

		resources.PostgreSQLFlexibleServer["main"] = server

		// Log export is on by default: `cloudcompose logs` has nothing
		// to query for a database whose logs were never exported.
		// Postgres logs its own error/notice output by default, so a
		// diagnostic setting alone is enough (unlike MySQL/MariaDB,
		// which additionally needs server parameters turned on first).
		resources.DiagnosticSetting["pg_diag"] = models.DiagnosticSetting{
			Name:                    getName("pg-diag"),
			TargetResourceID:        "${azurerm_postgresql_flexible_server.main.id}",
			LogAnalyticsWorkspaceID: env.LogAnalyticsWorkspaceID,
			EnabledLog: []models.DiagnosticSettingEnabledLog{
				{Category: "PostgreSQLLogs"},
			},
		}

		for _, service := range pgServices {
			dbName := service.Name
			if service.DatabaseName != nil && *service.DatabaseName != "" {
				dbName = *service.DatabaseName
			}

			dbKey := service.Name + "_db"
			db := models.NewPostgreSQLFlexibleDatabase()
			db.Name = dbName
			db.ServerID = "${azurerm_postgresql_flexible_server.main.id}"
			resources.PostgreSQLFlexibleServerDatabase[dbKey] = db

			port := shared.DefaultPortPostgres
			username := shared.DatabaseDefaultUsername
			connections[service.Name] = models.Connection{
				Host:     "${azurerm_postgresql_flexible_server.main.fqdn}",
				Port:     &port,
				Username: &username,
				Password: &passwordRef,
				Database: &dbName,
			}
		}
	}

	// Create MySQL server if needed.
	if len(mysqlServices) > 0 {
		serverName := getName("mysql")
		adminPasswordKey := "mysql_admin"
		resources.RandomPassword[adminPasswordKey] = models.RandomPassword{Length: 20, Special: false}

		passwordRef := fmt.Sprintf("${random_password.%s.result}", adminPasswordKey)
		server := models.NewMySQLFlexibleServer()
		server.Name = serverName
		server.ResourceGroupName = env.Name
		server.Location = env.Region
		server.AdministratorLogin = shared.DatabaseDefaultUsername
		server.AdministratorPassword = passwordRef
		server.SkuName = azureDBSkuFor(largestServiceSize(mysqlServices))
		server.BackupRetentionDays = env.BackupRetentionDays
		server.HighAvailability = highAvailabilityAzure(env)
		server.Tags = tags

		networking := privateNetworkingAzure(resources, env, app, "mysql", env.MysqlSubnetID, tags)
		server.DelegatedSubnetID = networking.DelegatedSubnetID
		server.PrivateDnsZoneID = networking.PrivateDnsZoneID
		// Unlike PostgreSQLFlexibleServer's bool field, MySQL's
		// public_network_access is a string, and only settable when NOT
		// VNet-integrated. Only set when public access is requested.
		if networking.PublicNetworkAccessEnabled {
			enabled := "Enabled"
			server.PublicNetworkAccess = &enabled
		}
		server.DependsOn = networking.DependsOn

		resources.MySQLFlexibleServer["main"] = server

		for _, service := range mysqlServices {
			dbName := service.Name
			if service.DatabaseName != nil && *service.DatabaseName != "" {
				dbName = *service.DatabaseName
			}

			dbKey := service.Name + "_db"
			db := models.NewMySQLFlexibleDatabase()
			db.Name = dbName
			db.ResourceGroupName = env.Name
			db.ServerName = "${azurerm_mysql_flexible_server.main.name}"
			resources.MySQLFlexibleDatabase[dbKey] = db

			port := shared.DefaultPortMySQL
			username := shared.DatabaseDefaultUsername
			connections[service.Name] = models.Connection{
				Host:     "${azurerm_mysql_flexible_server.main.fqdn}",
				Port:     &port,
				Username: &username,
				Password: &passwordRef,
				Database: &dbName,
			}
		}
	}

	return connections
}

// azureRedisSkuFor maps a service size to a Managed Redis SKU. Balanced
// tier is the general-purpose Managed Redis family; B0 is the smallest,
// and cheaper than the Basic C0 it replaces.
func azureRedisSkuFor(size models.ServiceSize) string {
	switch size {
	case models.ServiceSizeMedium:
		return "Balanced_B1"
	case models.ServiceSizeLarge:
		return "Balanced_B3"
	default:
		return "Balanced_B0"
	}
}

// inferCachesAzure infers Azure Managed Redis instances. Returns a
// mapping of service name to Connection for use in wiring.
func inferCachesAzure(
	resources *models.AzureResources,
	app *models.Application,
	env *models.AzureEnvironment,
	getName func(string) string,
	tags map[string]string,
) map[string]models.Connection {
	connections := map[string]models.Connection{}

	for i := range app.Services {
		service := &app.Services[i]
		if service.Capability != models.CapabilityCache {
			continue
		}

		cacheKey := service.Name + "_redis"
		redis := models.ManagedRedis{
			Name:                    getName(service.Name),
			ResourceGroupName:       env.Name,
			Location:                env.Region,
			SkuName:                 azureRedisSkuFor(service.Size),
			HighAvailabilityEnabled: false,
			DefaultDatabase: []map[string]any{
				{
					"access_keys_authentication_enabled": true,
					"client_protocol":                    "Encrypted",
					"eviction_policy":                    "AllKeysLRU",
				},
			},
			Tags: tags,
		}

		privateEndpointRedisAzure(resources, env, app, service, cacheKey, &redis, getName, tags)

		resources.ManagedRedis[cacheKey] = redis

		// The access key hangs off the nested database, not the cluster.
		db := fmt.Sprintf("azurerm_managed_redis.%s.default_database[0]", cacheKey)
		port := shared.DefaultPortAzureManagedRedis
		passwordRef := fmt.Sprintf("${%s.primary_access_key}", db)
		connections[service.Name] = models.Connection{
			Host:     fmt.Sprintf("${azurerm_managed_redis.%s.hostname}", cacheKey),
			Port:     &port,
			Password: &passwordRef,
		}
	}

	return connections
}

// inferStorageAzure infers Azure Blob Storage accounts and containers.
// Returns a mapping of service name to Connection for use in wiring.
func inferStorageAzure(
	resources *models.AzureResources,
	app *models.Application,
	env *models.AzureEnvironment,
	getName func(string) string,
	tags map[string]string,
) map[string]models.Connection {
	connections := map[string]models.Connection{}

	for i := range app.Services {
		service := &app.Services[i]
		if service.Capability != models.CapabilityObjectStorage {
			continue
		}

		accountKey := service.Name + "_storage"
		accountName := StorageAccountName(env.Name, app.Name, service.Name)

		account := models.NewStorageAccount()
		account.Name = accountName
		account.ResourceGroupName = env.Name
		account.Location = env.Region
		account.Tags = tags
		resources.StorageAccount[accountKey] = account

		containerKey := service.Name + "_container"
		container := models.NewStorageContainer()
		container.Name = service.Name
		container.StorageAccountName = fmt.Sprintf("${azurerm_storage_account.%s.name}", accountKey)
		resources.StorageContainer[containerKey] = container

		name := fmt.Sprintf("${azurerm_storage_account.%s.name}", accountKey)
		connections[service.Name] = models.Connection{
			Host:        fmt.Sprintf("${azurerm_storage_account.%s.primary_blob_endpoint}", accountKey),
			Name:        &name,
			AddressedBy: "name",
		}
	}

	return connections
}
