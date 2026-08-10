package models

import "fmt"

type Capability string

const (
	CapabilityContainer     Capability = "container"
	CapabilityDatabase      Capability = "database"
	CapabilityCache         Capability = "cache"
	CapabilityObjectStorage Capability = "object-storage"
)

type ScheduleKind string

const (
	ScheduleKindCron ScheduleKind = "cron"
	ScheduleKindRate ScheduleKind = "rate"
)

type RateUnit string

const (
	RateUnitMinutes RateUnit = "minutes"
	RateUnitHours   RateUnit = "hours"
	RateUnitDays    RateUnit = "days"
)

type CronSchedule struct {
	Kind       ScheduleKind `json:"kind"`
	Expression string       `json:"expression"`
}

type RateSchedule struct {
	Kind  ScheduleKind `json:"kind"`
	Value int          `json:"value"`
	Unit  RateUnit     `json:"unit"`
}

type Schedule interface {
	scheduleMarker()
}

func (CronSchedule) scheduleMarker() {}
func (RateSchedule) scheduleMarker() {}

type AutoScalingMetricType string

const (
	AutoScalingMetricCPU               AutoScalingMetricType = "cpu"
	AutoScalingMetricMemory            AutoScalingMetricType = "memory"
	AutoScalingMetricRequestsPerTarget AutoScalingMetricType = "requests_per_target"
)

type AutoScalingMetric struct {
	Type        AutoScalingMetricType `json:"type"`
	TargetValue float64               `json:"target_value"`
}

type AutoScalingConfig struct {
	Metrics          []AutoScalingMetric `json:"metrics"`
	ScaleInCooldown  int                 `json:"scale_in_cooldown"`
	ScaleOutCooldown int                 `json:"scale_out_cooldown"`
}

type HealthCheckType string

const (
	HealthCheckTypeHTTP HealthCheckType = "http"
	HealthCheckTypeTCP  HealthCheckType = "tcp"
)

type HealthCheck struct {
	Type HealthCheckType `json:"type"`
	Path string          `json:"path,omitempty"`
	Port *int            `json:"port,omitempty"`
}

type Ingress struct {
	Path        string      `json:"path"`
	Port        *int        `json:"port,omitempty"`
	HealthCheck HealthCheck `json:"health_check"`
}

type ServiceSize string

const (
	ServiceSizeSmall  ServiceSize = "small"
	ServiceSizeMedium ServiceSize = "medium"
	ServiceSizeLarge  ServiceSize = "large"
)

type Service struct {
	Name                     string             `json:"name"`
	Image                    string             `json:"image"`
	Capability               Capability         `json:"capability"`
	Size                     ServiceSize        `json:"size"`
	CPU                      *int               `json:"cpu,omitempty"`
	Memory                   *int               `json:"memory,omitempty"`
	Port                     *int               `json:"port,omitempty"`
	DatabaseName             *string            `json:"database_name,omitempty"`
	BuildContext             *string            `json:"build_context,omitempty"`
	Dockerfile               *string            `json:"dockerfile,omitempty"`
	Command                  []string           `json:"command,omitempty"`
	StartupGracePeriod       *int               `json:"startup_grace_period,omitempty"`
	MinScale                 int                `json:"min_scale"`
	MaxScale                 int                `json:"max_scale"`
	AutoScaling              *AutoScalingConfig `json:"auto_scaling,omitempty"`
	Schedule                 Schedule           `json:"schedule,omitempty"`
	CDNEnabled               bool               `json:"cdn_enabled"`
	Ingress                  *Ingress           `json:"ingress,omitempty"`
	NetworkIsolationSegments []string           `json:"network_isolation_segments,omitempty"`
	Env                      map[string]string  `json:"env,omitempty"`
	Config                   []string           `json:"config,omitempty"`
	Secrets                  []string           `json:"secrets,omitempty"`
}

func (s *Service) Validate() error {
	if s.Capability == CapabilityDatabase && s.DatabaseName == nil {
		return fmt.Errorf("service %q is a database and must carry a database_name; the normalizer derives one from the compose file, and no backend should have to guess", s.Name)
	}
	return nil
}

type Connection struct {
	Host        string  `json:"host"`
	Port        *int    `json:"port,omitempty"`
	Name        *string `json:"name,omitempty"`
	Username    *string `json:"username,omitempty"`
	Password    *string `json:"password,omitempty"`
	Database    *string `json:"database,omitempty"`
	AddressedBy string  `json:"addressed_by"`
}

func (c *Connection) BareReference() string {
	if c.AddressedBy == "name" && c.Name != nil {
		return *c.Name
	}
	return c.Host
}

type Relationship struct {
	Client string `json:"client"`
	Server string `json:"server"`
	Port   *int   `json:"port,omitempty"`
}

type Application struct {
	Name          string         `json:"name"`
	Services      []Service      `json:"services"`
	Relationships []Relationship `json:"relationships"`
}

func (a *Application) PublicServices() []Service {
	var public []Service
	for _, s := range a.Services {
		if s.Ingress != nil {
			public = append(public, s)
		}
	}
	return public
}
