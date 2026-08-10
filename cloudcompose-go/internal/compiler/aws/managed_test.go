package aws

import (
	"github.com/gecburton/cloudcompose/internal/compiler/shared"
	"testing"

	"github.com/gecburton/cloudcompose/internal/models"
)

func fullMockProdEnv() models.AwsEnvironment {
	env := models.NewAwsEnvironment()
	env.Name = "prod"
	env.VpcID = "vpc-123"
	env.PublicSubnets = []string{"subnet-1", "subnet-2"}
	env.PrivateSubnets = []string{"subnet-3", "subnet-4"}
	env.EcsClusterArn = "arn:aws:ecs:us-east-1:123456789012:cluster/prod-cluster"
	albArn := "arn:aws:lb:us-east-1:123456789012:loadbalancer/app/shared-alb/123"
	albListenerArn := "arn:aws:lb:us-east-1:123456789012:listener/app/shared-alb/123/456"
	albSG := "sg-alb0123456789"
	env.AlbArn = &albArn
	env.AlbListenerArn = &albListenerArn
	env.AlbSecurityGroupID = &albSG
	return env
}

// TestInferManagedServices_RealNginxFlaskMysqlExample exercises the real
// nginx-flask-mysql example (a mariadb-backed database service) through the
// real parser/normalizer boundary, per this phase's review discipline.
func TestInferManagedServices_RealNginxFlaskMysqlExample(t *testing.T) {
	t.Parallel()
	composeApp, err := shared.ParseCompose("../../../../examples/nginx-flask-mysql/compose.yml")
	if err != nil {
		t.Fatalf("ParseCompose failed: %v", err)
	}
	app, err := shared.Normalize(composeApp, "nginx-flask-mysql")
	if err != nil {
		t.Fatalf("Normalize failed: %v", err)
	}

	env := fullMockProdEnv()
	getName := minimalGetName("prod", "nginx-flask-mysql")

	resources := models.NewAWSResources()
	// Networking must run first: inferDatabase reads network security group
	// IDs from resources implicitly via SecurityGroupIDs(segments), which
	// only produces meaningful refs once InferNetworking has created them
	// (though the refs are just string templates, not validated here).
	InferNetworking(resources, app, &env, getName, nil)
	connections := InferManagedServices(resources, app, &env, getName, nil, false)

	dbInstance, ok := resources.DbInstance["db_db"]
	if !ok {
		t.Fatalf("expected aws_db_instance keyed 'db_db', got keys %v", keysOf(resources.DbInstance))
	}
	if dbInstance.Engine != "mariadb" {
		t.Errorf("Engine = %q, want mariadb", dbInstance.Engine)
	}
	if dbInstance.DbName != "example" {
		t.Errorf("DbName = %q, want example", dbInstance.DbName)
	}
	if dbInstance.Identifier != "prod-nginx-flask-mysql-db" {
		t.Errorf("Identifier = %q, want prod-nginx-flask-mysql-db", dbInstance.Identifier)
	}
	if dbInstance.InstanceClass != "db.t3.micro" {
		t.Errorf("InstanceClass = %q, want db.t3.micro", dbInstance.InstanceClass)
	}
	if dbInstance.SkipFinalSnapshot {
		t.Errorf("SkipFinalSnapshot = true, want false (retain_data_on_destroy defaults true)")
	}
	if dbInstance.FinalSnapshotIdentifier == nil {
		t.Errorf("expected a final_snapshot_identifier when not discarding")
	} else if *dbInstance.FinalSnapshotIdentifier != "prod-nginx-flask-mysql-db-final-${random_id.db_snapshot.hex}" {
		t.Errorf("FinalSnapshotIdentifier = %q, want prod-nginx-flask-mysql-db-final-${random_id.db_snapshot.hex}", *dbInstance.FinalSnapshotIdentifier)
	}
	if dbInstance.Username == nil || *dbInstance.Username != "cloudcompose" {
		t.Errorf("Username = %v, want cloudcompose", dbInstance.Username)
	}

	conn, ok := connections["db"]
	if !ok {
		t.Fatalf("expected a connection for service 'db', got %v", connections)
	}
	if conn.Port == nil || *conn.Port != 3306 {
		t.Errorf("connection port = %v, want 3306 for mariadb", conn.Port)
	}
}

