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
	"github.com/gecburton/composey/internal/models"
)

// declaredEnvironment loads the compose file a second time with both
// interpolation and env_file resolution skipped, so environment values keep
// their literal ${VAR} form and env_file contents are absent entirely —
// leaving only what the file's `environment:` block actually states.
//
// SkipInterpolation alone is not enough: it only skips ${VAR} substitution.
// env_file merging is a separate loading step (ResolveServicesEnvironment)
// that SkipInterpolation does not touch, so a real POSTGRES_PASSWORD from a
// .env file still showed up looking exactly like a value written in the
// compose file itself — the precise case this function exists to catch,
// silently defeated (confirmed against a real env_file 2026-08-05). A raw
// second YAML parse via gopkg.in/yaml.v3, bypassing compose-go entirely, was
// tried next and worked, but duplicated logic compose-go's own loader
// already has (the two accepted forms of `environment:` — a mapping, or a
// list of "KEY=value"/"KEY" strings) and would silently drift from whatever
// compose-go itself does if that logic ever changes. SkipResolveEnvironment
// is compose-go's own supported way to skip exactly the env_file merge step
// — no exported With... functional-option wraps it, but the field is public
// on loader.Options, so it's reachable the same way SkipInterpolation is set
// below. Confirmed empirically against a real .env file 2026-08-06: with
// both flags set, POSTGRES_PASSWORD from .env is absent, and ${DATABASE_URL}
// stays unresolved rather than substituted.
func declaredEnvironment(filePath, workingDir string) (map[string]map[string]*string, error) {
	project, err := loader.LoadWithContext(context.Background(), types.ConfigDetails{
		WorkingDir: workingDir,
		ConfigFiles: []types.ConfigFile{
			{Filename: filePath},
		},
	}, func(o *loader.Options) {
		o.SkipInterpolation = true
		o.SkipResolveEnvironment = true
		// See the identical call in ParseCompose: v2 requires a project
		// name at load time, and this function does not need the real one
		// — only the environment blocks, never anything project-name-shaped.
		o.SetProjectName("composey-parse-placeholder", true)
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

	// Load the compose file using Docker's native loader. LoadWithContext
	// normalizes (fills defaults, validates structure) and resolves
	// relative paths (build contexts, volumes, secrets) internally by
	// default — v1's separate loader.Normalize/loader.ResolveRelativePaths
	// calls no longer exist as a distinct step for this path; ResolvePaths
	// defaults to true in Options, and normalization happens unless
	// SkipNormalization is set, neither of which this call does.
	//
	// v2 also hard-requires a project name at load time ("project name
	// must not be empty") where v1 left it for composey's own Normalize to
	// assign — ParseCompose does not know the real project name yet, that
	// is normalize's job (Normalize(app, projectName)), so a placeholder is
	// set imperatively here purely to satisfy compose-go's own requirement.
	// Without imperativelySet, compose-go falls back to deriving one from
	// the compose file's own `name:` field or the working directory's
	// basename, which is real behavior this codebase does not want:
	// project naming for every cloud already flows entirely through the
	// project_name argument the CLI passes to Normalize, not through
	// whatever compose-go happens to guess at parse time.
	project, err := loader.LoadWithContext(context.Background(), types.ConfigDetails{
		WorkingDir: composeDir,
		ConfigFiles: []types.ConfigFile{
			{Filename: filePath},
		},
	}, func(o *loader.Options) {
		o.SetProjectName("composey-parse-placeholder", true)
	})
	if err != nil {
		return nil, fmt.Errorf("parse compose file: %w", err)
	}

	// The above resolves build contexts to absolute paths on the machine
	// doing the compiling — the same thing `docker compose config`
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

		// Convert build config. Args is compose-go's
		// MappingWithEquals — not converted, see BuildConfig's doc comment
		// for why.
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

		// Convert networks
		if len(service.Networks) > 0 {
			s.Networks = make(map[string]interface{})
			for netName, netConfig := range service.Networks {
				_ = netConfig // Network config not used currently
				s.Networks[netName] = nil
			}
		}

		// Convert volumes. service.Volumes is compose-go's own
		// types.ServiceVolumeConfig — compose-go normalizes both short-form
		// ("db-data:/data") and long-form (type/source/target mapping)
		// syntax into this one struct before the loader ever returns, so
		// there is no code path where a volume entry arrives as a bare
		// string. A local VolumeDefinition type that merely *resembled*
		// ServiceVolumeConfig's shape was tried first and silently matched
		// neither form in a type switch downstream — every named volume
		// compiled clean with no error and no record of the mount at all,
		// exactly the failure RejectPersistentVolumes exists to catch
		// (confirmed against a real compose file 2026-08-06). Converting
		// explicitly here, the same way Ports/Build/Command already are,
		// keeps that conversion in one place rather than depending on a
		// type switch elsewhere to happen to agree with what compose-go
		// actually produces.
		for _, volume := range service.Volumes {
			s.Volumes = append(s.Volumes, models.VolumeDefinition{
				Type:   volume.Type,
				Source: volume.Source,
			})
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

	// Convert networks with proper parsing. v1's NetworkConfig.External was
	// a struct with its own nested External bool field
	// (network.External.External); v2 collapsed it to a plain named bool
	// type (types.External, a `bool` underneath), so the field is the
	// value itself now, not a field on it. Name is not converted, see
	// NetworkDefinition's doc comment for why.
	for name, network := range project.Networks {
		app.Networks[name] = &models.NetworkDefinition{
			External: bool(network.External),
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
