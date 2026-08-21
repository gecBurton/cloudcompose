package shared

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/compose-spec/compose-go/v2/loader"
	"github.com/compose-spec/compose-go/v2/types"
	"github.com/gecburton/cloudcompose/internal/models"
)

// declaredEnvironment loads the compose file a second time with both
// interpolation and env_file resolution skipped, so environment values keep
// their literal ${VAR} form and env_file contents are absent entirely —
// leaving only what the file's `environment:` block actually states.
func declaredEnvironment(filePath, workingDir string) (map[string]map[string]*string, error) {
	project, err := loader.LoadWithContext(context.Background(), types.ConfigDetails{
		WorkingDir: workingDir,
		ConfigFiles: []types.ConfigFile{
			{Filename: filePath},
		},
	}, func(o *loader.Options) {
		o.SkipInterpolation = true
		o.SkipResolveEnvironment = true
		o.SetProjectName("cloudcompose-parse-placeholder", true)
	})
	if err != nil {
		return nil, fmt.Errorf("parse compose file (uninterpolated): %w", err)
	}

	declared := make(map[string]map[string]*string, len(project.Services))
	for _, service := range project.Services {
		env := make(map[string]*string, len(service.Environment))
		for key, val := range service.Environment {
			if val == nil {
				env[key] = nil
			} else {
				v := *val
				env[key] = &v
			}
		}
		declared[service.Name] = env
	}
	return declared, nil
}

// splitEnvironment separates values the compose file states literally from
// values it merely names. A value crosses into the deployed environment only
// when it is written literally in the compose file — committed, and
// therefore not secret. Everything else (env_file contents, ${VAR}
// substitutions resolved from a developer's local shell) contributes only
// its *name* via platformEnv: the application needs that variable, and the
// platform is expected to supply the value out of band.
func splitEnvironment(
	resolved map[string]*string, declared map[string]*string,
) (map[string]*string, []string) {
	literal := make(map[string]struct{})
	for key, value := range declared {
		if value != nil && !strings.Contains(*value, "${") {
			literal[key] = struct{}{}
		}
	}

	environment := make(map[string]*string)
	var platformEnv []string
	for key, value := range resolved {
		if _, ok := literal[key]; ok {
			environment[key] = value
		} else {
			platformEnv = append(platformEnv, key)
		}
	}
	sort.Strings(platformEnv)
	return environment, platformEnv
}

// ParseCompose parses a Docker Compose file using compose-go (Docker's native parser)
func ParseCompose(filePath string) (*models.ComposeApplication, error) {
	// WorkingDir must be absolute: ResolveRelativePaths resolves build
	// contexts against it, and left unset it defaults to the process's
	// cwd rather than the compose file's own directory.
	composeDir, err := filepath.Abs(filepath.Dir(filePath))
	if err != nil {
		return nil, fmt.Errorf("resolve compose file directory: %w", err)
	}

	// v2 requires a project name at load time; a placeholder is set here
	// since the real one is assigned later by Normalize.
	project, err := loader.LoadWithContext(context.Background(), types.ConfigDetails{
		WorkingDir: composeDir,
		ConfigFiles: []types.ConfigFile{
			{Filename: filePath},
		},
	}, func(o *loader.Options) {
		o.SetProjectName("cloudcompose-parse-placeholder", true)
	})
	if err != nil {
		return nil, fmt.Errorf("parse compose file: %w", err)
	}

	// The above resolves build contexts to absolute paths on the machine
	// doing the compiling, so they're re-rooted below to keep output
	// independent of where the repository is checked out.

	declared, err := declaredEnvironment(filePath, composeDir)
	if err != nil {
		return nil, err
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
			Image: service.Image,
		}

		// Convert build config. Args is compose-go's MappingWithEquals —
		// not converted, see BuildConfig's doc comment for why.
		if service.Build != nil {
			context := service.Build.Context
			if filepath.IsAbs(context) {
				if rel, err := filepath.Rel(composeDir, context); err == nil {
					context = rel
				}
			}
			s.Build = &models.BuildConfig{
				Context:    context,
				Dockerfile: service.Build.Dockerfile,
				Target:     service.Build.Target,
			}
		}

		// Convert ports. Protocol is not converted, see PortConfig's doc
		// comment for why.
		for _, port := range service.Ports {
			s.Ports = append(s.Ports, models.PortConfig{
				Target:    port.Target,
				Published: port.Published,
			})
		}

		// Convert environment variables. Split into what the compose file
		// states literally (crosses into the deployment) versus what it
		// merely names (becomes platform_env; see splitEnvironment).
		resolvedEnv := make(map[string]*string, len(service.Environment))
		for key, val := range service.Environment {
			if val == nil {
				resolvedEnv[key] = nil
			} else {
				v := *val
				resolvedEnv[key] = &v
			}
		}
		s.Environment, s.PlatformEnv = splitEnvironment(resolvedEnv, declared[service.Name])

		// Convert depends_on. Only the keys ever matter to Normalize (see
		// ComposeService.DependsOn's doc comment), so condition/required
		// are not converted.
		if len(service.DependsOn) > 0 {
			s.DependsOn = make(map[string]struct{}, len(service.DependsOn))
			for depName := range service.DependsOn {
				s.DependsOn[depName] = struct{}{}
			}
		}

		if len(service.Networks) > 0 {
			s.Networks = make(map[string]interface{})
			for netName := range service.Networks {
				s.Networks[netName] = nil
			}
		}

		// service.Volumes is compose-go's own types.ServiceVolumeConfig;
		// compose-go normalizes both short-form and long-form volume
		// syntax into this one struct before the loader returns.
		for _, volume := range service.Volumes {
			s.Volumes = append(s.Volumes, models.VolumeDefinition{
				Type:   volume.Type,
				Source: volume.Source,
			})
		}

		for _, secret := range service.Secrets {
			s.Secrets = append(s.Secrets, secret.Source)
		}

		// service.Command is compose-go's own ShellCommand (a named
		// []string type, already shell-split from the YAML form).
		if len(service.Command) > 0 {
			command := make([]string, len(service.Command))
			copy(command, service.Command)
			s.Command = command
		}

		if service.Extensions != nil {
			if xCloud, ok := service.Extensions["x-cloud"]; ok {
				s.XCloud = xCloud
			}
		}

		app.Services[service.Name] = s
	}

	// Name is not converted, see NetworkDefinition's doc comment for why.
	for name, network := range project.Networks {
		app.Networks[name] = &models.NetworkDefinition{
			External: bool(network.External),
		}
	}

	for name, volume := range project.Volumes {
		app.Volumes[name] = volume
	}

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
