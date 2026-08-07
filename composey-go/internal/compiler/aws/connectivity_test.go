package aws

import (
	"encoding/json"
	"github.com/gecburton/composey/internal/compiler/shared"
	"testing"

	"github.com/gecburton/composey/internal/models"
)

func minimalGetName(env, app string) func(string) string {
	return func(resource string) string {
		return env + "-" + app + "-" + resource
	}
}

// --- real-boundary tests: ParseCompose -> Normalize -> infer ---------------

// TestInferNetworking_RealHelloExample exercises the actual hello example
// through the real parser/normalizer boundary rather than a hand-built
// SemanticApplication, per this phase's review discipline: hand-built-struct
// tests alone let the Phase 2 volume bug through with 100% coverage of the
// broken code and 0% of the boundary production actually used.
func TestInferNetworking_RealHelloExample(t *testing.T) {
	t.Parallel()
	composeApp, err := shared.ParseCompose("../../../../examples/hello/compose.yml")
	if err != nil {
		t.Fatalf("ParseCompose failed: %v", err)
	}
	app, err := shared.Normalize(composeApp, "hello")
	if err != nil {
		t.Fatalf("Normalize failed: %v", err)
	}

	env := models.NewAwsEnvironment()
	env.Name = "prod"
	env.VpcID = "vpc-123"

	resources := models.NewAWSResources()
	getName := minimalGetName("prod", "hello")
	InferNetworking(resources, app, &env, getName, nil)

	if len(resources.SecurityGroup) != 1 {
		t.Fatalf("expected 1 security group for the 'public' network, got %d: %v",
			len(resources.SecurityGroup), resources.SecurityGroup)
	}
	sg, ok := resources.SecurityGroup["public_sg"]
	if !ok {
		t.Fatalf("expected security group keyed 'public_sg', got keys %v", keysOf(resources.SecurityGroup))
	}
	if sg.Name != "prod-hello-public" {
		t.Errorf("SecurityGroup.Name = %q, want %q", sg.Name, "prod-hello-public")
	}
	if sg.VpcID != "vpc-123" {
		t.Errorf("SecurityGroup.VpcID = %q, want vpc-123", sg.VpcID)
	}

	if len(resources.SecurityGroupRule) != 2 {
		t.Fatalf("expected 2 security group rules (internal + egress), got %d: %v",
			len(resources.SecurityGroupRule), keysOf(resources.SecurityGroupRule))
	}
	internal, ok := resources.SecurityGroupRule["public_sg_internal_rule"]
	if !ok {
		t.Fatalf("expected internal rule, got keys %v", keysOf(resources.SecurityGroupRule))
	}
	if internal.Type != "ingress" || internal.Protocol != "-1" {
		t.Errorf("internal rule = %+v, want ingress/-1", internal)
	}
	egress, ok := resources.SecurityGroupRule["public_sg_egress_rule"]
	if !ok {
		t.Fatalf("expected egress rule, got keys %v", keysOf(resources.SecurityGroupRule))
	}
	if egress.Type != "egress" || len(egress.CidrBlocks) != 1 || egress.CidrBlocks[0] != "0.0.0.0/0" {
		t.Errorf("egress rule = %+v, want egress/0.0.0.0/0", egress)
	}
}

