package gcp

import (
	"strings"

	"github.com/gecburton/cloudcompose/internal/compiler/shared"
	"github.com/gecburton/cloudcompose/internal/models"
)

// GenerateGcp renders a Terraform JSON manifest for the given GCP
// resources and environment.
func GenerateGcp(resources *models.GcpResources, env *models.GcpEnvironment, projectName string) (string, error) {
	requiredProviders := map[string]any{
		"google": map[string]any{"source": "hashicorp/google", "version": "~> 5.0"},
		"docker": map[string]any{"source": "kreuzwerker/docker", "version": "~> 3.0"},
		"random": map[string]any{"source": "hashicorp/random", "version": "~> 3.6"},
	}

	googleProvider := map[string]any{
		"project": env.ProjectID,
		"region":  env.Region,
	}
	if env.GcpEndpoint != nil && *env.GcpEndpoint != "" {
		googleProvider["credentials"] = *env.GcpEndpoint
	}

	provider := map[string]any{
		"google": googleProvider,
		"docker": map[string]any{},
		"random": map[string]any{},
	}

	resourceBlocksMap, err := shared.StructResourceBlocks(resources)
	if err != nil {
		return "", err
	}

	manifest := models.TerraformManifest{
		Terraform: map[string]any{"required_providers": requiredProviders},
		Provider:  provider,
		Resource:  resourceBlocksMap,
	}
	if backendBlock := shared.AppBackendBlock(env.Name, projectName, env.Backend); backendBlock != nil {
		manifest.Terraform["backend"] = backendBlock
	}
	if referencesProjectNumber(resources) {
		manifest.Data = map[string]any{"google_project": map[string]any{"current": map[string]any{"project_id": env.ProjectID}}}
	}

	return shared.MarshalIndentedJSON(manifest)
}

func referencesProjectNumber(resources *models.GcpResources) bool {
	for _, cr := range resources.CloudRunService {
		if cr.Template == nil {
			continue
		}
		for _, c := range cr.Template.Spec.Containers {
			for _, e := range c.Env {
				if strings.Contains(e.Value, ProjectNumberRef) {
					return true
				}
			}
		}
	}
	return false
}
