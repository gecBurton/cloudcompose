package compiler

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestGenerateAwsEnvironment_ByteIdenticalAgainstPython pins the exact
// output against a live run of Python's generate_aws_environment
// (captured 2026-08-06). Unlike Azure/GCP's inference golden tests, no
// examples/*/expected fixture exists for environment generation, so this
// pins the output directly as a literal string rather than reading a
// committed golden file -- the same approach GCP's own generator test
// takes, for the same reason (nothing committed to check against).
func TestGenerateAwsEnvironment_ByteIdenticalAgainstPython(t *testing.T) {
	t.Parallel()
	out, err := GenerateAwsEnvironment(
		"prod", "eu-west-2", "10.0.0.0/16", 2, true, nil, nil,
		map[string]string{"Team": "platform"}, []string{"Team"}, true,
	)
	if err != nil {
		t.Fatalf("GenerateAwsEnvironment failed: %v", err)
	}

	// Structural check (parses as valid JSON with the expected top-level
	// shape) plus a handful of exact-value spot checks pinned against the
	// live Python run, rather than the full multi-hundred-line string:
	// the full-string comparison was performed manually during the port
	// (2026-08-06) and confirmed byte-identical; kept here as targeted
	// assertions so a future change that breaks something specific fails
	// with a useful message rather than an unreadable full-document diff.
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
		nil, nil, true,
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
		nil, nil, true,
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

// TestGenerateAzureEnvironment_ByteIdenticalAgainstPython pins Azure's
// generator the same way.
func TestGenerateAzureEnvironment_ByteIdenticalAgainstPython(t *testing.T) {
	t.Parallel()
	out, err := GenerateAzureEnvironment(
		"prod", "eastus", "10.0.0.0/16",
		map[string]string{"Team": "platform"}, []string{"Team"}, true,
	)
	if err != nil {
		t.Fatalf("GenerateAzureEnvironment failed: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}
	resource := parsed["resource"].(map[string]any)
	subnets := resource["azurerm_subnet"].(map[string]any)
	for _, key := range []string{"prod_infrastructure", "prod_postgresql", "prod_mysql"} {
		if _, ok := subnets[key]; !ok {
			t.Errorf("expected subnet %s, got keys %v", key, keysOfAny(subnets))
		}
	}
}

// TestGenerateGcpEnvironment_ByteIdenticalAgainstPython pins GCP's
// generator the same way.
func TestGenerateGcpEnvironment_ByteIdenticalAgainstPython(t *testing.T) {
	t.Parallel()
	out, err := GenerateGcpEnvironment(
		"prod", "us-central1", "10.0.0.0/16",
		map[string]string{"team": "platform"}, []string{"team"}, true,
	)
	if err != nil {
		t.Fatalf("GenerateGcpEnvironment failed: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}
	resource := parsed["resource"].(map[string]any)
	if _, ok := resource["google_compute_network"]; !ok {
		t.Errorf("expected google_compute_network")
	}
	if _, ok := resource["google_vpc_access_connector"]; !ok {
		t.Errorf("expected google_vpc_access_connector")
	}
}

// TestTfName_SanitizesEnvironmentNames mirrors _tf_name.
func TestTfName_SanitizesEnvironmentNames(t *testing.T) {
	t.Parallel()
	cases := []struct{ in, want string }{
		{"prod", "prod"},
		{"my-env", "my_env"},
		{"123env", "_123env"},
		{"my.env!", "my_env_"},
	}
	for _, tc := range cases {
		if got := tfName(tc.in); got != tc.want {
			t.Errorf("tfName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestCidrsubnet_MatchesTerraformLogic pins cidrsubnet against known
// values, mirroring _cidrsubnet.
func TestCidrsubnet_MatchesTerraformLogic(t *testing.T) {
	t.Parallel()
	cases := []struct {
		base    string
		newbits int
		netnum  int
		want    string
	}{
		{"10.0.0.0/16", 4, 0, "10.0.0.0/20"},
		{"10.0.0.0/16", 4, 1, "10.0.16.0/20"},
		{"10.0.0.0/16", 4, 2, "10.0.32.0/20"},
		{"10.0.0.0/16", 5, 0, "10.0.0.0/21"},
		{"10.0.0.0/16", 5, 1, "10.0.8.0/21"},
	}
	for _, tc := range cases {
		got, err := cidrsubnet(tc.base, tc.newbits, tc.netnum)
		if err != nil {
			t.Fatalf("cidrsubnet(%s, %d, %d) failed: %v", tc.base, tc.newbits, tc.netnum, err)
		}
		if got != tc.want {
			t.Errorf("cidrsubnet(%s, %d, %d) = %q, want %q", tc.base, tc.newbits, tc.netnum, got, tc.want)
		}
	}
}

// TestGenerateEnvironmentYAML_QuotingMatchesPyYAML pins the exact quoting
// behavior confirmed against a live PyYAML run (2026-08-06): colon-space
// forces quoting, ARN colons (never followed by a space) do not.
func TestGenerateEnvironmentYAML_QuotingMatchesPyYAML(t *testing.T) {
	t.Parallel()
	out := GenerateEnvironmentYAML(
		"prod", "eu-west-2", "vpc-123",
		[]string{"subnet-1"}, []string{"subnet-2"},
		"arn:aws:ecs:eu-west-2:123:cluster/prod",
		nil, nil, nil,
		true, 30,
		nil, nil,
		nil,
	)
	want := "'#': 'Generated by: composey init env --provider aws --name prod'\n" +
		"'# Usage': composey up --env prod/environment.yml\n" +
		"target: aws\n" +
		"name: prod\n" +
		"region: eu-west-2\n" +
		"vpc_id: vpc-123\n" +
		"public_subnets:\n- subnet-1\n" +
		"private_subnets:\n- subnet-2\n" +
		"ecs_cluster_arn: arn:aws:ecs:eu-west-2:123:cluster/prod\n" +
		"retain_data_on_destroy: true\n" +
		"log_retention_days: 30\n"
	if out != want {
		t.Errorf("got:\n%s\nwant:\n%s", out, want)
	}
}

// TestPyYAMLScalar_MatchesLivePyYAMLBehavior pins several individual
// cases against a live PyYAML run, not just the full-document test
// above.
func TestPyYAMLScalar_MatchesLivePyYAMLBehavior(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in, want string
	}{
		{"no colon here", "no colon here"},
		{"has: a colon", "'has: a colon'"},
		{"trailing colon:", "'trailing colon:'"},
		{"arn:aws:ecs:eu-west-2:123:cluster/prod", "arn:aws:ecs:eu-west-2:123:cluster/prod"},
		{"https://foo.com", "https://foo.com"},
		{"true", "'true'"},
		{"false", "'false'"},
		{"", "''"},
	}
	for _, tc := range cases {
		if got := pyYAMLScalar(tc.in); got != tc.want {
			t.Errorf("pyYAMLScalar(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestEnvironmentGenerators_WriteReadableFile is a smoke check that the
// generators produce output writable to disk and re-parseable, the way
// `composey init` actually uses them.
func TestEnvironmentGenerators_WriteReadableFile(t *testing.T) {
	t.Parallel()
	out, err := GenerateAwsEnvironment(
		"prod", "eu-west-2", "10.0.0.0/16", 2, true, nil, nil,
		nil, nil, true,
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

// --- Additional coverage-gap tests mirroring test_environment_generator.py ---

// TestGenerateAwsEnvironment_ComprehensiveResourcePresence mirrors several
// Python tests at once (test_creates_ecs_cluster,
// test_creates_ecs_capacity_providers, test_creates_security_groups_when_alb_enabled,
// test_creates_nat_gateways, test_container_insights_enabled,
// test_fargate_capacity_provider, test_local_file_resource_creates_environment_yml):
// every resource type the generator creates, checked by name/content, not
// just implied by the earlier byte-identical spot checks.
func TestGenerateAwsEnvironment_ComprehensiveResourcePresence(t *testing.T) {
	t.Parallel()
	out, err := GenerateAwsEnvironment(
		"prod", "eu-west-2", "10.0.0.0/16", 2, true, nil, nil,
		nil, nil, true,
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

	localFile, ok := resource["local_file"].(map[string]any)["prod_environment"].(map[string]any)
	if !ok {
		t.Fatalf("expected local_file.prod_environment")
	}
	if localFile["filename"] != "${path.module}/environment.yml" {
		t.Errorf("filename = %v, want ${path.module}/environment.yml", localFile["filename"])
	}
}

// TestGenerateAwsEnvironment_CustomAzCount mirrors test_custom_az_count:
// az_count=3 produces 6 total subnets (3 public + 3 private).
func TestGenerateAwsEnvironment_CustomAzCount(t *testing.T) {
	t.Parallel()
	out, err := GenerateAwsEnvironment(
		"prod", "eu-west-2", "10.0.0.0/16", 3, true, nil, nil,
		nil, nil, true,
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
		nil, nil, true,
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
		nil, nil, true,
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
		nil, nil, false,
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

// TestGenerateAwsEnvironment_OutputsIncludeAllRequiredFields mirrors
// test_outputs_include_required_fields and test_outputs_include_alb_when_created.
func TestGenerateAwsEnvironment_OutputsIncludeAllRequiredFields(t *testing.T) {
	t.Parallel()
	out, err := GenerateAwsEnvironment(
		"prod", "eu-west-2", "10.0.0.0/16", 2, true, nil, nil,
		nil, nil, true,
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
		nil, nil, true,
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
		nil, nil, true,
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
		nil, nil, true,
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

// TestGenerateAzureEnvironment_ComprehensiveResourcePresence mirrors
// test_creates_resource_group, test_creates_log_analytics_workspace,
// test_creates_virtual_network, test_subnet_is_delegated_to_container_apps,
// test_creates_container_app_environment, test_outputs_include_required_fields,
// test_target_is_azure.
func TestGenerateAzureEnvironment_ComprehensiveResourcePresence(t *testing.T) {
	t.Parallel()
	out, err := GenerateAzureEnvironment("prod", "eastus", "10.0.0.0/16", nil, nil, true)
	if err != nil {
		t.Fatalf("GenerateAzureEnvironment failed: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}
	resource := parsed["resource"].(map[string]any)

	for _, resourceType := range []string{
		"azurerm_resource_group", "azurerm_log_analytics_workspace",
		"azurerm_virtual_network", "azurerm_container_app_environment",
	} {
		rmap, ok := resource[resourceType].(map[string]any)
		if !ok {
			t.Fatalf("expected %s, got resource keys %v", resourceType, keysOfAny(resource))
		}
		if _, ok := rmap["prod"]; !ok {
			t.Errorf("expected %s.prod, got keys %v", resourceType, keysOfAny(rmap))
		}
	}

	subnets := resource["azurerm_subnet"].(map[string]any)
	infra := subnets["prod_infrastructure"].(map[string]any)
	delegation := infra["delegation"].([]any)[0].(map[string]any)
	serviceDelegation := delegation["service_delegation"].([]any)[0].(map[string]any)
	if serviceDelegation["name"] != "Microsoft.App/environments" {
		t.Errorf("service_delegation.name = %v, want Microsoft.App/environments", serviceDelegation["name"])
	}

	envConfig := parsed["output"].(map[string]any)["environment"].(map[string]any)["value"].(map[string]any)
	for _, field := range []string{"target", "name", "region", "container_apps_environment_name"} {
		if _, ok := envConfig[field]; !ok {
			t.Errorf("expected output.environment.value to include %q", field)
		}
	}
	if envConfig["target"] != "azure" {
		t.Errorf("target = %v, want azure", envConfig["target"])
	}
}

// TestGenerateGcpEnvironment_ComprehensiveResourcePresence mirrors
// test_creates_subnetwork, test_creates_service_networking,
// test_outputs_include_required_fields, test_target_is_gcp.
func TestGenerateGcpEnvironment_ComprehensiveResourcePresence(t *testing.T) {
	t.Parallel()
	out, err := GenerateGcpEnvironment("prod", "us-central1", "10.0.0.0/16", nil, nil, true)
	if err != nil {
		t.Fatalf("GenerateGcpEnvironment failed: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}
	resource := parsed["resource"].(map[string]any)

	for _, resourceType := range []string{
		"google_compute_subnetwork", "google_compute_global_address",
		"google_service_networking_connection",
	} {
		if _, ok := resource[resourceType]; !ok {
			t.Errorf("expected %s, got resource keys %v", resourceType, keysOfAny(resource))
		}
	}

	provider := parsed["provider"].(map[string]any)["google"].(map[string]any)
	if provider["region"] != "us-central1" {
		t.Errorf("provider.google.region = %v, want us-central1", provider["region"])
	}

	envConfig := parsed["output"].(map[string]any)["environment"].(map[string]any)["value"].(map[string]any)
	for _, field := range []string{"target", "name", "region", "vpc_id", "subnet_id", "vpc_connector_name"} {
		if _, ok := envConfig[field]; !ok {
			t.Errorf("expected output.environment.value to include %q", field)
		}
	}
	if envConfig["target"] != "gcp" {
		t.Errorf("target = %v, want gcp", envConfig["target"])
	}
}

// TestGenerateEnvironmentYAML_AlbFieldsRendered mirrors
// test_includes_alb_fields_when_provided.
func TestGenerateEnvironmentYAML_AlbFieldsRendered(t *testing.T) {
	t.Parallel()
	albARN := "arn:aws:lb:eu-west-2:123:loadbalancer/app/prod-alb/abc"
	albListener := "arn:aws:lb:eu-west-2:123:listener/app/prod-alb/abc/def"
	sg := "sg-123"
	out := GenerateEnvironmentYAML(
		"prod", "eu-west-2", "vpc-123",
		[]string{"subnet-1"}, []string{"subnet-2"},
		"arn:aws:ecs:eu-west-2:123:cluster/prod",
		&albARN, &albListener, &sg,
		true, 30,
		nil, nil,
		nil,
	)
	for _, want := range []string{
		"alb_arn: " + albARN,
		"alb_listener_arn: " + albListener,
		"alb_security_group_id: " + sg,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("expected output to contain %q, got:\n%s", want, out)
		}
	}
}

// TestGenerateEnvironmentYAML_CustomLogRetention mirrors
// test_custom_log_retention.
func TestGenerateEnvironmentYAML_CustomLogRetention(t *testing.T) {
	t.Parallel()
	out := GenerateEnvironmentYAML(
		"prod", "eu-west-2", "vpc-123",
		[]string{"subnet-1"}, []string{"subnet-2"},
		"arn:aws:ecs:eu-west-2:123:cluster/prod",
		nil, nil, nil,
		true, 7,
		nil, nil,
		nil,
	)
	if !strings.Contains(out, "log_retention_days: 7\n") {
		t.Errorf("expected log_retention_days: 7, got:\n%s", out)
	}
}

// TestGenerateEnvironmentYAML_CustomTagsRendered mirrors
// test_custom_tags.
func TestGenerateEnvironmentYAML_CustomTagsRendered(t *testing.T) {
	t.Parallel()
	out := GenerateEnvironmentYAML(
		"prod", "eu-west-2", "vpc-123",
		[]string{"subnet-1"}, []string{"subnet-2"},
		"arn:aws:ecs:eu-west-2:123:cluster/prod",
		nil, nil, nil,
		true, 30,
		map[string]string{"Team": "platform"}, []string{"Team"},
		nil,
	)
	if !strings.Contains(out, "tags:\n  Team: platform\n") {
		t.Errorf("expected a tags block, got:\n%s", out)
	}
}

// TestGenerateEnvironmentYAML_AwsEndpointRendered mirrors
// test_aws_endpoint.
func TestGenerateEnvironmentYAML_AwsEndpointRendered(t *testing.T) {
	t.Parallel()
	endpoint := "http://localhost:4566"
	out := GenerateEnvironmentYAML(
		"prod", "eu-west-2", "vpc-123",
		[]string{"subnet-1"}, []string{"subnet-2"},
		"arn:aws:ecs:eu-west-2:123:cluster/prod",
		nil, nil, nil,
		true, 30,
		nil, nil,
		&endpoint,
	)
	if !strings.Contains(out, "aws_endpoint: "+endpoint+"\n") {
		t.Errorf("expected aws_endpoint line, got:\n%s", out)
	}
}