// TestInferServiceDiscovery_RealHelloExample checks that the one container
// service in the hello example, which publishes a port and carries no
// schedule, is discoverable and gets a Cloud Map entry.
func TestInferServiceDiscovery_RealHelloExample(t *testing.T) {
	t.Parallel()
	composeApp, err := shared.ParseCompose("../../../../examples/hello/compose.yml")
	if err != nil {
		t.Fatalf("ParseCompose failed: %v", err)
	}
	app, err := shared.Normalize(composeApp, "hello")
	if err != nil {
		t.Fatalf("Normalize failed: %v", err)
	}

	env := models.NewAwsEnvironment()
	env.Name = "prod"
	env.VpcID = "vpc-123"

	resources := models.NewAWSResources()
	getName := minimalGetName("prod", "hello")
	namespace := InferServiceDiscovery(resources, app, &env, getName, nil)

	if namespace != "prod-hello.internal" {
		t.Errorf("namespace = %q, want prod-hello.internal", namespace)
	}
	if len(resources.ServiceDiscoveryPrivateDnsNamespace) != 1 {
		t.Fatalf("expected 1 namespace, got %d", len(resources.ServiceDiscoveryPrivateDnsNamespace))
	}
	if len(resources.ServiceDiscoveryService) != 1 {
		t.Fatalf("expected 1 discoverable service, got %d: %v",
			len(resources.ServiceDiscoveryService), keysOf(resources.ServiceDiscoveryService))
	}
	svc, ok := resources.ServiceDiscoveryService["web_discovery"]
	if !ok {
		t.Fatalf("expected service keyed 'web_discovery', got keys %v", keysOf(resources.ServiceDiscoveryService))
	}
	if svc.Name != "web" {
		t.Errorf("ServiceDiscoveryService.Name = %q, want web", svc.Name)
	}
}

// --- hand-built edge cases --------------------------------------------------

func TestInferNetworking_MultipleNetworksSortedDeterministically(t *testing.T) {
	t.Parallel()
	app := &models.Application{
		Name: "app",
		Services: []models.Service{
			{Name: "a", Capability: models.CapabilityContainer, NetworkIsolationSegments: []string{"zebra"}},
			{Name: "b", Capability: models.CapabilityContainer, NetworkIsolationSegments: []string{"alpha"}},
		},
	}
	env := models.NewAwsEnvironment()
	env.Name = "prod"
	env.VpcID = "vpc-1"

	resources := models.NewAWSResources()
	InferNetworking(resources, app, &env, minimalGetName("prod", "app"), nil)

	if len(resources.SecurityGroup) != 2 {
		t.Fatalf("expected 2 security groups, got %d", len(resources.SecurityGroup))
	}
	if _, ok := resources.SecurityGroup["alpha_sg"]; !ok {
		t.Errorf("expected alpha_sg, got keys %v", keysOf(resources.SecurityGroup))
	}
	if _, ok := resources.SecurityGroup["zebra_sg"]; !ok {
		t.Errorf("expected zebra_sg, got keys %v", keysOf(resources.SecurityGroup))
	}
}

func TestIsDiscoverable(t *testing.T) {
	t.Parallel()
	port := 80

	cases := []struct {
		name string
		svc  models.Service
		want bool
	}{
		{
			name: "container with port, no schedule",
			svc:  models.Service{Capability: models.CapabilityContainer, Port: &port},
			want: true,
		},
		{
			name: "container with no port",
			svc:  models.Service{Capability: models.CapabilityContainer},
			want: false,
		},
		{
			name: "scheduled container",
			svc: models.Service{
				Capability: models.CapabilityContainer,
				Port:       &port,
				Schedule:   models.RateSchedule{Kind: models.ScheduleKindRate, Value: 1, Unit: models.RateUnitHours},
			},
			want: false,
		},
		{
			name: "database is not discoverable via this path",
			svc:  models.Service{Capability: models.CapabilityDatabase, Port: &port},
			want: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := IsDiscoverable(&tc.svc)
			if got != tc.want {
				t.Errorf("IsDiscoverable(%+v) = %v, want %v", tc.svc, got, tc.want)
			}
		})
	}
}

func TestSecurityGroupIDs_SortedAndFormatted(t *testing.T) {
	t.Parallel()
	ids := SecurityGroupIDs([]string{"zebra", "alpha"})
	want := []string{
		"${aws_security_group.alpha_sg.id}",
		"${aws_security_group.zebra_sg.id}",
	}
	if len(ids) != 2 || ids[0] != want[0] || ids[1] != want[1] {
		t.Errorf("SecurityGroupIDs = %v, want %v", ids, want)
	}
}

