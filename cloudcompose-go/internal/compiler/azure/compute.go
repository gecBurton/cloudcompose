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
// secret block it needs.
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
// Container Apps and Jobs.
//
// Both take the same shape, so a scheduled task gets the same image
// resolution and the same wired-in connection strings as a long-running
// one. cpu and memory sit directly on the container; azurerm has no nested
// "resources" block.
//
// Unlike AWS's general connections.go/ResolveValue, this only wires a
// connection when a Relationship declares it, and only ever adds one
// `<SERVER>_URL` env var per relationship rather than substituting into
// service.Env's own values -- both narrower than AWS by design, tracked
// as a Priority 3 item in docs/azure-aws-parity-todo.md, not fixed here.
// What *is* fixed here (2026-08-08, see docs/azure-aws-parity-todo.md
// Priority 1 item 3): the URL scheme now matches the target's actual
// capability (postgresql://, mysql://, rediss://, or a bare
// https://<host>/<container> for object storage) instead of always
// rendering a Postgres-shaped URL regardless of target -- previously a
// Redis/Storage relationship rendered as
// "postgresql://None:None@<redis-host>:None/None", a real bug, not just
// a stylistic mismatch with AWS. Credentials are also no longer
// interpolated as plaintext: a connection with a password uses
// ContainerAppEnvVar.SecretName, pointing at the Key Vault secret
// grantManagedServicePermissions stored, rather than
// ContainerAppEnvVar.Value.
//
// getName/tags/identityID (added 2026-08-08, see
// docs/azure-aws-parity-todo.md Priority 2 items 1-2) are only used to
// wire compose secrets:/platform config: -- see
// grantServiceSecretPermissions/grantPlatformConfigPermissions.
// identityID must be the *managed-service* identity (not just any
// identity the service happens to use), since secrets/config always go
// through Key Vault the same way managed-service credentials do.
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

	envVars := make([]models.ContainerAppEnvVar, 0, len(service.Env))
	for _, k := range shared.SortedKeys(service.Env) {
		envVars = append(envVars, models.ContainerAppEnvVar{Name: k, Value: service.Env[k]})
	}

	var secrets []models.ContainerAppSecret

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
			// Credential goes through Container Apps' own secretRef
			// mechanism, resolved from Key Vault via the app's identity
			// -- never interpolated into the env var's own value. See
			// docs/azure-aws-parity-todo.md Priority 1 items 1-2. The
			// secret block itself is app/job-level, not container-level
			// (azurerm's schema puts `secret` on ContainerApp/Job, only
			// `env.secret_name` lives on the container) -- returned here
			// for the caller to merge into that level.
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

	// Compose secrets:/platform config: -- see grantServiceSecretPermissions/
	// grantPlatformConfigPermissions's own doc comments.
	secretEnvVars, secretSecrets := grantServiceSecretPermissions(resources, service, app, getName, tags, managedServiceIdentityID)
	envVars = append(envVars, secretEnvVars...)
	secrets = append(secrets, secretSecrets...)

	configEnvVars, configSecrets := grantPlatformConfigPermissions(resources, service, getName, tags, managedServiceIdentityID)
	envVars = append(envVars, configEnvVars...)
	secrets = append(secrets, configSecrets...)

	container.Env = envVars
	return container, secrets, nil
}

// connectionURLAzure renders a connection as a URL, choosing the scheme
// from the target service's capability rather than always assuming
// Postgres -- see containerSpecAzure's own doc comment for why this
// matters. Credential-bearing connections should go through
// keyVaultSecretRefFor's secretRef path instead of this function; this
// path is only reached for a connection with no stored secret (i.e. no
// password at all, or no identity was provisioned to read one from Key
// Vault because inferManagedServiceIdentity found nothing needing it --
// which shouldn't happen if grantManagedServicePermissions ran, but this
// function still renders something sane rather than panicking if it
// didn't).
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

// findServiceByNameAzure finds a service by name, mirroring
// aws/permissions.go's findServiceByName.
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

