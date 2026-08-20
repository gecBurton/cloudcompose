package gcp

import (
	"encoding/json"
	"github.com/gecburton/cloudcompose/internal/compiler/shared"
	"testing"

	"github.com/gecburton/cloudcompose/internal/models"
)

// gcpTestEnv returns a minimal GcpEnvironment for tests, mirroring the
// smallest valid GcpEnvironment(name=..., project_id=...) construction
// (region/log_retention_days/retain_data_on_destroy all take their
// documented defaults).
func gcpTestEnv() models.GcpEnvironment {
	env := models.NewGcpEnvironment()
	env.Name = "prod"
	env.ProjectID = "my-project-123"
	return env
}

// TestInferGcp_RealExamplesProduceValidJSON runs the real
// parse->normalize->infer->generate pipeline against several actual
// compose files and checks the output parses as JSON with a resource
// block -- a lighter bar than AWS/Azure's byte-identical golden
// comparisons, deliberately: GCP has no golden examples and essentially
// no dedicated test suite to check against in the first place, so this
// is a smoke test, not a coverage claim.
func TestInferGcp_RealExamplesProduceValidJSON(t *testing.T) {
	examples := []string{"hello", "doctor", "flask-redis", "minio-s3", "production-stack", "nginx-flask-mysql"}
	for _, name := range examples {
		name := name
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			composeApp, err := shared.ParseCompose("../../../../examples/" + name + "/compose.yml")
			if err != nil {
				t.Fatalf("ParseCompose failed: %v", err)
			}
			app, err := shared.Normalize(composeApp, name)
			if err != nil {
				t.Fatalf("Normalize failed: %v", err)
			}
			env := gcpTestEnv()

			resources := InferGcp(app, &env)
			out, err := GenerateGcp(resources, &env, "app")
			if err != nil {
				t.Fatalf("GenerateGcp failed: %v", err)
			}

			var parsed map[string]any
			if err := json.Unmarshal([]byte(out), &parsed); err != nil {
				t.Fatalf("output is not valid JSON: %v\n%s", err, out)
			}
			if _, ok := parsed["resource"]; !ok {
				t.Errorf("expected a 'resource' key, got %v", parsed)
			}
			if _, ok := parsed["provider"].(map[string]any)["google"]; !ok {
				t.Errorf("expected provider.google, got %v", parsed["provider"])
			}
		})
	}
}

// TestInferGcp_DatabaseCreatesSharedCloudSqlInstance mirrors the one real
// structural decision worth pinning directly: unlike AWS/Azure (one
// managed server per service or per engine), GCP's inference creates
// exactly one shared Cloud SQL instance for every database-capability
// service in the app.
func TestInferGcp_DatabaseCreatesSharedCloudSqlInstance(t *testing.T) {
	t.Parallel()
	dbName1 := "db1"
	dbName2 := "db2"
	app := &models.Application{
		Name: "app",
		Services: []models.Service{
			{Name: "db1", Image: "postgres:16", Capability: models.CapabilityDatabase, DatabaseName: &dbName1},
			{Name: "db2", Image: "postgres:16", Capability: models.CapabilityDatabase, DatabaseName: &dbName2},
		},
	}
	env := gcpTestEnv()
	resources := InferGcp(app, &env)

	if len(resources.SqlDatabaseInstance) != 1 {
		t.Fatalf("expected exactly 1 shared Cloud SQL instance, got %d", len(resources.SqlDatabaseInstance))
	}
	if len(resources.SqlDatabase) != 2 {
		t.Fatalf("expected 2 databases inside the shared instance, got %d", len(resources.SqlDatabase))
	}
	for _, db := range resources.SqlDatabase {
		if db.Instance != "${google_sql_database_instance.main.name}" {
			t.Errorf("db.Instance = %q, want it to reference the shared instance", db.Instance)
		}
	}
}

// TestInferGcp_VpcConnectorOnlyCreatedForDatabases mirrors
// _infer_vpc_connector's one condition: a VPC connector is only created
// when a database-capability service exists, not for caches/storage.
func TestInferGcp_VpcConnectorOnlyCreatedForDatabases(t *testing.T) {
	t.Parallel()
	app := &models.Application{
		Name: "app",
		Services: []models.Service{
			{Name: "cache", Capability: models.CapabilityCache},
		},
	}
	env := gcpTestEnv()
	resources := InferGcp(app, &env)
	if len(resources.VpcAccessConnector) != 0 {
		t.Errorf("did not expect a VPC connector without a database, got %v", resources.VpcAccessConnector)
	}
}

