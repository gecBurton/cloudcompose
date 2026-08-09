package gcp

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/gecburton/composey/internal/compiler/shared"
	"github.com/gecburton/composey/internal/models"
)

// InferGcp is the main entry point for GCP inference, mirroring
// compiler/inference/gcp/__init__.py's infer().
//
// Ported with lighter verification than InferAWS/InferAzure: GCP has no
// golden examples and essentially no dedicated Python test suite either
// (see plan.md -- GCP was ported at deliberately lighter rigor) -- this reflects the Python
// source's own logic directly, sanity-checked against a couple of
// hand-run Python outputs, not cross-checked against an existing
// coverage survey the way AWS/Azure were, since no equivalent survey
// exists to run.
func InferGcp(app *models.Application, env *models.GcpEnvironment) *models.GcpResources {
	resources := models.NewGcpResources()

	getName := func(resourceName string) string {
		return env.Name + "-" + app.Name + "-" + resourceName
	}
	var tags map[string]string
	if len(env.Tags) > 0 {
		tags = env.Tags
	}
	_ = tags // Python's own inference accepts tags but never applies them to any GCP resource type -- ported faithfully, not an oversight here.

	// Step 1: Create VPC connector if needed.
	vpcConnectorName := inferVpcConnectorGcp(resources, app, env, getName)

	// Step 2: Create Cloud SQL instance if needed.
	connections := inferDatabasesGcp(resources, app, env, getName)

	// Step 3: Create Memorystore Redis if needed.
	for k, v := range inferCachesGcp(resources, app, env, getName) {
		connections[k] = v
	}

	// Step 4: Create Cloud Storage buckets if needed.
	for k, v := range inferStorageGcp(resources, app, env, getName) {
		connections[k] = v
	}

	// connectionOrder mirrors Python's own connections dict insertion
	// order: databases first, then caches, then storage -- each group in
	// the order its services appear in app.Services. Iterating a Go map
	// directly (as this code did before) diffs the wrong way against a
	// live Python run whenever a service references more than one
	// connection type -- confirmed as a real, not theoretical, divergence
	// against the doctor example (2026-08-06), the same bug class Azure's
	// port hit and fixed the same way.
	connectionOrder := connectionOrderForGcp(app, connections)

	// Step 5: Create Cloud Run services.
	inferCloudRunServicesGcp(resources, app, env, getName, vpcConnectorName, connections, connectionOrder)

	// Step 6: Load balancer for custom domains/CDN -- _infer_load_balancer
	// in Python is a documented no-op ("TODO: Implement if cdn_enabled or
	// custom domain needed"), so there is nothing to port here either.

	return resources
}

// connectionOrderForGcp returns connection keys in the order Python's own
// dict-merge (connections from _infer_databases, then
// .update(cache_connections), then .update(storage_connections)) would
// produce: every database-capability service (declaration order), then
// every cache-capability service, then every object-storage-capability
// service, filtered to those with a connection.
func connectionOrderForGcp(app *models.Application, connections map[string]models.Connection) []string {
	order := make([]string, 0, len(connections))
	for _, capability := range []models.Capability{
		models.CapabilityDatabase,
		models.CapabilityCache,
		models.CapabilityObjectStorage,
	} {
		for i := range app.Services {
			name := app.Services[i].Name
			if app.Services[i].Capability != capability {
				continue
			}
			if _, ok := connections[name]; ok {
				order = append(order, name)
			}
		}
	}
	return order
}

// inferVpcConnectorGcp creates a VPC connector for private networking if
// any service needs it, mirroring _infer_vpc_connector. Only created when
// a database-capability service exists -- Cloud SQL's private IP path is
// the only thing here that needs one.
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

// inferDatabasesGcp creates one Cloud SQL instance for every
// database-capability service, mirroring _infer_databases. Unlike AWS/
// Azure (one managed server per service or per engine), Python's GCP
// inference deliberately creates exactly one Cloud SQL instance shared by
// every database service in the app, always Postgres 14 -- ported as-is,
// not something to "fix" into per-engine servers during this port.
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
// cache-capability service, mirroring _infer_caches.
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
// object-storage-capability service, mirroring _infer_storage.
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
// container-capability service, mirroring _infer_cloud_run_services.
//
// Unlike ECS/Container Apps: built-in HTTPS (no separate load balancer
// needed for the simple case), scales to zero by default, request-based
// concurrency.
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
// limit string, mirroring _get_cpu_limit.
func cpuLimitGcp(service *models.Service) string {
	if service.CPU != nil {
		return fmt.Sprintf("%dm", *service.CPU)
	}
	switch service.Size {
	case models.ServiceSizeMedium:
		return "2000m"
	case models.ServiceSizeLarge:
		return "4000m"
	default:
		return "1000m"
	}
}

// memoryLimitGcp converts service size or explicit memory to a Cloud Run
// memory limit string, mirroring _get_memory_limit.
func memoryLimitGcp(service *models.Service) string {
	if service.Memory != nil {
		return fmt.Sprintf("%dMi", *service.Memory)
	}
	switch service.Size {
	case models.ServiceSizeMedium:
		return "1Gi"
	case models.ServiceSizeLarge:
		return "2Gi"
	default:
		return "512Mi"
	}
}

// buildConnectionURLGcp mirrors _build_connection_url. Unlike Azure's
// connectionURLAzure (which now branches on the target's actual
// capability -- see docs/azure-aws-parity-todo.md Priority 1 item 3),
// this checks whether the connection actually carries a database and
// falls back to a bare host:port otherwise -- ported exactly as Python
// wrote it, not reconciled with Azure's approach to the same problem.
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

// pyNoneStringGcp renders Python's f"{value}" for an Optional[str]:
// str(None) == "None" when value is unset, not an empty string. Still a
// live, unfixed divergence on GCP as of 2026-08-09 (an f-string calls
// str() on its argument unconditionally) -- Azure's equivalent bug
// (containerSpecAzure) was found and fixed in a later pass, see
// docs/azure-aws-parity-todo.md's Priority 1 item 3; this GCP one has
// not been revisited since.
func pyNoneStringGcp(value *string) string {
	if value == nil {
		return "None"
	}
	return *value
}
