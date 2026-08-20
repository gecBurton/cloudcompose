package aws

import (
	"encoding/json"
	"github.com/gecburton/cloudcompose/internal/compiler/shared"
	"testing"

	"github.com/gecburton/cloudcompose/internal/models"
)

// TestInferPermissionsAndWiring_RealDoctorExample exercises the full
// pipeline (networking, service discovery, managed services, compute,
// permissions) against the real doctor example, which references a
// database, cache, and bucket, including one confidential DATABASE_URL.
func TestInferPermissionsAndWiring_RealDoctorExample(t *testing.T) {
	t.Parallel()
	composeApp, err := shared.ParseCompose("../../../../examples/doctor/compose.yml")
	if err != nil {
		t.Fatalf("ParseCompose failed: %v", err)
	}
	app, err := shared.Normalize(composeApp, "doctor")
	if err != nil {
		t.Fatalf("Normalize failed: %v", err)
	}

	env := fullMockProdEnv()
	resources, err := InferAWS(app, &env)
	if err != nil {
		t.Fatalf("InferAWS failed: %v", err)
	}

	taskDef, ok := resources.EcsTaskDefinition["doctor_td"]
	if !ok {
		t.Fatalf("expected a task definition for doctor")
	}
	var containers []map[string]any
	if err := json.Unmarshal([]byte(taskDef.ContainerDefinitions), &containers); err != nil {
		t.Fatalf("container_definitions not valid JSON: %v", err)
	}
	container := containers[0]

	environment, _ := container["environment"].([]any)
	values := map[string]string{}
	for _, e := range environment {
		entry, _ := e.(map[string]any)
		name, _ := entry["name"].(string)
		value, _ := entry["value"].(string)
		values[name] = value
	}

	if _, ok := values["DATABASE_URL"]; ok {
		t.Errorf("DATABASE_URL carries a master password and must not reach the plain environment, got %v", values)
	}
	if got := values["DB_HOST"]; got != "${aws_db_instance.db_db.address}" {
		t.Errorf("DB_HOST = %q, want the RDS address reference", got)
	}
	if got := values["BUCKET_NAME"]; got != "${aws_s3_bucket.blobs_bucket.id}" {
		t.Errorf("BUCKET_NAME = %q, want the bucket id reference", got)
	}

	secrets, _ := container["secrets"].([]any)
	var databaseURLSecretRef string
	for _, s := range secrets {
		entry, _ := s.(map[string]any)
		if entry["name"] == "DATABASE_URL" {
			databaseURLSecretRef, _ = entry["valueFrom"].(string)
		}
	}
	if databaseURLSecretRef == "" {
		t.Fatalf("expected DATABASE_URL to be delivered as a secret, got secrets %v", secrets)
	}

	// The secret must hold the fully-substituted URL: host, port, and the
	// managed database's own generated credentials -- not what the compose
	// file wrote, which named a container that no longer exists.
	found := false
	for key, version := range resources.SecretsmanagerSecretVersion {
		if key == "doctor_database_url_url_v1" {
			found = true
			if !contains(version.SecretString, "aws_db_instance.db_db.address") {
				t.Errorf("secret_string = %q, want it to reference the RDS address", version.SecretString)
			}
			if !contains(version.SecretString, "random_password.db_password.result") {
				t.Errorf("secret_string = %q, want it to reference the generated password", version.SecretString)
			}
		}
	}
	if !found {
		t.Fatalf("expected a doctor_database_url_url_v1 secret version, got keys %v", keysOf(resources.SecretsmanagerSecretVersion))
	}

	if _, ok := resources.IamRolePolicy["doctor_to_db_rds_secret"]; !ok {
		t.Errorf("expected a policy granting doctor read access to db's credentials")
	}
	if _, ok := resources.IamRolePolicy["doctor_to_blobs_s3_policy"]; !ok {
		t.Errorf("expected a policy granting doctor s3 access to blobs")
	}
}

// --- determinism -------------------------------------------------------