func TestInferGcp_VpcConnectorCreatedForDatabase(t *testing.T) {
	t.Parallel()
	dbName := "db"
	app := &models.Application{
		Name: "app",
		Services: []models.Service{
			{Name: "db", Image: "postgres:16", Capability: models.CapabilityDatabase, DatabaseName: &dbName},
		},
	}
	env := gcpTestEnv()
	resources := InferGcp(app, &env)
	if _, ok := resources.VpcAccessConnector["main"]; !ok {
		t.Errorf("expected a VPC connector for a database service, got %v", resources.VpcAccessConnector)
	}
}

// TestCpuLimitGcp_SizeMapping and TestMemoryLimitGcp_SizeMapping pin the
// size-to-limit conversion, now derived from shared.SizeMappings (the
// same table AWS/Azure use) rather than a separately hardcoded table --
// see cpuLimitGcp's own doc comment for why (a real, already-drifted
// duplicate: this package's previous table gave "medium" a genuinely
// different CPU:memory ratio than AWS/Azure's "medium").
// shared.SizeMappings: small=256/512, medium=1024/2048, large=4096/8192
// (ECS CPU units/MB) -- CPU converts to millicores at *1000/1024,
// memory passes straight through as Mi.
func TestCpuLimitGcp_SizeMapping(t *testing.T) {
	t.Parallel()
	cases := []struct {
		size models.ServiceSize
		want string
	}{
		{models.ServiceSizeSmall, "250m"},
		{models.ServiceSizeMedium, "1000m"},
		{models.ServiceSizeLarge, "4000m"},
		{"", "250m"},
	}
	for _, tc := range cases {
		service := &models.Service{Size: tc.size}
		if got := cpuLimitGcp(service); got != tc.want {
			t.Errorf("cpuLimitGcp(size=%q) = %q, want %q", tc.size, got, tc.want)
		}
	}
}

func TestMemoryLimitGcp_SizeMapping(t *testing.T) {
	t.Parallel()
	cases := []struct {
		size models.ServiceSize
		want string
	}{
		{models.ServiceSizeSmall, "512Mi"},
		{models.ServiceSizeMedium, "2048Mi"},
		{models.ServiceSizeLarge, "8192Mi"},
		{"", "512Mi"},
	}
	for _, tc := range cases {
		service := &models.Service{Size: tc.size}
		if got := memoryLimitGcp(service); got != tc.want {
			t.Errorf("memoryLimitGcp(size=%q) = %q, want %q", tc.size, got, tc.want)
		}
	}
}

