package aws

import (
	"fmt"
	"strings"

	"github.com/gecburton/cloudcompose/internal/compiler/shared"
	"github.com/gecburton/cloudcompose/internal/models"
)

// InferManagedServices infers managed services (RDS, ElastiCache, S3) and
// returns their connections.
//
// Returns a mapping of service name to Connection for use in wiring.
func InferManagedServices(
	resources *models.AWSResources,
	app *models.Application,
	env *models.AwsEnvironment,
	getName func(string) string,
	tags map[string]string,
	discard bool,
) map[string]models.Connection {
	namespace := NamespaceFor(env.Name, app.Name)
	_ = namespace // computed but not currently used by anything in this function

	connections := map[string]models.Connection{}

	for i := range app.Services {
		service := &app.Services[i]
		switch service.Capability {
		case models.CapabilityDatabase:
			if conn := inferDatabase(resources, service, env, getName, tags, discard); conn != nil {
				connections[service.Name] = *conn
			}
		case models.CapabilityCache:
			if conn := inferCache(resources, service, env, getName, tags); conn != nil {
				connections[service.Name] = *conn
			}
		case models.CapabilityObjectStorage:
			if conn := inferObjectStorage(resources, service, getName, tags, discard); conn != nil {
				connections[service.Name] = *conn
			}
		}
	}

	return connections
}

// inferDatabase infers RDS database resources.
func inferDatabase(
	resources *models.AWSResources,
	service *models.Service,
	env *models.AwsEnvironment,
	getName func(string) string,
	tags map[string]string,
	discard bool,
) *models.Connection {
	engine := "postgres"
	imageLower := strings.ToLower(service.Image)
	if strings.Contains(imageLower, "mysql") {
		engine = "mysql"
	} else if strings.Contains(imageLower, "mariadb") {
		engine = "mariadb"
	}

	dbUsername := shared.DatabaseDefaultUsername

	// Create random master password.
	passwordKey := service.Name + "_password"
	resources.RandomPassword[passwordKey] = models.RandomPassword{Length: 20, Special: false}

	// Store credentials in Secrets Manager.
	dbSecretKey := service.Name + "_db_secret"
	desc := fmt.Sprintf("Credentials for %s RDS", service.Name)
	resources.SecretsmanagerSecret[dbSecretKey] = models.SecretsManagerSecret{
		Name:        getName(service.Name + "-credentials"),
		Description: &desc,
		Tags:        tags,
	}

	secretString := marshalJSONString(map[string]string{
		"username": dbUsername,
		"password": fmt.Sprintf("${random_password.%s.result}", passwordKey),
		"engine":   engine,
	})
	resources.SecretsmanagerSecretVersion[dbSecretKey+"_v1"] = models.SecretsManagerSecretVersion{
		SecretID:     fmt.Sprintf("${aws_secretsmanager_secret.%s.id}", dbSecretKey),
		SecretString: secretString,
	}

	// Create subnet group.
	sngKey := service.Name + "_sng"
	resources.DbSubnetGroup[sngKey] = models.DbSubnetGroup{
		Name:      getName(service.Name + "-sng"),
		SubnetIds: env.PrivateSubnets,
		Tags:      tags,
	}

	// Create unique snapshot identifier if retaining.
	if !discard {
		resources.RandomID[service.Name+"_snapshot"] = models.NewRandomId()
	}

	instanceClass, ok := shared.DBInstanceClasses[string(service.Size)]
	if !ok {
		instanceClass = shared.DBInstanceClasses["small"]
	}

	databaseName := ""
	if service.DatabaseName != nil {
		databaseName = *service.DatabaseName
	}

	dbKey := service.Name + "_db"
	dbInstance := models.NewDbInstance()
	dbInstance.Identifier = getName(service.Name)
	dbInstance.Engine = engine
	dbInstance.DbName = databaseName
	dbInstance.InstanceClass = instanceClass
	dbInstance.AllocatedStorage = 20
	dbInstance.DbSubnetGroupName = fmt.Sprintf("${aws_db_subnet_group.%s.name}", sngKey)
	dbInstance.VpcSecurityGroupIds = SecurityGroupIDs(service.NetworkIsolationSegments)
	dbInstance.SkipFinalSnapshot = discard
	if !discard {
		finalSnapshot := fmt.Sprintf("%s-final-${random_id.%s_snapshot.hex}", getName(service.Name), service.Name)
		dbInstance.FinalSnapshotIdentifier = &finalSnapshot
	}
	dbInstance.PubliclyAccessible = false
	dbInstance.MultiAz = env.HighAvailabilityEnabled
	dbInstance.BackupRetentionPeriod = env.BackupRetentionDays
	dbInstance.Username = &dbUsername
	passwordRef := fmt.Sprintf("${random_password.%s.result}", passwordKey)
	dbInstance.Password = &passwordRef
	dbInstance.Tags = tags
	resources.DbInstance[dbKey] = dbInstance

	port := shared.DefaultPortForDatabase(engine)
	return &models.Connection{
		Host:        fmt.Sprintf("${aws_db_instance.%s.address}", dbKey),
		Port:        &port,
		Username:    &dbUsername,
		Password:    &passwordRef,
		Database:    &databaseName,
		AddressedBy: "host",
	}
}

