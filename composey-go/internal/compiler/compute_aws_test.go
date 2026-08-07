package compiler

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/gecburton/composey/internal/models"
)

// TestInferComputeResources_RealHelloExample exercises the actual hello
// example (public ingress, discoverable service, no build/secrets) through
// the real parser/normalizer boundary.
func TestInferComputeResources_RealHelloExample(t *testing.T) {
	t.Parallel()
	composeApp, err := ParseCompose("../../../examples/hello/compose.yml")
	if err != nil {
		t.Fatalf("ParseCompose failed: %v", err)
	}
	app, err := Normalize(composeApp, "hello")
	if err != nil {
		t.Fatalf("Normalize failed: %v", err)
	}

	env := fullMockProdEnv()
	getName := minimalGetName("prod", "hello")

	resources := models.NewAWSResources()
	InferNetworking(resources, app, &env, getName, nil)
	priorities := CalculateListenerPriorities(app)
	namespace := InferServiceDiscovery(resources, app, &env, getName, nil)

	connections := InferComputeResources(resources, app, &env, getName, nil, false, priorities, namespace)

	taskDef, ok := resources.EcsTaskDefinition["web_td"]
	if !ok {
		t.Fatalf("expected aws_ecs_task_definition keyed 'web_td', got keys %v", keysOf(resources.EcsTaskDefinition))
	}
	if taskDef.CPU != "256" || taskDef.Memory != "512" {
		t.Errorf("cpu/memory = %s/%s, want 256/512 (small)", taskDef.CPU, taskDef.Memory)
	}
	if taskDef.Family != "prod-hello-web" {
		t.Errorf("Family = %q, want prod-hello-web", taskDef.Family)
	}

	var containers []map[string]any
	if err := json.Unmarshal([]byte(taskDef.ContainerDefinitions), &containers); err != nil {
		t.Fatalf("container_definitions is not valid JSON: %v\n%s", err, taskDef.ContainerDefinitions)
	}
	if len(containers) != 1 {
		t.Fatalf("expected exactly 1 container definition, got %d", len(containers))
	}
	if containers[0]["image"] != "nginxdemos/hello:plain-text" {
		t.Errorf("image = %v, want nginxdemos/hello:plain-text", containers[0]["image"])
	}

	svc, ok := resources.EcsService["web_service"]
	if !ok {
		t.Fatalf("expected aws_ecs_service keyed 'web_service', got keys %v", keysOf(resources.EcsService))
	}
	if len(svc.LoadBalancer) != 1 {
		t.Fatalf("expected a load balancer attached (public ingress), got %v", svc.LoadBalancer)
	}
	if svc.ServiceRegistries == nil {
		t.Errorf("expected service_registries for a discoverable service")
	}

	if _, ok := resources.LbTargetGroup["web_tg"]; !ok {
		t.Errorf("expected a target group for the public ingress")
	}
	if _, ok := resources.LbListenerRule["web_listener_rule"]; !ok {
		t.Errorf("expected a listener rule for the public ingress")
	}

	// hello has no ports referenced by other services, so no compute
	// connection should exist unless it's discoverable via networking. The
	// web service publishes port 80 and has no schedule, so it is
	// discoverable.
	if _, ok := connections["web"]; !ok {
		t.Errorf("expected a connection for the discoverable web service")
	}
}

