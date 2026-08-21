package azure

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/gecburton/cloudcompose/internal/compiler/shared"
	"github.com/gecburton/cloudcompose/internal/models"
)

// cronExpressionAzure renders a cloud-neutral schedule as the standard
// 5-field cron Azure wants.
//
// Azure has no rate dialect, so an interval is expressed as the cron that
// means the same thing. Intervals that cron cannot express (e.g. every 7
// hours) are rejected rather than silently rounded.
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
// secret block it needs.
//
// Uses username/password rather than managed-identity auth: a
// system-assigned identity's principal_id doesn't exist until the
// Container App/Job is created, but the create itself needs to pull the
// image, so a role assignment can't happen first. ACR's admin credentials
// are available as soon as the registry exists, avoiding that ordering
// problem.
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
// Container Apps and Jobs.
//
// cpu and memory sit directly on the container; azurerm has no nested
// "resources" block. Only wires a connection into a `<SERVER>_URL` env
// var when a Relationship declares it, using the URL scheme matching the
// target's capability (postgresql://, mysql://, rediss://, or a bare
// https://<host>/<container> for object storage). Credentials with a
// password use ContainerAppEnvVar.SecretName, pointing at the Key Vault
// secret grantManagedServicePermissions stored, rather than a plaintext
// value.
//
// identityID must be the managed-service identity (not just any identity
// the service uses), since secrets/config always go through Key Vault.
func containerSpecAzure(
	service *models.Service,
	app *models.Application,
	env *models.AzureEnvironment,
	resources *models.AzureResources,
	connections map[string]models.Connection,
	connectionOrder []string,
	getName func(string) string,
	tags map[string]string,
	managedServiceIdentityID string,
) (models.ContainerAppContainer, []models.ContainerAppSecret, error) {
	cpu, memory, err := resolveContainerResourcesAzure(service)
	if err != nil {
		return models.ContainerAppContainer{}, nil, err
	}

	container := models.ContainerAppContainer{
		Name:   service.Name,
		Image:  getContainerImageAzure(service, app, env),
		CPU:    cpu,
		Memory: memory,
		Args:   service.Command,
	}

	if service.Ingress != nil {
		liveness, startup, err := healthProbesAzure(service)
		if err != nil {
			return models.ContainerAppContainer{}, nil, err
		}
		if liveness != nil {
			container.LivenessProbe = []models.ContainerAppProbe{*liveness}
		}
		if startup != nil {
			container.StartupProbe = []models.ContainerAppProbe{*startup}
		}
	}

	envVars := make([]models.ContainerAppEnvVar, 0, len(service.Env))
	var secrets []models.ContainerAppSecret
	for _, k := range shared.SortedKeys(service.Env) {
		envVar, secret := resolveEnvVarAzure(resources, service.Name, k, service.Env[k], connections, connectionOrder, getName, tags, managedServiceIdentityID)
		envVars = append(envVars, envVar)
		if secret != nil {
			secrets = append(secrets, *secret)
		}
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

		server := findServiceByNameAzure(app, dbName)
		var capability models.Capability
		if server != nil {
			capability = server.Capability
		}

		envVarName := strings.ToUpper(dbName) + "_URL"

		if secretRef := keyVaultSecretRefFor(resources, dbName); secretRef != "" {
			// Resolved from Key Vault via the app's identity, never
			// interpolated as a plaintext value. The secret block is
			// app/job-level, not container-level, so it's returned here
			// for the caller to merge in.
			secretName := dbName + "-url"
			secrets = append(secrets, models.ContainerAppSecret{
				Name:             secretName,
				KeyVaultSecretID: secretRef,
				Identity:         managedServiceIdentityRef(resources),
			})
			envVars = append(envVars, models.ContainerAppEnvVar{Name: envVarName, SecretName: secretName})
			continue
		}

		envVars = append(envVars, models.ContainerAppEnvVar{
			Name:  envVarName,
			Value: connectionURLAzure(capability, &conn),
		})
	}

	// Compose secrets:/platform config:.
	secretEnvVars, secretSecrets := grantServiceSecretPermissions(resources, service, app, getName, tags, managedServiceIdentityID)
	envVars = append(envVars, secretEnvVars...)
	secrets = append(secrets, secretSecrets...)

	configEnvVars, configSecrets := grantPlatformConfigPermissions(resources, service, getName, tags, managedServiceIdentityID)
	envVars = append(envVars, configEnvVars...)
	secrets = append(secrets, configSecrets...)

	container.Env = envVars
	return container, secrets, nil
}