// TestInferPermissionsAndWiring_Deterministic runs the full pipeline 6
// times against a multi-reference example and diffs the resulting output,
// since permission wiring's `referenced` set is built from map keys and
// must be sorted before use, exactly the kind of thing Phase 2's
// nondeterminism bug was.
func TestInferPermissionsAndWiring_Deterministic(t *testing.T) {
	t.Parallel()
	composeApp, err := shared.ParseCompose("../../../../examples/doctor/compose.yml")
	if err != nil {
		t.Fatalf("ParseCompose failed: %v", err)
	}
	app, err := shared.Normalize(composeApp, "doctor")
	if err != nil {
		t.Fatalf("Normalize failed: %v", err)
	}
	env := fullMockProdEnv()

	var first string
	for i := 0; i < 6; i++ {
		resources, err := InferAWS(app, &env)
		if err != nil {
			t.Fatalf("InferAWS run %d failed: %v", i, err)
		}
		out, err := GenerateAWS(resources, &env, "app")
		if err != nil {
			t.Fatalf("GenerateAWS run %d failed: %v", i, err)
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

// TestGrantS3Permissions_UsesTaskRoleNotExecRole checks that S3 access is
// granted to the task role (the running application), not the exec role
// (which only ever needs to pull images/read secrets at startup).
func TestGrantS3Permissions_UsesTaskRoleNotExecRole(t *testing.T) {
	t.Parallel()
	resources := models.NewAWSResources()
	grantS3Permissions(resources, "web", "blobs", minimalGetName("prod", "app"))

	policy, ok := resources.IamRolePolicy["web_to_blobs_s3_policy"]
	if !ok {
		t.Fatalf("expected a policy, got keys %v", keysOf(resources.IamRolePolicy))
	}
	if policy.Role != "${aws_iam_role.web_task_role.name}" {
		t.Errorf("Role = %q, want the task role, not the exec role", policy.Role)
	}
}

// TestInferPermissionsAndWiring_RelationshipAloneGrantsNothing checks that
// a Relationship with no actual env-var reference to the server must not
// earn a permission grant. Go's InferPermissionsAndWiring never reads
// Application.Relationships at all (confirmed by inspection), so this
// holds by construction today -- but nothing pinned that until now, and a
// future change that started consulting Relationships for convenience
// would not be caught without this test.
func TestInferPermissionsAndWiring_RelationshipAloneGrantsNothing(t *testing.T) {
	t.Parallel()
	dbName := "app_db"
	app := &models.Application{
		Name: "app",
		Services: []models.Service{
			{Name: "web", Image: "web", Capability: models.CapabilityContainer, Env: map[string]string{"UNRELATED": "value"}},
			{Name: "db", Image: "postgres:16", Capability: models.CapabilityDatabase, DatabaseName: &dbName},
		},
		Relationships: []models.Relationship{
			{Client: "web", Server: "db"},
		},
	}
	env := fullMockProdEnv()
	getName := minimalGetName("prod", "app")

	resources := models.NewAWSResources()
	InferNetworking(resources, app, &env, getName, nil)
	priorities := CalculateListenerPriorities(app)
	namespace := InferServiceDiscovery(resources, app, &env, getName, nil)
	managedConnections := InferManagedServices(resources, app, &env, getName, nil, false)
	computeConnections := InferComputeResources(resources, app, &env, getName, nil, false, priorities, namespace)
	connections := map[string]models.Connection{}
	for k, v := range managedConnections {
		connections[k] = v
	}
	for k, v := range computeConnections {
		connections[k] = v
	}
	if err := InferPermissionsAndWiring(resources, app, &env, getName, connections); err != nil {
		t.Fatalf("InferPermissionsAndWiring failed: %v", err)
	}

	if _, ok := resources.IamRolePolicy["web_to_db_rds_secret"]; ok {
		t.Errorf("did not expect a grant from a Relationship with no matching env-var reference")
	}
}

// TestInferPermissionsAndWiring_GrantScopedToActuallyReferencedService
// checks that with two object-storage services, only the one actually
// named in an env var earns an S3 policy; a Relationship-only sibling
// earns nothing.
func TestInferPermissionsAndWiring_GrantScopedToActuallyReferencedService(t *testing.T) {
	t.Parallel()
	app := &models.Application{
		Name: "app",
		Services: []models.Service{
			{
				Name: "web", Image: "web", Capability: models.CapabilityContainer,
				Env: map[string]string{"BUCKET_NAME": "referenced-bucket"},
			},
			{Name: "referenced-bucket", Image: "minio/minio", Capability: models.CapabilityObjectStorage},
			{Name: "unreferenced-bucket", Image: "minio/minio", Capability: models.CapabilityObjectStorage},
		},
		Relationships: []models.Relationship{
			{Client: "web", Server: "unreferenced-bucket"},
		},
	}
	env := fullMockProdEnv()
	getName := minimalGetName("prod", "app")

	resources := models.NewAWSResources()
	InferNetworking(resources, app, &env, getName, nil)
	priorities := CalculateListenerPriorities(app)
	namespace := InferServiceDiscovery(resources, app, &env, getName, nil)
	managedConnections := InferManagedServices(resources, app, &env, getName, nil, false)
	computeConnections := InferComputeResources(resources, app, &env, getName, nil, false, priorities, namespace)
	connections := map[string]models.Connection{}
	for k, v := range managedConnections {
		connections[k] = v
	}
	for k, v := range computeConnections {
		connections[k] = v
	}
	if err := InferPermissionsAndWiring(resources, app, &env, getName, connections); err != nil {
		t.Fatalf("InferPermissionsAndWiring failed: %v", err)
	}

	if _, ok := resources.IamRolePolicy["web_to_referenced-bucket_s3_policy"]; !ok {
		t.Errorf("expected a grant for the actually-referenced bucket, got keys %v", keysOf(resources.IamRolePolicy))
	}
	if _, ok := resources.IamRolePolicy["web_to_unreferenced-bucket_s3_policy"]; ok {
		t.Errorf("did not expect a grant for the Relationship-only, unreferenced bucket")
	}
}
