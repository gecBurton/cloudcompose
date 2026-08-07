package aws

import (
	"encoding/json"
	"github.com/gecburton/composey/internal/compiler/shared"
	"strings"
	"testing"

	"github.com/gecburton/composey/internal/models"
)

func minimalAwsEnv() *models.AwsEnvironment {
	env := models.NewAwsEnvironment()
	env.Name = "prod"
	env.VpcID = "vpc-123"
	env.PublicSubnets = []string{"subnet-1", "subnet-2"}
	env.PrivateSubnets = []string{"subnet-3", "subnet-4"}
	env.EcsClusterArn = "arn:aws:ecs:us-east-1:123456789012:cluster/prod-cluster"
	return &env
}

// TestGenerateAWS_BaseProviderShape checks the manifest shape produced for
// an application with no resources at all matches generator.py's own
// baseline: just the aws provider, required_providers, and an *empty but
// present* resource block -- Pydantic's default_factory=AWSResources means
// Python's own output always has "resource": {} rather than omitting the
// key, confirmed against a real run of generator.generate() with an empty
// AWSResources() (2026-08-06), so this deliberately does not omit it either.
func TestGenerateAWS_BaseProviderShape(t *testing.T) {
	resources := models.NewAWSResources()
	env := minimalAwsEnv()

	out, err := GenerateAWS(resources, env)
	if err != nil {
		t.Fatalf("GenerateAWS: %v", err)
	}

	var manifest map[string]any
	if err := json.Unmarshal([]byte(out), &manifest); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}

	provider, ok := manifest["provider"].(map[string]any)
	if !ok {
		t.Fatalf("expected provider block, got %#v", manifest["provider"])
	}
	aws, ok := provider["aws"].(map[string]any)
	if !ok {
		t.Fatalf("expected provider.aws, got %#v", provider["aws"])
	}
	if aws["region"] != "us-east-1" {
		t.Errorf("expected region us-east-1, got %v", aws["region"])
	}

	if _, hasDocker := provider["docker"]; hasDocker {
		t.Errorf("did not expect docker provider without a docker_image resource")
	}
	if _, hasData := manifest["data"]; hasData {
		t.Errorf("did not expect a data block with no cloudfront/docker resources")
	}

	tf, ok := manifest["terraform"].(map[string]any)
	if !ok {
		t.Fatalf("expected terraform block, got %#v", manifest["terraform"])
	}
	requiredProviders, ok := tf["required_providers"].(map[string]any)
	if !ok {
		t.Fatalf("expected required_providers, got %#v", tf["required_providers"])
	}
	if _, hasAWS := requiredProviders["aws"]; !hasAWS {
		t.Errorf("expected aws in required_providers")
	}
	if _, hasRandom := requiredProviders["random"]; !hasRandom {
		t.Errorf("expected random in required_providers")
	}
	if _, hasDocker := requiredProviders["docker"]; hasDocker {
		t.Errorf("did not expect docker in required_providers without a build")
	}

	resource, ok := manifest["resource"].(map[string]any)
	if !ok {
		t.Fatalf("expected an empty-but-present resource block, got %#v", manifest["resource"])
	}
	if len(resource) != 0 {
		t.Errorf("expected resource block to be empty, got %#v", resource)
	}
}

// TestGenerateAWS_DockerProviderWiredWhenBuilding mirrors flask example's
// use of build-from-source: any docker_image resource must pull in the
// docker provider, its required_providers entry, and the ECR auth token
// data source.
func TestGenerateAWS_DockerProviderWiredWhenBuilding(t *testing.T) {
	resources := models.NewAWSResources()
	resources.DockerImage["web_image"] = models.DockerImage{
		Name:  "${aws_ecr_repository.web_ecr.repository_url}:latest",
		Build: map[string]any{"context": "."},
	}
	env := minimalAwsEnv()

	out, err := GenerateAWS(resources, env)
	if err != nil {
		t.Fatalf("GenerateAWS: %v", err)
	}

	var manifest map[string]any
	if err := json.Unmarshal([]byte(out), &manifest); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}

	provider := manifest["provider"].(map[string]any)
	if _, hasDocker := provider["docker"]; !hasDocker {
		t.Errorf("expected docker provider when a docker_image resource is present")
	}

	data, ok := manifest["data"].(map[string]any)
	if !ok {
		t.Fatalf("expected data block, got %#v", manifest["data"])
	}
	if _, hasToken := data["aws_ecr_authorization_token"]; !hasToken {
		t.Errorf("expected aws_ecr_authorization_token data source")
	}

	resource, ok := manifest["resource"].(map[string]any)
	if !ok {
		t.Fatalf("expected resource block, got %#v", manifest["resource"])
	}
	if _, hasImage := resource["docker_image"]; !hasImage {
		t.Errorf("expected docker_image resource block")
	}
}

