package gcp

import (
	"github.com/gecburton/cloudcompose/internal/compiler/shared"
	"github.com/gecburton/cloudcompose/internal/models"
)

// GenerateGcp renders a Terraform JSON manifest for the given GCP
// resources and environment.
func GenerateGcp(resources *models.GcpResources, env *models.GcpEnvironment) (string, error) {
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

	return shared.MarshalIndentedJSON(manifest)
}
