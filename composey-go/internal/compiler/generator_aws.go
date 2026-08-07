package compiler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/gecburton/composey/internal/models"
)

// awsProvider builds an AWS provider configuration for a given region,
// mirroring generator.py's _aws_provider. When env.AwsEndpoint is set
// (LocalStack-style testing), every AWS service endpoint used by this
// compiler is pointed at it and credential validation is skipped.
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
func resourceBlocks(resources *models.AWSResources) (map[string]any, error) {
	raw, err := json.Marshal(resources)
	if err != nil {
		return nil, fmt.Errorf("marshal AWS resources: %w", err)
	}

	var blocks map[string]any
	if err := unmarshalPreservingNumbers(raw, &blocks); err != nil {
		return nil, fmt.Errorf("unmarshal AWS resources: %w", err)
	}

	return blocks, nil
}

// unmarshalPreservingNumbers decodes JSON into v using json.Number rather
// than float64 for numeric tokens, so a value like "70.0" -- deliberately
// rendered with a decimal point by PyFloat's MarshalJSON to match Python's
// json.dumps(70.0) -- survives the Marshal-then-Unmarshal round trip this
// package uses to convert typed structs into the map[string]any shape the
// final encoder walks. Plain float64 would collapse 70.0 and 70 to the same
// value, silently losing the distinction PyFloat exists to preserve.
func unmarshalPreservingNumbers(data []byte, v any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	return decoder.Decode(v)
}

// hasAnyCloudfrontWebACL reports whether any WAF Web ACL in resources is
// CLOUDFRONT-scoped, which generator.py checks to decide whether an aliased
// provider pinned to us-east-1 is needed (CLOUDFRONT-scoped ACLs can only be
// created there, regardless of the environment's own region).
func hasCloudfrontScopedWebACL(resources *models.AWSResources) bool {
	for _, acl := range resources.Wafv2WebAcl {
		if acl.Scope == "CLOUDFRONT" {
			return true
		}
	}
	return false
}

// GenerateAWS renders a Terraform JSON manifest for the given AWS resources
// and environment, mirroring generator.py's generate(). Key ordering is
// deterministic because encoding/json.Marshal sorts map keys, matching
// Python's json.dumps(..., sort_keys=True).
func GenerateAWS(resources *models.AWSResources, env *models.AwsEnvironment) (string, error) {
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
			ALBDataSourceKey: map[string]any{"arn": *env.AlbArn},
		}
	}

	// CLOUDFRONT-scoped WAF web ACLs can only be created in us-east-1, so
	// they are pinned to an aliased provider rather than the environment's
	// region.
	if hasCloudfrontScopedWebACL(resources) {
		edgeProvider := awsProvider(env, CloudFrontScopeRegion)
		edgeProvider["alias"] = CloudFrontProviderAlias
		providers["aws"] = []any{provider, edgeProvider}
	}

	resourceBlocksMap, err := resourceBlocks(resources)
	if err != nil {
		return "", err
	}

	// generator.py's manifest.resource field is never nil (Pydantic's
	// default_factory=AWSResources), so "resource" is always present in the
	// output even when empty -- unlike "data", which is None (and thus
	// omitted) when there's nothing to look up. Matched here by giving
	// Resource no omitempty tag (see TerraformManifest).
	// generator.py's manifest.resource field is never nil (Pydantic's
	// default_factory=AWSResources), so "resource" is always present in the
	// output even when empty -- unlike "data", which is None (and thus
	// omitted) when there's nothing to look up. Matched here by giving
	// Resource no omitempty tag (see TerraformManifest).
	manifest := models.TerraformManifest{
		Terraform: map[string]any{"required_providers": requiredProviders},
		Provider:  providers,
		Resource:  resourceBlocksMap,
	}
	if len(dataSources) > 0 {
		manifest.Data = dataSources
	}

	return marshalTerraformJSON(manifest)
}

// marshalTerraformJSON renders a manifest the same way generator.py's
// json.dumps(manifest_dict, indent=2, sort_keys=True) does: keys sorted
// alphabetically at every nesting level, and literal "<"/">" characters left
// unescaped (encoding/json's default HTML-safe escaping would otherwise turn
// "~> 5.0" into "~\u003e 5.0", which Terraform's version-constraint parser
// still accepts but which would never match Python's byte-for-byte output).
//
// Struct field declaration order is not alphabetical, and Go's
// encoding/json does not sort struct fields the way it sorts map keys, so
// the manifest is round-tripped through a plain map first -- the same
// map[string]any shape Python's own model_dump() produces before its
// json.dumps call.
func marshalTerraformJSON(manifest models.TerraformManifest) (string, error) {
	raw, err := json.Marshal(manifest)
	if err != nil {
		return "", fmt.Errorf("marshal manifest to intermediate form: %w", err)
	}

	var asMap map[string]any
	if err := unmarshalPreservingNumbers(raw, &asMap); err != nil {
		return "", fmt.Errorf("unmarshal manifest to map: %w", err)
	}

	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(asMap); err != nil {
		return "", fmt.Errorf("encode manifest: %w", err)
	}

	// json.Marshal (and Encoder.Encode) already sort map keys
	// lexicographically, matching Python's sort_keys=True. Encoder.Encode
	// appends a trailing newline; trimmed so output matches json.dumps
	// exactly, which does not.
	return strings.TrimSuffix(buf.String(), "\n"), nil
}