func TestNamespaceFor(t *testing.T) {
	t.Parallel()
	cases := []struct {
		env, app, want string
	}{
		{"prod", "hello", "prod-hello.internal"},
		{"", "hello", "hello.internal"},
		// The remaining three mirror test_service_discovery.py's
		// parametrized test_the_namespace_is_scoped_and_dns_safe exactly.
		{"prod", "web-api", "prod-web-api.internal"},
		{"prod", "my_app", "prod-my-app.internal"},
		{"Prod", "App", "prod-app.internal"},
	}
	for _, tc := range cases {
		t.Run(tc.env+"/"+tc.app, func(t *testing.T) {
			if got := NamespaceFor(tc.env, tc.app); got != tc.want {
				t.Errorf("NamespaceFor(%q, %q) = %q, want %q", tc.env, tc.app, got, tc.want)
			}
		})
	}
}

func TestPathPatterns(t *testing.T) {
	t.Parallel()
	if got := PathPatterns("/"); len(got) != 1 || got[0] != "/*" {
		t.Errorf("PathPatterns(/) = %v, want [/*]", got)
	}
	got := PathPatterns("/api/")
	want := []string{"/api", "/api/*"}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("PathPatterns(/api/) = %v, want %v", got, want)
	}
}

// --- determinism ------------------------------------------------------------

