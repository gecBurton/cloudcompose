package compiler

import (
	"strings"
	"testing"

	"github.com/gecburton/cloudcompose/internal/models"
)

// TestExplain_RealExamples runs the real parse->normalize->explain
// pipeline against several actual compose files and pins the exact
// rendered output, the same discipline as the AWS/Azure ports:
// byte-identical, not merely equivalent.
func TestExplain_RealExamples(t *testing.T) {
	cases := []struct {
		name string
		want string
	}{
		{
			name: "hello",
			want: "\n[bold]web[/]\n  [green]inferred[/]  runs as a container\n            [dim]image 'nginxdemos/hello:plain-text' is not a recognised managed service[/]\n  [green]inferred[/]  listens on 80\n            [dim]first published port[/]\n  [cyan]declared[/]  served at / on port 80\n            [dim]declared by x-cloud: ingress[/]\n  [cyan]declared[/]  healthy when / returns 2xx/3xx\n            [dim]declared[/]\n\n4 decision(s)",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			composeApp, err := ParseCompose("../../../examples/" + tc.name + "/compose.yml")
			if err != nil {
				t.Fatalf("ParseCompose failed: %v", err)
			}
			app, err := Normalize(composeApp, tc.name)
			if err != nil {
				t.Fatalf("Normalize failed: %v", err)
			}
			got := Render(Explain(nil, app))
			if got != tc.want {
				t.Errorf("got:\n%s\nwant:\n%s", got, tc.want)
			}
		})
	}
}

// TestExplain_MultiPortWarnsAboutIgnoredPorts exercises the port-decisions
// warning branch through the real composeApp (not nil) path -- a branch no
// current caller actually reaches (every current call site passes a nil
// composeApp), but implemented for completeness, and verified working
// here rather than left untested because nothing calls it yet.
func TestExplain_MultiPortWarnsAboutIgnoredPorts(t *testing.T) {
	t.Parallel()
	composeApp, err := ParseCompose("../../../examples/flask/compose.yml")
	if err != nil {
		t.Fatalf("ParseCompose failed: %v", err)
	}
	app, err := Normalize(composeApp, "flask")
	if err != nil {
		t.Fatalf("Normalize failed: %v", err)
	}

	decisions := Explain(composeApp, app)
	found := false
	for _, d := range decisions {
		if d.Subject == "backend" && strings.Contains(d.Decision, "are not exposed") {
			found = true
			if d.Decision != "ports 9229, 9230 are not exposed" {
				t.Errorf("Decision = %q, want 'ports 9229, 9230 are not exposed'", d.Decision)
			}
		}
	}
	if !found {
		t.Errorf("expected a warning about unexposed ports, got %+v", decisions)
	}
}

// TestExplain_DroppedMountsWarning exercises the dropped-mounts warning,
// also only reachable through the composeApp-provided path.
func TestExplain_DroppedMountsWarning(t *testing.T) {
	t.Parallel()
	composeApp, err := ParseCompose("../../../examples/flask/compose.yml")
	if err != nil {
		t.Fatalf("ParseCompose failed: %v", err)
	}
	app, err := Normalize(composeApp, "flask")
	if err != nil {
		t.Fatalf("Normalize failed: %v", err)
	}

	decisions := Explain(composeApp, app)
	found := false
	for _, d := range decisions {
		if d.Subject == "backend" && strings.Contains(d.Decision, "mount(s) dropped") {
			found = true
			if d.Decision != "3 mount(s) dropped" {
				t.Errorf("Decision = %q, want '3 mount(s) dropped'", d.Decision)
			}
		}
	}
	if !found {
		t.Errorf("expected a dropped-mounts warning, got %+v", decisions)
	}
}

// TestExplain_NoPublicServiceWarnsApplicationUnreachable exercises the
// ingress-decisions fallback branch.
func TestExplain_NoPublicServiceWarnsApplicationUnreachable(t *testing.T) {
	t.Parallel()
	port := 80
	app := &models.Application{
		Name: "app",
		Services: []models.Service{
			{Name: "web", Capability: models.CapabilityContainer, Port: &port},
		},
	}
	decisions := Explain(nil, app)

	found := false
	for _, d := range decisions {
		if d.Subject == "application" {
			found = true
			if d.Decision != "NOT reachable from outside" {
				t.Errorf("Decision = %q, want 'NOT reachable from outside'", d.Decision)
			}
			if !strings.Contains(d.Because, "web") {
				t.Errorf("Because = %q, want it to mention web (the only port-publishing service)", d.Because)
			}
		}
	}
	if !found {
		t.Errorf("expected an 'application' subject decision, got %+v", decisions)
	}
}

