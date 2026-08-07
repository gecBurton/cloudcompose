package models

// Cloud-agnostic Terraform resource models, shared across cloud backends.
//
// These mirror composey/models/terraform_common.py: every backend that
// builds from source pushes through the same Docker provider resources
// (ECR, ACR, Artifact Registry all speak the same registry protocol from
// Docker's perspective), and every backend that provisions a managed
// database generates its own master password the same way rather than
// accepting one from the compose file, which never had a real secret to
// give in the first place.

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