// resolveEnvVarAzure substitutes a real managed-service connection into
// one authored `environment:` value, built on shared.ResolveValue.
//
// This is additive to containerSpecAzure's own <SERVER>_URL synthesis:
// an app can consume either the synthesized <SERVER>_URL or its own
// authored value (e.g. `DATABASE_URL: postgres://db:5432/app` or
// `DATABASE_HOST: db`), which needs substituting to the real managed
// endpoint since the literal compose value points at a local hostname
// unreachable once the service becomes managed.
//
// A confidential resolution (the value now carries a real password) is
// stored in Key Vault, one secret per (service, env-var-name), since two
// services' own same-named env var referencing the same managed service
// still need their own secret.
func resolveEnvVarAzure(
	resources *models.AzureResources,
	serviceName, varName, value string,
	connections map[string]models.Connection,
	connectionOrder []string,
	getName func(string) string,
	tags map[string]string,
	managedServiceIdentityID string,
) (models.ContainerAppEnvVar, *models.ContainerAppSecret) {
	resolved := shared.ResolveValue(value, connections, connectionOrder)
	if !resolved.Confidential {
		return models.ContainerAppEnvVar{Name: varName, Value: resolved.Value}, nil
	}

	// A confidential value with no identity to grant Key Vault access is
	// a real gap in setup, not a case with a sane fallback: falling back
	// to a plaintext env var here would silently leak a credential into
	// Terraform state, worse than failing loudly.
	if managedServiceIdentityID == "" {
		return models.ContainerAppEnvVar{Name: varName, Value: resolved.Value}, nil
	}

	// azurerm_key_vault_secret's name may only contain alphanumeric
	// characters and dashes, so varSlug replaces underscores with
	// dashes for the Key Vault secret's Name and the Container App
	// secret block's Name. secretKey (the Terraform resource
	// identifier) keeps underscores.
	varSlug := strings.ReplaceAll(strings.ToLower(varName), "_", "-")
	secretKey := fmt.Sprintf("%s_%s_url", serviceName, strings.ToLower(varName))
	secret := models.NewKeyVaultSecret()
	secret.Name = getName(fmt.Sprintf("%s-%s", serviceName, varSlug))
	secret.KeyVaultID = "${azurerm_key_vault.main.id}"
	secret.Value = resolved.Value
	resources.KeyVaultSecret[secretKey] = secret

	// Granted here too, not just by grantManagedServicePermissions's
	// Relationships-driven pass: a service can reference a managed
	// service by URL without also declaring depends_on: for it. The
	// underlying map write is naturally idempotent if both paths run.
	grantedKeyVault := false
	grantKeyVaultAccessOnce(resources, &grantedKeyVault, principalIDRefForIdentity())

	secretName := fmt.Sprintf("%s-%s-url", serviceName, varSlug)
	return models.ContainerAppEnvVar{Name: varName, SecretName: secretName},
		&models.ContainerAppSecret{
			Name:             secretName,
			KeyVaultSecretID: fmt.Sprintf("${azurerm_key_vault_secret.%s.versionless_id}", secretKey),
			Identity:         managedServiceIdentityID,
		}
}

// connectionURLAzure renders a connection as a URL, choosing the scheme
// from the target service's capability. Only reached for a connection
// with no stored secret; credential-bearing connections should go
// through keyVaultSecretRefFor instead.
func connectionURLAzure(capability models.Capability, conn *models.Connection) string {
	switch capability {
	case models.CapabilityCache:
		scheme := "redis"
		password := ""
		if conn.Password != nil {
			password = ":" + *conn.Password + "@"
		}
		port := ""
		if conn.Port != nil {
			port = fmt.Sprintf(":%d", *conn.Port)
		}
		return fmt.Sprintf("%s://%s%s%s", scheme, password, conn.Host, port)

	case models.CapabilityObjectStorage:
		return conn.Host

	default: // database (postgres or mysql)
		scheme := "postgresql"
		if conn.Port != nil && *conn.Port == shared.DefaultPortMySQL {
			scheme = "mysql"
		}
		username := ""
		password := ""
		if conn.Username != nil {
			username = *conn.Username
		}
		if conn.Password != nil {
			password = ":" + *conn.Password
		}
		port := ""
		if conn.Port != nil {
			port = fmt.Sprintf(":%d", *conn.Port)
		}
		database := ""
		if conn.Database != nil {
			database = "/" + *conn.Database
		}
		return fmt.Sprintf("%s://%s%s@%s%s%s", scheme, username, password, conn.Host, port, database)
	}
}

