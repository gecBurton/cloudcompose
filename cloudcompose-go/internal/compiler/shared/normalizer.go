package shared

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/gecburton/cloudcompose/internal/models"
)

func ParseSchedule(raw string) (models.Schedule, error) {
	text := strings.TrimSpace(raw)

	ratePattern := regexp.MustCompile(`^(?:rate\(\s*|every\s+)(?:(\d+)\s+)?(minute|hour|day)s?\s*\)?$`)
	if matches := ratePattern.FindStringSubmatch(strings.ToLower(text)); matches != nil {
		value := 1
		if matches[1] != "" {
			fmt.Sscanf(matches[1], "%d", &value)
		}
		unit := matches[2] + "s"
		return &models.RateSchedule{
			Kind:  models.ScheduleKindRate,
			Value: value,
			Unit:  models.RateUnit(unit),
		}, nil
	}

	cronWrapperPattern := regexp.MustCompile(`^cron\(\s*(.*?)\s*\)$`)
	var fields []string
	if matches := cronWrapperPattern.FindStringSubmatch(text); matches != nil {
		fields = strings.Split(matches[1], " ")
	} else {
		fields = strings.Split(text, " ")
	}

	if len(fields) == 6 {
		fields = fields[:5]
	}
	if len(fields) != 5 {
		return nil, fmt.Errorf("schedule %q is not a 5-field cron expression or an interval like \"every 1 hour\"", raw)
	}

	for i, f := range fields {
		if f == "?" {
			fields[i] = "*"
		}
	}

	return &models.CronSchedule{
		Kind:       models.ScheduleKindCron,
		Expression: strings.Join(fields, " "),
	}, nil
}

func InferCapability(image string) string {
	reference := strings.ToLower(strings.Split(image, "@")[0])
	parts := strings.Split(reference, "/")
	var segments []string

	if len(parts) > 1 && (strings.Contains(parts[0], ".") || parts[0] == "localhost") {
		segments = parts[1:]
	} else {
		segments = parts
	}

	for i, seg := range segments {
		if idx := strings.Index(seg, ":"); idx != -1 {
			segments[i] = seg[:idx]
		}
	}

	for capability, known := range CapabilityImages {
		for _, segment := range segments {
			for _, knownName := range known {
				if segment == knownName {
					return capability
				}
			}
		}
	}

	return string(models.CapabilityContainer)
}

func DatabaseName(appName, serviceName string, environment map[string]string) string {
	for _, variable := range DatabaseNameVariables {
		if stated, ok := environment[variable]; ok && stated != "" {
			return SanitizeDatabaseName(stated)
		}
	}

	var compound string
	if appName != "" {
		compound = appName + "_" + serviceName
	} else {
		compound = serviceName
	}
	return SanitizeDatabaseName(compound)
}

func SanitizeDatabaseName(raw string) string {
	lower := strings.ToLower(raw)
	var cleaned strings.Builder
	for _, c := range lower {
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') {
			cleaned.WriteRune(c)
		} else {
			cleaned.WriteRune('_')
		}
	}
	result := strings.TrimLeft(cleaned.String(), "0123456789_")
	if result == "" {
		result = "app"
	}
	if len(result) > DatabaseNameMaxLength {
		result = result[:DatabaseNameMaxLength]
	}
	return result
}

func RejectPersistentVolumes(name string, service *models.ComposeService, capability string) error {
	if capability != string(models.CapabilityContainer) {
		return nil
	}

	named := make([]string, 0)
	for _, volume := range service.Volumes {
		if source := NamedVolumeSource(volume); source != "" {
			named = append(named, source)
		}
	}
	sort.Strings(named)
	unique := uniqueStrings(named)

	if len(unique) == 0 {
		return nil
	}

	return fmt.Errorf("service %q mounts named volume(s) %s. Cloud Compose Compiler cannot provide a persistent filesystem, and running the service without one would lose whatever is written there on every restart. Use a `minio` service for object storage, or drop the volume if the path only needs scratch space, which the task already has", name, strings.Join(unique, ", "))
}

