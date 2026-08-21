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
	// DependsOn only records which services this one depends on; only
	// the map's keys are read. cloudcompose does not model startup
	// ordering -- connectivity comes from `networks:`, not depends_on.
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

// BuildConfig carries only what this codebase reads back out: Context
// feeds docker_image.build.context in the generated Terraform, and
// Dockerfile/Target likewise.
type BuildConfig struct {
	Context    string `json:"context,omitempty"`
	Dockerfile string `json:"dockerfile,omitempty"`
	Target     string `json:"target,omitempty"`
}

// PortConfig is a service's `ports:` entry. Only TCP is assumed
// downstream, so no protocol field.
type PortConfig struct {
	Target    uint32 `json:"target"`
	Published string `json:"published,omitempty"`
}

type ComposeSecret struct {
	File string `json:"file,omitempty"`
}

// NetworkDefinition is a `networks:` entry. Only External is read
// (RejectUnsupportedNetworks).
type NetworkDefinition struct {
	External bool `json:"external,omitempty"`
}

// VolumeDefinition is a service's `volumes:` entry. Only Type/Source are
// read: whether a mount is a named volume, and if so, what it's called.
type VolumeDefinition struct {
	Type   string `json:"type"`
	Source string `json:"source,omitempty"`
}

// XCloud is the `x-cloud` block on a service.
//
// Unknown keys and out-of-range values are rejected outright in
// UnmarshalJSON rather than silently dropped, since a misspelled key
// silently accepted (e.g. `capabilty: database`) would deploy as
// whatever the compiler guessed instead of failing loudly.
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

// intOrString accepts a JSON number or a numeric JSON string ("5"),
// since docker-compose users occasionally quote numeric x-cloud values.
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
			// route, rather than parsing as no ingress at all.
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
