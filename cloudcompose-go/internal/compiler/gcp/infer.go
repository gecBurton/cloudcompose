package gcp

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/gecburton/cloudcompose/internal/compiler/shared"
	"github.com/gecburton/cloudcompose/internal/models"
)

// InferGcp is the main entry point for GCP inference.
func InferGcp(app *models.Application, env *models.GcpEnvironment) *models.GcpResources {
	resources := models.NewGcpResources()

	getName := shared.ResourceNamer(env.Name, app.Name)
	var tags map[string]string
	if len(env.Tags) > 0 {
		tags = env.Tags
	}
	_ = tags // tags are accepted but not yet applied to any GCP resource type.

	vpcConnectorName := inferVpcConnectorGcp(resources, app, env, getName)

	connections := inferDatabasesGcp(resources, app, env, getName)

	for k, v := range inferCachesGcp(resources, app, env, getName) {
		connections[k] = v
	}

	for k, v := range inferStorageGcp(resources, app, env, getName) {
		connections[k] = v
	}

	// connectionOrder preserves insertion order (databases, then caches,
	// then storage) so output is deterministic; iterating a map directly
	// would produce nondeterministic diffs.
	connectionOrder := shared.ConnectionOrder(app, connections)

	inferCloudRunServicesGcp(resources, app, env, getName, vpcConnectorName, connections, connectionOrder)

	// Load balancer for custom domains/CDN is not yet implemented.

	return resources
}

// inferVpcConnectorGcp creates a VPC connector for private networking,
// needed when a database-capability service exists (Cloud SQL's private
// IP path requires one).
func inferVpcConnectorGcp(
	resources *models.GcpResources,
	app *models.Application,
	env *models.GcpEnvironment,
	getName func(string) string,
) *string {
	needsPrivate := false
	for i := range app.Services {
		if app.Services[i].Capability == models.CapabilityDatabase {
			needsPrivate = true
			break
		}
	}
	if !needsPrivate {
		return nil
	}

	connectorName := getName("vpc-connector")

	network := "default"
	if env.VpcID != nil && *env.VpcID != "" {
		network = *env.VpcID
	}

	connector := models.NewVpcConnector()
	connector.Name = connectorName
	connector.ProjectID = env.ProjectID
	connector.Region = env.Region
	connector.Network = network
	resources.VpcAccessConnector["main"] = connector

	return &connectorName
}

// inferDatabasesGcp creates one shared Cloud SQL (Postgres 14) instance
// for every database-capability service.
func inferDatabasesGcp(
	resources *models.GcpResources,
	app *models.Application,
	env *models.GcpEnvironment,
	getName func(string) string,
) map[string]models.Connection {
	connections := map[string]models.Connection{}

	var dbServices []*models.Service
	for i := range app.Services {
		if app.Services[i].Capability == models.CapabilityDatabase {
			dbServices = append(dbServices, &app.Services[i])
		}
	}
	if len(dbServices) == 0 {
		return connections
	}

	instanceName := getName("db")
	instanceKey := "main"

	resources.RandomPassword["db_root"] = models.RandomPassword{Length: 20, Special: false}

	instance := models.NewCloudSqlInstance()
	instance.Name = instanceName
	instance.ProjectID = env.ProjectID
	instance.Region = env.Region
	rootPasswordRef := "${random_password.db_root.result}"
	instance.RootPassword = &rootPasswordRef
	instance.BackupConfiguration = &models.CloudSqlBackupConfiguration{Enabled: true, StartTime: "03:00"}
	instance.IpConfiguration = models.CloudSqlIPConfiguration{Ipv4Enabled: true}
	resources.SqlDatabaseInstance[instanceKey] = instance

	for _, service := range dbServices {
		dbName := service.Name
		if service.DatabaseName != nil && *service.DatabaseName != "" {
			dbName = *service.DatabaseName
		}

		dbKey := service.Name + "_db"
		resources.SqlDatabase[dbKey] = models.CloudSqlDatabase{
			Name:      dbName,
			Instance:  fmt.Sprintf("${google_sql_database_instance.%s.name}", instanceKey),
			ProjectID: env.ProjectID,
		}

		port := 5432
		username := shared.DatabaseDefaultUsername
		connections[service.Name] = models.Connection{
			Host:     fmt.Sprintf("${google_sql_database_instance.%s.public_ip_address}", instanceKey),
			Port:     &port,
			Username: &username,
			Password: &rootPasswordRef,
			Database: &dbName,
		}
	}

	return connections
}