// TestExplain_NoServicePublishesAnyPort mirrors the other half of that
// same fallback branch: nothing publishes a port at all.
func TestExplain_NoServicePublishesAnyPort(t *testing.T) {
	t.Parallel()
	app := &models.Application{
		Name: "app",
		Services: []models.Service{
			{Name: "worker", Capability: models.CapabilityContainer},
		},
	}
	decisions := Explain(nil, app)

	for _, d := range decisions {
		if d.Subject == "application" {
			if d.Because != "no service publishes a port" {
				t.Errorf("Because = %q, want 'no service publishes a port'", d.Because)
			}
			return
		}
	}
	t.Error("expected an 'application' subject decision")
}

// TestExplain_CannotReachWarning exercises the wiring-decisions "cannot
// reach" branch: a referenced container with no port has no address to
// hand out.
func TestExplain_CannotReachWarning(t *testing.T) {
	t.Parallel()
	app := &models.Application{
		Name: "app",
		Services: []models.Service{
			{Name: "web", Capability: models.CapabilityContainer, Env: map[string]string{"WORKER_HOST": "worker"}},
			{Name: "worker", Capability: models.CapabilityContainer}, // no port
		},
		Relationships: []models.Relationship{{Client: "web", Server: "worker"}},
	}
	decisions := Explain(nil, app)

	found := false
	for _, d := range decisions {
		if d.Subject == "web" && d.Decision == "cannot reach worker" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a 'cannot reach worker' decision, got %+v", decisions)
	}
}

// TestExplain_NothingWiredWarning exercises the wiring-decisions "nothing
// wired" branch: a Relationship exists but no env var actually
// references the server.
func TestExplain_NothingWiredWarning(t *testing.T) {
	t.Parallel()
	dbName := "db"
	app := &models.Application{
		Name: "app",
		Services: []models.Service{
			{Name: "web", Capability: models.CapabilityContainer},
			{Name: "db", Capability: models.CapabilityDatabase, DatabaseName: &dbName},
		},
		Relationships: []models.Relationship{{Client: "web", Server: "db"}},
	}
	decisions := Explain(nil, app)

	found := false
	for _, d := range decisions {
		if d.Subject == "web" && d.Decision == "nothing wired to db" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a 'nothing wired to db' decision, got %+v", decisions)
	}
}

// TestExplain_Deterministic runs the same input 6 times and diffs the
// rendered output, per this package's own review discipline elsewhere.
func TestExplain_Deterministic(t *testing.T) {
	t.Parallel()
	composeApp, err := ParseCompose("../../../examples/doctor/compose.yml")
	if err != nil {
		t.Fatalf("ParseCompose failed: %v", err)
	}
	app, err := Normalize(composeApp, "doctor")
	if err != nil {
		t.Fatalf("Normalize failed: %v", err)
	}

	var first string
	for i := 0; i < 6; i++ {
		out := Render(Explain(nil, app))
		if i == 0 {
			first = out
			continue
		}
		if out != first {
			t.Fatalf("run %d differs from run 0", i)
		}
	}
}

// --- Additional tests using synthetic ComposeApplication/Application
// pairs built by hand: explain() takes both models directly and does
// not depend on Normalize() to produce them. ---

func intPtrExplain(i int) *int { return &i }

// TestExplain_WiredManagedServiceIsNotAWarning verifies that a
// correctly-wired reference produces the "KEY -> server" decision and
// no "nothing wired" warning.
func TestExplain_WiredManagedServiceIsNotAWarning(t *testing.T) {
	t.Parallel()
	dbName := "db"
	app := &models.Application{
		Name: "test-project",
		Services: []models.Service{
			{Name: "web", Image: "web", Capability: models.CapabilityContainer, Env: map[string]string{"POSTGRES_HOST": "db"}},
			{Name: "db", Image: "postgres:16", Capability: models.CapabilityDatabase, DatabaseName: &dbName},
		},
		Relationships: []models.Relationship{{Client: "web", Server: "db"}},
	}
	decisions := Explain(nil, app)

	foundWired := false
	for _, d := range decisions {
		if strings.Contains(d.Decision, "POSTGRES_HOST → db") {
			foundWired = true
		}
		if strings.Contains(d.Decision, "nothing wired") {
			t.Errorf("did not expect a 'nothing wired' decision, got %q", d.Decision)
		}
	}
	if !foundWired {
		t.Errorf("expected a 'POSTGRES_HOST → db' decision, got %+v", decisions)
	}
}

// TestExplain_EmptySecretsAreWarnedAbout verifies that a service with
// an empty secret is warned about.
func TestExplain_EmptySecretsAreWarnedAbout(t *testing.T) {
	t.Parallel()
	port := 80
	app := &models.Application{
		Name: "test-project",
		Services: []models.Service{
			{Name: "web", Image: "web", Capability: models.CapabilityContainer, Port: &port, Secrets: []string{"api-key"}},
		},
	}
	decisions := Explain(nil, app)

	found := false
	for _, d := range decisions {
		if d.Source == SourceWarning && strings.Contains(d.Decision, "api-key") && strings.Contains(d.Decision, "empty") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a warning mentioning 'api-key' and 'empty', got %+v", decisions)
	}
}

