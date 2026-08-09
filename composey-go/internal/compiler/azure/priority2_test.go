package azure

import (
	"testing"

	"github.com/gecburton/composey/internal/models"
)

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
	env := azureTestEnv()
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
	env := azureTestEnv()
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

func TestGrantServiceSecretPermissions_CreatesKeyVaultSecretAndRoleAssignment(t *testing.T) {
	t.Parallel()
	service := &models.Service{Name: "web", Secrets: []string{"db-password"}}
	app := &models.Application{Name: "myapp", Services: []models.Service{*service}}
	resources := models.NewAzureResources()
	identityID := "${azurerm_user_assigned_identity.main.id}"
	resources.UserAssignedIdentity["main"] = models.UserAssignedIdentity{Name: "prod-app-identity"}

	envVars, secrets := grantServiceSecretPermissions(resources, service, app, minimalGetName("prod", "myapp"), nil, identityID)

	if len(envVars) != 1 {
		t.Fatalf("expected 1 env var, got %d", len(envVars))
	}
	if envVars[0].Name != "DB_PASSWORD" {
		t.Errorf("env var name = %q, want DB_PASSWORD", envVars[0].Name)
	}
	if envVars[0].SecretName == "" {
		t.Errorf("expected a SecretName, got none")
	}
	if len(secrets) != 1 {
		t.Fatalf("expected 1 container secret, got %d", len(secrets))
	}
	if secrets[0].Identity != identityID {
		t.Errorf("secret identity = %q, want %q", secrets[0].Identity, identityID)
	}

	if len(resources.KeyVaultSecret) != 1 {
		t.Fatalf("expected 1 KeyVaultSecret resource, got %d", len(resources.KeyVaultSecret))
	}
	for _, secret := range resources.KeyVaultSecret {
		if secret.Value != secretsPlaceholderValueAzure {
			t.Errorf("secret value = %q, want the placeholder (never the real value)", secret.Value)
		}
	}

	if len(resources.RoleAssignment) != 1 {
		t.Fatalf("expected 1 RoleAssignment, got %d", len(resources.RoleAssignment))
	}
	for _, ra := range resources.RoleAssignment {
		if ra.RoleDefinitionName != keyVaultSecretsUserRole {
			t.Errorf("role = %q, want %q", ra.RoleDefinitionName, keyVaultSecretsUserRole)
		}
	}
}

func TestGrantServiceSecretPermissions_NoSecretsIsNoop(t *testing.T) {
	t.Parallel()
	service := &models.Service{Name: "web"}
	app := &models.Application{Name: "myapp", Services: []models.Service{*service}}
	resources := models.NewAzureResources()

	envVars, secrets := grantServiceSecretPermissions(resources, service, app, minimalGetName("prod", "myapp"), nil, "${azurerm_user_assigned_identity.main.id}")
	if envVars != nil || secrets != nil {
		t.Errorf("expected no env vars or secrets for a service with no compose secrets, got %v, %v", envVars, secrets)
	}
}

func TestGrantPlatformConfigPermissions_CreatesKeyVaultSecretPerKey(t *testing.T) {
	t.Parallel()
	service := &models.Service{Name: "web", Config: []string{"API_TOKEN", "SENTRY_DSN"}}
	resources := models.NewAzureResources()
	identityID := "${azurerm_user_assigned_identity.main.id}"

	envVars, secrets := grantPlatformConfigPermissions(resources, service, minimalGetName("prod", "myapp"), nil, identityID)

	if len(envVars) != 2 {
		t.Fatalf("expected 2 env vars, got %d", len(envVars))
	}
	gotNames := map[string]bool{envVars[0].Name: true, envVars[1].Name: true}
	if !gotNames["API_TOKEN"] || !gotNames["SENTRY_DSN"] {
		t.Errorf("expected env var names API_TOKEN and SENTRY_DSN, got %v", gotNames)
	}
	if len(secrets) != 2 {
		t.Fatalf("expected 2 container secrets, got %d", len(secrets))
	}
	if len(resources.KeyVaultSecret) != 2 {
		t.Fatalf("expected 2 KeyVaultSecret resources (one per config key), got %d", len(resources.KeyVaultSecret))
	}
	// Only one Key Vault role assignment, even though two secrets were
	// created -- see grantKeyVaultAccessOnce's own doc comment for why a
	// second identical grant would be an Azure-rejected duplicate, not
	// just redundant.
	if len(resources.RoleAssignment) != 1 {
		t.Errorf("expected exactly 1 RoleAssignment (deduplicated), got %d", len(resources.RoleAssignment))
	}
}

