package azure

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/gecburton/composey/internal/compiler/shared"
	"github.com/gecburton/composey/internal/models"
)

// cronExpressionAzure renders a cloud-neutral schedule as the standard
// 5-field cron Azure wants, mirroring _cron_expression.
//
// Azure has no rate dialect, so an interval is expressed as the cron that
// means the same thing. Intervals that cron cannot express -- anything
// that does not divide its unit evenly, like every 7 hours -- are rejected
// rather than silently rounded to something that runs at the wrong time.
func cronExpressionAzure(schedule models.Schedule) (string, error) {
	rate, isRate := shared.AsRateSchedule(schedule)
	if !isRate {
		cron, ok := shared.AsCronSchedule(schedule)
		if !ok {
			return "", fmt.Errorf("unknown schedule type %T", schedule)
		}
		return cron.Expression, nil
	}

	value := rate.Value
	switch rate.Unit {
	case models.RateUnitMinutes:
		if value == 1 {
			return "* * * * *", nil
		}
		if 60%value != 0 {
			return "", fmt.Errorf(
				"a rate of every %d minutes cannot be expressed as cron, which Azure "+
					"requires: use an interval that divides an hour evenly, or give a "+
					"cron expression directly", value)
		}
		return fmt.Sprintf("*/%d * * * *", value), nil

	case models.RateUnitHours:
		if value == 1 {
			return "0 * * * *", nil
		}
		if 24%value != 0 {
			return "", fmt.Errorf(
				"a rate of every %d hours cannot be expressed as cron, which Azure "+
					"requires: use an interval that divides a day evenly, or give a "+
					"cron expression directly", value)
		}
		return fmt.Sprintf("0 */%d * * *", value), nil

	default: // days
		if value == 1 {
			return "0 0 * * *", nil
		}
		return "", fmt.Errorf(
			"a rate of every %d days cannot be expressed as cron, which Azure "+
				"requires: months are not all the same length, so */%d on "+
				"day-of-month would drift. Give a cron expression directly", value, value)
	}
}

// registryAuthAzure returns registry pull config for a service, plus any
// secret block it needs, mirroring _registry_auth.
//
// Deliberately username/password rather than the managed-identity form of
// registry (server + identity): a system-assigned identity's principal_id
// does not exist until the Container App/Job itself has been created, so
// granting it an AcrPull role assignment cannot happen before that first
// create -- and the create itself needs to pull the image. ACR's admin
// credentials are stable resource attributes available the moment the
// registry exists, so authenticating with them sidesteps the ordering
// problem entirely.
func registryAuthAzure(service *models.Service) (registry []models.ContainerAppRegistry, secret []models.ContainerAppSecret) {
	if service.BuildContext == nil {
		return nil, nil
	}

	secret = []models.ContainerAppSecret{
		{Name: "acr-password", Value: "${azurerm_container_registry.main.admin_password}"},
	}
	registry = []models.ContainerAppRegistry{
		{
			Server:             "${azurerm_container_registry.main.login_server}",
			Username:           "${azurerm_container_registry.main.admin_username}",
			PasswordSecretName: "acr-password",
		},
	}
	return registry, secret
}