// findServiceByNameAzure finds a service by name.
func findServiceByNameAzure(app *models.Application, name string) *models.Service {
	for i := range app.Services {
		if app.Services[i].Name == name {
			return &app.Services[i]
		}
	}
	return nil
}

func sortStringsAzure(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}

// getContainerImageAzure gets the container image reference.
//
// For a built service this must resolve to the exact sha256 digest
// inferContainerRegistry pushed, not a mutable ":latest" tag, so
// Container Apps can't pull a different image than the one Terraform
// just built.
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

// azureConsumptionMaxCPU and azureConsumptionMaxMemoryGB are Container
// Apps' Consumption workload profile limits per container (2 vCPU / 4
// GiB). Not enforced by Terraform's schema, so cloudcompose checks it
// itself.
const (
	azureConsumptionMaxCPU      = 2.0
	azureConsumptionMaxMemoryGB = 4.0
)

// azureConsumptionCPUStep is the granularity Consumption-plan CPU/memory
// allocations must land on: valid combinations step in 0.25 vCPU
// increments, each paired with exactly 2x that many GiB of memory.
const azureConsumptionCPUStep = 0.25

// azureCPUMemoryPairAzure validates that a resolved (cpu, memoryGB) pair
// is one Container Apps' Consumption plan actually accepts: CPU and
// memory must land on one exact matched pair from a fixed table, not
// just independently under the cap. Terraform's schema doesn't enforce
// this; only the real API rejects an unpaired combination, at apply
// time.
func azureCPUMemoryPairAzure(serviceName string, cpu, memoryGB float64) error {
	steps := cpu / azureConsumptionCPUStep
	if steps != math.Round(steps) {
		return fmt.Errorf(
			"service %q resolves to %g vCPU, which is not a multiple of %g vCPU; Azure Container Apps' Consumption plan only accepts CPU in %g vCPU increments",
			serviceName, cpu, azureConsumptionCPUStep, azureConsumptionCPUStep,
		)
	}
	wantMemoryGB := 2 * cpu
	if memoryGB != wantMemoryGB {
		return fmt.Errorf(
			"service %q resolves to %g vCPU + %gGi memory, which Azure Container Apps' Consumption plan does not accept; %g vCPU must be paired with exactly %gGi memory (CPU and memory must come from one of the plan's fixed pairs, not be set independently) -- see learn.microsoft.com/azure/container-apps/containers#vcpu-and-memory-allocation-requirements",
			serviceName, cpu, memoryGB, cpu, wantMemoryGB,
		)
	}
	return nil
}

// getCPUCoresAzure converts service size or explicit CPU to cores, using
// shared.SizeMappings (converted from ECS CPU units to vCPU cores).
// Returns an error if the result exceeds the Consumption tier's
// per-container limit.
//
// This only checks CPU's own ceiling; use resolveContainerResourcesAzure
// to validate the CPU/memory pair together as Azure requires.
func getCPUCoresAzure(service *models.Service) (float64, error) {
	if service.CPU != nil {
		cores := float64(*service.CPU) / 1024.0
		if cores > azureConsumptionMaxCPU {
			return 0, fmt.Errorf(
				"service %q requests %g vCPU, which exceeds Azure Container Apps' Consumption tier limit of %g vCPU per container",
				service.Name, cores, azureConsumptionMaxCPU,
			)
		}
		return cores, nil
	}
	mapping, ok := shared.SizeMappings[string(service.Size)]
	if !ok {
		mapping = shared.SizeMappings["small"]
	}
	cores := float64(mapping.CPU) / 1024.0
	if cores > azureConsumptionMaxCPU {
		return 0, fmt.Errorf(
			"service %q has size %q (%g vCPU), which exceeds Azure Container Apps' Consumption tier limit of %g vCPU per container; use an explicit cpu: override within the limit, or a dedicated workload profile (not yet supported by cloudcompose)",
			service.Name, service.Size, cores, azureConsumptionMaxCPU,
		)
	}
	return cores, nil
}

