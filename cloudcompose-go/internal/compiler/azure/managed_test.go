package azure

import (
	"testing"

	"github.com/gecburton/cloudcompose/internal/models"
)

// --- MySQL: entirely untested before this pass (isMySQLImage's true
// branch and the MySQL Flexible Server code path in azure_managed.go had
// zero coverage, unit or golden -- every "mysql"-named example actually
// uses mariadb images, which isMySQLImage classifies as Postgres).

func TestInferDatabasesAzure_MySQLImageCreatesFlexibleServer(t *testing.T) {
	t.Parallel()
	dbName := "mydb"
	app := &models.Application{
		Name: "myapp",
		Services: []models.Service{
			{Name: "db", Image: "mysql:8.0", Capability: models.CapabilityDatabase, Size: models.ServiceSizeSmall, DatabaseName: &dbName},
		},
	}
	env := testAppEnv(0)
	resources := models.NewAzureResources()
	inferDatabasesAzure(resources, app, &env, minimalGetName("prod", "myapp"), nil)

	server, ok := resources.MySQLFlexibleServer["main"]
	if !ok {
		t.Fatalf("expected a MySQL Flexible Server, got keys %v", keysOf(resources.MySQLFlexibleServer))
	}
	if server.Version != "8.0.21" {
		t.Errorf("Version = %q, want 8.0.21", server.Version)
	}
	if server.SkuName != "B_Standard_B1ms" {
		t.Errorf("SkuName = %q, want B_Standard_B1ms", server.SkuName)
	}
	if _, ok := resources.PostgreSQLFlexibleServer["main"]; ok {
		t.Errorf("did not expect a PostgreSQL server for a mysql image")
	}
}

func TestInferDatabasesAzure_MySQLDatabaseCreatedWithCharset(t *testing.T) {
	t.Parallel()
	dbName := "mydb"
	app := &models.Application{
		Name: "myapp",
		Services: []models.Service{
			{Name: "db", Image: "mysql:8.0", Capability: models.CapabilityDatabase, Size: models.ServiceSizeSmall, DatabaseName: &dbName},
		},
	}
	env := testAppEnv(0)
	resources := models.NewAzureResources()
	inferDatabasesAzure(resources, app, &env, minimalGetName("prod", "myapp"), nil)

	db, ok := resources.MySQLFlexibleDatabase["db_db"]
	if !ok {
		t.Fatalf("expected a MySQL database, got keys %v", keysOf(resources.MySQLFlexibleDatabase))
	}
	if db.Name != "mydb" {
		t.Errorf("Name = %q, want mydb", db.Name)
	}
	if db.Charset != "utf8mb4" {
		t.Errorf("Charset = %q, want utf8mb4", db.Charset)
	}
	if db.Collation != "utf8mb4_unicode_ci" {
		t.Errorf("Collation = %q, want utf8mb4_unicode_ci", db.Collation)
	}
}

func TestInferDatabasesAzure_PostgresImageCreatesOnlyPostgresServer(t *testing.T) {
	t.Parallel()
	dbName := "mydb"
	app := &models.Application{
		Name: "myapp",
		Services: []models.Service{
			{Name: "db", Image: "postgres:16", Capability: models.CapabilityDatabase, Size: models.ServiceSizeSmall, DatabaseName: &dbName},
		},
	}
	env := testAppEnv(0)
	resources := models.NewAzureResources()
	inferDatabasesAzure(resources, app, &env, minimalGetName("prod", "myapp"), nil)

	if _, ok := resources.PostgreSQLFlexibleServer["main"]; !ok {
		t.Fatalf("expected a PostgreSQL server")
	}
	if _, ok := resources.MySQLFlexibleServer["main"]; ok {
		t.Errorf("did not expect a MySQL server for a postgres image")
	}
}

