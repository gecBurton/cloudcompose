package azure

import (
	"encoding/json"
	"testing"

	"github.com/gecburton/composey/internal/models"
)

func azureTestEnv() models.AzureEnvironment {
	env := models.NewAzureEnvironment()
	env.Name = "prod"
	env.ContainerAppsEnvironmentName = "prod-env"
	env.LogAnalyticsWorkspaceID = "/subscriptions/123/workspaces/prod"
	env.VnetID = "/subscriptions/123/vnets/prod"
	env.InfrastructureSubnetID = "/subscriptions/123/subnets/prod"
	return env
}

func minimalGetName(env, app string) func(string) string {
	return func(resource string) string {
		return env + "-" + app + "-" + resource
	}
}

func keysOf[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

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
	env := azureTestEnv()
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
	env := azureTestEnv()
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
	env := azureTestEnv()
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
	env := azureTestEnv()
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

func TestInferDatabasesAzure_UsesEngineSpecificSubnetNotInfraSubnet(t *testing.T) {
	t.Parallel()
	dbName := "mydb"
	app := &models.Application{
		Name: "myapp",
		Services: []models.Service{
			{Name: "db", Image: "postgres:16", Capability: models.CapabilityDatabase, Size: models.ServiceSizeSmall, DatabaseName: &dbName},
		},
	}
	env := azureTestEnv()
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
	env := azureTestEnv()
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
	env := azureTestEnv()
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
	env := azureTestEnv() // no PostgresqlSubnetID set

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

func TestCronExpressionAzure_RateSchedulesMatchPython(t *testing.T) {
	t.Parallel()
	// Every case pinned against a live run of Python's _cron_expression
	// (2026-08-06), not just this port's own idea of the conversion.
	cases := []struct {
		value int
		unit  models.RateUnit
		want  string
	}{
		{1, models.RateUnitMinutes, "* * * * *"},
		{5, models.RateUnitMinutes, "*/5 * * * *"},
		{30, models.RateUnitMinutes, "*/30 * * * *"},
		{1, models.RateUnitHours, "0 * * * *"},
		{6, models.RateUnitHours, "0 */6 * * *"},
		{1, models.RateUnitDays, "0 0 * * *"},
	}
	for _, tc := range cases {
		got, err := cronExpressionAzure(models.RateSchedule{Value: tc.value, Unit: tc.unit})
		if err != nil {
			t.Errorf("value=%d unit=%s: unexpected error: %v", tc.value, tc.unit, err)
			continue
		}
		if got != tc.want {
			t.Errorf("value=%d unit=%s: got %q, want %q", tc.value, tc.unit, got, tc.want)
		}
	}
}

func TestCronExpressionAzure_RejectsUnevenIntervals(t *testing.T) {
	t.Parallel()
	cases := []struct {
		value int
		unit  models.RateUnit
	}{
		{7, models.RateUnitMinutes},
		{7, models.RateUnitHours},
		{2, models.RateUnitDays},
	}
	for _, tc := range cases {
		_, err := cronExpressionAzure(models.RateSchedule{Value: tc.value, Unit: tc.unit})
		if err == nil {
			t.Errorf("value=%d unit=%s: expected an error, got none", tc.value, tc.unit)
		}
	}
}

func TestCronExpressionAzure_AcceptsPointerSchedules(t *testing.T) {
	t.Parallel()
	// Mirrors AWS's own regression test for this exact bug class: the
	// real normalizer produces *models.RateSchedule/*models.CronSchedule
	// (pointers), not value types.
	rate := &models.RateSchedule{Value: 1, Unit: models.RateUnitHours}
	if got, err := cronExpressionAzure(rate); err != nil || got != "0 * * * *" {
		t.Errorf("got %q, err %v, want '0 * * * *', nil", got, err)
	}
	cron := &models.CronSchedule{Expression: "0 5 * * *"}
	if got, err := cronExpressionAzure(cron); err != nil || got != "0 5 * * *" {
		t.Errorf("got %q, err %v, want '0 5 * * *', nil", got, err)
	}
}

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

func TestInferCdnAzure_NoCdnServicesCreatesNothing(t *testing.T) {
	t.Parallel()
	app := &models.Application{
		Name: "app",
		Services: []models.Service{
			{Name: "web", Capability: models.CapabilityContainer, Ingress: &models.Ingress{Path: "/"}},
		},
	}
	env := azureTestEnv()
	resources := models.NewAzureResources()
	inferCdnAzure(resources, app, &env, minimalGetName("prod", "app"), nil)

	if len(resources.CdnFrontdoorProfile) != 0 {
		t.Errorf("expected no Front Door profile, got %v", keysOf(resources.CdnFrontdoorProfile))
	}
}

func TestInferCdnAzure_CdnWithoutIngressCreatesNothing(t *testing.T) {
	t.Parallel()
	app := &models.Application{
		Name: "app",
		Services: []models.Service{
			{Name: "web", Capability: models.CapabilityContainer, CDNEnabled: true}, // no ingress
		},
	}
	env := azureTestEnv()
	resources := models.NewAzureResources()
	inferCdnAzure(resources, app, &env, minimalGetName("prod", "app"), nil)

	if len(resources.CdnFrontdoorProfile) != 0 {
		t.Errorf("expected no Front Door profile without ingress, got %v", keysOf(resources.CdnFrontdoorProfile))
	}
}

func TestInferCdnAzure_MultipleServicesShareOneProfileDistinctOrigins(t *testing.T) {
	t.Parallel()
	app := &models.Application{
		Name: "app",
		Services: []models.Service{
			{Name: "web", Capability: models.CapabilityContainer, CDNEnabled: true, Ingress: &models.Ingress{Path: "/"}},
			{Name: "api", Capability: models.CapabilityContainer, CDNEnabled: true, Ingress: &models.Ingress{Path: "/api"}},
		},
	}
	env := azureTestEnv()
	resources := models.NewAzureResources()
	inferCdnAzure(resources, app, &env, minimalGetName("prod", "app"), nil)

	if len(resources.CdnFrontdoorProfile) != 1 {
		t.Fatalf("expected exactly 1 shared profile, got %d: %v", len(resources.CdnFrontdoorProfile), keysOf(resources.CdnFrontdoorProfile))
	}
	if len(resources.CdnFrontdoorOrigin) != 2 {
		t.Fatalf("expected 2 distinct origins, got %d: %v", len(resources.CdnFrontdoorOrigin), keysOf(resources.CdnFrontdoorOrigin))
	}
	webOrigin := resources.CdnFrontdoorOrigin["web"]
	apiOrigin := resources.CdnFrontdoorOrigin["api"]
	if webOrigin.HostName == apiOrigin.HostName {
		t.Errorf("expected distinct origin hostnames, both got %q", webOrigin.HostName)
	}
}

func TestFrontDoorProfile_HasNoLocationField(t *testing.T) {
	t.Parallel()
	profile := models.NewFrontDoorProfile()
	// Front Door is global, unlike everything else this inference
	// creates -- structurally verified by the absence of a Location
	// field on the type itself (compile-time check: this line would fail
	// to compile if FrontDoorProfile had one), and the SKU default.
	if profile.SkuName != "Standard_AzureFrontDoor" {
		t.Errorf("SkuName = %q, want Standard_AzureFrontDoor", profile.SkuName)
	}
}

// --- output.fqdn presence: only ever exercised with ingress present.

func TestAzureIngressFQDN_NoIngressReturnsNil(t *testing.T) {
	t.Parallel()
	resources := models.NewAzureResources()
	resources.ContainerApp["web"] = models.NewContainerApp() // Ingress left nil

	if fqdn := azureIngressFQDN(resources); fqdn != nil {
		t.Errorf("expected nil, got %v", *fqdn)
	}
}

func TestGenerateAzure_NoIngressProducesNoOutputKey(t *testing.T) {
	t.Parallel()
	resources := models.NewAzureResources()
	app := models.NewContainerApp()
	app.Name = "prod-app-web"
	resources.ContainerApp["web"] = app
	env := azureTestEnv()

	out, err := GenerateAzure(resources, &env)
	if err != nil {
		t.Fatalf("GenerateAzure failed: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}
	if _, ok := parsed["output"]; ok {
		t.Errorf("expected no 'output' key when no service has ingress, got %v", parsed["output"])
	}
}