// TestInferManagedServices_RealFlaskRedisExample exercises the real
// flask-redis example (a cache service) through the real boundary.
func TestInferManagedServices_RealFlaskRedisExample(t *testing.T) {
	t.Parallel()
	composeApp, err := shared.ParseCompose("../../../../examples/flask-redis/compose.yml")
	if err != nil {
		t.Fatalf("ParseCompose failed: %v", err)
	}
	app, err := shared.Normalize(composeApp, "flask-redis")
	if err != nil {
		t.Fatalf("Normalize failed: %v", err)
	}

	env := fullMockProdEnv()
	getName := minimalGetName("prod", "flask-redis")

	resources := models.NewAWSResources()
	InferNetworking(resources, app, &env, getName, nil)
	connections := InferManagedServices(resources, app, &env, getName, nil, false)

	cluster, ok := resources.ElastiCacheCluster["redis_cache"]
	if !ok {
		t.Fatalf("expected aws_elasticache_cluster keyed 'redis_cache', got keys %v", keysOf(resources.ElastiCacheCluster))
	}
	if cluster.Engine != "redis" {
		t.Errorf("Engine = %q, want redis", cluster.Engine)
	}
	if cluster.NumCacheNodes != 1 {
		t.Errorf("NumCacheNodes = %d, want 1", cluster.NumCacheNodes)
	}

	conn, ok := connections["redis"]
	if !ok {
		t.Fatalf("expected a connection for service 'redis', got %v", connections)
	}
	if conn.Port == nil || *conn.Port != 6379 {
		t.Errorf("connection port = %v, want 6379", conn.Port)
	}
}

// TestInferManagedServices_RealMinioS3Example exercises the real minio-s3
// example (an object-storage service) through the real boundary.
func TestInferManagedServices_RealMinioS3Example(t *testing.T) {
	t.Parallel()
	composeApp, err := shared.ParseCompose("../../../../examples/minio-s3/compose.yml")
	if err != nil {
		t.Fatalf("ParseCompose failed: %v", err)
	}
	app, err := shared.Normalize(composeApp, "minio-s3")
	if err != nil {
		t.Fatalf("Normalize failed: %v", err)
	}

	env := fullMockProdEnv()
	getName := minimalGetName("prod", "minio-s3")

	resources := models.NewAWSResources()
	InferNetworking(resources, app, &env, getName, nil)
	connections := InferManagedServices(resources, app, &env, getName, nil, false)

	bucket, ok := resources.S3Bucket["blobs_bucket"]
	if !ok {
		t.Fatalf("expected aws_s3_bucket keyed 'blobs_bucket', got keys %v", keysOf(resources.S3Bucket))
	}
	if bucket.ForceDestroy {
		t.Errorf("ForceDestroy = true, want false (not discarding)")
	}

	conn, ok := connections["blobs"]
	if !ok {
		t.Fatalf("expected a connection for service 'blobs', got %v", connections)
	}
	if conn.AddressedBy != "name" {
		t.Errorf("AddressedBy = %q, want name", conn.AddressedBy)
	}
	if conn.Name == nil {
		t.Fatalf("expected connection.Name to be set for a bucket")
	}
}

// --- hand-built edge cases --------------------------------------------------

func TestInferDatabase_EngineDetection(t *testing.T) {
	t.Parallel()
	cases := []struct {
		image string
		want  string
	}{
		{"postgres:16", "postgres"},
		{"mysql:8.0", "mysql"},
		{"mariadb:10.6.4-focal", "mariadb"},
		{"percona/percona-server-mysql:8.0", "mysql"},
	}

	for _, tc := range cases {
		t.Run(tc.image, func(t *testing.T) {
			dbName := "app_db"
			service := &models.Service{
				Name:         "db",
				Image:        tc.image,
				Capability:   models.CapabilityDatabase,
				DatabaseName: &dbName,
				Size:         models.ServiceSizeSmall,
			}
			env := fullMockProdEnv()
			resources := models.NewAWSResources()
			conn := inferDatabase(resources, service, &env, minimalGetName("prod", "app"), nil, false)
			if conn == nil {
				t.Fatal("expected a connection")
			}
			if resources.DbInstance["db_db"].Engine != tc.want {
				t.Errorf("Engine = %q, want %q", resources.DbInstance["db_db"].Engine, tc.want)
			}
		})
	}
}

