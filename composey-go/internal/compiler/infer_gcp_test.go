package compiler

import (
	"encoding/json"
	"testing"

	"github.com/gecburton/composey/internal/models"
)

// gcpTestEnv returns a minimal GcpEnvironment for tests, mirroring the
// smallest valid environment.py GcpEnvironment(name=..., project_id=...)
// construction (region/log_retention_days/retain_data_on_destroy all take
// their Pydantic defaults).
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
// no dedicated Python test suite to check against in the first place (see
// plan.md's Phase 4 GCP section), so this is a smoke test, not a coverage
// claim. Byte-identity against live Python runs for these same examples
// was checked manually during the port (2026-08-06, see the port's
// session notes), but not preserved as a golden-file test the way AWS/
// Azure's were, since no committed golden files exist for GCP to check
// future changes against.
func TestInferGcp_RealExamplesProduceValidJSON(t *testing.T) {
	examples := []string{"hello", "doctor", "flask-redis", "minio-s3", "production-stack", "nginx-flask-mysql"}
	for _, name := range examples {
		name := name
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			composeApp, err := ParseCompose("../../../examples/" + name + "/compose.yml")
			if err != nil {
				t.Fatalf("ParseCompose failed: %v", err)
			}
			app, err := Normalize(composeApp, name)
			if err != nil {
				t.Fatalf("Normalize failed: %v", err)
			}
			env := gcpTestEnv()

			resources := InferGcp(app, &env)
			out, err := GenerateGcp(resources, &env)
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
// size-to-limit conversion tables directly, matching _get_cpu_limit/
// _get_memory_limit.
func TestCpuLimitGcp_SizeMapping(t *testing.T) {
	t.Parallel()
	cases := []struct {
		size models.ServiceSize
		want string
	}{
		{models.ServiceSizeSmall, "1000m"},
		{models.ServiceSizeMedium, "2000m"},
		{models.ServiceSizeLarge, "4000m"},
		{"", "1000m"},
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
		{models.ServiceSizeMedium, "1Gi"},
		{models.ServiceSizeLarge, "2Gi"},
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

	out, err := GenerateGcp(resources, &env)
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
	composeApp, err := ParseCompose("../../../examples/doctor/compose.yml")
	if err != nil {
		t.Fatalf("ParseCompose failed: %v", err)
	}
	app, err := Normalize(composeApp, "doctor")
	if err != nil {
		t.Fatalf("Normalize failed: %v", err)
	}
	env := gcpTestEnv()

	var first string
	for i := 0; i < 6; i++ {
		resources := InferGcp(app, &env)
		out, err := GenerateGcp(resources, &env)
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

// TestGcp_ByteIdenticalAgainstPython pins the exact byte output for the
// hello example against a live run of Python's inference+generate
// (captured 2026-08-06, not re-verified automatically since GCP has no
// Go binary dependency for its own tests -- unlike AWS/Azure, this
// doesn't shell out to Python at test time, it just pins what a
// real comparison found once).
func TestGcp_ByteIdenticalAgainstPython(t *testing.T) {
	t.Parallel()
	composeApp, err := ParseCompose("../../../examples/hello/compose.yml")
	if err != nil {
		t.Fatalf("ParseCompose failed: %v", err)
	}
	app, err := Normalize(composeApp, "hello")
	if err != nil {
		t.Fatalf("Normalize failed: %v", err)
	}
	env := gcpTestEnv()

	resources := InferGcp(app, &env)
	out, err := GenerateGcp(resources, &env)
	if err != nil {
		t.Fatalf("GenerateGcp failed: %v", err)
	}

	want := `{
  "terraform": {
    "required_providers": {
      "google": {
        "source": "hashicorp/google",
        "version": "~> 5.0"
      },
      "docker": {
        "source": "kreuzwerker/docker",
        "version": "~> 3.0"
      },
      "random": {
        "source": "hashicorp/random",
        "version": "~> 3.6"
      }
    }
  },
  "provider": {
    "google": {
      "project": "my-project-123",
      "region": "us-central1"
    },
    "docker": {},
    "random": {}
  },
  "resource": {
    "google_cloud_run_service": {
      "web": {
        "name": "prod-hello-web",
        "location": "us-central1",
        "project_id": "my-project-123",
        "template": {
          "spec": {
            "containers": [
              {
                "image": "nginxdemos/hello:plain-text",
                "resources": {
                  "limits": {
                    "cpu": "1000m",
                    "memory": "512Mi"
                  }
                }
              }
            ],
            "service_account_name": "my-project-123-compute@developer.gserviceaccount.com"
          },
          "metadata": {
            "annotations": {
              "autoscaling.knative.dev/minScale": "1",
              "autoscaling.knative.dev/maxScale": "1",
              "run.googleapis.com/cpu-throttling": "true",
              "run.googleapis.com/execution-environment": "gen2"
            }
          }
        },
        "traffic": [
          {
            "percent": 100,
            "latest_revision": true
          }
        ],
        "autogenerate_revision_name": true,
        "ingress": "all"
      }
    }
  }
}`
	if out != want {
		t.Errorf("got:\n%s\nwant:\n%s", out, want)
	}
}