// TestInferComputeResources_RealBuildWebappExample exercises the
// build-webapp example (build-from-source, ECR, docker provider wiring)
// through the real boundary.
func TestInferComputeResources_RealBuildWebappExample(t *testing.T) {
	t.Parallel()
	composeApp, err := ParseCompose("../../../examples/build-webapp/compose.yml")
	if err != nil {
		t.Fatalf("ParseCompose failed: %v", err)
	}
	app, err := Normalize(composeApp, "build-webapp")
	if err != nil {
		t.Fatalf("Normalize failed: %v", err)
	}

	env := fullMockProdEnv()
	getName := minimalGetName("prod", "build-webapp")

	resources := models.NewAWSResources()
	InferNetworking(resources, app, &env, getName, nil)
	priorities := CalculateListenerPriorities(app)
	namespace := InferServiceDiscovery(resources, app, &env, getName, nil)
	InferComputeResources(resources, app, &env, getName, nil, false, priorities, namespace)

	if _, ok := resources.EcrRepository["web_ecr"]; !ok {
		t.Fatalf("expected an ECR repository for a build-from-source service, got keys %v", keysOf(resources.EcrRepository))
	}
	image, ok := resources.DockerImage["web_image"]
	if !ok {
		t.Fatalf("expected a docker_image resource, got keys %v", keysOf(resources.DockerImage))
	}
	build, ok := image.Build.(map[string]any)
	if !ok {
		t.Fatalf("expected image.Build to be a map[string]any, got %T", image.Build)
	}
	if build["platform"] != "linux/amd64" {
		t.Errorf("build.platform = %v, want linux/amd64", build["platform"])
	}
	if _, ok := resources.DockerRegistryImage["web_push"]; !ok {
		t.Errorf("expected a docker_registry_image push resource")
	}

	taskDef := resources.EcsTaskDefinition["web_td"]
	var containers []map[string]any
	json.Unmarshal([]byte(taskDef.ContainerDefinitions), &containers)
	image0, _ := containers[0]["image"].(string)
	if image0 == "" {
		t.Fatalf("expected container image to be set")
	}
	if !contains(image0, "docker_registry_image.web_push.sha256_digest") {
		t.Errorf("container image = %q, want it to reference the pushed digest", image0)
	}
}

// TestInferComputeResources_RealScalingExample exercises the scaling
// example (min/max scale, size overrides) through the real boundary.
func TestInferComputeResources_RealScalingExample(t *testing.T) {
	t.Parallel()
	composeApp, err := ParseCompose("../../../examples/scaling/compose.yml")
	if err != nil {
		t.Fatalf("ParseCompose failed: %v", err)
	}
	app, err := Normalize(composeApp, "scaling")
	if err != nil {
		t.Fatalf("Normalize failed: %v", err)
	}

	env := fullMockProdEnv()
	getName := minimalGetName("prod", "scaling")

	resources := models.NewAWSResources()
	InferNetworking(resources, app, &env, getName, nil)
	priorities := CalculateListenerPriorities(app)
	namespace := InferServiceDiscovery(resources, app, &env, getName, nil)
	InferComputeResources(resources, app, &env, getName, nil, false, priorities, namespace)

	taskDef, ok := resources.EcsTaskDefinition["web_td"]
	if !ok {
		t.Fatalf("expected task def for web, got keys %v", keysOf(resources.EcsTaskDefinition))
	}
	if taskDef.CPU != "4096" || taskDef.Memory != "8192" {
		t.Errorf("cpu/memory = %s/%s, want 4096/8192 (large)", taskDef.CPU, taskDef.Memory)
	}
}

