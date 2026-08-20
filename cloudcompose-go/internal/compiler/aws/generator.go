package aws

import (
	"github.com/gecburton/cloudcompose/internal/compiler/shared"
	"github.com/gecburton/cloudcompose/internal/models"
)

// awsProvider builds an AWS provider configuration for a given region.
// When env.AwsEndpoint is set (LocalStack-style testing), every AWS service
// endpoint used by this compiler is pointed at it and credential
// validation is skipped.
func awsProvider(env *models.AwsEnvironment, region string) map[string]any {
	provider := map[string]any{"region": region}

	if env.AwsEndpoint != nil {
		provider["access_key"] = "test"
		provider["secret_key"] = "test"
		provider["skip_credentials_validation"] = true
		provider["skip_metadata_api_check"] = true
		provider["skip_requesting_account_id"] = true
		provider["s3_use_path_style"] = true
		provider["endpoints"] = map[string]any{
			"s3":                   *env.AwsEndpoint,
			"ecs":                  *env.AwsEndpoint,
			"ec2":                  *env.AwsEndpoint,
			"secretsmanager":       *env.AwsEndpoint,
			"iam":                  *env.AwsEndpoint,
			"elasticloadbalancing": *env.AwsEndpoint,
			"cloudwatch":           *env.AwsEndpoint,
			"logs":                 *env.AwsEndpoint,
			"wafv2":                *env.AwsEndpoint,
		}
	}

	return provider
}

// resourceBlocks converts AWSResources into the map Terraform's JSON syntax
// expects under "resource": one entry per resource type, present only if it
// has at least one instance. Marshalling AWSResources directly and
// round-tripping through a map[string]any is deliberate, not incidental: the
// struct's own `omitempty` tags are the single source of truth for which
// resource types are "empty" (len==0 maps), so this can't drift from the
// struct definition the way a hand-written list of non-empty checks could.
// Delegates to shared.StructResourceBlocks, which all three clouds' own
// generators use the same way.
func resourceBlocks(resources *models.AWSResources) (map[string]any, error) {
	return shared.StructResourceBlocks(resources)
}

// hasAnyCloudfrontWebACL reports whether any WAF Web ACL in resources is
// CLOUDFRONT-scoped, which decides whether an aliased provider pinned to
// us-east-1 is needed (CLOUDFRONT-scoped ACLs can only be created there,
// regardless of the environment's own region).
func hasCloudfrontScopedWebACL(resources *models.AWSResources) bool {
	for _, acl := range resources.Wafv2WebAcl {
		if acl.Scope == "CLOUDFRONT" {
			return true
		}
	}
	return false
}

// GenerateAWS renders a Terraform JSON manifest for the given AWS
// resources and environment. Key ordering is deterministic because
// encoding/json.Marshal sorts map keys alphabetically at every level.
//
// projectName is this app's own name (the same value `cloudcompose
// compile -p`/`--project` resolves to -- see compile.go's own
// resolveProjectName), used only to derive this app's own backend
// state key when env.Backend is set (shared.AppBackendBlock); it has no
// effect at all when env.Backend is nil (today's default -- see
// docs/multi-user-state.md).
func GenerateAWS(resources *models.AWSResources, env *models.AwsEnvironment, projectName string) (string, error) {
	provider := awsProvider(env, env.Region)

	requiredProviders := map[string]any{
		"aws":    map[string]any{"source": "hashicorp/aws", "version": "~> 5.0"},
		"random": map[string]any{"source": "hashicorp/random", "version": "~> 3.6"},
	}
	providers := map[string]any{"aws": provider}
	dataSources := map[string]any{}

	// If any service builds from source, wire up the docker provider so it
	// can build images and push to ECR, authenticated via an ECR token data
	// source.
	if len(resources.DockerImage) > 0 {
		requiredProviders["docker"] = map[string]any{
			"source":  "kreuzwerker/docker",
			"version": "~> 3.0",
		}
		dataSources["aws_ecr_authorization_token"] = map[string]any{"token": map[string]any{}}
		providers["docker"] = map[string]any{
			"registry_auth": map[string]any{
				"address":  "${data.aws_ecr_authorization_token.token.proxy_endpoint}",
				"username": "${data.aws_ecr_authorization_token.token.user_name}",
				"password": "${data.aws_ecr_authorization_token.token.password}",
			},
		}
	}

	// CloudFront origins reference the shared ALB by DNS name, which the
	// environment only supplies as an ARN. Look it up at apply time.
	if len(resources.CloudfrontDistribution) > 0 && env.AlbArn != nil {
		dataSources["aws_lb"] = map[string]any{
			shared.ALBDataSourceKey: map[string]any{"arn": *env.AlbArn},
		}
	}

	// CLOUDFRONT-scoped WAF web ACLs can only be created in us-east-1, so
	// they are pinned to an aliased provider rather than the environment's
	// region.
	if hasCloudfrontScopedWebACL(resources) {
		edgeProvider := awsProvider(env, shared.CloudFrontScopeRegion)
		edgeProvider["alias"] = shared.CloudFrontProviderAlias
		providers["aws"] = []any{provider, edgeProvider}
	}

	resourceBlocksMap, err := resourceBlocks(resources)
	if err != nil {
		return "", err
	}

	// The manifest's Resource field is never nil (see NewAWSResources'
	// default construction), so "resource" is always present in the
	// output even when empty -- unlike "data", which is omitted when
	// there's nothing to look up. Matched here by giving Resource no
	// omitempty tag (see TerraformManifest).
	terraformBlock := map[string]any{"required_providers": requiredProviders}
	if backendBlock := shared.AppBackendBlock(env.Name, projectName, env.Backend); backendBlock != nil {
		terraformBlock["backend"] = backendBlock
	}
	manifest := models.TerraformManifest{
		Terraform: terraformBlock,
		Provider:  providers,
		Resource:  resourceBlocksMap,
	}
	if len(dataSources) > 0 {
		manifest.Data = dataSources
	}

	return shared.MarshalIndentedJSON(manifest)
}