// inferCachesGcp creates a Memorystore Redis instance for every
// cache-capability service.
func inferCachesGcp(
	resources *models.GcpResources,
	app *models.Application,
	env *models.GcpEnvironment,
	getName func(string) string,
) map[string]models.Connection {
	connections := map[string]models.Connection{}

	for i := range app.Services {
		service := &app.Services[i]
		if service.Capability != models.CapabilityCache {
			continue
		}

		cacheKey := service.Name + "_redis"
		redis := models.NewRedisInstance()
		redis.Name = getName(service.Name)
		redis.ProjectID = env.ProjectID
		redis.Region = env.Region
		resources.RedisInstance[cacheKey] = redis

		port := shared.DefaultPortRedis
		connections[service.Name] = models.Connection{
			Host: fmt.Sprintf("${google_redis_instance.%s.host}", cacheKey),
			Port: &port,
		}
	}

	return connections
}

// inferStorageGcp creates a Cloud Storage bucket for every
// object-storage-capability service.
func inferStorageGcp(
	resources *models.GcpResources,
	app *models.Application,
	env *models.GcpEnvironment,
	getName func(string) string,
) map[string]models.Connection {
	connections := map[string]models.Connection{}

	for i := range app.Services {
		service := &app.Services[i]
		if service.Capability != models.CapabilityObjectStorage {
			continue
		}

		bucketKey := service.Name + "_bucket"
		bucketName := strings.ToLower(strings.ReplaceAll(getName(service.Name), "_", "-"))

		bucket := models.NewStorageBucket()
		bucket.Name = bucketName
		bucket.ProjectID = env.ProjectID
		bucket.ForceDestroy = !env.RetainDataOnDestroy
		bucket.Versioning = models.StorageBucketVersioning{Enabled: false}
		resources.StorageBucket[bucketKey] = bucket

		name := fmt.Sprintf("${google_storage_bucket.%s.name}", bucketKey)
		connections[service.Name] = models.Connection{
			Host:        fmt.Sprintf("${google_storage_bucket.%s.url}", bucketKey),
			Name:        &name,
			AddressedBy: "name",
		}
	}

	return connections
}

