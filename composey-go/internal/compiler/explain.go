package compiler

import (
	"fmt"
	"sort"
	"strings"

	"github.com/gecburton/composey/internal/compiler/shared"
	"github.com/gecburton/composey/internal/models"
)

// Reporting every decision the compiler made, and why, mirroring
// compiler/explain.py.
//
// Inference is only safe when a wrong guess is visible. Most of composey's
// failures on real compose files were silent: a database deployed as a
// container, an application with no ingress at all, a bind mount quietly
// dropped. This turns those into something readable before anything is
// deployed.
//
// Ported both branches Python has (with and without the raw compose
// model), even though the Go CLI always has both available from
// ParseCompose -- unlike Python's own Go-parser path, which only ever had
// the semantic model and lost the docker_app-only detail. Passing nil for
// composeApp still works exactly as it does in Python, for the same
// reason: it's a documented, deliberate degradation path, not dead code.

// DecisionSource mirrors explain.py's Source Literal.
type DecisionSource string

const (
	SourceDeclared DecisionSource = "declared"
	SourceInferred DecisionSource = "inferred"
	SourceDefault  DecisionSource = "default"
	SourceWarning  DecisionSource = "warning"
)

// Decision mirrors explain.py's Decision dataclass: one choice the
// compiler made about one subject.
type Decision struct {
	Subject  string
	Decision string
	Because  string
	Source   DecisionSource
}

// Explain describes every inference made while normalizing this
// application, mirroring explain(). composeApp is optional: only a
// handful of decisions need the raw compose model specifically (whether
// capability was declared verbatim rather than inferred, the exact list
// of dropped ports/mounts, and the fallback "which services publish a
// port" detail when nothing is public). Everything else is answerable
// from the semantic model alone.
func Explain(composeApp *models.ComposeApplication, semantic *models.Application) []Decision {
	var decisions []Decision

	for i := range semantic.Services {
		service := &semantic.Services[i]
		name := service.Name

		var declaredCapability bool
		var composeService *models.ComposeService
		if composeApp != nil {
			if cs, ok := composeApp.Services[name]; ok {
				composeService = &cs
			}
		}
		if composeService != nil {
			declaredCapability = xCloudHasKey(composeService.XCloud, "capability")
		} else {
			// Without the raw compose model there is no direct record of
			// whether capability was written explicitly. Whether the
			// inferred value would have guessed the same capability from
			// the image name is the closest available proxy: if it
			// disagrees, something must have overridden it.
			declaredCapability = string(service.Capability) != shared.InferCapability(service.Image)
		}
		decisions = append(decisions, capabilityDecision(name, service, declaredCapability))

		if service.Capability != models.CapabilityContainer {
			continue
		}

		if composeService != nil {
			decisions = append(decisions, portDecisions(name, composeService, service)...)
		} else if service.Port != nil {
			decisions = append(decisions, Decision{
				Subject: name, Decision: fmt.Sprintf("listens on %d", *service.Port),
				Because: "first published port", Source: SourceInferred,
			})
		}

		if service.BuildContext != nil {
			dockerfile := "Dockerfile"
			if service.Dockerfile != nil {
				dockerfile = *service.Dockerfile
			}
			decisions = append(decisions, Decision{
				Subject: name, Decision: "built from source and pushed to a registry",
				Because: fmt.Sprintf("build context %s, %s", pyRepr(*service.BuildContext), dockerfile),
				Source:  SourceInferred,
			})
		}

		if service.Schedule != nil {
			shape := scheduleShape(service.Schedule)
			decisions = append(decisions, Decision{
				Subject: name, Decision: "runs as a scheduled task, not a long-running service",
				Because: fmt.Sprintf("schedule: %s", shape), Source: SourceDeclared,
			})
		}

		if service.MaxScale > 1 {
			decisions = append(decisions, Decision{
				Subject: name, Decision: fmt.Sprintf("scales between %d and %d", service.MinScale, service.MaxScale),
				Because: "max_scale is greater than one", Source: SourceDeclared,
			})
		}

		if service.CDNEnabled {
			decisions = append(decisions, Decision{
				Subject: name, Decision: "fronted by a CDN with a WAF",
				Because: "cdn: true", Source: SourceDeclared,
			})
		}

		if service.Size != models.ServiceSizeSmall || service.CPU != nil || service.Memory != nil {
			decisions = append(decisions, Decision{
				Subject: name, Decision: fmt.Sprintf("size %s", service.Size),
				Because: "declared by x-cloud", Source: SourceDeclared,
			})
		}

		if composeService != nil {
			decisions = append(decisions, volumeDecisions(name, composeService)...)
		}
		decisions = append(decisions, wiringDecisions(name, semantic, service)...)

		if len(service.Config) > 0 {
			shown := service.Config
			more := ""
			if len(shown) > 4 {
				more = fmt.Sprintf(" and %d more", len(shown)-4)
				shown = shown[:4]
			}
			decisions = append(decisions, Decision{
				Subject:  name,
				Decision: fmt.Sprintf("%d variable(s) need values from the platform", len(service.Config)),
				Because: strings.Join(shown, ", ") + more +
					" come from env_file or ${...}, so the compose file names " +
					"them but does not value them; set them in Secrets Manager " +
					"before the service will work",
				Source: SourceWarning,
			})
		}

		for _, secret := range service.Secrets {
			decisions = append(decisions, Decision{
				Subject: name, Decision: fmt.Sprintf("secret %s created empty", pyRepr(secret)),
				Because: "the value must be set out of band before the service works",
				Source:  SourceWarning,
			})
		}
	}

	decisions = append(decisions, ingressDecisions(composeApp, semantic)...)

	for _, r := range semantic.Relationships {
		decisions = append(decisions, Decision{
			Subject: r.Client, Decision: fmt.Sprintf("may connect to %s", r.Server),
			Because: "depends_on", Source: SourceInferred,
		})
	}

	return decisions
}