// TestExplain_SubstitutionReportsTheImageItMatched verifies that an
// inferred (not declared) capability substitution reports the matched
// image in Because, with Source == SourceInferred.
func TestExplain_SubstitutionReportsTheImageItMatched(t *testing.T) {
	t.Parallel()
	dbName := "db"
	app := &models.Application{
		Name: "test-project",
		Services: []models.Service{
			{Name: "db", Image: "pgvector/pgvector:pg17", Capability: models.CapabilityDatabase, DatabaseName: &dbName},
		},
	}
	decisions := Explain(nil, app)

	var substitution *Decision
	for i := range decisions {
		if strings.Contains(decisions[i].Decision, "managed database") {
			substitution = &decisions[i]
		}
	}
	if substitution == nil {
		t.Fatalf("expected a 'managed database' decision, got %+v", decisions)
	}
	if !strings.Contains(substitution.Because, "pgvector/pgvector:pg17") {
		t.Errorf("Because = %q, want it to mention the matched image", substitution.Because)
	}
	if substitution.Source != SourceInferred {
		t.Errorf("Source = %q, want inferred", substitution.Source)
	}
}

// TestExplain_DeclaredCapabilityIsReportedAsDeclared verifies that an
// explicitly declared x-cloud: capability reports Source ==
// SourceDeclared.
func TestExplain_DeclaredCapabilityIsReportedAsDeclared(t *testing.T) {
	t.Parallel()
	dbName := "thing"
	composeApp := &models.ComposeApplication{
		Services: map[string]models.ComposeService{
			"thing": {
				Image:  "acme/thing",
				XCloud: map[string]any{"capability": "database"},
			},
		},
	}
	app := &models.Application{
		Name: "test-project",
		Services: []models.Service{
			{Name: "thing", Image: "acme/thing", Capability: models.CapabilityDatabase, DatabaseName: &dbName},
		},
	}
	decisions := Explain(composeApp, app)

	var substitution *Decision
	for i := range decisions {
		if strings.Contains(decisions[i].Decision, "managed database") {
			substitution = &decisions[i]
		}
	}
	if substitution == nil {
		t.Fatalf("expected a 'managed database' decision, got %+v", decisions)
	}
	if substitution.Source != SourceDeclared {
		t.Errorf("Source = %q, want declared", substitution.Source)
	}
}

// TestExplain_BuildReportsTheDockerfile verifies that a build reports
// the Dockerfile path.
func TestExplain_BuildReportsTheDockerfile(t *testing.T) {
	t.Parallel()
	port := 80
	buildContext := "."
	dockerfile := "./backend/Dockerfile"
	app := &models.Application{
		Name: "test-project",
		Services: []models.Service{
			{
				Name: "web", Image: "placeholder", Capability: models.CapabilityContainer,
				Port: &port, BuildContext: &buildContext, Dockerfile: &dockerfile,
			},
		},
	}
	decisions := Explain(nil, app)

	found := false
	for _, d := range decisions {
		if strings.Contains(d.Because, "./backend/Dockerfile") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a decision mentioning the Dockerfile path, got %+v", decisions)
	}
}

// TestExplain_RenderCountsWarnings_WithWarningsPresent exercises the
// "worth checking" branch (only the zero-warnings branch was exercised
// via the hello golden fixture before this test).
func TestExplain_RenderCountsWarnings_WithWarningsPresent(t *testing.T) {
	t.Parallel()
	port := 8080
	app := &models.Application{
		Name: "test-project",
		Services: []models.Service{
			{Name: "web", Image: "web", Capability: models.CapabilityContainer, Port: &port},
		},
	}
	output := Render(Explain(nil, app))
	if !strings.Contains(output, "worth checking") {
		t.Errorf("expected 'worth checking' in output, got:\n%s", output)
	}
}

// TestExplain_MissingIngressNamesTheCandidatesAndMentionsXCloud verifies
// that the warning names the candidate service and mentions
// "x-cloud: ingress".
func TestExplain_MissingIngressNamesTheCandidatesAndMentionsXCloud(t *testing.T) {
	t.Parallel()
	port := 8080
	app := &models.Application{
		Name: "test-project",
		Services: []models.Service{
			{Name: "web", Image: "web", Capability: models.CapabilityContainer, Port: &port},
		},
	}
	decisions := Explain(nil, app)

	var warning *Decision
	for i := range decisions {
		if decisions[i].Source == SourceWarning {
			warning = &decisions[i]
		}
	}
	if warning == nil {
		t.Fatalf("expected a warning, got %+v", decisions)
	}
	if !strings.Contains(warning.Because, "web") {
		t.Errorf("Because = %q, want it to mention web", warning.Because)
	}
	if !strings.Contains(warning.Because, "x-cloud: ingress") {
		t.Errorf("Because = %q, want it to mention x-cloud: ingress", warning.Because)
	}
}
