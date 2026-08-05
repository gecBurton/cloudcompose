package compiler

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/compose-spec/compose-go/loader"
	"github.com/compose-spec/compose-go/types"
	"github.com/gecburton/composey/internal/models"
)

// declaredEnvironment loads the compose file a second time with
// interpolation skipped, so environment values still contain their literal
// ${VAR} form (or env_file-sourced values, still unresolved) rather than
// whatever a developer's local shell or .env happened to substitute.
//
// Mirrors _declared_environment in the Python parser it replaces, which
// re-read the raw YAML directly for the same reason: `docker compose config`
// (the Python parser's own subprocess call) folds env_file and ${VAR}
// substitutions into one flat map, indistinguishable from a value actually
// written in the compose file — the whole point of platform_env is telling
// those apart, so it needs a view compose-go's own interpolation has not
// touched.
func declaredEnvironment(filePath, workingDir string) (map[string]map[string]*string, error) {
	project, err := loader.Load(types.ConfigDetails{
		WorkingDir: workingDir,
		ConfigFiles: []types.ConfigFile{
			{Filename: filePath},
		},
	}, func(o *loader.Options) {
		o.SkipInterpolation = true
	})
	if err != nil {
		return nil, fmt.Errorf("parse compose file (uninterpolated): %w", err)
	}
	if err := loader.Normalize(project); err != nil {
		return nil, fmt.Errorf("normalize compose (uninterpolated): %w", err)
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
	// WorkingDir must be set explicitly and absolute: ResolveRelativePaths
	// resolves build contexts against it, and left unset it defaults to the
	// process's current working directory rather than the compose file's own
	// directory — so a relative build context like "app" silently resolved
	// against wherever this binary happened to be invoked from, not against
	// examples/doctor/ where the compose file (and "app") actually live.
	composeDir, err := filepath.Abs(filepath.Dir(filePath))
	if err != nil {
		return nil, fmt.Errorf("resolve compose file directory: %w", err)
	}

	// Load the compose file using Docker's native loader
	project, err := loader.Load(types.ConfigDetails{
		WorkingDir: composeDir,
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

	// ResolveRelativePaths, above, makes build contexts absolute on the
	// machine doing the compiling — the same thing `docker compose config`
	// does, which is what the Python parser it replaces had to re-root for
	// exactly this reason: an absolute path here leaks the local filesystem
	// into the generated Terraform (docker_image.build.context) and makes
	// output depend on where the repository happens to be checked out,
	// rather than compiling to the same thing on any machine.

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

		// Convert build config
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

		// Convert command. service.Command is compose-go's own ShellCommand
		// (a named []string type — YAML `command: npm run start-watch` is
		// already shell-split by the time it reaches here), not a bare
		// []interface{} or string. Passed through as the named type inside
		// an interface{} field, normalizer.go's type switch on
		// []interface{}/string never matched it, so every declared command
		// was silently dropped. Converting to plain []string here means the
		// normalizer only has to handle one shape.
		if len(service.Command) > 0 {
			command := make([]string, len(service.Command))
			copy(command, service.Command)
			s.Command = command
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