// NamedVolumeSource returns the name of the volume a mount refers to, or ""
// if the mount is local-only (a bind mount or anonymous volume) and
// therefore not something RejectPersistentVolumes needs to flag.
//
// Takes models.VolumeDefinition directly rather than interface{}: every
// volume entry parser.go produces is already this concrete type, since
// compose-go itself normalizes both short-form ("db-data:/data") and
// long-form volume syntax into one struct shape before the loader returns.
// A prior version of this function type-switched on interface{} expecting
// either a bare string or this struct, and matched neither in production —
// what actually flows through parser.go's Volumes field is compose-go's own
// types.ServiceVolumeConfig, converted here rather than left as a look-alike
// type nothing ever matches.
func NamedVolumeSource(volume models.VolumeDefinition) string {
	if volume.Type != "volume" {
		return ""
	}
	// Belt and suspenders: compose-go's own Type field already distinguishes
	// a bind mount from a named volume, and a real named volume's Source is
	// always a bare volume name, never a path — so this loop should never
	// actually match anything once Type == "volume" is confirmed. Kept
	// rather than removed: it costs nothing, and it is the only thing
	// standing between a future compose-go behavior change and this
	// silently returning a path as if it were a volume name again.
	source := volume.Source
	for _, prefix := range BindSourcePrefixes {
		if strings.HasPrefix(source, prefix) {
			return ""
		}
	}
	return source
}

func NetworkSegmentsFor(name string, service *models.ComposeService, reserved int) ([]string, error) {
	segments := make([]string, 0)
	if len(service.Networks) > 0 {
		for netName := range service.Networks {
			segments = append(segments, netName)
		}
		sort.Strings(segments)
	} else {
		segments = []string{DefaultNetworkName}
	}

	if len(segments)+reserved > MaxNetworksPerService {
		return nil, fmt.Errorf("service %q joins %d network segments (%s). At most %d are supported here, because each becomes a security group (AWS) or equivalent isolation mechanism and clouds have limits on attachments", name, len(segments), strings.Join(segments, ", "), MaxNetworksPerService-reserved)
	}

	return segments, nil
}

func RejectUnsupportedNetworks(app *models.ComposeApplication) error {
	external := make([]string, 0)
	for name, definition := range app.Networks {
		if definition != nil && definition.External {
			external = append(external, name)
		}
	}
	sort.Strings(external)

	if len(external) == 0 {
		return nil
	}

	return fmt.Errorf("networks %s are declared external. Cloud Compose Compiler cannot map a network it does not create to a security group", strings.Join(external, ", "))
}

func RejectOverlappingPaths(services []models.Service) error {
	seen := make(map[string]string)
	for _, service := range services {
		if service.Ingress == nil {
			continue
		}
		path := service.Ingress.Path
		if existing, ok := seen[path]; ok {
			return fmt.Errorf("services %q and %q both serve %q. Give each ingress a distinct path", existing, service.Name, path)
		}
		seen[path] = service.Name
	}
	return nil
}

func uniqueStrings(slice []string) []string {
	seen := make(map[string]bool)
	result := make([]string, 0)
	for _, s := range slice {
		if !seen[s] {
			seen[s] = true
			result = append(result, s)
		}
	}
	return result
}

