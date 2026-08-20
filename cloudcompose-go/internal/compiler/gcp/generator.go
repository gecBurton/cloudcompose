package gcp

import (
	"github.com/gecburton/cloudcompose/internal/compiler/shared"
	"github.com/gecburton/cloudcompose/internal/models"
)

// GenerateGcp renders a Terraform JSON manifest for the given GCP
// resources and environment.
//
// projectName mirrors aws.GenerateAWS's own parameter of the same name
// -- see its doc comment for what it's used for and when it has no
// effect.
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

	return shared.MarshalIndentedJSON(manifest)
}