// containerSpecAzure builds the container block for a service, shared by
// Container Apps and Jobs, mirroring _container_spec.
//
// Both take the same shape, so a scheduled task gets the same image
// resolution and the same wired-in connection strings as a long-running
// one. cpu and memory sit directly on the container; azurerm has no nested
// "resources" block.
//
// Ported bug-for-bug: Python's env-var substitution here is narrower than
// AWS's connections.go (hardcoded postgresql:// URL format,
// relationship-driven only -- it does not use resolve_value/ResolveValue's
// general logic at all, and does not touch service.Env directly beyond
// copying it verbatim). Redis/Storage connections wired via Relationships
// would render as if they were Postgres URLs; this is a known, pre-existing
// limitation of the Python implementation, not something to silently fix
// during the port.
func containerSpecAzure(
	service *models.Service,
	app *models.Application,
	env *models.AzureEnvironment,
	connections map[string]models.Connection,
	connectionOrder []string,
) models.ContainerAppContainer {
	container := models.ContainerAppContainer{
		Name:   service.Name,
		Image:  getContainerImageAzure(service, app, env),
		CPU:    getCPUCoresAzure(service),
		Memory: getMemoryGBAzure(service),
		Args:   service.Command,
	}

	envVars := make([]models.ContainerAppEnvVar, 0, len(service.Env))
	for _, k := range shared.SortedKeys(service.Env) {
		envVars = append(envVars, models.ContainerAppEnvVar{Name: k, Value: service.Env[k]})
	}

	for _, dbName := range connectionOrder {
		conn, ok := connections[dbName]
		if !ok {
			continue
		}
		referenced := false
		for _, r := range app.Relationships {
			if r.Client == service.Name && r.Server == dbName {
				referenced = true
				break
			}
		}
		if !referenced {
			continue
		}

		// Python's f-string renders "None" literally for any unset field
		// (an f-string calls str() on its argument, and str(None) ==
		// "None") -- not an empty string. Confirmed as the actual
		// behavior, not assumed, by running the equivalent Python
		// f-string against a connection with every optional field unset
		// (2026-08-06): a bucket Connection (host+name only, no
		// username/password/port/database) produces
		// "postgresql://None:None@<host>:None/None", not
		// "postgresql://:@<host>:/". Matched here even though it reads
		// as a bug in the Python implementation (a Redis/Storage
		// connection substituted into this Postgres-shaped template
		// produces a nonsensical URL) -- ported bug-for-bug per this
		// phase's own decision to replicate current Python behavior
		// rather than silently fix it during the port.
		username := "None"
		if conn.Username != nil {
			username = *conn.Username
		}
		password := "None"
		if conn.Password != nil {
			password = *conn.Password
		}
		port := "None"
		if conn.Port != nil {
			port = strconv.Itoa(*conn.Port)
		}
		database := "None"
		if conn.Database != nil {
			database = *conn.Database
		}

		envVars = append(envVars, models.ContainerAppEnvVar{
			Name:  strings.ToUpper(dbName) + "_URL",
			Value: fmt.Sprintf("postgresql://%s:%s@%s:%s/%s", username, password, conn.Host, port, database),
		})
	}

	container.Env = envVars
	return container
}

func sortStringsAzure(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}

// getContainerImageAzure gets the container image reference, mirroring
// _get_container_image.
//
// For a built service this must resolve to the exact image
// inferContainerRegistry pushed -- the sha256 digest, not a mutable
// ":latest" tag -- so Container Apps can't pull a different image than the
// one Terraform just built.
func getContainerImageAzure(service *models.Service, app *models.Application, env *models.AzureEnvironment) string {
	if service.BuildContext != nil {
		pushKey := service.Name + "_push"
		return fmt.Sprintf(
			"${azurerm_container_registry.main.login_server}/%s@${docker_registry_image.%s.sha256_digest}",
			service.Name, pushKey,
		)
	}
	return service.Image
}

// getCPUCoresAzure converts service size or explicit CPU to cores,
// mirroring _get_cpu_cores.
func getCPUCoresAzure(service *models.Service) float64 {
	if service.CPU != nil {
		return float64(*service.CPU) / 1024.0
	}
	switch service.Size {
	case models.ServiceSizeMedium:
		return 0.5
	case models.ServiceSizeLarge:
		return 1.0
	default:
		return 0.25
	}
}

// getMemoryGBAzure converts service size or explicit memory to a GB
// string, mirroring _get_memory_gb.
func getMemoryGBAzure(service *models.Service) string {
	if service.Memory != nil {
		return strconv.Itoa(*service.Memory) + "Mi"
	}
	switch service.Size {
	case models.ServiceSizeMedium:
		return "1Gi"
	case models.ServiceSizeLarge:
		return "2Gi"
	default:
		return "0.5Gi"
	}
}

// inferScheduledJobs creates a Container Apps Job for each scheduled
// service, mirroring _infer_scheduled_jobs.
//
// A Job runs to completion on its trigger and stops, which is what a
// schedule asks for. Deploying these as Container Apps instead runs a
// nightly task continuously, and restarts one that exits as soon as it has
// finished.
// managedIdentityAzure builds the `identity` block shared by Container
// Apps and Jobs: a user-assigned identity when the environment names one,
// otherwise a system-assigned identity Azure creates and manages itself.
func managedIdentityAzure(identityID string) *models.ManagedIdentity {
	if identityID != "" {
		return &models.ManagedIdentity{Type: "UserAssigned", IdentityIDs: []string{identityID}}
	}
	return &models.ManagedIdentity{Type: "SystemAssigned"}
}

