package models

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
)

type ComposeApplication struct {
	Services map[string]ComposeService     `json:"services,omitempty"`
	Networks map[string]*NetworkDefinition `json:"networks,omitempty"`
	Volumes  map[string]interface{}        `json:"volumes,omitempty"`
	Secrets  map[string]ComposeSecret      `json:"secrets,omitempty"`
}

type ComposeService struct {
	Image       string             `json:"image,omitempty"`
	Build       *BuildConfig       `json:"build,omitempty"`
	Ports       []PortConfig       `json:"ports,omitempty"`
	Environment map[string]*string `json:"environment,omitempty"`
	// DependsOn only ever needs to answer "which services does this one
	// depend on" — Normalize reads nothing but the map's keys, to build
	// Relationships. compose-go's own condition/required semantics
	// (service_healthy, service_completed_successfully, etc.) describe
	// startup ordering, which cloudcompose does not model at all: connectivity
	// comes from `networks:`, not from depends_on (see normalizer_test.go's
	// TestNormalizeRelationships). A bare marker type, rather than a
	// struct with fields nothing reads, makes that explicit instead of
	// letting fields imply a meaning cloudcompose does not act on. This is a
	// pre-existing simplification opportunity, not a regression.
	DependsOn   map[string]struct{}    `json:"depends_on,omitempty"`
	Networks    map[string]interface{} `json:"networks,omitempty"`
	Volumes     []VolumeDefinition     `json:"volumes,omitempty"`
	Secrets     []interface{}          `json:"secrets,omitempty"`
	Command     interface{}            `json:"command,omitempty"`
	XCloud      interface{}            `json:"x-cloud,omitempty"`
	PlatformEnv []string               `json:"platform_env,omitempty"`
}

func (s *ComposeService) GetNetworks() []string {
	if len(s.Networks) == 0 {
		return nil
	}
	networks := make([]string, 0, len(s.Networks))
	for name := range s.Networks {
		networks = append(networks, name)
	}
	return networks
}

// BuildConfig carries only what this codebase ever reads back out: the
// context feeds docker_image.build.context in the Terraform this compiles
// to, and Dockerfile/Target likewise. compose-go's own BuildConfig has
// 25+ more fields (CacheFrom, Secrets, Ulimits, SSH forwarding, Platforms,
// ...) — none of them modeled here because nothing downstream consumes
// them. Args was carried and converted here for one release without ever
// being read; removed rather than kept as a field implying support that
// does not exist.
type BuildConfig struct {
	Context    string `json:"context,omitempty"`
	Dockerfile string `json:"dockerfile,omitempty"`
	Target     string `json:"target,omitempty"`
}

// PortConfig. Protocol (tcp/udp) was carried and converted here for one
// release without ever being read downstream — every inferred resource
// this compiles to assumes TCP. Removed rather than kept implying a
// choice nothing acts on.
type PortConfig struct {
	Target    uint32 `json:"target"`
	Published string `json:"published,omitempty"`
}

type ComposeSecret struct {
	File string `json:"file,omitempty"`
}

// NetworkDefinition. Name was carried and converted here for one release
// without ever being read — RejectUnsupportedNetworks only ever checks
// External, and reports the network by its compose-file key (the map this
// type lives in), not by this field. Removed rather than kept implying a
// use nothing makes of it.
type NetworkDefinition struct {
	External bool `json:"external,omitempty"`
}

// VolumeDefinition. Target and ReadOnly were carried and converted here
// for one release without ever being read — NamedVolumeSource (the only
// consumer) only inspects Type and Source: whether a mount is a named
// volume at all, and if so, what it's called. The rejection error reports
// the volume's name, not its mount path or read/write mode. Removed
// rather than kept implying a use nothing makes of them.
type VolumeDefinition struct {
	Type   string `json:"type"`
	Source string `json:"source,omitempty"`
}

// XCloud is the `x-cloud` block on a service.
//
// Unknown keys and out-of-range values (via per-field bounds checks) are
// rejected outright, enforced by hand in UnmarshalJSON since Go's
// encoding/json has no declarative validation equivalent — deliberately,
// since the failure this exists to prevent is exactly a misspelled key or
// an out-of-range value being silently accepted rather than rejected:
// `capabilty: database` was once silently dropped, and the service
// deployed as whatever the compiler guessed from the image name instead.
type XCloud struct {
	Capability             *Capability               `json:"capability,omitempty"`
	Ingress                *IngressConfig            `json:"ingress,omitempty"`
	HealthCheck            *HealthCheckConfig        `json:"health_check,omitempty"`
	AutoScaling            *ComposeAutoScalingConfig `json:"auto_scaling,omitempty"`
	Size                   string                    `json:"size,omitempty"`
	CPU                    *int                      `json:"cpu,omitempty"`
	Memory                 *int                      `json:"memory,omitempty"`
	MinScale               int                       `json:"min_scale"`
	MaxScale               int                       `json:"max_scale"`
	Schedule               string                    `json:"schedule,omitempty"`
	CDN                    bool                      `json:"cdn,omitempty"`
	StartupGracePeriod     *int                      `json:"startup_grace_period,omitempty"`
	HealthCheckGracePeriod *int                      `json:"health_check_grace_period,omitempty"`
}

var validCapabilities = map[string]bool{
	"container": true, "database": true, "cache": true, "object-storage": true,
}

var validSizes = map[string]bool{"small": true, "medium": true, "large": true}

