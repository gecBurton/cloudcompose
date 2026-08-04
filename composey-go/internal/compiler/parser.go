package compiler

import (
	"encoding/json"
	"fmt"

	"github.com/compose-spec/compose-go/loader"
	"github.com/compose-spec/compose-go/types"
	"github.com/gecburton/composey/internal/models"
)

// ParseCompose parses a Docker Compose file using compose-go (Docker's native parser)
func ParseCompose(filePath string) (*models.ComposeApplication, error) {
	// Load the compose file using Docker's native loader
	project, err := loader.Load(types.ConfigDetails{
		ConfigFiles: []types.ConfigFile{
			{Filename: filePath},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("parse compose file: %w", err)
	}

	// Normalize the project (fills defaults, validates structure) - like Docker does
	if err := loader.Normalize(project); err != nil {
		return nil, fmt.Errorf("normalize compose: %w", err)
	}

	// Resolve relative paths (build contexts, volumes, secrets) - like Docker does
	if err := loader.ResolveRelativePaths(project); err != nil {
		return nil, fmt.Errorf("resolve paths: %w", err)
	}

	app := &models.ComposeApplication{
		Services: make(map[string]models.ComposeService),
		Networks: make(map[string]*models.NetworkDefinition),
		Volumes:  make(map[string]interface{}),
		Secrets:  make(map[string]models.ComposeSecret),
	}

	// Convert services
	for _, service := range project.Services {
		s := models.ComposeService{
			Image:       service.Image,
			Environment: make(map[string]*string),
		}

		// Convert build config
		if service.Build != nil {
			s.Build = &models.BuildConfig{
				Context:    service.Build.Context,
				Dockerfile: service.Build.Dockerfile,
				Target:     service.Build.Target,
			}
			// Convert build args
			for _, arg := range service.Build.Args {
				if arg != nil {
					s.Build.Args = append(s.Build.Args, *arg)
				}
			}
		}

		// Convert ports
		for _, port := range service.Ports {
			s.Ports = append(s.Ports, models.PortConfig{
				Target:    port.Target,
				Published: port.Published,
				Protocol:  string(port.Protocol),
			})
		}

		// Convert environment variables
		for key, val := range service.Environment {
			if val == nil {
				s.Environment[key] = nil
			} else {
				v := *val
				s.Environment[key] = &v
			}
		}

		// Convert depends_on
		if len(service.DependsOn) > 0 {
			s.DependsOn = make(map[string]models.Dependency)
			for depName, depConfig := range service.DependsOn {
				s.DependsOn[depName] = models.Dependency{
					Condition: depConfig.Condition,
					Required:  depConfig.Required,
				}
			}
		}

		// Convert networks
		if len(service.Networks) > 0 {
			s.Networks = make(map[string]interface{})
			for netName, netConfig := range service.Networks {
				_ = netConfig // Network config not used currently
				s.Networks[netName] = nil
			}
		}

		// Convert volumes
		for _, volume := range service.Volumes {
			s.Volumes = append(s.Volumes, volume)
		}

		// Convert secrets
		for _, secret := range service.Secrets {
			s.Secrets = append(s.Secrets, secret.Source)
		}

		// Convert command
		if len(service.Command) > 0 {
			s.Command = service.Command
		}

		// Extract x-composey extension
		if service.Extensions != nil {
			if xComposey, ok := service.Extensions["x-composey"]; ok {
				s.XComposey = xComposey
			}
		}

		app.Services[service.Name] = s
	}

	// Convert networks with proper parsing
	for name, network := range project.Networks {
		app.Networks[name] = &models.NetworkDefinition{
			Name:     network.Name,
			External: network.External.External,
		}
	}

	// Convert volumes
	for name, volume := range project.Volumes {
		app.Volumes[name] = volume
	}

	// Convert secrets
	for name, secret := range project.Secrets {
		app.Secrets[name] = models.ComposeSecret{
			File: secret.File,
		}
	}

	return app, nil
}

// ParseComposeJSON parses a compose file and returns JSON output
func ParseComposeJSON(filePath string) (string, error) {
	app, err := ParseCompose(filePath)
	if err != nil {
		return "", err
	}

	output, err := json.MarshalIndent(app, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal to JSON: %w", err)
	}

	return string(output), nil
}