// getMemoryGBAzure converts service size or explicit memory to a GB
// string, applying the same ceiling check as getCPUCoresAzure. Use
// resolveContainerResourcesAzure to validate the CPU/memory pair
// together.
func getMemoryGBAzure(service *models.Service) (string, error) {
	if service.Memory != nil {
		gb := float64(*service.Memory) / 1024.0
		if gb > azureConsumptionMaxMemoryGB {
			return "", fmt.Errorf(
				"service %q requests %dMi memory, which exceeds Azure Container Apps' Consumption tier limit of %gGi per container",
				service.Name, *service.Memory, azureConsumptionMaxMemoryGB,
			)
		}
		return strconv.Itoa(*service.Memory) + "Mi", nil
	}
	mapping, ok := shared.SizeMappings[string(service.Size)]
	if !ok {
		mapping = shared.SizeMappings["small"]
	}
	gb := float64(mapping.Memory) / 1024.0
	if gb > azureConsumptionMaxMemoryGB {
		return "", fmt.Errorf(
			"service %q has size %q (%gGi memory), which exceeds Azure Container Apps' Consumption tier limit of %gGi per container; use an explicit memory: override within the limit, or a dedicated workload profile (not yet supported by cloudcompose)",
			service.Name, service.Size, gb, azureConsumptionMaxMemoryGB,
		)
	}
	return fmt.Sprintf("%gGi", gb), nil
}

// memoryGBFromContainerAppsString parses back the "<N>Mi"/"<N>Gi" shape
// getMemoryGBAzure returns into GiB.
func memoryGBFromContainerAppsString(memory string) (float64, error) {
	switch {
	case strings.HasSuffix(memory, "Mi"):
		mi, err := strconv.ParseFloat(strings.TrimSuffix(memory, "Mi"), 64)
		if err != nil {
			return 0, fmt.Errorf("invalid memory string %q: %w", memory, err)
		}
		return mi / 1024.0, nil
	case strings.HasSuffix(memory, "Gi"):
		gi, err := strconv.ParseFloat(strings.TrimSuffix(memory, "Gi"), 64)
		if err != nil {
			return 0, fmt.Errorf("invalid memory string %q: %w", memory, err)
		}
		return gi, nil
	default:
		return 0, fmt.Errorf("memory string %q has neither an Mi nor Gi suffix", memory)
	}
}

// resolveContainerResourcesAzure resolves a service's CPU/memory and
// validates them as the single pair the Consumption plan requires, not
// as two independently-under-the-cap values.
func resolveContainerResourcesAzure(service *models.Service) (float64, string, error) {
	cpu, err := getCPUCoresAzure(service)
	if err != nil {
		return 0, "", err
	}
	memory, err := getMemoryGBAzure(service)
	if err != nil {
		return 0, "", err
	}
	memoryGB, err := memoryGBFromContainerAppsString(memory)
	if err != nil {
		return 0, "", err
	}
	if err := azureCPUMemoryPairAzure(service.Name, cpu, memoryGB); err != nil {
		return 0, "", err
	}
	return cpu, memory, nil
}

// azureProbeIntervalSeconds and azureProbeMaxFailureCount are the
// interval/threshold used when expressing StartupGracePeriod as a
// startup_probe budget. Their product defines the largest
// StartupGracePeriod this mapping can express (240*10 = 2400s = 40
// minutes) before rejecting outright.
const (
	azureProbeIntervalSeconds = 10
	azureProbeMaxFailureCount = 240
)