func TestInferDatabasesAzure_BothEngineTypesCreateBothServers(t *testing.T) {
	t.Parallel()
	pgName := "pgdb"
	myName := "mydb"
	app := &models.Application{
		Name: "myapp",
		Services: []models.Service{
			{Name: "pgdb", Image: "postgres:16", Capability: models.CapabilityDatabase, Size: models.ServiceSizeSmall, DatabaseName: &pgName},
			{Name: "mydb", Image: "mysql:8.0", Capability: models.CapabilityDatabase, Size: models.ServiceSizeSmall, DatabaseName: &myName},
		},
	}
	env := testAppEnv(0)
	resources := models.NewAzureResources()
	inferDatabasesAzure(resources, app, &env, minimalGetName("prod", "myapp"), nil)

	if _, ok := resources.PostgreSQLFlexibleServer["main"]; !ok {
		t.Errorf("expected a PostgreSQL server")
	}
	if _, ok := resources.MySQLFlexibleServer["main"]; !ok {
		t.Errorf("expected a MySQL server")
	}
}

// --- Private networking: entirely untested before this pass
// (mockAzureProdEnv never sets PostgresqlSubnetID/MysqlSubnetID, so
// privateNetworkingAzure's non-fallback branch -- delegated subnet + DNS
// zone + VNet link + depends_on -- had zero coverage).

// --- Private networking: entirely untested before this pass
// (mockAzureProdEnv never sets PostgresqlSubnetID/MysqlSubnetID, so
// privateNetworkingAzure's non-fallback branch -- delegated subnet + DNS
// zone + VNet link + depends_on -- had zero coverage).

func TestInferDatabasesAzure_UsesEngineSpecificSubnetNotInfraSubnet(t *testing.T) {
	t.Parallel()
	dbName := "mydb"
	app := &models.Application{
		Name: "myapp",
		Services: []models.Service{
			{Name: "db", Image: "postgres:16", Capability: models.CapabilityDatabase, Size: models.ServiceSizeSmall, DatabaseName: &dbName},
		},
	}
	env := testAppEnv(0)
	pgSubnet := "/subscriptions/123/subnets/postgres"
	env.PostgresqlSubnetID = &pgSubnet

	resources := models.NewAzureResources()
	inferDatabasesAzure(resources, app, &env, minimalGetName("prod", "myapp"), nil)

	server := resources.PostgreSQLFlexibleServer["main"]
	if server.DelegatedSubnetID == nil || *server.DelegatedSubnetID != pgSubnet {
		t.Errorf("DelegatedSubnetID = %v, want %s (not the infra subnet)", server.DelegatedSubnetID, pgSubnet)
	}
	if server.PublicNetworkAccessEnabled {
		t.Errorf("PublicNetworkAccessEnabled = true, want false when a delegated subnet is set")
	}
}

func TestInferDatabasesAzure_PrivateDnsZoneCreatedAndLinked(t *testing.T) {
	t.Parallel()
	dbName := "mydb"
	app := &models.Application{
		Name: "myapp",
		Services: []models.Service{
			{Name: "db", Image: "postgres:16", Capability: models.CapabilityDatabase, Size: models.ServiceSizeSmall, DatabaseName: &dbName},
		},
	}
	env := testAppEnv(0)
	pgSubnet := "/subscriptions/123/subnets/postgres"
	env.PostgresqlSubnetID = &pgSubnet

	resources := models.NewAzureResources()
	inferDatabasesAzure(resources, app, &env, minimalGetName("prod", "myapp"), nil)

	zone, ok := resources.PrivateDnsZone["postgresql"]
	if !ok {
		t.Fatalf("expected a private DNS zone, got keys %v", keysOf(resources.PrivateDnsZone))
	}
	if zone.Name != "myapp-postgresql.postgres.database.azure.com" {
		t.Errorf("zone.Name = %q, want myapp-postgresql.postgres.database.azure.com", zone.Name)
	}

	link, ok := resources.PrivateDnsZoneVirtualNetworkLink["postgresql"]
	if !ok {
		t.Fatalf("expected a private DNS zone VNet link")
	}
	if link.VirtualNetworkID != env.VnetID {
		t.Errorf("link.VirtualNetworkID = %q, want %q", link.VirtualNetworkID, env.VnetID)
	}

	server := resources.PostgreSQLFlexibleServer["main"]
	if server.PrivateDnsZoneID == nil || *server.PrivateDnsZoneID != "${azurerm_private_dns_zone.postgresql.id}" {
		t.Errorf("server.PrivateDnsZoneID = %v, want ${azurerm_private_dns_zone.postgresql.id}", server.PrivateDnsZoneID)
	}
}

