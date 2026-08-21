package models

// Cloud-agnostic Terraform resource models, shared across cloud backends.

type DockerImage struct {
	Name     string            `json:"name"`
	Build    any               `json:"build"`
	Triggers map[string]string `json:"triggers,omitempty"`
}

type DockerRegistryImage struct {
	Name         string            `json:"name"`
	KeepRemotely bool              `json:"keep_remotely"`
	Triggers     map[string]string `json:"triggers,omitempty"`
}

func NewDockerRegistryImage() DockerRegistryImage {
	return DockerRegistryImage{KeepRemotely: true}
}

type RandomPassword struct {
	Length  int  `json:"length"`
	Special bool `json:"special"`
}

func NewRandomPassword() RandomPassword {
	return RandomPassword{Length: 16, Special: false}
}
