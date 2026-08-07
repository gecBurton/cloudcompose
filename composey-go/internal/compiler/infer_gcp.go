package compiler

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/gecburton/composey/internal/models"
)

// InferGcp is the main entry point for GCP inference, mirroring
// compiler/inference/gcp/__init__.py's infer().
//
// Ported with lighter verification than InferAWS/InferAzure: GCP has no
// golden examples and essentially no dedicated Python test suite either
// (see plan.md's Phase 4 GCP section) -- this reflects the Python
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

	resources.RandomPassword["db_root"] = PyOrdered{p("length", 20)}

	instance := models.NewCloudSqlInstance()
	instance.Name = instanceName
	instance.ProjectID = env.ProjectID
	instance.Region = env.Region
	rootPasswordRef := "${random_password.db_root.result}"
	instance.RootPassword = &rootPasswordRef
	// Python's default_factory dicts for backup_configuration/
	// ip_configuration, reproduced in the exact key order the dict
	// literals in gcp.py state (enabled/start_time,
	// ipv4_enabled/private_network) rather than left to whatever order a
	// plain map would sort to once this goes through PyDumpsIndent-style
	// rendering.
	instance.BackupConfiguration = PyOrdered{
		p("enabled", true),
		p("start_time", "03:00"),
	}
	instance.IpConfiguration = PyOrdered{
		p("ipv4_enabled", true),
		p("private_network", nil),
	}
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
		username := DatabaseDefaultUsername
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

		port := DefaultPortRedis
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
		// Python's default_factory=lambda: {"enabled": False} for
		// versioning -- confirmed as a real, not theoretical, gap by
		// diffing actual output for the doctor example, where the Go
		// zero value (nil, since Versioning is declared `any`) rendered
		// as JSON null instead of {"enabled": false} (2026-08-06).
		bucket.Versioning = PyOrdered{p("enabled", false)}
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

		container := PyOrdered{
			p("image", service.Image),
			p("resources", PyOrdered{
				p("limits", PyOrdered{
					p("cpu", cpuLimitGcp(service)),
					p("memory", memoryLimitGcp(service)),
				}),
			}),
		}
		if len(service.Command) > 0 {
			container = append(container, p("command", service.Command))
		}

		envVars := make([]any, 0, len(service.Env))
		for _, k := range sortedKeys(service.Env) {
			envVars = append(envVars, PyOrdered{p("name", k), p("value", service.Env[k])})
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
			envVars = append(envVars, PyOrdered{
				p("name", strings.ToUpper(targetName)+"_URL"),
				p("value", buildConnectionURLGcp(&conn)),
			})
		}

		if len(envVars) > 0 {
			container = append(container, p("env", envVars))
		}

		serviceAccount := env.ProjectID + "-compute@developer.gserviceaccount.com"
		if env.ServiceAccountEmail != nil && *env.ServiceAccountEmail != "" {
			serviceAccount = *env.ServiceAccountEmail
		}

		spec := PyOrdered{
			p("containers", []any{container}),
			p("service_account_name", serviceAccount),
		}

		annotations := PyOrdered{
			p("autoscaling.knative.dev/minScale", strconv.Itoa(service.MinScale)),
			p("autoscaling.knative.dev/maxScale", strconv.Itoa(service.MaxScale)),
			p("run.googleapis.com/cpu-throttling", "true"),
			p("run.googleapis.com/execution-environment", "gen2"),
		}
		if vpcConnectorName != nil {
			annotations = append(annotations,
				p("run.googleapis.com/vpc-access-connector", *vpcConnectorName),
				p("run.googleapis.com/vpc-access-egress", "all-traffic"),
			)
		}

		template := PyOrdered{
			p("spec", spec),
			p("metadata", PyOrdered{p("annotations", annotations)}),
		}

		traffic := []any{PyOrdered{p("percent", 100), p("latest_revision", true)}}

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
// _container_spec (which always renders a postgresql:// URL, even for a
// Redis/Storage connection, and renders "None" for unset fields), this
// checks whether the connection actually carries a database and falls
// back to a bare host:port otherwise -- ported exactly as Python wrote
// it, not reconciled with Azure's different (and differently buggy)
// approach to the same problem.
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
// str(None) == "None" when value is unset, not an empty string --
// matching the same divergence Azure's _container_spec has, confirmed via
// the same reasoning (an f-string calls str() on its argument
// unconditionally).
func pyNoneStringGcp(value *string) string {
	if value == nil {
		return "None"
	}
	return *value
}