func TestInferDatabasesAzure_ServerDependsOnTheDnsZoneLink(t *testing.T) {
	t.Parallel()
	dbName := "mydb"
	app := &models.Application{
		Name: "myapp",
		Services: []models.Service{
			{Name: "db", Image: "postgres:16", Capability: models.CapabilityDatabase, Size: models.ServiceSizeSmall, DatabaseName: &dbName},
		},
	}
	env := testAppEnv(0)
	pgSubnet := "/subscriptions/123/subnets/postgres"
	env.PostgresqlSubnetID = &pgSubnet

	resources := models.NewAzureResources()
	inferDatabasesAzure(resources, app, &env, minimalGetName("prod", "myapp"), nil)

	server := resources.PostgreSQLFlexibleServer["main"]
	found := false
	for _, dep := range server.DependsOn {
		if dep == "azurerm_private_dns_zone_virtual_network_link.postgresql" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected server.DependsOn to reference the DNS zone VNet link, got %v", server.DependsOn)
	}
}

func TestInferDatabasesAzure_MissingSubnetFallsBackToPublicAccessNoZone(t *testing.T) {
	t.Parallel()
	dbName := "mydb"
	app := &models.Application{
		Name: "myapp",
		Services: []models.Service{
			{Name: "db", Image: "postgres:16", Capability: models.CapabilityDatabase, Size: models.ServiceSizeSmall, DatabaseName: &dbName},
		},
	}
	env := testAppEnv(0) // no PostgresqlSubnetID set

	resources := models.NewAzureResources()
	inferDatabasesAzure(resources, app, &env, minimalGetName("prod", "myapp"), nil)

	server := resources.PostgreSQLFlexibleServer["main"]
	if !server.PublicNetworkAccessEnabled {
		t.Errorf("PublicNetworkAccessEnabled = false, want true when no subnet is configured")
	}
	if server.DelegatedSubnetID != nil {
		t.Errorf("DelegatedSubnetID = %v, want nil", server.DelegatedSubnetID)
	}
	if len(resources.PrivateDnsZone) != 0 {
		t.Errorf("expected no private DNS zone when falling back to public access, got %v", keysOf(resources.PrivateDnsZone))
	}
}

// --- Azure schedule cron conversion: entirely untested before this pass.

// --- Redis size mapping: only Balanced_B0 (default) was ever exercised.

func TestAzureRedisSkuFor_SizeMapping(t *testing.T) {
	t.Parallel()
	cases := []struct {
		size models.ServiceSize
		want string
	}{
		{models.ServiceSizeSmall, "Balanced_B0"},
		{models.ServiceSizeMedium, "Balanced_B1"},
		{models.ServiceSizeLarge, "Balanced_B3"},
		{"", "Balanced_B0"},
	}
	for _, tc := range cases {
		if got := azureRedisSkuFor(tc.size); got != tc.want {
			t.Errorf("azureRedisSkuFor(%q) = %q, want %q", tc.size, got, tc.want)
		}
	}
}

// --- CDN: only ever exercised with exactly one CDN-enabled service.

// Tests for docs/azure-aws-parity-todo.md's Priority 2 items: compose
// secrets:, platform config:, database sizing, MariaDB detection, and
// CPU/Memory autoscaling -- all previously silent no-ops on Azure.

func TestIsMySQLImage_DetectsMariaDB(t *testing.T) {
	t.Parallel()
	cases := []struct {
		image string
		want  bool
	}{
		{"mariadb:10.6.4-focal", true},
		{"mysql:8.0.27", true},
		{"postgres:15", false},
		{"postgres-mysql-compat:1.0", false}, // "postgres" wins
		{"pgvector/pgvector:pg15", false},
	}
	for _, tc := range cases {
		if got := isMySQLImage(tc.image); got != tc.want {
			t.Errorf("isMySQLImage(%q) = %v, want %v", tc.image, got, tc.want)
		}
	}
}