// inferCache infers ElastiCache Redis resources.
func inferCache(
	resources *models.AWSResources,
	service *models.Service,
	env *models.AwsEnvironment,
	getName func(string) string,
	tags map[string]string,
) *models.Connection {
	// Create subnet group.
	sngKey := service.Name + "_sng"
	resources.ElastiCacheSubnetGroup[sngKey] = models.ElastiCacheSubnetGroup{
		Name:      getName(service.Name + "-sng"),
		SubnetIds: env.PrivateSubnets,
		Tags:      tags,
	}

	nodeType, ok := shared.CacheNodeTypes[string(service.Size)]
	if !ok {
		nodeType = shared.CacheNodeTypes["small"]
	}

	cacheKey := service.Name + "_cache"
	resources.ElastiCacheCluster[cacheKey] = models.ElastiCacheCluster{
		ClusterID:        getName(service.Name),
		Engine:           "redis",
		NodeType:         nodeType,
		NumCacheNodes:    1,
		SubnetGroupName:  fmt.Sprintf("${aws_elasticache_subnet_group.%s.name}", sngKey),
		SecurityGroupIds: SecurityGroupIDs(service.NetworkIsolationSegments),
		Tags:             tags,
	}

	port := shared.DefaultPortRedis
	return &models.Connection{
		Host:        fmt.Sprintf("${aws_elasticache_cluster.%s.cache_nodes[0].address}", cacheKey),
		Port:        &port,
		AddressedBy: "host",
	}
}

// inferObjectStorage infers S3 bucket resources (substitutes for Minio).
func inferObjectStorage(
	resources *models.AWSResources,
	service *models.Service,
	getName func(string) string,
	tags map[string]string,
	discard bool,
) *models.Connection {
	bucketKey := service.Name + "_bucket"

	bucketName := strings.ToLower(getName(service.Name))
	bucketName = strings.ReplaceAll(bucketName, "_", "-")
	if len(bucketName) > 63 {
		bucketName = bucketName[:63]
	}
	bucketName = strings.TrimRight(bucketName, "-")

	bucket := models.NewS3Bucket()
	bucket.Bucket = bucketName
	bucket.ForceDestroy = discard
	bucket.Tags = tags
	resources.S3Bucket[bucketKey] = bucket

	name := fmt.Sprintf("${aws_s3_bucket.%s.id}", bucketKey)
	return &models.Connection{
		Host:        fmt.Sprintf("${aws_s3_bucket.%s.bucket_domain_name}", bucketKey),
		Name:        &name,
		AddressedBy: "name",
	}
}