func TestGrantPlatformConfigPermissions_NoConfigIsNoop(t *testing.T) {
	t.Parallel()
	service := &models.Service{Name: "web"}
	resources := models.NewAzureResources()

	envVars, secrets := grantPlatformConfigPermissions(resources, service, minimalGetName("prod", "myapp"), nil, "${azurerm_user_assigned_identity.main.id}")
	if envVars != nil || secrets != nil {
		t.Errorf("expected no env vars or secrets for a service with no platform config, got %v, %v", envVars, secrets)
	}
}

func TestGrantManagedServicePermissions_DoesNotDuplicateKeyVaultRoleAssignment(t *testing.T) {
	t.Parallel()
	// Regression test for a real bug found while implementing Priority 2
	// (2026-08-08): an app with both a database and a cache relationship
	// used to get two RoleAssignment resources granting the identical
	// (principal_id, role_definition_name, scope) triple, which Azure's
	// ARM API rejects as a duplicate. Confirmed via the doctor/
	// production-stack golden fixtures, which had exactly this before
	// the fix.
	app := &models.Application{
		Name: "myapp",
		Relationships: []models.Relationship{
			{Client: "web", Server: "db"},
			{Client: "web", Server: "cache"},
		},
	}
	env := azureTestEnv()
	resources := models.NewAzureResources()
	dbPassword := "dbpass"
	cachePassword := "cachepass"
	connections := map[string]models.Connection{
		"db":    {Host: "db.example.com", Password: &dbPassword},
		"cache": {Host: "cache.example.com", Password: &cachePassword},
	}

	grantManagedServicePermissions(resources, app, &env, minimalGetName("prod", "myapp"), nil, "${azurerm_user_assigned_identity.main.id}", connections)

	kvRoleAssignments := 0
	for _, ra := range resources.RoleAssignment {
		if ra.RoleDefinitionName == keyVaultSecretsUserRole {
			kvRoleAssignments++
		}
	}
	if kvRoleAssignments != 1 {
		t.Errorf("expected exactly 1 Key Vault Secrets User RoleAssignment for 2 credential-bearing connections sharing 1 Key Vault, got %d", kvRoleAssignments)
	}
	if len(resources.KeyVaultSecret) != 2 {
		t.Errorf("expected 2 KeyVaultSecret resources (one per connection), got %d", len(resources.KeyVaultSecret))
	}
}

func TestInferContainerApps_DefaultAutoScalingWhenMaxScaleAboveOne(t *testing.T) {
	t.Parallel()
	app := &models.Application{
		Name: "myapp",
		Services: []models.Service{
			{Name: "web", Capability: models.CapabilityContainer, MinScale: 1, MaxScale: 3},
		},
	}
	env := azureTestEnv()
	resources := models.NewAzureResources()

	inferContainerApps(resources, app, &env, minimalGetName("prod", "myapp"), nil, "", "", nil, nil)

	containerApp, ok := resources.ContainerApp["web"]
	if !ok {
		t.Fatalf("expected a Container App for web")
	}
	rules := containerApp.Template.CustomScaleRule
	if len(rules) != 2 {
		t.Fatalf("expected 2 custom scale rules (cpu, memory), got %d: %+v", len(rules), rules)
	}
	byType := map[string]models.ContainerAppCustomScaleRule{}
	for _, r := range rules {
		byType[r.CustomRuleType] = r
	}
	cpu, ok := byType["cpu"]
	if !ok {
		t.Fatalf("expected a cpu scale rule, got %+v", rules)
	}
	if cpu.Metadata["type"] != "Utilization" || cpu.Metadata["value"] != "70" {
		t.Errorf("cpu rule metadata = %v, want type=Utilization value=70", cpu.Metadata)
	}
	mem, ok := byType["memory"]
	if !ok {
		t.Fatalf("expected a memory scale rule, got %+v", rules)
	}
	if mem.Metadata["type"] != "Utilization" || mem.Metadata["value"] != "80" {
		t.Errorf("memory rule metadata = %v, want type=Utilization value=80", mem.Metadata)
	}
}