func inferScheduledJobs(
	resources *models.AzureResources,
	app *models.Application,
	env *models.AzureEnvironment,
	getName func(string) string,
	tags map[string]string,
	identityID string,
	connections map[string]models.Connection,
	connectionOrder []string,
) error {
	for i := range app.Services {
		service := &app.Services[i]
		if service.Capability != models.CapabilityContainer || service.Schedule == nil {
			continue
		}

		cronExpr, err := cronExpressionAzure(service.Schedule)
		if err != nil {
			return err
		}

		registryConfig, secretConfig := registryAuthAzure(service)

		job := models.NewContainerAppJob()
		job.Name = getName(service.Name)
		job.ResourceGroupName = env.Name
		job.Location = env.Region
		job.ContainerAppEnvironmentID = "${data.azurerm_container_app_environment.main.id}"
		job.ScheduleTriggerConfig = []models.ContainerAppJobScheduleTrigger{{CronExpression: cronExpr}}
		job.Template = []models.ContainerAppJobTemplate{
			{Container: []models.ContainerAppContainer{containerSpecAzure(service, app, env, connections, connectionOrder)}},
		}
		job.Identity = managedIdentityAzure(identityID)
		job.Secret = secretConfig
		job.Registry = registryConfig
		job.Tags = tags

		resources.ContainerAppJob[service.Name] = job
	}

	return nil
}

// inferContainerApps creates a Container App for each non-scheduled
// container service, mirroring _infer_container_apps.
func inferContainerApps(
	resources *models.AzureResources,
	app *models.Application,
	env *models.AzureEnvironment,
	getName func(string) string,
	tags map[string]string,
	identityID string,
	connections map[string]models.Connection,
	connectionOrder []string,
) {
	for i := range app.Services {
		service := &app.Services[i]
		if service.Capability != models.CapabilityContainer {
			continue
		}
		// Scheduled services are Jobs, not always-on apps.
		if service.Schedule != nil {
			continue
		}

		minReplicas := service.MinScale
		maxReplicas := service.MaxScale

		// Container Apps can scale to 0 (unlike ECS), but default to at
		// least 1 for web services.
		if service.Ingress != nil && minReplicas == 0 {
			minReplicas = 1
		}

		containerSpec := containerSpecAzure(service, app, env, connections, connectionOrder)

		var ingressConfig *models.ContainerAppIngress
		if service.Ingress != nil {
			port := 80
			if service.Ingress.Port != nil {
				port = *service.Ingress.Port
			} else if service.Port != nil {
				port = *service.Port
			}
			ingressConfig = &models.ContainerAppIngress{
				ExternalEnabled: true,
				TargetPort:      port,
				Transport:       "auto",
				TrafficWeight:   []models.ContainerAppTrafficWeight{{LatestRevision: true, Percentage: 100}},
			}
		}

		// Build scale rules. azurerm models HTTP scaling as its own
		// http_scale_rule block with a concurrent_requests string, not as
		// a generic custom rule.
		//
		// Python also checks `metric.type == "http"`, which the semantic
		// model's AutoScalingMetric.type field never actually allows
		// (Literal["cpu", "memory", "requests_per_target"]) -- dead code
		// in Python, not ported here, since there is no way to construct
		// a metric with that type in the first place.
		var httpScaleRules []models.ContainerAppHTTPScaleRule
		if service.AutoScaling != nil {
			for _, metric := range service.AutoScaling.Metrics {
				if metric.Type == models.AutoScalingMetricRequestsPerTarget {
					httpScaleRules = append(httpScaleRules, models.ContainerAppHTTPScaleRule{
						Name:               "http-rule",
						ConcurrentRequests: strconv.Itoa(int(metric.TargetValue)),
					})
				}
			}
		}

		// If no HTTP rule but has ingress, add default HTTP scaling.
		if service.Ingress != nil && len(httpScaleRules) == 0 {
			httpScaleRules = append(httpScaleRules, models.ContainerAppHTTPScaleRule{
				Name:               "http-default",
				ConcurrentRequests: "100",
			})
		}

		// Build template. Replica counts live directly on the template;
		// there is no "scale" block in the provider schema.
		template := models.ContainerAppTemplate{
			Container:     []models.ContainerAppContainer{containerSpec},
			MinReplicas:   minReplicas,
			MaxReplicas:   maxReplicas,
			HTTPScaleRule: httpScaleRules,
		}

		registryConfig, secretConfig := registryAuthAzure(service)

		containerApp := models.NewContainerApp()
		containerApp.Name = getName(service.Name)
		containerApp.ResourceGroupName = env.Name
		containerApp.ContainerAppEnvironmentID = "${data.azurerm_container_app_environment.main.id}"
		containerApp.Template = template
		containerApp.Ingress = ingressConfig
		containerApp.Identity = managedIdentityAzure(identityID)
		containerApp.Secret = secretConfig
		containerApp.Registry = registryConfig
		containerApp.Tags = tags

		resources.ContainerApp[service.Name] = containerApp
	}
}