func Normalize(composeApp *models.ComposeApplication, projectName string) (*models.Application, error) {
	semanticServices := make([]models.Service, 0)
	relationships := make([]models.Relationship, 0)

	if err := RejectUnsupportedNetworks(composeApp); err != nil {
		return nil, err
	}

	// Go map iteration order is randomized per the language spec, so
	// iterating composeApp.Services directly would make Normalize's own
	// output order nondeterministic across runs on identical input.
	// Determinism is a stated project invariant (output must be
	// byte-identical for the same input), so the iteration order has to
	// be fixed independently of whatever order the map happens to hand
	// keys back in.
	serviceNames := make([]string, 0, len(composeApp.Services))
	for serviceName := range composeApp.Services {
		serviceNames = append(serviceNames, serviceName)
	}
	sort.Strings(serviceNames)

	for _, serviceName := range serviceNames {
		dockerService := composeApp.Services[serviceName]
		settings, err := SettingsFor(serviceName, dockerService)
		if err != nil {
			return nil, err
		}

		var ingress *models.Ingress
		if settings.Ingress != nil {
			ingressPath := settings.Ingress.Path
			if ingressPath == "" {
				ingressPath = "/"
			}

			ingress = &models.Ingress{
				Path: ingressPath,
				Port: settings.Ingress.Port,
				HealthCheck: models.HealthCheck{
					Type: models.HealthCheckTypeHTTP,
					Path: "/",
				},
			}
			if settings.Ingress.HealthCheck.Type != "" {
				ingress.HealthCheck.Type = models.HealthCheckType(settings.Ingress.HealthCheck.Type)
			}
			if settings.Ingress.HealthCheck.Path != "" {
				ingress.HealthCheck.Path = settings.Ingress.HealthCheck.Path
			}
			if settings.Ingress.HealthCheck.Port != nil {
				ingress.HealthCheck.Port = settings.Ingress.HealthCheck.Port
			}
		}

		reserved := 0
		if ingress != nil {
			reserved = 1
		}
		networkSegments, err := NetworkSegmentsFor(serviceName, &dockerService, reserved)
		if err != nil {
			return nil, err
		}

		var primaryPort *int
		if len(dockerService.Ports) > 0 {
			port := int(dockerService.Ports[0].Target)
			primaryPort = &port
		}

		secretNames := make([]string, 0)
		for _, s := range dockerService.Secrets {
			switch v := s.(type) {
			case string:
				secretNames = append(secretNames, v)
			case map[string]interface{}:
				if source, ok := v["source"].(string); ok {
					secretNames = append(secretNames, source)
				}
			}
		}

		capability := InferCapability(dockerService.Image)
		if settings.Capability != nil {
			capability = string(*settings.Capability)
		}

		// dockerService.Command is always []string by the time it reaches
		// here (parser.go converts compose-go's ShellCommand, which is
		// itself already shell-split from a YAML string form — `docker
		// compose config`, the equivalent step in the parser this replaces,
		// does the same normalization). The []interface{}/string cases
		// below exist only in case that assumption is ever violated by a
		// different caller of Normalize; a bare string would previously
		// have been wrapped in ["/bin/sh", "-c", ...], but that shape has
		// never actually been observed from either parser in practice.
		var command []string
		switch v := dockerService.Command.(type) {
		case []string:
			command = v
		case []interface{}:
			command = make([]string, len(v))
			for i, cmd := range v {
				command[i] = fmt.Sprintf("%v", cmd)
			}
		case string:
			command = []string{"/bin/sh", "-c", v}
		}

		var buildContext, dockerfile *string
		if dockerService.Build != nil {
			buildContext = &dockerService.Build.Context
			if dockerService.Build.Dockerfile != "" {
				dockerfile = &dockerService.Build.Dockerfile
			}
		}

		var schedule models.Schedule
		if settings.Schedule != "" {
			schedule, err = ParseSchedule(settings.Schedule)
			if err != nil {
				return nil, err
			}
		}

		var autoScaling *models.AutoScalingConfig
		if settings.AutoScaling != nil {
			autoScaling = &models.AutoScalingConfig{
				Metrics: make([]models.AutoScalingMetric, len(settings.AutoScaling.Metrics)),
			}
			for i, m := range settings.AutoScaling.Metrics {
				autoScaling.Metrics[i] = models.AutoScalingMetric{
					Type:        models.AutoScalingMetricType(m.Type),
					TargetValue: m.Target,
				}
			}
			autoScaling.ScaleInCooldown = settings.AutoScaling.ScaleInCooldown
			autoScaling.ScaleOutCooldown = settings.AutoScaling.ScaleOutCooldown
		}

		if err := RejectPersistentVolumes(serviceName, &dockerService, capability); err != nil {
			return nil, err
		}

		env := make(map[string]string)
		for k, v := range dockerService.Environment {
			if v != nil {
				env[k] = *v
			}
		}

		var databaseName *string
		if capability == string(models.CapabilityDatabase) {
			name := DatabaseName(projectName, serviceName, env)
			databaseName = &name
		}

		size := settings.Size
		if size == "" {
			size = "small"
		}

		semanticService := models.Service{
			Name:                     serviceName,
			Image:                    dockerService.Image,
			Capability:               models.Capability(capability),
			Size:                     models.ServiceSize(size),
			CPU:                      settings.CPU,
			Memory:                   settings.Memory,
			Port:                     primaryPort,
			DatabaseName:             databaseName,
			BuildContext:             buildContext,
			Dockerfile:               dockerfile,
			Command:                  command,
			StartupGracePeriod:       settings.GetGracePeriod(),
			MinScale:                 settings.MinScale,
			MaxScale:                 settings.MaxScale,
			AutoScaling:              autoScaling,
			Schedule:                 schedule,
			CDNEnabled:               settings.CDN,
			Ingress:                  ingress,
			NetworkIsolationSegments: networkSegments,
			Env:                      env,
			Config:                   dockerService.PlatformEnv,
			Secrets:                  secretNames,
		}

		// Set defaults
		if semanticService.Image == "" {
			semanticService.Image = "placeholder"
		}

		if err := semanticService.Validate(); err != nil {
			return nil, err
		}

		semanticServices = append(semanticServices, semanticService)

		// Same determinism concern as the outer loop: DependsOn is a map,
		// so its keys need sorting before use rather than being trusted to
		// come back in a stable order.
		depNames := make([]string, 0, len(dockerService.DependsOn))
		for depName := range dockerService.DependsOn {
			depNames = append(depNames, depName)
		}
		sort.Strings(depNames)
		for _, depName := range depNames {
			relationships = append(relationships, models.Relationship{
				Client: serviceName,
				Server: depName,
			})
		}
	}

	for i := range semanticServices {
		if semanticServices[i].Ingress != nil && semanticServices[i].Ingress.Port == nil {
			port := 80
			if semanticServices[i].Port != nil {
				port = *semanticServices[i].Port
			}
			semanticServices[i].Ingress.Port = &port
		}
	}

	if err := RejectOverlappingPaths(semanticServices); err != nil {
		return nil, err
	}

	return &models.Application{
		Name:          projectName,
		Services:      semanticServices,
		Relationships: relationships,
	}, nil
}

