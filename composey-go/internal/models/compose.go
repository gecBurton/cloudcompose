package models

type DockerApplication struct {
	Services map[string]DockerService `json:"services,omitempty"`
	Networks map[string]interface{}   `json:"networks,omitempty"`
	Volumes  map[string]interface{}   `json:"volumes,omitempty"`
	Secrets  map[string]DockerSecret  `json:"secrets,omitempty"`
}

type DockerService struct {
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

type DockerSecret struct {
	File string `json:"file,omitempty"`
}