func TestAzureDBSkuFor_MapsSizeToSku(t *testing.T) {
	t.Parallel()
	cases := []struct {
		size models.ServiceSize
		want string
	}{
		{models.ServiceSizeSmall, "B_Standard_B1ms"},
		{models.ServiceSizeMedium, "GP_Standard_D2s_v3"},
		{models.ServiceSizeLarge, "GP_Standard_D4s_v3"},
	}
	for _, tc := range cases {
		if got := azureDBSkuFor(tc.size); got != tc.want {
			t.Errorf("azureDBSkuFor(%q) = %q, want %q", tc.size, got, tc.want)
		}
	}
}

func TestLargestServiceSize_PicksLargest(t *testing.T) {
	t.Parallel()
	services := []*models.Service{
		{Name: "a", Size: models.ServiceSizeSmall},
		{Name: "b", Size: models.ServiceSizeLarge},
		{Name: "c", Size: models.ServiceSizeMedium},
	}
	if got := largestServiceSize(services); got != models.ServiceSizeLarge {
		t.Errorf("largestServiceSize = %q, want large", got)
	}
}

func TestLargestServiceSize_DefaultsToSmall(t *testing.T) {
	t.Parallel()
	if got := largestServiceSize(nil); got != models.ServiceSizeSmall {
		t.Errorf("largestServiceSize(nil) = %q, want small", got)
	}
}

func TestInferDatabasesAzure_SizeFlowsIntoSku(t *testing.T) {
	t.Parallel()
	dbName := "mydb"
	app := &models.Application{
		Name: "myapp",
		Services: []models.Service{
			{Name: "db", Image: "postgres:15", Capability: models.CapabilityDatabase, Size: models.ServiceSizeLarge, DatabaseName: &dbName},
		},
	}
	env := testAppEnv(0)
	resources := models.NewAzureResources()
	inferDatabasesAzure(resources, app, &env, minimalGetName("prod", "myapp"), nil)

	server, ok := resources.PostgreSQLFlexibleServer["main"]
	if !ok {
		t.Fatalf("expected a PostgreSQL Flexible Server")
	}
	if server.SkuName != "GP_Standard_D4s_v3" {
		t.Errorf("SkuName = %q, want GP_Standard_D4s_v3 (large)", server.SkuName)
	}
}

func TestInferDatabasesAzure_SharedServerSizedForLargestConsumer(t *testing.T) {
	t.Parallel()
	db1, db2 := "db1", "db2"
	app := &models.Application{
		Name: "myapp",
		Services: []models.Service{
			{Name: "svc1", Image: "postgres:15", Capability: models.CapabilityDatabase, Size: models.ServiceSizeSmall, DatabaseName: &db1},
			{Name: "svc2", Image: "postgres:15", Capability: models.CapabilityDatabase, Size: models.ServiceSizeLarge, DatabaseName: &db2},
		},
	}
	env := testAppEnv(0)
	resources := models.NewAzureResources()
	inferDatabasesAzure(resources, app, &env, minimalGetName("prod", "myapp"), nil)

	server, ok := resources.PostgreSQLFlexibleServer["main"]
	if !ok {
		t.Fatalf("expected a PostgreSQL Flexible Server")
	}
	if server.SkuName != "GP_Standard_D4s_v3" {
		t.Errorf("SkuName = %q, want GP_Standard_D4s_v3 (sized for the larger of the two services sharing this server)", server.SkuName)
	}
}

// Tests for docs/azure-aws-parity-todo.md's Priority 3 Redis private
// networking item.