// StripMarkup removes Rich-style markup tags ([bold], [cyan], [/], etc.)
// from Render's output, for terminals/environments with no ANSI color
// rendering wired up. Render's own markup is otherwise Rich-compatible
// (same tag vocabulary Python's Rich console understands), which this
// intentionally does not attempt to reproduce as real ANSI escapes --
// the Go CLI prints plain text rather than adding a color-rendering
// dependency to match Rich visually.
func StripMarkup(s string) string {
	var b strings.Builder
	inTag := false
	for _, r := range s {
		switch {
		case r == '[':
			inTag = true
		case r == ']':
			inTag = false
		case !inTag:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// pyRepr renders a string the way Python's !r format spec does for a
// plain str: single-quoted, not double-quoted like Go's %q. explain.py's
// own messages use !r throughout (image!r, build_context!r, secret!r,
// server_name!r, schedule.expression!r), and matching that exactly is the
// whole point of porting this file rather than writing a similar-looking
// report from scratch.
func pyRepr(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "\\'") + "'"
}

// block declares the given key at all, mirroring Python's
// "capability" in docker_app.services[name].x_composey_raw check against
// the raw dict rather than the parsed model (which always has some
// value for capability, declared or defaulted).
func xCloudHasKey(raw any, key string) bool {
	m, ok := raw.(map[string]any)
	if !ok {
		return false
	}
	_, ok = m[key]
	return ok
}

// capabilityDecision reports what a service was treated as, mirroring
// _capability_decision.
//
// The wording deliberately does not change with the source: the same
// outcome should read the same whether it was declared or guessed, so
// that scanning the report for what happened is separate from asking why.
func capabilityDecision(name string, service *models.Service, declared bool) Decision {
	var outcome string
	if service.Capability == models.CapabilityContainer {
		outcome = "runs as a container"
	} else {
		outcome = fmt.Sprintf("substituted for a managed %s", service.Capability)
	}

	if declared {
		return Decision{Subject: name, Decision: outcome, Because: "declared by x-cloud: capability", Source: SourceDeclared}
	}

	var because string
	if service.Capability == models.CapabilityContainer {
		because = fmt.Sprintf("image %s is not a recognised managed service", pyRepr(service.Image))
	} else {
		because = fmt.Sprintf("image %s is a recognised %s", pyRepr(service.Image), service.Capability)
	}
	return Decision{Subject: name, Decision: outcome, Because: because, Source: SourceInferred}
}

// portDecisions mirrors _port_decisions.
func portDecisions(name string, composeService *models.ComposeService, service *models.Service) []Decision {
	var decisions []Decision
	if service.Port == nil {
		return decisions
	}

	decisions = append(decisions, Decision{
		Subject: name, Decision: fmt.Sprintf("listens on %d", *service.Port),
		Because: "first published port", Source: SourceInferred,
	})
	if len(composeService.Ports) > 1 {
		ignored := make([]string, 0, len(composeService.Ports)-1)
		for _, p := range composeService.Ports[1:] {
			ignored = append(ignored, fmt.Sprintf("%d", p.Target))
		}
		decisions = append(decisions, Decision{
			Subject: name, Decision: fmt.Sprintf("ports %s are not exposed", strings.Join(ignored, ", ")),
			Because: "only the first port of a service is used", Source: SourceWarning,
		})
	}
	return decisions
}

// volumeDecisions mirrors _volume_decisions. Named volumes are rejected
// before this runs, so any left are local-only.
func volumeDecisions(name string, composeService *models.ComposeService) []Decision {
	dropped := len(composeService.Volumes)
	if dropped == 0 {
		return nil
	}
	return []Decision{{
		Subject: name, Decision: fmt.Sprintf("%d mount(s) dropped", dropped),
		Because: "bind mounts and anonymous volumes have no deployed meaning",
		Source:  SourceWarning,
	}}
}

// wiringDecisions reports whether this service's references to its
// dependencies resolve, mirroring _wiring_decisions.
func wiringDecisions(name string, semantic *models.Application, service *models.Service) []Decision {
	var decisions []Decision

	// Map iteration order in Python is dict-insertion order (services
	// list order, filtered); Go must reproduce that explicitly rather
	// than iterate a map, to keep output deterministic.
	type serverEntry struct {
		name    string
		service *models.Service
	}
	var servers []serverEntry
	for i := range semantic.Services {
		s := &semantic.Services[i]
		if s.Name == name {
			continue
		}
		referenced := false
		for _, r := range semantic.Relationships {
			if r.Client == name && r.Server == s.Name {
				referenced = true
				break
			}
		}
		if referenced {
			servers = append(servers, serverEntry{s.Name, s})
		}
	}

	for _, entry := range servers {
		serverName := entry.name
		server := entry.service

		// A container with no port has no address to hand out.
		if server.Capability == models.CapabilityContainer && server.Port == nil &&
			envReferencesServer(service.Env, serverName) {
			decisions = append(decisions, Decision{
				Subject: name, Decision: fmt.Sprintf("cannot reach %s", serverName),
				Because: fmt.Sprintf("%s publishes no port, so it has no address to be found at", serverName),
				Source:  SourceWarning,
			})
			continue
		}

		matched := matchedEnvKeys(service.Env, serverName)
		if len(matched) > 0 {
			sort.Strings(matched)
			decisions = append(decisions, Decision{
				Subject: name, Decision: fmt.Sprintf("%s → %s", strings.Join(matched, ", "), serverName),
				Because: fmt.Sprintf("value references %s", pyRepr(serverName)), Source: SourceInferred,
			})
		} else {
			decisions = append(decisions, Decision{
				Subject: name, Decision: fmt.Sprintf("nothing wired to %s", serverName),
				Because: fmt.Sprintf("no environment variable references %s; the service will not be able to find it", pyRepr(serverName)),
				Source:  SourceWarning,
			})
		}
	}
	return decisions
}

func envReferencesServer(env map[string]string, serverName string) bool {
	pattern := shared.URLPattern(serverName)
	for _, value := range env {
		if value == serverName || pattern.MatchString(value) {
			return true
		}
	}
	return false
}

func matchedEnvKeys(env map[string]string, serverName string) []string {
	pattern := shared.URLPattern(serverName)
	var matched []string
	for key, value := range env {
		if value == serverName || pattern.MatchString(value) {
			matched = append(matched, key)
		}
	}
	return matched
}

func scheduleShape(schedule models.Schedule) string {
	if cron, ok := shared.AsCronSchedule(schedule); ok {
		return fmt.Sprintf("cron %s", pyRepr(cron.Expression))
	}
	if rate, ok := shared.AsRateSchedule(schedule); ok {
		return fmt.Sprintf("every %d %s", rate.Value, rate.Unit)
	}
	return fmt.Sprintf("%v", schedule)
}

// ingressDecisions mirrors _ingress_decisions.
func ingressDecisions(composeApp *models.ComposeApplication, semantic *models.Application) []Decision {
	public := semantic.PublicServices()
	if len(public) > 0 {
		var decisions []Decision
		for _, service := range public {
			port := 0
			if service.Ingress.Port != nil {
				port = *service.Ingress.Port
			}
			decisions = append(decisions, Decision{
				Subject: service.Name, Decision: fmt.Sprintf("served at %s on port %d", service.Ingress.Path, port),
				Because: "declared by x-cloud: ingress", Source: SourceDeclared,
			})

			healthPath := service.Ingress.HealthCheck.Path
			if healthPath == "" {
				healthPath = "/"
			}
			// Ported faithfully: Python's `if service.ingress.health_check`
			// is always truthy (default_factory=HealthCheck never leaves
			// it None), so this branch is always "declared" in practice,
			// never "default" -- confirmed by running Python directly
			// rather than assumed from reading the model. Not "fixed" here:
			// this port replicates current behavior, not a corrected
			// version of it.
			decisions = append(decisions, Decision{
				Subject: service.Name, Decision: fmt.Sprintf("healthy when %s returns 2xx/3xx", healthPath),
				Because: "declared", Source: SourceDeclared,
			})
		}
		return decisions
	}

	var published []string
	if composeApp != nil {
		for name, service := range composeApp.Services {
			hasPublished := false
			for _, p := range service.Ports {
				if p.Published != "" {
					hasPublished = true
					break
				}
			}
			if hasPublished {
				published = append(published, name)
			}
		}
		sort.Strings(published)
	} else {
		// Without the raw compose model, a service's own resolved port is
		// the closest available signal for "this looked like it wanted to
		// be reached".
		for i := range semantic.Services {
			if semantic.Services[i].Port != nil {
				published = append(published, semantic.Services[i].Name)
			}
		}
	}

	var because string
	if len(published) > 0 {
		because = fmt.Sprintf(
			"%s publish ports; declare x-cloud: ingress on whichever should be reachable from outside",
			strings.Join(published, ", "),
		)
	} else {
		because = "no service publishes a port"
	}
	return []Decision{{Subject: "application", Decision: "NOT reachable from outside", Because: because, Source: SourceWarning}}
}

// Render renders decisions as grouped, readable text, mirroring render().
// Rich markup tags ([bold], [cyan], etc.) are preserved verbatim so the
// same string can be fed through a Rich-compatible terminal renderer;
// this function does not itself interpret them.
func Render(decisions []Decision) string {
	marks := map[DecisionSource]string{
		SourceDeclared: "[cyan]declared[/]",
		SourceInferred: "[green]inferred[/]",
		SourceDefault:  "[dim]default [/]",
		SourceWarning:  "[yellow]warning [/]",
	}

	var subjects []string
	seen := map[string]bool{}
	for _, d := range decisions {
		if !seen[d.Subject] {
			seen[d.Subject] = true
			subjects = append(subjects, d.Subject)
		}
	}

	var lines []string
	for _, subject := range subjects {
		lines = append(lines, "", fmt.Sprintf("[bold]%s[/]", subject))
		for _, d := range decisions {
			if d.Subject != subject {
				continue
			}
			lines = append(lines, fmt.Sprintf("  %s  %s", marks[d.Source], d.Decision))
			lines = append(lines, fmt.Sprintf("            [dim]%s[/]", d.Because))
		}
	}

	warnings := 0
	for _, d := range decisions {
		if d.Source == SourceWarning {
			warnings++
		}
	}
	if warnings > 0 {
		lines = append(lines, "", fmt.Sprintf("%d decision(s), [yellow]%d worth checking[/]", len(decisions), warnings))
	} else {
		lines = append(lines, "", fmt.Sprintf("%d decision(s)", len(decisions)))
	}

	return strings.Join(lines, "\n")
}