// healthProbesAzure builds Container Apps' liveness_probe/startup_probe
// values from a service's ingress health check and StartupGracePeriod.
// Returns single pointers rather than slices; the caller wraps each into
// a one-element slice.
//
// Only liveness_probe and startup_probe are built, not readiness_probe:
// this codebase has no concept of "ready but not yet healthy" distinct
// from "healthy".
//
// Container Apps has no direct equivalent of a load-balancer-level
// "ignore failures for N seconds after start"; startup_probe's failure
// budget approximates it. 120s becomes 12 failures at a 10s interval;
// StartupGracePeriod=0 (or nil) omits startup_probe entirely.
//
// HealthCheck.Type (http/tcp) maps directly to Transport (HTTP/TCP);
// Path is only meaningful for HTTP, so it's left empty for TCP.
func healthProbesAzure(service *models.Service) (liveness, startup *models.ContainerAppProbe, err error) {
	ingress := service.Ingress
	port := 80
	if ingress.Port != nil {
		port = *ingress.Port
	} else if service.Port != nil {
		port = *service.Port
	}

	transport := "HTTP"
	path := ingress.HealthCheck.Path
	if ingress.HealthCheck.Type == models.HealthCheckTypeTCP {
		transport = "TCP"
		path = ""
	}

	liveness = &models.ContainerAppProbe{
		Transport: transport,
		Port:      port,
		Path:      path,
	}

	if service.StartupGracePeriod == nil || *service.StartupGracePeriod <= 0 {
		return liveness, nil, nil
	}

	failureCount := (*service.StartupGracePeriod + azureProbeIntervalSeconds - 1) / azureProbeIntervalSeconds
	if failureCount > azureProbeMaxFailureCount {
		return nil, nil, fmt.Errorf(
			"service %q has startup_grace_period=%ds, which needs %d failures at a %ds interval to express as a Container Apps startup_probe budget -- exceeding the %ds (%d failures) this mapping supports; reduce startup_grace_period or split the slow-starting work out of the container's own startup path",
			service.Name, *service.StartupGracePeriod, failureCount, azureProbeIntervalSeconds,
			azureProbeMaxFailureCount*azureProbeIntervalSeconds, azureProbeMaxFailureCount,
		)
	}

	startup = &models.ContainerAppProbe{
		Transport:             transport,
		Port:                  port,
		Path:                  path,
		IntervalSeconds:       azureProbeIntervalSeconds,
		FailureCountThreshold: failureCount,
	}
	return liveness, startup, nil
}

// inferScheduledJobs creates a Container Apps Job for each scheduled
// service: a Job runs to completion on its trigger and stops, rather
// than running continuously and restarting once it exits.

// managedIdentityAzure builds the `identity` block shared by Container
// Apps and Jobs: a user-assigned identity when the environment names one,
// otherwise a system-assigned identity Azure creates and manages itself.
func managedIdentityAzure(identityID string) *models.ManagedIdentity {
	if identityID != "" {
		return &models.ManagedIdentity{Type: "UserAssigned", IdentityIDs: []string{identityID}}
	}
	return &models.ManagedIdentity{Type: "SystemAssigned"}
}

// defaultAutoScalingConfigAzure returns CPU 70%/Memory 80% scaling,
// matching shared.AutoScalingCPUTarget/AutoScalingMemoryTarget. Used
// whenever a service declares max_scale>1 but no explicit auto_scaling
// block.
func defaultAutoScalingConfigAzure() *models.AutoScalingConfig {
	return &models.AutoScalingConfig{
		Metrics: []models.AutoScalingMetric{
			{Type: models.AutoScalingMetricCPU, TargetValue: shared.AutoScalingCPUTarget},
			{Type: models.AutoScalingMetricMemory, TargetValue: shared.AutoScalingMemoryTarget},
		},
	}
}

// identityForService picks which identity a specific service's Container
// App/Job should use: the app-wide managed-service identity only if this
// service actually references a database/cache/storage connection,
// falling back to identityID otherwise. Without this per-service check,
// every service would switch to UserAssigned the moment any service
// needed the managed-service identity.
//
// Checks actual env-var usage (referenced), not app.Relationships
// (depends_on:) directly: a service could depends_on: db purely for
// startup ordering, without referencing it in an env var.
func identityForService(
	service *models.Service,
	identityID, managedServiceIdentityID string,
	referenced map[string]map[string]bool,
) string {
	if managedServiceIdentityID == "" {
		return identityID
	}
	if len(service.Secrets) > 0 || len(service.Config) > 0 {
		return managedServiceIdentityID
	}
	if len(referenced[service.Name]) > 0 {
		return managedServiceIdentityID
	}
	return identityID
}