func TestInferCachesAzure_NoPrivateEndpointWithoutRedisSubnetID(t *testing.T) {
	t.Parallel()
	app := &models.Application{
		Name: "myapp",
		Services: []models.Service{
			{Name: "cache", Capability: models.CapabilityCache, Size: models.ServiceSizeSmall},
		},
	}
	env := testAppEnv(0) // RedisSubnetID left unset
	resources := models.NewAzureResources()

	inferCachesAzure(resources, app, &env, minimalGetName("prod", "myapp"), nil)

	redis, ok := resources.ManagedRedis["cache_redis"]
	if !ok {
		t.Fatalf("expected a ManagedRedis resource")
	}
	if redis.PublicNetworkAccess != nil {
		t.Errorf("expected PublicNetworkAccess to be nil (public access, the provider default) when no subnet is configured, got %v", *redis.PublicNetworkAccess)
	}
	if len(resources.PrivateEndpoint) != 0 {
		t.Errorf("expected no PrivateEndpoint resources without env.RedisSubnetID, got %d", len(resources.PrivateEndpoint))
	}
}

func TestInferCachesAzure_CreatesPrivateEndpointWithRedisSubnetID(t *testing.T) {
	t.Parallel()
	app := &models.Application{
		Name: "myapp",
		Services: []models.Service{
			{Name: "cache", Capability: models.CapabilityCache, Size: models.ServiceSizeSmall},
		},
	}
	env := testAppEnv(0)
	subnetID := "/subscriptions/123/subnets/redis"
	env.RedisSubnetID = &subnetID
	resources := models.NewAzureResources()

	inferCachesAzure(resources, app, &env, minimalGetName("prod", "myapp"), nil)

	redis, ok := resources.ManagedRedis["cache_redis"]
	if !ok {
		t.Fatalf("expected a ManagedRedis resource")
	}
	if redis.PublicNetworkAccess == nil || *redis.PublicNetworkAccess != "Disabled" {
		t.Errorf("expected PublicNetworkAccess = Disabled once a private endpoint exists, got %v", redis.PublicNetworkAccess)
	}

	pe, ok := resources.PrivateEndpoint["cache_redis_pe"]
	if !ok {
		t.Fatalf("expected a PrivateEndpoint resource, got keys %v", keysOf(resources.PrivateEndpoint))
	}
	if pe.SubnetID != subnetID {
		t.Errorf("SubnetID = %q, want %q", pe.SubnetID, subnetID)
	}
	if len(pe.PrivateServiceConnection) != 1 {
		t.Fatalf("expected 1 private_service_connection, got %d", len(pe.PrivateServiceConnection))
	}
	psc := pe.PrivateServiceConnection[0]
	if psc.PrivateConnectionResourceID != "${azurerm_managed_redis.cache_redis.id}" {
		t.Errorf("private_connection_resource_id = %q, want a reference to the managed redis resource", psc.PrivateConnectionResourceID)
	}
	if len(psc.SubresourceNames) != 1 || psc.SubresourceNames[0] != "redisEnterprise" {
		t.Errorf("subresource_names = %v, want [redisEnterprise]", psc.SubresourceNames)
	}

	if _, ok := resources.PrivateDnsZone["redis"]; !ok {
		t.Errorf("expected a private DNS zone for redis")
	}
	if _, ok := resources.PrivateDnsZoneVirtualNetworkLink["redis"]; !ok {
		t.Errorf("expected a private DNS zone VNet link for redis")
	}
}

func TestInferCachesAzure_SharedPrivateDnsZoneForMultipleCaches(t *testing.T) {
	t.Parallel()
	app := &models.Application{
		Name: "myapp",
		Services: []models.Service{
			{Name: "cache1", Capability: models.CapabilityCache, Size: models.ServiceSizeSmall},
			{Name: "cache2", Capability: models.CapabilityCache, Size: models.ServiceSizeSmall},
		},
	}
	env := testAppEnv(0)
	subnetID := "/subscriptions/123/subnets/redis"
	env.RedisSubnetID = &subnetID
	resources := models.NewAzureResources()

	inferCachesAzure(resources, app, &env, minimalGetName("prod", "myapp"), nil)

	if len(resources.PrivateDnsZone) != 1 {
		t.Errorf("expected exactly 1 shared private DNS zone for 2 caches, got %d", len(resources.PrivateDnsZone))
	}
	if len(resources.PrivateEndpoint) != 2 {
		t.Errorf("expected 2 private endpoints (one per cache), got %d", len(resources.PrivateEndpoint))
	}
}