// TestInferNetworking_Deterministic runs InferNetworking against the same
// input 5+ times and diffs the resulting maps' JSON encoding, per this
// phase's review discipline: cheap to check while writing the function,
// expensive to find after the fact (Phase 2's own nondeterminism bug).
func TestInferNetworking_Deterministic(t *testing.T) {
	t.Parallel()
	app := &models.Application{
		Name: "app",
		Services: []models.Service{
			{Name: "a", Capability: models.CapabilityContainer, NetworkIsolationSegments: []string{"one", "two", "three"}},
			{Name: "b", Capability: models.CapabilityContainer, NetworkIsolationSegments: []string{"three", "four"}},
		},
	}
	env := models.NewAwsEnvironment()
	env.Name = "prod"
	env.VpcID = "vpc-1"

	var firstBlocks map[string]any
	for i := 0; i < 6; i++ {
		resources := models.NewAWSResources()
		InferNetworking(resources, app, &env, minimalGetName("prod", "app"), nil)
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

func keysOf[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func mapsJSONEqual(t *testing.T, a, b map[string]any) bool {
	t.Helper()
	aj, err := json.Marshal(a)
	if err != nil {
		t.Fatalf("marshal a: %v", err)
	}
	bj, err := json.Marshal(b)
	if err != nil {
		t.Fatalf("marshal b: %v", err)
	}
	return string(aj) == string(bj)
}

// TestCalculateListenerPriorities_MatchesPythonBand pins the priority band
// for "hello" to the value confirmed against a real run of Python's
// _priority_band('hello') (11401, 2026-08-06) -- the hash-based band
// derivation has to match Python's sha256-of-name scheme bit for bit, or
// listener rule priorities silently collide across a fleet compiled by both
// implementations during any transition period.
func TestCalculateListenerPriorities_MatchesPythonBand(t *testing.T) {
	t.Parallel()
	app := &models.Application{
		Name: "hello",
		Services: []models.Service{
			{Name: "web", Capability: models.CapabilityContainer, Ingress: &models.Ingress{Path: "/"}},
		},
	}
	priorities := CalculateListenerPriorities(app)
	if priorities["web"] != 11401 {
		t.Errorf("priorities[web] = %d, want 11401 (confirmed against Python _priority_band('hello'))", priorities["web"])
	}
}

// TestCalculateListenerPriorities_MoreSpecificPathsEvaluatedFirst mirrors
// test_ingress.py's test_more_specific_paths_are_evaluated_first: within
// one application, a longer (more specific) path must get a lower
// (higher-precedence) priority number than a shorter one, so /api is
// matched before /.
func TestCalculateListenerPriorities_MoreSpecificPathsEvaluatedFirst(t *testing.T) {
	t.Parallel()
	app := &models.Application{
		Name: "app",
		Services: []models.Service{
			{Name: "frontend", Capability: models.CapabilityContainer, Ingress: &models.Ingress{Path: "/"}},
			{Name: "backend", Capability: models.CapabilityContainer, Ingress: &models.Ingress{Path: "/api"}},
		},
	}
	priorities := CalculateListenerPriorities(app)
	if priorities["backend"] >= priorities["frontend"] {
		t.Errorf("backend priority (%d) should be lower than frontend's (%d): more specific paths must be evaluated first",
			priorities["backend"], priorities["frontend"])
	}
}

// TestCalculateListenerPriorities_TwoApplicationsDoNotCollide mirrors
// test_ingress.py's test_two_applications_do_not_collide_on_one_listener:
// priority was once hardcoded to 100, so a second application deploying to
// the same shared listener failed to apply. Two different applications'
// same-named service must get different priorities.
func TestCalculateListenerPriorities_TwoApplicationsDoNotCollide(t *testing.T) {
	t.Parallel()
	serviceFor := func(appName string) *models.Application {
		return &models.Application{
			Name: appName,
			Services: []models.Service{
				{Name: "web", Capability: models.CapabilityContainer, Ingress: &models.Ingress{Path: "/"}},
			},
		}
	}
	first := CalculateListenerPriorities(serviceFor("alpha"))
	second := CalculateListenerPriorities(serviceFor("beta"))

	if first["web"] == second["web"] {
		t.Errorf("expected different priorities for the same service name in two different applications, both got %d", first["web"])
	}
}

// TestPriorityBand_StableAndBounded mirrors test_ingress.py's
// test_priority_is_stable_across_runs: determinism is a stated guarantee,
// so the band cannot come from a salted hash (Go map/string hashing is
// randomized per-process unless a fixed hash like sha256 is used
// deliberately, which priorityBand does).
func TestPriorityBand_StableAndBounded(t *testing.T) {
	t.Parallel()
	if priorityBand("alpha") != priorityBand("alpha") {
		t.Error("expected priorityBand to be stable across repeated calls")
	}
	band := priorityBand("alpha")
	if band < 1 || band > 50000 {
		t.Errorf("priorityBand(alpha) = %d, want it bounded within [1, 50000]", band)
	}
}

// TestPathPatterns_AllPythonCases fills in the /api (no trailing slash)
// and /a/b cases test_ingress.py's parametrized test_path_patterns covers
// that the existing TestPathPatterns test did not.
func TestPathPatterns_AllPythonCases(t *testing.T) {
	t.Parallel()
	cases := []struct {
		path string
		want []string
	}{
		{"/", []string{"/*"}},
		{"/api", []string{"/api", "/api/*"}},
		{"/api/", []string{"/api", "/api/*"}},
		{"/a/b", []string{"/a/b", "/a/b/*"}},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			got := PathPatterns(tc.path)
			if len(got) != len(tc.want) {
				t.Fatalf("PathPatterns(%q) = %v, want %v", tc.path, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("PathPatterns(%q)[%d] = %q, want %q", tc.path, i, got[i], tc.want[i])
				}
			}
		})
	}
}

// TestInferNetworking_NoExplicitNetworksIsFlat mirrors test_networks.py's
// test_a_file_with_no_networks_is_flat: compose materializes a single
// "default" network when none is declared, so everything can reach
// everything -- the behavior of a compose file that says nothing. Uses the
// real scaling example, which declares no networks: block, through the
// real parser/normalizer boundary.
func TestInferNetworking_NoExplicitNetworksIsFlat(t *testing.T) {
	t.Parallel()
	composeApp, err := shared.ParseCompose("../../../../examples/scaling/compose.yml")
	if err != nil {
		t.Fatalf("ParseCompose failed: %v", err)
	}
	app, err := shared.Normalize(composeApp, "scaling")
	if err != nil {
		t.Fatalf("Normalize failed: %v", err)
	}

	env := fullMockProdEnv()
	resources := models.NewAWSResources()
	InferNetworking(resources, app, &env, minimalGetName("prod", "scaling"), nil)

	if _, ok := resources.SecurityGroup["default_sg"]; !ok {
		t.Fatalf("expected a default_sg security group, got keys %v", keysOf(resources.SecurityGroup))
	}

	var webSegments []string
	for _, s := range app.Services {
		if s.Name == "web" {
			webSegments = s.NetworkIsolationSegments
		}
	}
	found := false
	for _, seg := range webSegments {
		if seg == "default" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected web's network segments to include 'default', got %v", webSegments)
	}
}