// TestInferComputeResources_RealPlatformConfigExample exercises the
// platform-config example (env vars named but not valued, becoming Secrets
// Manager references) through the real boundary.
func TestInferComputeResources_RealPlatformConfigExample(t *testing.T) {
	t.Parallel()
	composeApp, err := ParseCompose("../../../examples/platform-config/compose.yml")
	if err != nil {
		t.Fatalf("ParseCompose failed: %v", err)
	}
	app, err := Normalize(composeApp, "platform-config")
	if err != nil {
		t.Fatalf("Normalize failed: %v", err)
	}

	env := fullMockProdEnv()
	getName := minimalGetName("prod", "platform-config")

	resources := models.NewAWSResources()
	InferNetworking(resources, app, &env, getName, nil)
	priorities := CalculateListenerPriorities(app)
	namespace := InferServiceDiscovery(resources, app, &env, getName, nil)
	InferComputeResources(resources, app, &env, getName, nil, false, priorities, namespace)

	if _, ok := resources.SecretsmanagerSecret["web_config"]; !ok {
		t.Fatalf("expected a config secret for platform-supplied values, got keys %v", keysOf(resources.SecretsmanagerSecret))
	}

	taskDef := resources.EcsTaskDefinition["web_td"]
	var containers []map[string]any
	json.Unmarshal([]byte(taskDef.ContainerDefinitions), &containers)
	secrets, _ := containers[0]["secrets"].([]any)

	foundAPIToken, foundSentryDSN := false, false
	for _, s := range secrets {
		entry, _ := s.(map[string]any)
		switch entry["name"] {
		case "API_TOKEN":
			foundAPIToken = true
		case "SENTRY_DSN":
			foundSentryDSN = true
		}
	}
	if !foundAPIToken || !foundSentryDSN {
		t.Errorf("expected API_TOKEN and SENTRY_DSN as secrets, got %v", secrets)
	}

	environment, _ := containers[0]["environment"].([]any)
	foundLogLevel := false
	for _, e := range environment {
		entry, _ := e.(map[string]any)
		if entry["name"] == "LOG_LEVEL" && entry["value"] == "info" {
			foundLogLevel = true
		}
	}
	if !foundLogLevel {
		t.Errorf("expected LOG_LEVEL=info as a plain environment variable, got %v", environment)
	}
}

// --- determinism -------------------------------------------------------