// TestGenerateAWS_CloudfrontScopedWAFPinsProvider checks that a
// CLOUDFRONT-scoped WAF ACL produces an aliased second AWS provider block
// pinned to us-east-1, matching generator.py's own edge_provider handling.
func TestGenerateAWS_CloudfrontScopedWAFPinsProvider(t *testing.T) {
	resources := models.NewAWSResources()
	resources.Wafv2WebAcl["web_waf"] = models.NewWafv2WebAcl()
	acl := resources.Wafv2WebAcl["web_waf"]
	acl.Name = "prod-web-waf"
	acl.Scope = "CLOUDFRONT"
	acl.VisibilityConfig = map[string]any{
		"cloudwatch_metrics_enabled": true,
		"metric_name":                "webWAF",
		"sampled_requests_enabled":   true,
	}
	resources.Wafv2WebAcl["web_waf"] = acl

	env := minimalAwsEnv()
	env.Region = "eu-west-1"

	out, err := GenerateAWS(resources, env)
	if err != nil {
		t.Fatalf("GenerateAWS: %v", err)
	}

	var manifest map[string]any
	if err := json.Unmarshal([]byte(out), &manifest); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}

	provider := manifest["provider"].(map[string]any)
	awsProviders, ok := provider["aws"].([]any)
	if !ok {
		t.Fatalf("expected provider.aws to be a list once a CLOUDFRONT-scoped ACL exists, got %#v", provider["aws"])
	}
	if len(awsProviders) != 2 {
		t.Fatalf("expected exactly 2 aws provider configs, got %d", len(awsProviders))
	}

	primary := awsProviders[0].(map[string]any)
	if primary["region"] != "eu-west-1" {
		t.Errorf("expected primary provider region eu-west-1, got %v", primary["region"])
	}
	edge := awsProviders[1].(map[string]any)
	if edge["region"] != shared.CloudFrontScopeRegion {
		t.Errorf("expected edge provider region %s, got %v", shared.CloudFrontScopeRegion, edge["region"])
	}
	if edge["alias"] != shared.CloudFrontProviderAlias {
		t.Errorf("expected edge provider alias %s, got %v", shared.CloudFrontProviderAlias, edge["alias"])
	}
}

// TestGenerateAWS_Deterministic runs the same input through GenerateAWS
// repeatedly and diffs the output byte-for-byte, per this phase's own review
// discipline (plan.md): every new map-shaped output gets this check as it's
// written, not discovered later the way Phase 2's ordering bug was.
func TestGenerateAWS_Deterministic(t *testing.T) {
	resources := models.NewAWSResources()
	resources.SecurityGroup["public_sg"] = models.SecurityGroup{Name: "prod-app-public", VpcID: "vpc-123", Description: "d"}
	resources.SecurityGroup["private_sg"] = models.SecurityGroup{Name: "prod-app-private", VpcID: "vpc-123", Description: "d2"}
	resources.EcsService["web_service"] = models.NewEcsService()
	resources.DockerImage["web_image"] = models.DockerImage{Name: "x", Build: map[string]any{"context": "."}}
	env := minimalAwsEnv()

	first, err := GenerateAWS(resources, env)
	if err != nil {
		t.Fatalf("GenerateAWS: %v", err)
	}

	for i := 0; i < 5; i++ {
		next, err := GenerateAWS(resources, env)
		if err != nil {
			t.Fatalf("GenerateAWS run %d: %v", i, err)
		}
		if next != first {
			t.Fatalf("run %d differs from run 0:\n--- run0 ---\n%s\n--- run%d ---\n%s", i, first, i, next)
		}
	}
}

// TestGenerateAWS_NoEmptyResourceBlocks checks that a resource type with an
// empty map (e.g. a struct field present with zero entries after JSON
// round-tripping) is stripped from the output, matching generator.py's own
// post-model_dump cleanup. Constructed via strings.Contains rather than
// full unmarshalling since the point under test is precisely which keys
// survive serialization.
func TestGenerateAWS_NoEmptyResourceBlocks(t *testing.T) {
	resources := models.NewAWSResources()
	resources.SecurityGroup["sg"] = models.SecurityGroup{Name: "n", VpcID: "v", Description: "d"}
	env := minimalAwsEnv()

	out, err := GenerateAWS(resources, env)
	if err != nil {
		t.Fatalf("GenerateAWS: %v", err)
	}

	if strings.Contains(out, "aws_ecs_service") {
		t.Errorf("expected no aws_ecs_service key when no ECS services were added:\n%s", out)
	}
	if !strings.Contains(out, "aws_security_group") {
		t.Errorf("expected aws_security_group key:\n%s", out)
	}
}