// intOrString accepts a JSON number or a numeric JSON string ("5").
// docker-compose users occasionally quote numeric x-cloud values, so this
// coerces a quoted number the same way an unquoted one would be handled
// rather than rejecting it outright: Go's encoding/json does not coerce
// string-to-int by default, and returning that error unchanged would turn
// any quoted number into a hard, surprising break for a file that used to
// compile.
func intOrString(raw json.RawMessage, field string) (int, error) {
	var asInt int
	if err := json.Unmarshal(raw, &asInt); err == nil {
		return asInt, nil
	}
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		if n, err := strconv.Atoi(asString); err == nil {
			return n, nil
		}
	}
	return 0, fmt.Errorf("%s must be an integer, got %s", field, string(raw))
}

// UnmarshalJSON rejects unknown keys and out-of-range values outright
// rather than silently ignoring or clamping them.
func (x *XCloud) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	known := map[string]bool{
		"capability": true, "ingress": true, "health_check": true,
		"auto_scaling": true, "size": true, "cpu": true, "memory": true,
		"min_scale": true, "max_scale": true, "schedule": true, "cdn": true,
		"startup_grace_period": true, "health_check_grace_period": true,
	}
	unknown := make([]string, 0)
	for key := range raw {
		if !known[key] {
			unknown = append(unknown, key)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return fmt.Errorf("unknown field(s): %v", unknown)
	}

	result := XCloud{Size: "small", MinScale: 1, MaxScale: 1}

	if v, ok := raw["capability"]; ok {
		var s string
		if err := json.Unmarshal(v, &s); err != nil {
			return err
		}
		if !validCapabilities[s] {
			return fmt.Errorf("capability must be one of container, database, cache, object-storage, got %q", s)
		}
		c := Capability(s)
		result.Capability = &c
	}
	if v, ok := raw["ingress"]; ok {
		if string(v) == "null" {
			// Bare `ingress:` with nothing under it declares a default
			// route. Left as null it parses as no ingress at all, quietly
			// making the service internal — reintroducing, at the only
			// place it still could, exactly the silent non-exposure this
			// design exists to prevent.
			result.Ingress = &IngressConfig{}
		} else {
			result.Ingress = &IngressConfig{}
			if err := json.Unmarshal(v, result.Ingress); err != nil {
				return err
			}
		}
	}
	if v, ok := raw["health_check"]; ok {
		result.HealthCheck = &HealthCheckConfig{}
		if err := json.Unmarshal(v, result.HealthCheck); err != nil {
			return err
		}
	}
	if v, ok := raw["auto_scaling"]; ok {
		result.AutoScaling = &ComposeAutoScalingConfig{}
		if err := json.Unmarshal(v, result.AutoScaling); err != nil {
			return err
		}
	}
	if v, ok := raw["size"]; ok {
		if err := json.Unmarshal(v, &result.Size); err != nil {
			return err
		}
		if !validSizes[result.Size] {
			return fmt.Errorf("size must be one of small, medium, large, got %q", result.Size)
		}
	}
	if v, ok := raw["cpu"]; ok {
		n, err := intOrString(v, "cpu")
		if err != nil {
			return err
		}
		if n <= 0 {
			return fmt.Errorf("cpu must be greater than 0, got %d", n)
		}
		result.CPU = &n
	}
	if v, ok := raw["memory"]; ok {
		n, err := intOrString(v, "memory")
		if err != nil {
			return err
		}
		if n <= 0 {
			return fmt.Errorf("memory must be greater than 0, got %d", n)
		}
		result.Memory = &n
	}
	if v, ok := raw["min_scale"]; ok {
		n, err := intOrString(v, "min_scale")
		if err != nil {
			return err
		}
		if n < 0 {
			return fmt.Errorf("min_scale must be greater than or equal to 0, got %d", n)
		}
		result.MinScale = n
	}
	if v, ok := raw["max_scale"]; ok {
		n, err := intOrString(v, "max_scale")
		if err != nil {
			return err
		}
		if n < 1 {
			return fmt.Errorf("max_scale must be greater than or equal to 1, got %d", n)
		}
		result.MaxScale = n
	}
	if v, ok := raw["schedule"]; ok {
		if err := json.Unmarshal(v, &result.Schedule); err != nil {
			return err
		}
	}
	if v, ok := raw["cdn"]; ok {
		if err := json.Unmarshal(v, &result.CDN); err != nil {
			return err
		}
	}
	if v, ok := raw["startup_grace_period"]; ok {
		n, err := intOrString(v, "startup_grace_period")
		if err != nil {
			return err
		}
		if n < 0 {
			return fmt.Errorf("startup_grace_period must be greater than or equal to 0, got %d", n)
		}
		result.StartupGracePeriod = &n
	}
	if v, ok := raw["health_check_grace_period"]; ok {
		n, err := intOrString(v, "health_check_grace_period")
		if err != nil {
			return err
		}
		if n < 0 {
			return fmt.Errorf("health_check_grace_period must be greater than or equal to 0, got %d", n)
		}
		result.HealthCheckGracePeriod = &n
	}

	*x = result
	return nil
}

func (x *XCloud) GetGracePeriod() *int {
	if x.StartupGracePeriod != nil {
		return x.StartupGracePeriod
	}
	return x.HealthCheckGracePeriod
}

type IngressConfig struct {
	Path        string            `json:"path"`
	Port        *int              `json:"port,omitempty"`
	HealthCheck HealthCheckConfig `json:"health_check,omitempty"`
}

type HealthCheckConfig struct {
	Type string `json:"type,omitempty"`
	Path string `json:"path,omitempty"`
	Port *int   `json:"port,omitempty"`
}

type ComposeAutoScalingConfig struct {
	Metrics          []AutoScalingMetricConfig `json:"metrics,omitempty"`
	ScaleInCooldown  int                       `json:"scale_in_cooldown,omitempty"`
	ScaleOutCooldown int                       `json:"scale_out_cooldown,omitempty"`
}

type AutoScalingMetricConfig struct {
	Type   string  `json:"type,omitempty"`
	Target float64 `json:"target,omitempty"`
}
