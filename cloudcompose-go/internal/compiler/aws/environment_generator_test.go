package aws

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestGenerateAwsEnvironment_ValidStructure checks the shared AWS
// environment generator produces valid JSON with the expected resource
// types and field values present.
func TestGenerateAwsEnvironment_ValidStructure(t *testing.T) {
	t.Parallel()
	out, err := GenerateAwsEnvironment(
		"prod", "eu-west-2", "10.0.0.0/16", 2, true, nil, nil,
		map[string]string{"Team": "platform"}, true, false, 7, 7,
	)
	if err != nil {
		t.Fatalf("GenerateAwsEnvironment failed: %v", err)
	}

	// Structural check (parses as valid JSON with the expected top-level
	// shape) plus a handful of exact-value spot checks, rather than the
	// full multi-hundred-line string: kept here as targeted assertions
	// so a future change that breaks something specific fails with a
	// useful message rather than an unreadable full-document diff.
	var parsed map[string]any
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}

	resource := parsed["resource"].(map[string]any)
	vpc := resource["aws_vpc"].(map[string]any)["prod"].(map[string]any)
	if vpc["cidr_block"] != "10.0.0.0/16" {
		t.Errorf("vpc cidr_block = %v, want 10.0.0.0/16", vpc["cidr_block"])
	}
	tags := vpc["tags"].(map[string]any)
	if tags["Team"] != "platform" || tags["Name"] != "prod" || tags["Environment"] != "prod" {
		t.Errorf("vpc tags = %v, want Team=platform, Name=prod, Environment=prod", tags)
	}

	subnets := resource["aws_subnet"].(map[string]any)
	if _, ok := subnets["prod_public_0"]; !ok {
		t.Errorf("expected prod_public_0 subnet, got keys %v", keysOfAny(subnets))
	}
	if _, ok := subnets["prod_private_1"]; !ok {
		t.Errorf("expected prod_private_1 subnet, got keys %v", keysOfAny(subnets))
	}

	if _, ok := resource["aws_lb"]; !ok {
		t.Errorf("expected an ALB since create_alb=true")
	}

	output := parsed["output"].(map[string]any)
	if _, ok := output["alb_dns_name"]; !ok {
		t.Errorf("expected alb_dns_name output since create_alb=true")
	}
}