func TestInferContainerApps_NoDefaultAutoScalingWhenMaxScaleIsOne(t *testing.T) {
	t.Parallel()
	app := &models.Application{
		Name: "myapp",
		Services: []models.Service{
			{Name: "web", Capability: models.CapabilityContainer, MinScale: 1, MaxScale: 1},
		},
	}
	env := azureTestEnv()
	resources := models.NewAzureResources()

	inferContainerApps(resources, app, &env, minimalGetName("prod", "myapp"), nil, "", "", nil, nil)

	containerApp := resources.ContainerApp["web"]
	if len(containerApp.Template.CustomScaleRule) != 0 {
		t.Errorf("expected no custom scale rules when max_scale=1, got %+v", containerApp.Template.CustomScaleRule)
	}
}

func TestInferContainerApps_ExplicitAutoScalingOverridesDefault(t *testing.T) {
	t.Parallel()
	app := &models.Application{
		Name: "myapp",
		Services: []models.Service{
			{
				Name: "web", Capability: models.CapabilityContainer, MinScale: 1, MaxScale: 3,
				AutoScaling: &models.AutoScalingConfig{
					Metrics: []models.AutoScalingMetric{
						{Type: models.AutoScalingMetricCPU, TargetValue: 50},
					},
				},
			},
		},
	}
	env := azureTestEnv()
	resources := models.NewAzureResources()

	inferContainerApps(resources, app, &env, minimalGetName("prod", "myapp"), nil, "", "", nil, nil)

	containerApp := resources.ContainerApp["web"]
	rules := containerApp.Template.CustomScaleRule
	if len(rules) != 1 {
		t.Fatalf("expected exactly the explicitly-declared cpu rule (no memory default added), got %d: %+v", len(rules), rules)
	}
	if rules[0].Metadata["value"] != "50" {
		t.Errorf("expected the explicit target value (50) to be used, not the default (70), got %v", rules[0].Metadata)
	}
}

func TestInferContainerApps_RequestsPerTargetMetricStillUsesHttpScaleRule(t *testing.T) {
	t.Parallel()
	app := &models.Application{
		Name: "myapp",
		Services: []models.Service{
			{
				Name: "web", Capability: models.CapabilityContainer, MinScale: 1, MaxScale: 3,
				AutoScaling: &models.AutoScalingConfig{
					Metrics: []models.AutoScalingMetric{
						{Type: models.AutoScalingMetricRequestsPerTarget, TargetValue: 200},
					},
				},
			},
		},
	}
	env := azureTestEnv()
	resources := models.NewAzureResources()

	inferContainerApps(resources, app, &env, minimalGetName("prod", "myapp"), nil, "", "", nil, nil)

	containerApp := resources.ContainerApp["web"]
	if len(containerApp.Template.CustomScaleRule) != 0 {
		t.Errorf("requests_per_target should not produce a custom_scale_rule, got %+v", containerApp.Template.CustomScaleRule)
	}
	if len(containerApp.Template.HTTPScaleRule) != 1 || containerApp.Template.HTTPScaleRule[0].ConcurrentRequests != "200" {
		t.Errorf("expected 1 http_scale_rule with concurrent_requests=200, got %+v", containerApp.Template.HTTPScaleRule)
	}
}