// azureConsumptionMaxCPU and azureConsumptionMaxMemoryGB are Container
// Apps' Consumption workload profile limits per container (2 vCPU / 4
// GiB), confirmed against Microsoft's own container resource-allocation
// documentation (learn.microsoft.com/azure/container-apps/containers#allocations).
// Not enforced by Terraform's own schema (a plain `number`/`string`
// with no validation) -- this is an Azure API-level constraint cloudcompose
// checks itself, added 2026-08-08 (see docs/azure-aws-parity-todo.md's
// Priority 4 size-ceiling item).
const (
	azureConsumptionMaxCPU      = 2.0
	azureConsumptionMaxMemoryGB = 4.0
)

// azureConsumptionCPUStep is the granularity Consumption-plan CPU/memory
// allocations must land on -- confirmed against the same Microsoft
// documentation as azureConsumptionMaxCPU: the vCPU/memory table there
// lists every valid combination in steps of exactly 0.25 vCPU (0.25,
// 0.5, 0.75, ... 4.0), each paired with exactly 2x that many GiB of
// memory (0.5Gi, 1.0Gi, 1.5Gi, ... 8.0Gi). This is the constraint
// azureCPUMemoryPairAzure enforces: not just "under the cap" (checked
// independently below) but "on the step and at the paired memory value",
// which the cap check alone cannot catch.
const azureConsumptionCPUStep = 0.25

// azureCPUMemoryPairAzure validates that a resolved (cpu, memoryGB) pair
// is one Container Apps' Consumption plan actually accepts.
//
// This is a real, separate constraint from either value's own ceiling
// check: Consumption requires CPU and memory to land on one exact
// matched pair from a fixed table (0.25vCPU/0.5Gi, 0.5/1.0Gi, ...,
// 2.0/4.0Gi) -- not just independently under the 2vCPU/4GiB cap.
// Terraform's schema does not enforce this (a plain number/string with
// no validation), so `terraform validate` passes regardless; only
// Azure's own API rejects an unpaired combination, at `apply` time.
// Confirmed via the compute-tuning example's own worker service
// (size: medium = 1.0 vCPU + an explicit memory: 4096 override = 4Gi):
// terraform validate accepts cpu=1, memory="4096Mi" even though 1.0vCPU
// only pairs validly with 2.0Gi -- this function is what closes that gap
// (docs/azure-aws-parity-todo.md's Priority 4 "New gap found" item).
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

// getCPUCoresAzure converts service size or explicit CPU to cores.
// Size-derived values come from
// shared.SizeMappings (the same table AWS uses, converted from ECS CPU
// units to vCPU cores) rather than a separately hardcoded table, fixing
// a real, already-drifted duplicate: Azure's own table previously
// defined medium as 0.5 vCPU where AWS's medium is 1.0 vCPU -- half,
// not matching, despite using the same size name (see
// docs/azure-aws-parity-todo.md's Priority 4 size-table-consolidation
// item). Returns an error if the result would exceed the Consumption
// tier's per-container limit -- see azureConsumptionMaxCPU's own
// comment for why this wasn't previously reachable (the old table
// topped out at 1.0 vCPU for "large", comfortably under the 2 vCPU cap;
// deriving from AWS's table directly reaches 4.0 vCPU for "large",
// which is over it) and needed the rejection added at the same time as
// the table consolidation, not as a separate step.
//
// This only checks CPU's own ceiling, not whether the returned value
// pairs validly with whatever getMemoryGBAzure resolves separately --
// see resolveContainerResourcesAzure, which is what callers should use
// so both values are validated together as the pair Azure actually
// requires.
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
// string. See getCPUCoresAzure's own doc
// comment for the size-table consolidation and ceiling-rejection this
// mirrors on the memory side, and resolveContainerResourcesAzure for why
// this alone doesn't guarantee a valid Consumption-plan pairing.
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
// getMemoryGBAzure returns into GiB, so resolveContainerResourcesAzure
// can validate it against azureCPUMemoryPairAzure without either
// function needing to know the other's string format.
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
// validates them as the single pair Consumption plan requires, not as
// two independently-under-the-cap values -- see azureCPUMemoryPairAzure
// for why that distinction is real, not pedantic.
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