func SettingsFor(name string, service models.ComposeService) (*models.XCloud, error) {
	if service.XCloud == nil {
		// Must match UnmarshalJSON's own defaults exactly, not a bare zero
		// value — MinScale/MaxScale are meaningful at 0 (scale-to-zero is a
		// real, validated setting: min_scale allows 0, only max_scale
		// requires >= 1), so a zero value here is indistinguishable from a
		// service that explicitly asked for 0. A second pass in Normalize
		// used to paper over exactly this by resetting MinScale/MaxScale to
		// 1 whenever both were 0 — which also reset an explicit
		// min_scale: 0 back to 1 for any service that did have an
		// x-cloud block, since there was no way from that check alone to
		// tell "unset" apart from "deliberately zero" (confirmed against a
		// real x-cloud block). Fixed at the one place the
		// default actually needs to live, rather than reapplied downstream.
		return &models.XCloud{Size: "small", MinScale: 1, MaxScale: 1}, nil
	}

	settings := &models.XCloud{}
	jsonBytes, err := json.Marshal(service.XCloud)
	if err != nil {
		return nil, fmt.Errorf("service '%s' has an invalid x-cloud block: %v", name, err)
	}

	if err := json.Unmarshal(jsonBytes, settings); err != nil {
		return nil, fmt.Errorf("service '%s' has an invalid x-cloud block: %v", name, err)
	}

	return settings, nil
}

func SemanticToJSON(app *models.Application) (string, error) {
	output, err := json.MarshalIndent(app, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal to JSON: %w", err)
	}
	return string(output), nil
}
