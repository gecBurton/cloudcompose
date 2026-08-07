package compiler

import (
	"fmt"
	"strings"

	"github.com/gecburton/composey/internal/models"
)

// privateNetworkingArgs holds the fields _private_networking's Python
// return dict maps onto: PostgreSQLFlexibleServer/MySQLFlexibleServer
// fields, not a free-form dict, since Go structs fix field order already
// (see PostgreSQLFlexibleServer/MySQLFlexibleServer in models/azure.go).
type privateNetworkingArgs struct {
	DelegatedSubnetID          *string
	PrivateDnsZoneID           *string
	PublicNetworkAccessEnabled bool
	DependsOn                  []string
}

// privateNetworkingAzure computes the arguments placing a Flexible Server
// on the environment's private network, mirroring _private_networking.
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

// isMySQLImage classifies a database service's image the same way
// _infer_databases does: "mysql" in the image name (and not "postgres",
// which would otherwise misclassify a hypothetical postgres-based image
// that happens to mention mysql in passing) means MySQL; everything else
// -- including postgres, postgresql, pgvector, timescale, etc. -- defaults
// to PostgreSQL.
func isMySQLImage(image string) bool {
	lower := strings.ToLower(image)
	return strings.Contains(lower, "mysql") && !strings.Contains(lower, "postgres")
}

// inferDatabasesAzure infers PostgreSQL and MySQL Flexible Server
// databases, mirroring _infer_databases. Returns a mapping of service name
// to Connection for use in wiring.
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
		server.AdministratorLogin = DatabaseDefaultUsername
		server.AdministratorPassword = passwordRef
		server.Tags = tags

		networking := privateNetworkingAzure(resources, env, app, "postgresql", env.PostgresqlSubnetID, tags)
		server.DelegatedSubnetID = networking.DelegatedSubnetID
		server.PrivateDnsZoneID = networking.PrivateDnsZoneID
		server.PublicNetworkAccessEnabled = networking.PublicNetworkAccessEnabled
		server.DependsOn = networking.DependsOn

		resources.PostgreSQLFlexibleServer["main"] = server

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

			port := DefaultPortPostgres
			username := DatabaseDefaultUsername
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
		server.AdministratorLogin = DatabaseDefaultUsername
		server.AdministratorPassword = passwordRef
		server.Tags = tags

		networking := privateNetworkingAzure(resources, env, app, "mysql", env.MysqlSubnetID, tags)
		server.DelegatedSubnetID = networking.DelegatedSubnetID
		server.PrivateDnsZoneID = networking.PrivateDnsZoneID
		server.PublicNetworkAccessEnabled = networking.PublicNetworkAccessEnabled
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
			db.ServerID = "${azurerm_mysql_flexible_server.main.id}"
			resources.MySQLFlexibleDatabase[dbKey] = db

			port := DefaultPortMySQL
			username := DatabaseDefaultUsername
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

// azureRedisSkuFor maps a service size to a Managed Redis SKU, mirroring
// _infer_caches' size_sku_map. Balanced tier is the general-purpose
// Managed Redis family; B0 is the smallest, and cheaper than the Basic C0
// it replaces.
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

// inferCachesAzure infers Azure Managed Redis instances, mirroring
// _infer_caches. Returns a mapping of service name to Connection for use
// in wiring.
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
		resources.ManagedRedis[cacheKey] = redis

		// The access key hangs off the nested database, not the cluster.
		// The port does too, but Connection.Port is an int, so the
		// well-known Managed Redis port is named directly rather than
		// interpolated.
		db := fmt.Sprintf("azurerm_managed_redis.%s.default_database[0]", cacheKey)
		port := DefaultPortAzureManagedRedis
		passwordRef := fmt.Sprintf("${%s.primary_access_key}", db)
		connections[service.Name] = models.Connection{
			Host:     fmt.Sprintf("${azurerm_managed_redis.%s.hostname}", cacheKey),
			Port:     &port,
			Password: &passwordRef,
		}
	}

	return connections
}

// inferStorageAzure infers Azure Blob Storage accounts and containers,
// mirroring _infer_storage. Returns a mapping of service name to
// Connection for use in wiring.
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