// inferScheduledJobs creates a Container Apps Job for each scheduled
// service.
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

// defaultAutoScalingConfigAzure mirrors aws/compute.go's own
// defaultAutoScalingConfig(): CPU 70%/Memory 80%, matching
// shared.AutoScalingCPUTarget/AutoScalingMemoryTarget. Used whenever a
// service declares max_scale>1 but no explicit auto_scaling block --
// see inferContainerApps's own comment on this for why it's needed at
// all (without it, such a service got zero scale rules on Azure, unlike
// AWS which has applied this default since the original port).
// ScaleInCooldown/ScaleOutCooldown aren't included: those are
// AppAutoscalingPolicy-specific fields with no Container Apps
// equivalent (KEDA's own cooldownPeriod/pollingInterval live on the
// template, not per-rule, and aren't wired here -- see
// docs/azure-aws-parity-todo.md for other Azure/AWS granularity gaps).
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
// particular service has a Relationship to a database/cache/storage
// connection (see permissions.go's inferManagedServiceIdentity), falling
// back to identityID (env.UserAssignedIdentityID, or "" for
// system-assigned) for every other service. Without this per-service
// check, every service in the app would switch to UserAssigned the
// moment *any* service needed the managed-service identity -- confirmed
// as a real, not theoretical, divergence while regenerating Azure golden
// fixtures for this fix (2026-08-08): the flask example's "frontend"
// service, which has no relationship to "db" at all, switched identity
// types for no reason before this function existed.
func identityForService(
	app *models.Application,
	service *models.Service,
	identityID, managedServiceIdentityID string,
	connections map[string]models.Connection,
) string {
	if managedServiceIdentityID == "" {
		return identityID
	}
	if len(service.Secrets) > 0 || len(service.Config) > 0 {
		return managedServiceIdentityID
	}
	for _, r := range app.Relationships {
		if r.Client != service.Name {
			continue
		}
		if _, ok := connections[r.Server]; ok {
			return managedServiceIdentityID
		}
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
		job.ContainerAppEnvironmentID = "${data.azurerm_container_app_environment.main.id}"
		job.ScheduleTriggerConfig = []models.ContainerAppJobScheduleTrigger{{CronExpression: cronExpr}}
		job.Template = []models.ContainerAppJobTemplate{
			{Container: []models.ContainerAppContainer{containerSpec}},
		}
		job.Identity = managedIdentityAzure(identityForService(app, service, identityID, managedServiceIdentityID, connections))
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
		// CPU/Memory as generic custom_scale_rule (KEDA) blocks -- not a
		// single uniform shape the way AWS's AppAutoscalingPolicy is.
		//
		// Note that the semantic model's AutoScalingMetric.type field
		// never actually allows "http" (Literal["cpu", "memory",
		// "requests_per_target"]), so there is no way to construct a
		// metric with that type in the first place -- an explicit check
		// for it here would be dead code.
		//
		// CPU/Memory custom_scale_rule support and the
		// MaxScale>1-with-no-explicit-policy default added 2026-08-08
		// (see docs/azure-aws-parity-todo.md Priority 2 items 5-6):
		// previously only requests_per_target was handled at all, and a
		// service with max_scale>1 but no ingress and no explicit
		// auto_scaling block got zero scale rules -- min/max replicas
		// were honored, but nothing ever drove scaling past 1. Mirrors
		// aws/compute.go's defaultAutoScalingConfig(): applies whenever
		// service.AutoScaling is nil, regardless of whether ingress is
		// present (unlike the http-default rule below, which only
		// applies when there's ingress but no explicit policy).
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
		containerApp.ContainerAppEnvironmentID = "${data.azurerm_container_app_environment.main.id}"
		containerApp.Template = template
		containerApp.Ingress = ingressConfig
		containerApp.Identity = managedIdentityAzure(identityForService(app, service, identityID, managedServiceIdentityID, connections))
		containerApp.Secret = secretConfig
		containerApp.Registry = registryConfig
		containerApp.Tags = tags

		resources.ContainerApp[service.Name] = containerApp
	}
	return nil
}