func keysOfAny(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// TestGenerateAwsEnvironment_NoAlbOmitsAlbResources mirrors create_alb's
// false branch: no security group, load balancer, or listener at all,
// and no alb_* keys in the environment config or outputs.
func TestGenerateAwsEnvironment_NoAlbOmitsAlbResources(t *testing.T) {
	t.Parallel()
	out, err := GenerateAwsEnvironment(
		"staging", "us-east-1", "10.0.0.0/16", 2, false, nil, nil,
		nil, true, false, 7, 7,
	)
	if err != nil {
		t.Fatalf("GenerateAwsEnvironment failed: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}
	resource := parsed["resource"].(map[string]any)
	for _, resourceType := range []string{"aws_lb", "aws_lb_listener", "aws_security_group"} {
		if _, ok := resource[resourceType]; ok {
			t.Errorf("did not expect %s when create_alb=false", resourceType)
		}
	}
	output := parsed["output"].(map[string]any)
	if _, ok := output["alb_dns_name"]; ok {
		t.Errorf("did not expect alb_dns_name output when create_alb=false")
	}
}

// TestGenerateAwsEnvironment_CertificateEnablesHTTPS mirrors the
// certificate_arn branch: HTTPS listener on 443 with the cert attached.
func TestGenerateAwsEnvironment_CertificateEnablesHTTPS(t *testing.T) {
	t.Parallel()
	cert := "arn:aws:acm:us-east-1:123:certificate/abc"
	out, err := GenerateAwsEnvironment(
		"prod", "us-east-1", "10.0.0.0/16", 2, true, &cert, nil,
		nil, true, false, 7, 7,
	)
	if err != nil {
		t.Fatalf("GenerateAwsEnvironment failed: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}
	listener := parsed["resource"].(map[string]any)["aws_lb_listener"].(map[string]any)["prod"].(map[string]any)
	if listener["port"] != float64(443) {
		t.Errorf("listener port = %v, want 443", listener["port"])
	}
	if listener["protocol"] != "HTTPS" {
		t.Errorf("listener protocol = %v, want HTTPS", listener["protocol"])
	}
	if listener["certificate_arn"] != cert {
		t.Errorf("listener certificate_arn = %v, want %s", listener["certificate_arn"], cert)
	}
}

// TestEnvironmentGenerators_WriteReadableFile is a smoke check that the
// generators produce output writable to disk and re-parseable, the way
// `cloudcompose init` actually uses them.
func TestEnvironmentGenerators_WriteReadableFile(t *testing.T) {
	t.Parallel()
	out, err := GenerateAwsEnvironment(
		"prod", "eu-west-2", "10.0.0.0/16", 2, true, nil, nil,
		nil, true, false, 7, 7,
	)
	if err != nil {
		t.Fatalf("GenerateAwsEnvironment failed: %v", err)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "main.tf.json")
	if err := os.WriteFile(path, []byte(out), 0644); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("written file is not valid JSON: %v", err)
	}
}

// --- Additional coverage-gap tests -----------------------------------------

// TestGenerateAwsEnvironment_ComprehensiveResourcePresence checks that
// every resource type the generator creates (ECS cluster, ECS capacity
// providers, security groups when ALB is enabled, NAT gateways, container
// insights, Fargate capacity provider) is present, checked by
// name/content, not just implied by the earlier spot checks.
func TestGenerateAwsEnvironment_ComprehensiveResourcePresence(t *testing.T) {
	t.Parallel()
	out, err := GenerateAwsEnvironment(
		"prod", "eu-west-2", "10.0.0.0/16", 2, true, nil, nil,
		nil, true, false, 7, 7,
	)
	if err != nil {
		t.Fatalf("GenerateAwsEnvironment failed: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}
	resource := parsed["resource"].(map[string]any)

	cluster, ok := resource["aws_ecs_cluster"].(map[string]any)["prod"].(map[string]any)
	if !ok {
		t.Fatalf("expected aws_ecs_cluster.prod")
	}
	settings := cluster["setting"].([]any)
	if len(settings) != 1 {
		t.Fatalf("expected 1 setting, got %d", len(settings))
	}
	setting := settings[0].(map[string]any)
	if setting["name"] != "containerInsights" || setting["value"] != "enabled" {
		t.Errorf("setting = %v, want containerInsights=enabled", setting)
	}

	capacityProviders, ok := resource["aws_ecs_cluster_capacity_providers"].(map[string]any)["prod"].(map[string]any)
	if !ok {
		t.Fatalf("expected aws_ecs_cluster_capacity_providers.prod")
	}
	providers := capacityProviders["capacity_providers"].([]any)
	if len(providers) != 2 || providers[0] != "FARGATE" || providers[1] != "FARGATE_SPOT" {
		t.Errorf("capacity_providers = %v, want [FARGATE, FARGATE_SPOT]", providers)
	}

	sgs := resource["aws_security_group"].(map[string]any)
	if _, ok := sgs["prod_alb"]; !ok {
		t.Errorf("expected security group keyed prod_alb, got keys %v", keysOfAny(sgs))
	}

	nats := resource["aws_nat_gateway"].(map[string]any)
	eips := resource["aws_eip"].(map[string]any)
	if len(nats) != 2 {
		t.Errorf("expected 2 NAT gateways (one per AZ), got %d", len(nats))
	}
	if len(eips) != 2 {
		t.Errorf("expected 2 EIPs (one per AZ), got %d", len(eips))
	}

	// The environment's facts are exposed only via a plain Terraform
	// output -- no local_file resource writes them to disk (see
	// docs/authored-environment-config.md); cloudcompose main reads them
	// directly via `terraform output -json` instead.
	output, ok := parsed["output"].(map[string]any)["environment"].(map[string]any)["value"].(map[string]any)
	if !ok {
		t.Fatalf("expected output.environment.value")
	}
	if output["vpc_id"] == "" {
		t.Errorf("expected output.environment.value.vpc_id to be set")
	}
}

// TestGenerateAwsEnvironment_CustomAzCount mirrors test_custom_az_count:
// az_count=3 produces 6 total subnets (3 public + 3 private).
func TestGenerateAwsEnvironment_CustomAzCount(t *testing.T) {
	t.Parallel()
	out, err := GenerateAwsEnvironment(
		"prod", "eu-west-2", "10.0.0.0/16", 3, true, nil, nil,
		nil, true, false, 7, 7,
	)
	if err != nil {
		t.Fatalf("GenerateAwsEnvironment failed: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}
	subnets := parsed["resource"].(map[string]any)["aws_subnet"].(map[string]any)
	if len(subnets) != 6 {
		t.Fatalf("expected 6 subnets for az_count=3, got %d: %v", len(subnets), keysOfAny(subnets))
	}
	for _, key := range []string{"prod_public_0", "prod_public_1", "prod_public_2", "prod_private_0", "prod_private_1", "prod_private_2"} {
		if _, ok := subnets[key]; !ok {
			t.Errorf("expected subnet %s, got keys %v", key, keysOfAny(subnets))
		}
	}
}

// TestGenerateAwsEnvironment_CustomVpcCidr mirrors test_custom_vpc_cidr:
// a genuinely non-default CIDR is respected.
func TestGenerateAwsEnvironment_CustomVpcCidr(t *testing.T) {
	t.Parallel()
	out, err := GenerateAwsEnvironment(
		"prod", "eu-west-2", "172.16.0.0/16", 2, true, nil, nil,
		nil, true, false, 7, 7,
	)
	if err != nil {
		t.Fatalf("GenerateAwsEnvironment failed: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}
	vpc := parsed["resource"].(map[string]any)["aws_vpc"].(map[string]any)["prod"].(map[string]any)
	if vpc["cidr_block"] != "172.16.0.0/16" {
		t.Errorf("cidr_block = %v, want 172.16.0.0/16", vpc["cidr_block"])
	}
}

// TestGenerateAwsEnvironment_HyphenatedName mirrors
// test_hyphenated_name_converted_to_underscores: resource keys are
// underscored, but the Name tag keeps the hyphenated form.
func TestGenerateAwsEnvironment_HyphenatedName(t *testing.T) {
	t.Parallel()
	out, err := GenerateAwsEnvironment(
		"my-prod-env", "eu-west-2", "10.0.0.0/16", 2, true, nil, nil,
		nil, true, false, 7, 7,
	)
	if err != nil {
		t.Fatalf("GenerateAwsEnvironment failed: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}
	resource := parsed["resource"].(map[string]any)
	vpcs := resource["aws_vpc"].(map[string]any)
	vpc, ok := vpcs["my_prod_env"].(map[string]any)
	if !ok {
		t.Fatalf("expected VPC keyed my_prod_env, got keys %v", keysOfAny(vpcs))
	}
	tags := vpc["tags"].(map[string]any)
	if tags["Name"] != "my-prod-env" {
		t.Errorf("Name tag = %v, want my-prod-env (hyphens preserved in the tag value)", tags["Name"])
	}
}

// TestGenerateAwsEnvironment_RetainDataFalse mirrors
// test_retain_data_flag_in_output.
func TestGenerateAwsEnvironment_RetainDataFalse(t *testing.T) {
	t.Parallel()
	out, err := GenerateAwsEnvironment(
		"prod", "eu-west-2", "10.0.0.0/16", 2, true, nil, nil,
		nil, false, false, 7, 7,
	)
	if err != nil {
		t.Fatalf("GenerateAwsEnvironment failed: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}
	envConfig := parsed["output"].(map[string]any)["environment"].(map[string]any)["value"].(map[string]any)
	if envConfig["retain_data_on_destroy"] != false {
		t.Errorf("retain_data_on_destroy = %v, want false", envConfig["retain_data_on_destroy"])
	}
}

// TestGenerateAwsEnvironment_LogRetentionDaysFlowsIntoOutput is the
// counterpart to docs/azure-aws-parity-todo.md's "per-service
// log-retention" item: LogRetentionDays existed on the runtime
// AwsEnvironment model and was read by aws/compute.go's CloudWatch Log
// Group inference, but had no environment.yaml field and was never
// written into this output block -- dead on the input side, always
// silently defaulting to 7 no matter what a user wrote. This checks the
// value a caller passes in actually reaches the output block
// `LoadAwsEnvironment` later reads back, not just that the generator
// runs without error.
func TestGenerateAwsEnvironment_LogRetentionDaysFlowsIntoOutput(t *testing.T) {
	t.Parallel()
	out, err := GenerateAwsEnvironment(
		"prod", "eu-west-2", "10.0.0.0/16", 2, true, nil, nil,
		nil, true, false, 7, 90,
	)
	if err != nil {
		t.Fatalf("GenerateAwsEnvironment failed: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}
	envConfig := parsed["output"].(map[string]any)["environment"].(map[string]any)["value"].(map[string]any)
	if envConfig["log_retention_days"] != float64(90) {
		t.Errorf("log_retention_days = %v, want 90", envConfig["log_retention_days"])
	}
}

// TestGenerateAwsEnvironment_OutputsIncludeAllRequiredFields mirrors
// test_outputs_include_required_fields and test_outputs_include_alb_when_created.
func TestGenerateAwsEnvironment_OutputsIncludeAllRequiredFields(t *testing.T) {
	t.Parallel()
	out, err := GenerateAwsEnvironment(
		"prod", "eu-west-2", "10.0.0.0/16", 2, true, nil, nil,
		nil, true, false, 7, 7,
	)
	if err != nil {
		t.Fatalf("GenerateAwsEnvironment failed: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}
	envConfig := parsed["output"].(map[string]any)["environment"].(map[string]any)["value"].(map[string]any)

	for _, field := range []string{
		"target", "name", "region", "vpc_id", "public_subnets", "private_subnets",
		"ecs_cluster_arn", "retain_data_on_destroy", "alb_arn", "alb_listener_arn",
		"alb_security_group_id",
	} {
		if _, ok := envConfig[field]; !ok {
			t.Errorf("expected output.environment.value to include %q, got keys %v", field, keysOfAny(envConfig))
		}
	}
	if envConfig["target"] != "aws" {
		t.Errorf("target = %v, want aws", envConfig["target"])
	}
}

// TestGenerateAwsEnvironment_NoAlbExcludesAlbOutputFields mirrors
// test_outputs_exclude_alb_when_not_created.
func TestGenerateAwsEnvironment_NoAlbExcludesAlbOutputFields(t *testing.T) {
	t.Parallel()
	out, err := GenerateAwsEnvironment(
		"prod", "eu-west-2", "10.0.0.0/16", 2, false, nil, nil,
		nil, true, false, 7, 7,
	)
	if err != nil {
		t.Fatalf("GenerateAwsEnvironment failed: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}
	envConfig := parsed["output"].(map[string]any)["environment"].(map[string]any)["value"].(map[string]any)
	for _, field := range []string{"alb_arn", "alb_listener_arn", "alb_security_group_id"} {
		if _, ok := envConfig[field]; ok {
			t.Errorf("did not expect %q in output when create_alb=false", field)
		}
	}
}

// TestGenerateAwsEnvironment_HttpListenerWhenNoCertificate mirrors
// test_http_listener_when_no_certificate.
func TestGenerateAwsEnvironment_HttpListenerWhenNoCertificate(t *testing.T) {
	t.Parallel()
	out, err := GenerateAwsEnvironment(
		"prod", "eu-west-2", "10.0.0.0/16", 2, true, nil, nil,
		nil, true, false, 7, 7,
	)
	if err != nil {
		t.Fatalf("GenerateAwsEnvironment failed: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}
	listener := parsed["resource"].(map[string]any)["aws_lb_listener"].(map[string]any)["prod"].(map[string]any)
	if listener["port"] != float64(80) {
		t.Errorf("port = %v, want 80", listener["port"])
	}
	if listener["protocol"] != "HTTP" {
		t.Errorf("protocol = %v, want HTTP", listener["protocol"])
	}
	if _, ok := listener["ssl_policy"]; ok {
		t.Errorf("did not expect ssl_policy without a certificate")
	}
}

// TestGenerateAwsEnvironment_AwsEndpointFlowsIntoProvider mirrors
// test_init_with_aws_endpoint: a custom endpoint appears in the provider
// block's endpoints map.
func TestGenerateAwsEnvironment_AwsEndpointFlowsIntoProvider(t *testing.T) {
	t.Parallel()
	endpoint := "http://localhost:4566"
	out, err := GenerateAwsEnvironment(
		"prod", "eu-west-2", "10.0.0.0/16", 2, true, nil, &endpoint,
		nil, true, false, 7, 7,
	)
	if err != nil {
		t.Fatalf("GenerateAwsEnvironment failed: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}
	awsProvider := parsed["provider"].(map[string]any)["aws"].(map[string]any)
	endpoints, ok := awsProvider["endpoints"].(map[string]any)
	if !ok {
		t.Fatalf("expected provider.aws.endpoints, got %v", awsProvider)
	}
	if endpoints["s3"] != endpoint {
		t.Errorf("endpoints.s3 = %v, want %s", endpoints["s3"], endpoint)
	}
	envConfig := parsed["output"].(map[string]any)["environment"].(map[string]any)["value"].(map[string]any)
	if envConfig["aws_endpoint"] != endpoint {
		t.Errorf("output aws_endpoint = %v, want %s", envConfig["aws_endpoint"], endpoint)
	}
}