// Tests for docs/azure-aws-parity-todo.md's Priority 3 Redis private
// networking item (added 2026-08-08).

func TestInferCachesAzure_NoPrivateEndpointWithoutRedisSubnetID(t *testing.T) {
	t.Parallel()
	app := &models.Application{
		Name: "myapp",
		Services: []models.Service{
			{Name: "cache", Capability: models.CapabilityCache, Size: models.ServiceSizeSmall},
		},
	}
	env := azureTestEnv() // RedisSubnetID left unset
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
	env := azureTestEnv()
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
	env := azureTestEnv()
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
// item (added 2026-08-08).

func TestGetCPUCoresAzure_RejectsSizeAboveConsumptionCap(t *testing.T) {
	t.Parallel()
	// "large" now derives from shared.SizeMappings (4096 CPU units =
	// 4.0 vCPU), which exceeds Container Apps' Consumption tier limit
	// of 2 vCPU per container -- this is exactly what the "scaling"
	// example's web service hits, which is why it was removed from
	// azureGoldenExamples rather than golden-tested (there's no valid
	// Azure output to compare against; the correct behavior is a
	// rejection, not a value).
	service := &models.Service{Name: "web", Size: models.ServiceSizeLarge}
	_, err := getCPUCoresAzure(service)
	if err == nil {
		t.Fatalf("expected an error for size: large (4 vCPU > 2 vCPU cap)")
	}
}

func TestGetCPUCoresAzure_RejectsExplicitCPUAboveConsumptionCap(t *testing.T) {
	t.Parallel()
	cpu := 3072 // 3.0 vCPU
	service := &models.Service{Name: "web", CPU: &cpu}
	_, err := getCPUCoresAzure(service)
	if err == nil {
		t.Fatalf("expected an error for an explicit cpu: override above the 2 vCPU cap")
	}
}

func TestGetCPUCoresAzure_AllowsSizesWithinCap(t *testing.T) {
	t.Parallel()
	for _, size := range []models.ServiceSize{models.ServiceSizeSmall, models.ServiceSizeMedium} {
		service := &models.Service{Name: "web", Size: size}
		cores, err := getCPUCoresAzure(service)
		if err != nil {
			t.Errorf("size %q should be within the cap, got error: %v", size, err)
		}
		if cores <= 0 {
			t.Errorf("size %q returned non-positive cores: %v", size, cores)
		}
	}
}

func TestGetMemoryGBAzure_RejectsSizeAboveConsumptionCap(t *testing.T) {
	t.Parallel()
	service := &models.Service{Name: "web", Size: models.ServiceSizeLarge}
	_, err := getMemoryGBAzure(service)
	if err == nil {
		t.Fatalf("expected an error for size: large (8Gi > 4Gi cap)")
	}
}

func TestGetMemoryGBAzure_RejectsExplicitMemoryAboveConsumptionCap(t *testing.T) {
	t.Parallel()
	mem := 5120 // 5Gi
	service := &models.Service{Name: "web", Memory: &mem}
	_, err := getMemoryGBAzure(service)
	if err == nil {
		t.Fatalf("expected an error for an explicit memory: override above the 4Gi cap")
	}
}

func TestGetCPUCoresAzure_MediumMatchesAwsSizeMappings(t *testing.T) {
	t.Parallel()
	// Regression test for the real, already-drifted duplicate found
	// while consolidating the size table (2026-08-08): Azure's own
	// table previously defined medium as 0.5 vCPU where AWS's medium
	// (shared.SizeMappings) is 1.0 vCPU.
	service := &models.Service{Name: "web", Size: models.ServiceSizeMedium}
	cores, err := getCPUCoresAzure(service)
	if err != nil {
		t.Fatalf("getCPUCoresAzure failed: %v", err)
	}
	if cores != 1.0 {
		t.Errorf("medium CPU = %v, want 1.0 (matching shared.SizeMappings[\"medium\"].CPU / 1024)", cores)
	}
}
