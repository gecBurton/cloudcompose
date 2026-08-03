package models

type ComposeApplication struct {
	Services map[string]ComposeService     `json:"services,omitempty"`
	Networks map[string]*NetworkDefinition `json:"networks,omitempty"`
	Volumes  map[string]interface{}        `json:"volumes,omitempty"`
	Secrets  map[string]ComposeSecret      `json:"secrets,omitempty"`
}

type ComposeService struct {
	Image       string                 `json:"image,omitempty"`
	Build       *BuildConfig           `json:"build,omitempty"`
	Ports       []PortConfig           `json:"ports,omitempty"`
	Environment map[string]*string     `json:"environment,omitempty"`
	DependsOn   map[string]Dependency  `json:"depends_on,omitempty"`
	Networks    map[string]interface{} `json:"networks,omitempty"`
	Volumes     []interface{}          `json:"volumes,omitempty"`
	Secrets     []interface{}          `json:"secrets,omitempty"`
	Command     interface{}            `json:"command,omitempty"`
	XComposey   interface{}            `json:"x-composey,omitempty"`
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

type BuildConfig struct {
	Context    string   `json:"context,omitempty"`
	Dockerfile string   `json:"dockerfile,omitempty"`
	Args       []string `json:"args,omitempty"`
	Target     string   `json:"target,omitempty"`
}

type PortConfig struct {
	Target    uint32 `json:"target"`
	Published string `json:"published,omitempty"`
	Protocol  string `json:"protocol,omitempty"`
}

type Dependency struct {
	Condition string `json:"condition,omitempty"`
	Required  bool   `json:"required,omitempty"`
}

type ComposeSecret struct {
	File string `json:"file,omitempty"`
}

type NetworkDefinition struct {
	Name     string `json:"name,omitempty"`
	External bool   `json:"external,omitempty"`
}

type VolumeDefinition struct {
	Type     string `json:"type"`
	Source   string `json:"source,omitempty"`
	Target   string `json:"target"`
	ReadOnly bool   `json:"read_only,omitempty"`
}

type XComposey struct {
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

func (x *XComposey) GetGracePeriod() *int {
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