// TestGenerateGcp_AlwaysWiresDockerAndRandomProviders mirrors a real
// structural difference from AWS/Azure worth pinning: GCP's generator
// unconditionally includes the docker and random providers, regardless
// of whether anything actually builds an image or generates a password
// (unlike AWS/Azure, which wire the docker provider in conditionally).
func TestGenerateGcp_AlwaysWiresDockerAndRandomProviders(t *testing.T) {
	t.Parallel()
	resources := models.NewGcpResources()
	env := gcpTestEnv()

	out, err := GenerateGcp(resources, &env, "app")
	if err != nil {
		t.Fatalf("GenerateGcp failed: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}
	provider := parsed["provider"].(map[string]any)
	if _, ok := provider["docker"]; !ok {
		t.Errorf("expected docker provider even with zero resources")
	}
	if _, ok := provider["random"]; !ok {
		t.Errorf("expected random provider even with zero resources")
	}
}

// TestGenerateGcp_Deterministic runs the same input 6 times and diffs the
// output, per this package's own review discipline elsewhere.
func TestGenerateGcp_Deterministic(t *testing.T) {
	t.Parallel()
	composeApp, err := shared.ParseCompose("../../../../examples/doctor/compose.yml")
	if err != nil {
		t.Fatalf("ParseCompose failed: %v", err)
	}
	app, err := shared.Normalize(composeApp, "doctor")
	if err != nil {
		t.Fatalf("Normalize failed: %v", err)
	}
	env := gcpTestEnv()

	var first string
	for i := 0; i < 6; i++ {
		resources := InferGcp(app, &env)
		out, err := GenerateGcp(resources, &env, "app")
		if err != nil {
			t.Fatalf("GenerateGcp run %d failed: %v", i, err)
		}
		if i == 0 {
			first = out
			continue
		}
		if out != first {
			t.Fatalf("run %d differs from run 0", i)
		}
	}
}

// TestGcp_MatchesExpectedStructure checks the hello example's generated
// structure field-by-field, rather than pinning an exact byte string:
// Go's encoding/json now sorts map keys alphabetically at every level
// (matching AWS/Azure's own generators), so a literal ordered-string
// pin is no longer meaningful. This checks the same information the old
// pinned string encoded, structurally instead.
func TestGcp_MatchesExpectedStructure(t *testing.T) {
	t.Parallel()
	composeApp, err := shared.ParseCompose("../../../../examples/hello/compose.yml")
	if err != nil {
		t.Fatalf("ParseCompose failed: %v", err)
	}
	app, err := shared.Normalize(composeApp, "hello")
	if err != nil {
		t.Fatalf("Normalize failed: %v", err)
	}
	env := gcpTestEnv()

	resources := InferGcp(app, &env)
	out, err := GenerateGcp(resources, &env, "app")
	if err != nil {
		t.Fatalf("GenerateGcp failed: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}

	cloudRun := parsed["resource"].(map[string]any)["google_cloud_run_service"].(map[string]any)["web"].(map[string]any)
	if cloudRun["name"] != "prod-hello-web" {
		t.Errorf("name = %v, want prod-hello-web", cloudRun["name"])
	}
	if cloudRun["ingress"] != "all" {
		t.Errorf("ingress = %v, want all", cloudRun["ingress"])
	}

	template := cloudRun["template"].(map[string]any)
	spec := template["spec"].(map[string]any)
	containers := spec["containers"].([]any)
	if len(containers) != 1 {
		t.Fatalf("expected 1 container, got %d", len(containers))
	}
	container := containers[0].(map[string]any)
	if container["image"] != "nginxdemos/hello:plain-text" {
		t.Errorf("image = %v, want nginxdemos/hello:plain-text", container["image"])
	}
	limits := container["resources"].(map[string]any)["limits"].(map[string]any)
	if limits["cpu"] != "250m" || limits["memory"] != "512Mi" {
		t.Errorf("limits = %v, want cpu=250m memory=512Mi", limits)
	}

	annotations := template["metadata"].(map[string]any)["annotations"].(map[string]any)
	if annotations["autoscaling.knative.dev/minScale"] != "1" {
		t.Errorf("minScale annotation = %v, want 1", annotations["autoscaling.knative.dev/minScale"])
	}

	traffic := cloudRun["traffic"].([]any)
	if len(traffic) != 1 {
		t.Fatalf("expected 1 traffic entry, got %d", len(traffic))
	}
	trafficEntry := traffic[0].(map[string]any)
	if trafficEntry["percent"] != float64(100) || trafficEntry["latest_revision"] != true {
		t.Errorf("traffic entry = %v, want percent=100 latest_revision=true", trafficEntry)
	}

	provider := parsed["provider"].(map[string]any)
	if _, ok := provider["docker"]; !ok {
		t.Errorf("expected docker provider (always wired, unconditionally)")
	}
}

// TestGenerateGcp_NilEnvBackendOmitsBackendBlock mirrors
// aws.TestGenerateAWS_NilEnvBackendOmitsBackendBlock for the GCP
// app-level generator. See docs/multi-user-state.md.
func TestGenerateGcp_NilEnvBackendOmitsBackendBlock(t *testing.T) {
	t.Parallel()
	resources := models.NewGcpResources()
	env := gcpTestEnv()

	out, err := GenerateGcp(resources, &env, "checkout-api")
	if err != nil {
		t.Fatalf("GenerateGcp failed: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}
	if _, ok := parsed["terraform"].(map[string]any)["backend"]; ok {
		t.Errorf("did not expect terraform.backend when env.Backend is nil")
	}
}

// TestGenerateGcp_EnvBackendProducesAppSpecificBackendBlock mirrors
// aws.TestGenerateAWS_EnvBackendProducesAppSpecificBackendBlock for the
// GCP app-level generator.
func TestGenerateGcp_EnvBackendProducesAppSpecificBackendBlock(t *testing.T) {
	t.Parallel()
	resources := models.NewGcpResources()
	env := gcpTestEnv()
	env.Backend = &models.BackendConfig{Gcp: &models.GcpBackendConfig{Bucket: "my-org-tfstate"}}

	out, err := GenerateGcp(resources, &env, "checkout-api")
	if err != nil {
		t.Fatalf("GenerateGcp failed: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}
	gcsBackend, ok := parsed["terraform"].(map[string]any)["backend"].(map[string]any)["gcs"].(map[string]any)
	if !ok {
		t.Fatalf("expected terraform.backend.gcs, got %v", parsed["terraform"])
	}
	wantPrefix := "cloudcompose/prod/apps/checkout-api.tfstate"
	if gcsBackend["prefix"] != wantPrefix {
		t.Errorf("prefix = %v, want %v", gcsBackend["prefix"], wantPrefix)
	}
	if gcsBackend["bucket"] != "my-org-tfstate" {
		t.Errorf("bucket = %v, want my-org-tfstate", gcsBackend["bucket"])
	}
}