// inferCloudRunServicesGcp creates a Cloud Run service for every
// container-capability service.
func inferCloudRunServicesGcp(
	resources *models.GcpResources,
	app *models.Application,
	env *models.GcpEnvironment,
	getName func(string) string,
	vpcConnectorName *string,
	connections map[string]models.Connection,
	connectionOrder []string,
) {
	for i := range app.Services {
		service := &app.Services[i]
		if service.Capability != models.CapabilityContainer {
			continue
		}

		container := models.CloudRunContainer{
			Image:   service.Image,
			Command: service.Command,
			Resources: models.CloudRunContainerLimits{
				Limits: models.CloudRunResourceLimits{
					CPU:    cpuLimitGcp(service),
					Memory: memoryLimitGcp(service),
				},
			},
		}

		envVars := make([]models.CloudRunEnvVar, 0, len(service.Env))
		for _, k := range shared.SortedKeys(service.Env) {
			envVars = append(envVars, models.CloudRunEnvVar{Name: k, Value: service.Env[k]})
		}

		for _, targetName := range connectionOrder {
			conn, ok := connections[targetName]
			if !ok {
				continue
			}
			referenced := false
			for _, r := range app.Relationships {
				if r.Client == service.Name && r.Server == targetName {
					referenced = true
					break
				}
			}
			if !referenced {
				continue
			}
			envVars = append(envVars, models.CloudRunEnvVar{
				Name:  strings.ToUpper(targetName) + "_URL",
				Value: buildConnectionURLGcp(&conn),
			})
		}

		container.Env = envVars

		serviceAccount := env.ProjectID + "-compute@developer.gserviceaccount.com"
		if env.ServiceAccountEmail != nil && *env.ServiceAccountEmail != "" {
			serviceAccount = *env.ServiceAccountEmail
		}

		annotations := map[string]string{
			"autoscaling.knative.dev/minScale":         strconv.Itoa(service.MinScale),
			"autoscaling.knative.dev/maxScale":         strconv.Itoa(service.MaxScale),
			"run.googleapis.com/cpu-throttling":        "true",
			"run.googleapis.com/execution-environment": "gen2",
		}
		if vpcConnectorName != nil {
			annotations["run.googleapis.com/vpc-access-connector"] = *vpcConnectorName
			annotations["run.googleapis.com/vpc-access-egress"] = "all-traffic"
		}

		template := &models.CloudRunTemplate{
			Spec: models.CloudRunSpec{
				Containers:         []models.CloudRunContainer{container},
				ServiceAccountName: serviceAccount,
			},
			Metadata: models.CloudRunTemplateMeta{Annotations: annotations},
		}

		traffic := []models.CloudRunTraffic{{Percent: 100, LatestRevision: true}}

		ingress := "all"
		if service.Ingress == nil {
			ingress = "internal"
		}

		cr := models.NewCloudRunService()
		cr.Name = getName(service.Name)
		cr.Location = env.Region
		cr.ProjectID = env.ProjectID
		cr.Template = template
		cr.Traffic = traffic
		cr.Ingress = ingress

		resources.CloudRunService[service.Name] = cr
	}
}

// cpuLimitGcp converts service size or explicit CPU to a Cloud Run CPU
// limit string (millicores).
func cpuLimitGcp(service *models.Service) string {
	if service.CPU != nil {
		return fmt.Sprintf("%dm", *service.CPU)
	}
	mapping, ok := shared.SizeMappings[string(service.Size)]
	if !ok {
		mapping = shared.SizeMappings["small"]
	}
	// mapping.CPU is in ECS CPU units, where 1024 units == 1000m.
	millicores := mapping.CPU * 1000 / 1024
	return fmt.Sprintf("%dm", millicores)
}

// memoryLimitGcp converts service size or explicit memory to a Cloud Run
// memory limit string.
func memoryLimitGcp(service *models.Service) string {
	if service.Memory != nil {
		return fmt.Sprintf("%dMi", *service.Memory)
	}
	mapping, ok := shared.SizeMappings[string(service.Size)]
	if !ok {
		mapping = shared.SizeMappings["small"]
	}
	return fmt.Sprintf("%dMi", mapping.Memory)
}

// buildConnectionURLGcp builds a connection URL string, falling back to
// a bare host:port for connections that don't carry a database.
func buildConnectionURLGcp(conn *models.Connection) string {
	if conn.Database != nil {
		username := pyNoneStringGcp(conn.Username)
		password := pyNoneStringGcp(conn.Password)
		port := "None"
		if conn.Port != nil {
			port = strconv.Itoa(*conn.Port)
		}
		return fmt.Sprintf("postgresql://%s:%s@%s:%s/%s", username, password, conn.Host, port, *conn.Database)
	}
	port := "None"
	if conn.Port != nil {
		port = strconv.Itoa(*conn.Port)
	}
	return fmt.Sprintf("%s:%s", conn.Host, port)
}

// pyNoneStringGcp renders the literal string "None" for an unset
// Optional[str] value, not an empty string.
func pyNoneStringGcp(value *string) string {
	if value == nil {
		return "None"
	}
	return *value
}