// TestInferComputeResources_Deterministic runs the same input 6 times and
// diffs the resulting JSON, per this phase's review discipline.
func TestInferComputeResources_Deterministic(t *testing.T) {
	t.Parallel()
	composeApp, err := ParseCompose("../../../examples/platform-config/compose.yml")
	if err != nil {
		t.Fatalf("ParseCompose failed: %v", err)
	}
	app, err := Normalize(composeApp, "platform-config")
	if err != nil {
		t.Fatalf("Normalize failed: %v", err)
	}
	env := fullMockProdEnv()
	getName := minimalGetName("prod", "platform-config")

	var firstBlocks map[string]any
	for i := 0; i < 6; i++ {
		resources := models.NewAWSResources()
		InferNetworking(resources, app, &env, getName, nil)
		priorities := CalculateListenerPriorities(app)
		namespace := InferServiceDiscovery(resources, app, &env, getName, nil)
		InferComputeResources(resources, app, &env, getName, nil, false, priorities, namespace)

		blocks, err := resourceBlocks(resources)
		if err != nil {
			t.Fatalf("resourceBlocks: %v", err)
		}
		if i == 0 {
			firstBlocks = blocks
			continue
		}
		if !mapsJSONEqual(t, firstBlocks, blocks) {
			t.Fatalf("run %d differs from run 0", i)
		}
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (func() bool {
		for i := 0; i+len(substr) <= len(s); i++ {
			if s[i:i+len(substr)] == substr {
				return true
			}
		}
		return false
	})()
}

// TestEcsTasksAssumeRolePolicy_KeyOrderMatchesPython pins the exact string
// Python's json.dumps produces for the assume-role policy, including key
// order (Version before Statement, matching the dict literal in
// _compute.py) -- caught as a real divergence, not a theoretical one, when
// this phase's byte-identical-output check against a live Python run
// surfaced encoding/json's alphabetical map-key sorting reordering it to
// Statement-before-Version (2026-08-06).
func TestEcsTasksAssumeRolePolicy_KeyOrderMatchesPython(t *testing.T) {
	t.Parallel()
	want := `{"Version": "2012-10-17", "Statement": [{"Action": "sts:AssumeRole", "Effect": "Allow", "Principal": {"Service": "ecs-tasks.amazonaws.com"}}]}`
	if got := ecsTasksAssumeRolePolicy(); got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

// TestEcsService_LoadBalancerDefaultsToEmptyList checks that a service with
// no ingress still emits "load_balancer": [] rather than omitting the key,
// matching Pydantic's default_factory=list (not Optional[...]=None) --
// caught as a real divergence against the nginx-flask-mysql and
// compute-tuning golden files (2026-08-06), both of which have no public
// ingress and both of which show "load_balancer": [] in their expected
// output.
func TestEcsService_LoadBalancerDefaultsToEmptyList(t *testing.T) {
	t.Parallel()
	service := &models.Service{Name: "backend", Capability: models.CapabilityContainer, MinScale: 1, MaxScale: 1}
	env := fullMockProdEnv()
	svc := createEcsService(service, &env, minimalGetName("prod", "app"), nil, "backend_td")

	blocks, err := resourceBlocks(&models.AWSResources{EcsService: map[string]models.EcsService{"backend_service": svc}})
	if err != nil {
		t.Fatalf("resourceBlocks: %v", err)
	}
	ecsServices := blocks["aws_ecs_service"].(map[string]any)
	backend := ecsServices["backend_service"].(map[string]any)
	lb, ok := backend["load_balancer"]
	if !ok {
		t.Fatalf("expected load_balancer key to be present even when empty, got %v", backend)
	}
	lbSlice, ok := lb.([]any)
	if !ok || len(lbSlice) != 0 {
		t.Errorf("load_balancer = %v, want an empty list", lb)
	}
}

// TestHandleAutoscaling_DefaultsToCpuAndMemoryWhenUnspecified checks that a
// scaling service with no explicit auto_scaling block still gets both a CPU
// and a Memory policy, matching AutoScalingConfig's own Pydantic defaults
// in semantic.py (default_factory, not an empty configuration) -- caught as
// a real divergence, not a theoretical one, against the production-stack
// golden file, which relies on exactly this default (2026-08-06).
func TestHandleAutoscaling_DefaultsToCpuAndMemoryWhenUnspecified(t *testing.T) {
	t.Parallel()
	service := &models.Service{Name: "web", MinScale: 2, MaxScale: 10}
	resources := models.NewAWSResources()
	handleAutoscaling(resources, service, minimalGetName("prod", "app"))

	if len(resources.AppAutoscalingPolicy) != 2 {
		t.Fatalf("expected 2 default policies (cpu, memory), got %d: %v",
			len(resources.AppAutoscalingPolicy), keysOf(resources.AppAutoscalingPolicy))
	}
	cpu, ok := resources.AppAutoscalingPolicy["web_scaling_0"]
	if !ok {
		t.Fatalf("expected web_scaling_0 (cpu), got keys %v", keysOf(resources.AppAutoscalingPolicy))
	}
	spec, _ := cpu.TargetTrackingScalingPolicyConfiguration["predefined_metric_specification"].(map[string]any)
	if spec["predefined_metric_type"] != "ECSServiceAverageCPUUtilization" {
		t.Errorf("cpu policy metric = %v, want ECSServiceAverageCPUUtilization", spec["predefined_metric_type"])
	}

	memory, ok := resources.AppAutoscalingPolicy["web_scaling_1"]
	if !ok {
		t.Fatalf("expected web_scaling_1 (memory)")
	}
	spec2, _ := memory.TargetTrackingScalingPolicyConfiguration["predefined_metric_specification"].(map[string]any)
	if spec2["predefined_metric_type"] != "ECSServiceAverageMemoryUtilization" {
		t.Errorf("memory policy metric = %v, want ECSServiceAverageMemoryUtilization", spec2["predefined_metric_type"])
	}
}

// TestHandleAutoscaling_TargetValueRendersAsFloat checks that target_value
// serializes with a decimal point even for a whole number (70.0, not 70),
// matching Python's json.dumps output for a float field -- caught as a
// real divergence when encoding/json's default float handling collapsed
// 70.0 to 70 in the production-stack golden comparison (2026-08-06).
func TestHandleAutoscaling_TargetValueRendersAsFloat(t *testing.T) {
	t.Parallel()
	service := &models.Service{Name: "web", MinScale: 1, MaxScale: 5}
	resources := models.NewAWSResources()
	handleAutoscaling(resources, service, minimalGetName("prod", "app"))

	blocks, err := resourceBlocks(resources)
	if err != nil {
		t.Fatalf("resourceBlocks: %v", err)
	}
	raw, err := json.Marshal(blocks["aws_appautoscaling_policy"])
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(raw), `"target_value":70.0`) &&
		!strings.Contains(string(raw), `"target_value": 70.0`) {
		t.Errorf("expected target_value to render as 70.0, got: %s", raw)
	}
}

// TestHandleBuildContext_DiscardForcesDelete mirrors test_data_retention.py's
// test_a_throwaway_environment_discards_everything (ECR half): a
// non-retained environment must force_delete=True on the ECR repo, so
// `terraform destroy` removes it even with images in it -- untested in Go
// until now, since no golden example builds from source with
// retain_data_on_destroy: false.
func TestHandleBuildContext_DiscardForcesDelete(t *testing.T) {
	t.Parallel()
	buildContext := "app"
	service := &models.Service{Name: "web", BuildContext: &buildContext}
	env := fullMockProdEnv()
	resources := models.NewAWSResources()
	handleBuildContext(resources, service, &env, minimalGetName("prod", "app"), nil, true, "web_exec_role")

	ecr, ok := resources.EcrRepository["web_ecr"]
	if !ok {
		t.Fatalf("expected an ECR repository")
	}
	if !ecr.ForceDelete {
		t.Errorf("ForceDelete = false, want true when discarding")
	}
}

// TestHandleBuildContext_RetainDoesNotForceDelete is the complementary
// positive case: a retained environment must not force-delete the ECR repo.
func TestHandleBuildContext_RetainDoesNotForceDelete(t *testing.T) {
	t.Parallel()
	buildContext := "app"
	service := &models.Service{Name: "web", BuildContext: &buildContext}
	env := fullMockProdEnv()
	resources := models.NewAWSResources()
	handleBuildContext(resources, service, &env, minimalGetName("prod", "app"), nil, false, "web_exec_role")

	ecr := resources.EcrRepository["web_ecr"]
	if ecr.ForceDelete {
		t.Errorf("ForceDelete = true, want false when retaining")
	}
}

// TestHandleIngress_CustomHealthCheckPathPropagates mirrors
// test_ingress.py's test_health_check_path_is_honoured: a custom
// ingress.health_check.path must reach the target group's own
// health_check.path, not silently fall back to "/". Untested in Go until
// now -- no golden example declares a non-default health check path.
func TestHandleIngress_CustomHealthCheckPathPropagates(t *testing.T) {
	t.Parallel()
	port := 8080
	service := &models.Service{
		Name:       "web",
		Port:       &port,
		Capability: models.CapabilityContainer,
		Ingress: &models.Ingress{
			Path:        "/",
			HealthCheck: models.HealthCheck{Type: models.HealthCheckTypeHTTP, Path: "/healthz"},
		},
	}
	env := fullMockProdEnv()
	resources := models.NewAWSResources()
	priorities := map[string]int{"web": 100}
	ecsService := models.NewEcsService()
	ecsService.NetworkConfiguration = map[string]any{"security_groups": []string{}}

	handleIngress(resources, service, &env, minimalGetName("prod", "app"), nil, priorities, &ecsService)

	tg, ok := resources.LbTargetGroup["web_tg"]
	if !ok {
		t.Fatalf("expected a target group")
	}
	path, _ := tg.HealthCheck["path"].(string)
	if path != "/healthz" {
		t.Errorf("health_check.path = %q, want /healthz", path)
	}
}

// TestHandleIngress_DefaultHealthCheckPathIsSlash is the complementary
// case: no health check path declared falls back to "/".
func TestHandleIngress_DefaultHealthCheckPathIsSlash(t *testing.T) {
	t.Parallel()
	port := 80
	service := &models.Service{
		Name:       "web",
		Port:       &port,
		Capability: models.CapabilityContainer,
		Ingress:    &models.Ingress{Path: "/"},
	}
	env := fullMockProdEnv()
	resources := models.NewAWSResources()
	priorities := map[string]int{"web": 100}
	ecsService := models.NewEcsService()
	ecsService.NetworkConfiguration = map[string]any{"security_groups": []string{}}

	handleIngress(resources, service, &env, minimalGetName("prod", "app"), nil, priorities, &ecsService)

	tg := resources.LbTargetGroup["web_tg"]
	path, _ := tg.HealthCheck["path"].(string)
	if path != "/" {
		t.Errorf("health_check.path = %q, want / (default)", path)
	}
}

// TestInferComputeResources_LogRetentionDaysOverride mirrors
// test_platform_settings.py's test_log_retention_is_set_by_the_environment:
// an environment declaring a non-default log_retention_days must flow
// through to the log group's retention_in_days. Untested in Go until now
// -- every existing test/example uses the 7-day default.
func TestInferComputeResources_LogRetentionDaysOverride(t *testing.T) {
	t.Parallel()
	app := &models.Application{
		Name: "app",
		Services: []models.Service{
			{Name: "web", Image: "web", Capability: models.CapabilityContainer, Size: models.ServiceSizeSmall, MinScale: 1, MaxScale: 1},
		},
	}
	env := fullMockProdEnv()
	env.LogRetentionDays = 90

	resources := models.NewAWSResources()
	getName := minimalGetName("prod", "app")
	namespace := InferServiceDiscovery(resources, app, &env, getName, nil)
	InferComputeResources(resources, app, &env, getName, nil, false, map[string]int{}, namespace)

	logGroup, ok := resources.CloudWatchLogGroup["web_lg"]
	if !ok {
		t.Fatalf("expected a log group")
	}
	if logGroup.RetentionInDays != 90 {
		t.Errorf("RetentionInDays = %d, want 90 (overridden)", logGroup.RetentionInDays)
	}
}

// TestCreateEcsService_DesiredCountMatchesMinScale mirrors
// test_desired_count.py's parametrized test_the_floor_and_the_count_never_
// disagree: desired_count always equals min_scale, directly against
// createEcsService rather than only through the two data points golden
// examples happen to provide.
func TestCreateEcsService_DesiredCountMatchesMinScale(t *testing.T) {
	t.Parallel()
	cases := []struct{ minScale, maxScale int }{
		{1, 1},
		{4, 4},
		{2, 10},
	}
	for _, tc := range cases {
		service := &models.Service{Name: "web", MinScale: tc.minScale, MaxScale: tc.maxScale}
		env := fullMockProdEnv()
		svc := createEcsService(service, &env, minimalGetName("prod", "app"), nil, "web_td")

		if svc.DesiredCount != tc.minScale {
			t.Errorf("min_scale=%d max_scale=%d: DesiredCount = %d, want %d",
				tc.minScale, tc.maxScale, svc.DesiredCount, tc.minScale)
		}
	}
}

// TestCreateEcsService_LifecycleNilWhenNotScaling mirrors
// test_desired_count.py's test_an_unscaled_service_keeps_its_count_declared:
// min_scale == max_scale means Terraform should not ignore desired_count
// changes, since nothing else (autoscaling) is meant to own it.
func TestCreateEcsService_LifecycleNilWhenNotScaling(t *testing.T) {
	t.Parallel()
	service := &models.Service{Name: "web", MinScale: 2, MaxScale: 1}
	env := fullMockProdEnv()
	svc := createEcsService(service, &env, minimalGetName("prod", "app"), nil, "web_td")

	if svc.Lifecycle != nil {
		t.Errorf("Lifecycle = %+v, want nil when min_scale >= max_scale (not really scaling)", svc.Lifecycle)
	}
}
