package azure

import (
	"fmt"
	"strings"

	"github.com/gecburton/composey/internal/compiler/shared"
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
// _infer_databases does: "mysql" or "mariadb" in the image name (and not
// "postgres", which would otherwise misclassify a hypothetical
// postgres-based image that happens to mention mysql in passing) means
// the MySQL-compatible Flexible Server family; everything else --
// including postgres, postgresql, pgvector, timescale, etc. -- defaults
// to PostgreSQL.
//
// MariaDB detection added 2026-08-08 (see
// docs/azure-aws-parity-todo.md's Priority 2 item 4): previously only
// checked for "mysql", so a mariadb image was silently misclassified as
// PostgreSQL -- AWS's inferDatabase (aws/managed.go) already detected
// both. Azure has no dedicated MariaDB Flexible Server product, so a
// MariaDB image is still provisioned onto the MySQL Flexible Server
// (the closest wire-compatible managed offering Azure has), the same way
// AWS's own "mariadb" branch still creates an RDS instance with
// engine="mariadb" rather than a distinct product.
func isMySQLImage(image string) bool {
	lower := strings.ToLower(image)
	if strings.Contains(lower, "postgres") {
		return false
	}
	return strings.Contains(lower, "mysql") || strings.Contains(lower, "mariadb")
}

// azureDBSkuFor maps a service size to a PostgreSQL/MySQL Flexible
// Server SKU name, mirroring shared.DBInstanceClasses' AWS equivalent.
// B_* (Burstable) is Azure Flexible Server's cheapest tier, roughly
// comparable to AWS's db.t3.*; GP_* (General Purpose) is the first tier
// with a dedicated (non-burstable) vCPU allocation, used for medium/large
// since a shared database server is exactly the resource most likely to
// be CPU-starved under real load if left on a burstable SKU.
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

// largestServiceSize returns the largest ServiceSize declared among
// services, defaulting to small if none is set. Used when multiple
// services share one Flexible Server (Azure's shared-server-per-engine
// topology, a deliberate design difference from AWS's one-instance-per-
// service -- see docs/azure-aws-parity-todo.md's "explicitly not a gap"
// section): the shared server is sized for its largest consumer, since
// under-provisioning a resource multiple services depend on is a worse
// failure mode than over-provisioning it for the smallest one.
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
		server.AdministratorLogin = shared.DatabaseDefaultUsername
		server.AdministratorPassword = passwordRef
		server.SkuName = azureDBSkuFor(largestServiceSize(pgServices))
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
		server.Tags = tags

		networking := privateNetworkingAzure(resources, env, app, "mysql", env.MysqlSubnetID, tags)
		server.DelegatedSubnetID = networking.DelegatedSubnetID
		server.PrivateDnsZoneID = networking.PrivateDnsZoneID
		// Unlike PostgreSQLFlexibleServer's bool field, MySQL's
		// public_network_access is a string, and only settable at all
		// when NOT VNet-integrated (the provider auto-manages it to
		// "Disabled" whenever delegated_subnet_id+private_dns_zone_id
		// are set -- see MySQLFlexibleServer.PublicNetworkAccess's own
		// doc comment). Only set it when public access is genuinely
		// being requested (no delegated subnet); leave nil otherwise.
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
