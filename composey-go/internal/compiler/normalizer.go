package compiler

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/gecburton/composey/internal/models"
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

	return fmt.Errorf("service %q mounts named volume(s) %s. Composey cannot provide a persistent filesystem, and running the service without one would lose whatever is written there on every restart. Use a `minio` service for object storage, or drop the volume if the path only needs scratch space, which the task already has", name, strings.Join(unique, ", "))
}

func NamedVolumeSource(volume interface{}) string {
	switch v := volume.(type) {
	case string:
		parts := strings.Split(v, ":")
		if len(parts) < 2 {
			return ""
		}
		source := parts[0]
		for _, prefix := range BindSourcePrefixes {
			if strings.HasPrefix(source, prefix) {
				return ""
			}
		}
		return source
	case models.VolumeDefinition:
		if v.Type == "volume" {
			return v.Source
		}
	}
	return ""
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

	return fmt.Errorf("networks %s are declared external. Composey cannot map a network it does not create to a security group", strings.Join(external, ", "))
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

	for serviceName, dockerService := range composeApp.Services {
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

		if semanticService.MinScale == 0 && settings.MinScale == 0 {
			semanticService.MinScale = 1
		}

		if semanticService.MaxScale == 0 && settings.MaxScale == 0 {
			semanticService.MaxScale = 1
		}

		if err := semanticService.Validate(); err != nil {
			return nil, err
		}

		semanticServices = append(semanticServices, semanticService)

		for depName := range dockerService.DependsOn {
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

func SettingsFor(name string, service models.ComposeService) (*models.XComposey, error) {
	if service.XComposey == nil {
		return &models.XComposey{}, nil
	}

	settings := &models.XComposey{}
	jsonBytes, err := json.Marshal(service.XComposey)
	if err != nil {
		return nil, fmt.Errorf("service %q has an invalid x-composey block: %v", name, err)
	}

	if err := json.Unmarshal(jsonBytes, settings); err != nil {
		return nil, fmt.Errorf("service %q has an invalid x-composey block: %v", name, err)
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