func inferScheduledJobs(
	resources *models.AzureResources,
	app *models.Application,
	env *models.AzureEnvironment,
	getName func(string) string,
	tags map[string]string,
	identityID, managedServiceIdentityID string,
	connections map[string]models.Connection,
	connectionOrder []string,
	referenced map[string]map[string]bool,
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
		containerSpec, connSecrets, err := containerSpecAzure(service, app, env, resources, connections, connectionOrder, getName, tags, managedServiceIdentityID)
		if err != nil {
			return err
		}
		secretConfig = append(secretConfig, connSecrets...)

		job := models.NewContainerAppJob()
		job.Name = getName(service.Name)
		job.ResourceGroupName = env.Name
		job.Location = env.Region
		job.ContainerAppEnvironmentID = "${azurerm_container_app_environment.main.id}"
		job.ScheduleTriggerConfig = []models.ContainerAppJobScheduleTrigger{{CronExpression: cronExpr}}
		job.Template = []models.ContainerAppJobTemplate{
			{Container: []models.ContainerAppContainer{containerSpec}},
		}
		job.Identity = managedIdentityAzure(identityForService(service, identityID, managedServiceIdentityID, referenced))
		job.Secret = secretConfig
		job.Registry = registryConfig
		job.Tags = tags

		resources.ContainerAppJob[service.Name] = job
	}

	return nil
}

// inferContainerApps creates a Container App for each non-scheduled
// container service.
func inferContainerApps(
	resources *models.AzureResources,
	app *models.Application,
	env *models.AzureEnvironment,
	getName func(string) string,
	tags map[string]string,
	identityID, managedServiceIdentityID string,
	connections map[string]models.Connection,
	connectionOrder []string,
	referenced map[string]map[string]bool,
) error {
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

		containerSpec, connSecrets, err := containerSpecAzure(service, app, env, resources, connections, connectionOrder, getName, tags, managedServiceIdentityID)
		if err != nil {
			return err
		}

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
		// http_scale_rule block with a concurrent_requests string, and
		// CPU/Memory as generic custom_scale_rule (KEDA) blocks.
		autoScaling := service.AutoScaling
		if autoScaling == nil && maxReplicas > 1 {
			autoScaling = defaultAutoScalingConfigAzure()
		}

		var httpScaleRules []models.ContainerAppHTTPScaleRule
		var customScaleRules []models.ContainerAppCustomScaleRule
		if autoScaling != nil {
			for _, metric := range autoScaling.Metrics {
				switch metric.Type {
				case models.AutoScalingMetricRequestsPerTarget:
					httpScaleRules = append(httpScaleRules, models.ContainerAppHTTPScaleRule{
						Name:               "http-rule",
						ConcurrentRequests: strconv.Itoa(int(metric.TargetValue)),
					})
				case models.AutoScalingMetricCPU, models.AutoScalingMetricMemory:
					customScaleRules = append(customScaleRules, models.ContainerAppCustomScaleRule{
						Name:           string(metric.Type) + "-rule",
						CustomRuleType: string(metric.Type),
						Metadata: map[string]string{
							"type":  "Utilization",
							"value": strconv.Itoa(int(metric.TargetValue)),
						},
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
			Container:       []models.ContainerAppContainer{containerSpec},
			MinReplicas:     minReplicas,
			MaxReplicas:     maxReplicas,
			HTTPScaleRule:   httpScaleRules,
			CustomScaleRule: customScaleRules,
		}

		registryConfig, secretConfig := registryAuthAzure(service)
		secretConfig = append(secretConfig, connSecrets...)

		containerApp := models.NewContainerApp()
		containerApp.Name = getName(service.Name)
		containerApp.ResourceGroupName = env.Name
		containerApp.ContainerAppEnvironmentID = "${azurerm_container_app_environment.main.id}"
		containerApp.Template = template
		containerApp.Ingress = ingressConfig
		containerApp.Identity = managedIdentityAzure(identityForService(service, identityID, managedServiceIdentityID, referenced))
		containerApp.Secret = secretConfig
		containerApp.Registry = registryConfig
		containerApp.Tags = tags

		resources.ContainerApp[service.Name] = containerApp
	}
	return nil
}