// Tests for docs/azure-aws-parity-todo.md's Priority 4 size-ceiling
// item.

// Tests for docs/azure-aws-parity-todo.md's Priority 4 backup/HA item:
// AzureEnvironment.HighAvailabilityEnabled/BackupRetentionDays wired
// into azurerm_postgresql_flexible_server/azurerm_mysql_flexible_server's
// high_availability/backup_retention_days.

func TestInferDatabasesAzure_HighAvailabilityDisabledByDefault(t *testing.T) {
	t.Parallel()
	app := &models.Application{
		Services: []models.Service{
			{Name: "db", Image: "postgres:16", Capability: models.CapabilityDatabase},
		},
	}
	env := mockAzureProdEnv()
	resources := models.NewAzureResources()
	inferDatabasesAzure(resources, app, &env, testGetNameAzure, nil)

	server := resources.PostgreSQLFlexibleServer["main"]
	if server.HighAvailability != nil {
		t.Errorf("expected HighAvailability = nil by default (HA doubles compute cost, so it's opt-in), got %v", server.HighAvailability)
	}
	if server.BackupRetentionDays != 7 {
		t.Errorf("BackupRetentionDays = %d, want 7 (the default both NewAwsEnvironment and NewAzureEnvironment share)", server.BackupRetentionDays)
	}
}

func TestInferDatabasesAzure_HighAvailabilityEnabledSetsZoneRedundant(t *testing.T) {
	t.Parallel()
	app := &models.Application{
		Services: []models.Service{
			{Name: "db", Image: "postgres:16", Capability: models.CapabilityDatabase},
		},
	}
	env := mockAzureProdEnv()
	env.HighAvailabilityEnabled = true
	env.BackupRetentionDays = 14
	resources := models.NewAzureResources()
	inferDatabasesAzure(resources, app, &env, testGetNameAzure, nil)

	server := resources.PostgreSQLFlexibleServer["main"]
	if server.HighAvailability == nil || server.HighAvailability["mode"] != "ZoneRedundant" {
		t.Errorf(`expected HighAvailability = {"mode": "ZoneRedundant"}, got %v`, server.HighAvailability)
	}
	if server.BackupRetentionDays != 14 {
		t.Errorf("BackupRetentionDays = %d, want 14", server.BackupRetentionDays)
	}
}

func TestInferDatabasesAzure_HighAvailabilityEnabledForMySQL(t *testing.T) {
	t.Parallel()
	app := &models.Application{
		Services: []models.Service{
			{Name: "db", Image: "mysql:8", Capability: models.CapabilityDatabase},
		},
	}
	env := mockAzureProdEnv()
	env.HighAvailabilityEnabled = true
	resources := models.NewAzureResources()
	inferDatabasesAzure(resources, app, &env, testGetNameAzure, nil)

	server := resources.MySQLFlexibleServer["main"]
	if server.HighAvailability == nil || server.HighAvailability["mode"] != "ZoneRedundant" {
		t.Errorf(`expected HighAvailability = {"mode": "ZoneRedundant"} on MySQL Flexible Server too, got %v`, server.HighAvailability)
	}
	if server.BackupRetentionDays != 7 {
		t.Errorf("BackupRetentionDays = %d, want 7", server.BackupRetentionDays)
	}
}

func TestHighAvailabilityAzure_ReturnsNilWhenDisabled(t *testing.T) {
	t.Parallel()
	env := &models.AzureEnvironment{HighAvailabilityEnabled: false}
	if got := highAvailabilityAzure(env); got != nil {
		t.Errorf("highAvailabilityAzure(disabled) = %v, want nil", got)
	}
}

func TestHighAvailabilityAzure_ReturnsZoneRedundantWhenEnabled(t *testing.T) {
	t.Parallel()
	env := &models.AzureEnvironment{HighAvailabilityEnabled: true}
	got := highAvailabilityAzure(env)
	if got == nil || got["mode"] != "ZoneRedundant" {
		t.Errorf(`highAvailabilityAzure(enabled) = %v, want {"mode": "ZoneRedundant"}`, got)
	}
}