func TestInferDatabase_DiscardSkipsSnapshot(t *testing.T) {
	t.Parallel()
	dbName := "app_db"
	service := &models.Service{
		Name:         "db",
		Image:        "postgres:16",
		Capability:   models.CapabilityDatabase,
		DatabaseName: &dbName,
		Size:         models.ServiceSizeSmall,
	}
	env := fullMockProdEnv()
	resources := models.NewAWSResources()
	inferDatabase(resources, service, &env, minimalGetName("prod", "app"), nil, true)

	dbInstance := resources.DbInstance["db_db"]
	if !dbInstance.SkipFinalSnapshot {
		t.Errorf("expected SkipFinalSnapshot = true when discarding")
	}
	if dbInstance.FinalSnapshotIdentifier != nil {
		t.Errorf("expected no FinalSnapshotIdentifier when discarding, got %v", *dbInstance.FinalSnapshotIdentifier)
	}
	if _, ok := resources.RandomID["db_snapshot"]; ok {
		t.Errorf("did not expect a random_id resource when discarding")
	}
}

func TestInferObjectStorage_BucketNameSanitized(t *testing.T) {
	t.Parallel()
	service := &models.Service{Name: "blobs", Capability: models.CapabilityObjectStorage}
	getName := func(s string) string { return "PROD_App_" + s }
	resources := models.NewAWSResources()
	conn := inferObjectStorage(resources, service, getName, nil, false)
	if conn == nil {
		t.Fatal("expected a connection")
	}
	bucket := resources.S3Bucket["blobs_bucket"]
	if bucket.Bucket != "prod-app-blobs" {
		t.Errorf("Bucket = %q, want prod-app-blobs", bucket.Bucket)
	}
}

// TestInferObjectStorage_DiscardForcesDestroy checks that a non-retained
// environment must force_destroy=True on the bucket so `terraform destroy`
// doesn't stop at a non-empty bucket -- untested in Go until now, since no
// golden example uses retain_data_on_destroy: false.
func TestInferObjectStorage_DiscardForcesDestroy(t *testing.T) {
	t.Parallel()
	service := &models.Service{Name: "blobs", Capability: models.CapabilityObjectStorage}
	resources := models.NewAWSResources()
	conn := inferObjectStorage(resources, service, minimalGetName("prod", "app"), nil, true)
	if conn == nil {
		t.Fatal("expected a connection")
	}
	bucket := resources.S3Bucket["blobs_bucket"]
	if !bucket.ForceDestroy {
		t.Errorf("ForceDestroy = false, want true when discarding")
	}
}

// TestInferObjectStorage_RetainDoesNotForceDestroy is the complementary
// positive case: a retained environment must not force-destroy.
func TestInferObjectStorage_RetainDoesNotForceDestroy(t *testing.T) {
	t.Parallel()
	service := &models.Service{Name: "blobs", Capability: models.CapabilityObjectStorage}
	resources := models.NewAWSResources()
	inferObjectStorage(resources, service, minimalGetName("prod", "app"), nil, false)
	bucket := resources.S3Bucket["blobs_bucket"]
	if bucket.ForceDestroy {
		t.Errorf("ForceDestroy = true, want false when retaining")
	}
}

// TestInferDatabase_SecretsAlwaysHardDeletedRegardlessOfRetention checks
// that, unlike the database/bucket, a credentials secret's
// recovery_window_in_days is 0 regardless of retain_data_on_destroy -- a
// recovery window keeps the name reserved, which blocks re-creating one
// with the same name for up to 30 days, and a retained database is
// recoverable from its own snapshot without the old credentials anyway.
// Checked for both discard=true and discard=false, since only the
// discard=true path had any coverage before (via the general discard
// test) and the "regardless of" half specifically needs both.
func TestInferDatabase_SecretsAlwaysHardDeletedRegardlessOfRetention(t *testing.T) {
	t.Parallel()
	for _, discard := range []bool{true, false} {
		dbName := "app_db"
		service := &models.Service{
			Name:         "db",
			Image:        "postgres:16",
			Capability:   models.CapabilityDatabase,
			DatabaseName: &dbName,
			Size:         models.ServiceSizeSmall,
		}
		env := fullMockProdEnv()
		resources := models.NewAWSResources()
		inferDatabase(resources, service, &env, minimalGetName("prod", "app"), nil, discard)

		secret, ok := resources.SecretsmanagerSecret["db_db_secret"]
		if !ok {
			t.Fatalf("discard=%v: expected a secret", discard)
		}
		if secret.RecoveryWindowInDays != 0 {
			t.Errorf("discard=%v: RecoveryWindowInDays = %d, want 0", discard, secret.RecoveryWindowInDays)
		}
	}
}
