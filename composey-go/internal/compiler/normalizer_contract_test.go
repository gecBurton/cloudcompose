package compiler

import (
	"strings"
	"testing"

	"github.com/gecburton/composey/internal/models"
)

// Ported from the deleted tests/unit/test_normalizer.py, test_volumes.py,
// test_capability_detection.py, test_database_name.py, test_schedule.py,
// test_networks.py, test_platform_settings.py, and test_ingress.py
// (Python, deleted in 0244d4a when parser.go/normalizer.go replaced
// parser.py/normalizer.py). These assert the normalizer's actual contract —
// specific edge cases the Python version got right after getting them
// wrong once — not just that it runs without crashing on real example
// files.

// --- test_normalizer.py: core contract -------------------------------------

func TestNormalizeBasicService(t *testing.T) {
	app := &models.ComposeApplication{
		Services: map[string]models.ComposeService{
			"web": {
				Image: "nginx",
				Ports: []models.PortConfig{{Target: 80, Published: "80"}},
				Environment: map[string]*string{
					"DEBUG": strPtr("true"),
				},
			},
		},
	}

	result, err := Normalize(app, "myapp")
	if err != nil {
		t.Fatalf("Normalize failed: %v", err)
	}

	if result.Name != "myapp" {
		t.Errorf("Name = %q, want %q", result.Name, "myapp")
	}
	if len(result.Services) != 1 {
		t.Fatalf("expected 1 service, got %d", len(result.Services))
	}
	svc := result.Services[0]
	if svc.Port == nil || *svc.Port != 80 {
		t.Errorf("Port = %v, want 80", svc.Port)
	}
	if len(result.PublicServices()) != 0 {
		t.Errorf("expected no public services, got %v", result.PublicServices())
	}
	if svc.Env["DEBUG"] != "true" {
		t.Errorf("env[DEBUG] = %q, want %q", svc.Env["DEBUG"], "true")
	}
}

func TestNormalizeRelationships(t *testing.T) {
	app := &models.ComposeApplication{
		Services: map[string]models.ComposeService{
			"web": {
				Image:     "nginx",
				DependsOn: map[string]models.Dependency{"db": {}},
			},
			"db": {Image: "postgres:16"},
		},
	}

	result, err := Normalize(app, "myapp")
	if err != nil {
		t.Fatalf("Normalize failed: %v", err)
	}

	if len(result.Relationships) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(result.Relationships))
	}
	rel := result.Relationships[0]
	if rel.Client != "web" || rel.Server != "db" {
		t.Errorf("relationship = %+v, want client=web server=db", rel)
	}
	if len(result.PublicServices()) != 0 {
		t.Errorf("depends_on must not make anything public, got %v", result.PublicServices())
	}
}

func TestNormalizeNoPublicServiceWithoutPorts(t *testing.T) {
	app := &models.ComposeApplication{
		Services: map[string]models.ComposeService{
			"worker": {Image: "myapp/worker"},
		},
	}

	result, err := Normalize(app, "myapp")
	if err != nil {
		t.Fatalf("Normalize failed: %v", err)
	}

	if result.Services[0].Port != nil {
		t.Errorf("Port = %v, want nil", result.Services[0].Port)
	}
	if len(result.PublicServices()) != 0 {
		t.Errorf("expected no public services, got %v", result.PublicServices())
	}
}

func TestNormalizeMultiplePortsTakesFirst(t *testing.T) {
	// The target of the *first declared* port, not the published one — and
	// not the smallest, largest, or any other port in the list.
	app := &models.ComposeApplication{
		Services: map[string]models.ComposeService{
			"web": {
				Image: "nginx",
				Ports: []models.PortConfig{
					{Target: 3000, Published: "80"},
					{Target: 9000, Published: "9000"},
				},
			},
		},
	}

	result, err := Normalize(app, "myapp")
	if err != nil {
		t.Fatalf("Normalize failed: %v", err)
	}

	if result.Services[0].Port == nil || *result.Services[0].Port != 3000 {
		t.Errorf("Port = %v, want 3000", result.Services[0].Port)
	}
}

func TestNormalizeScalingPassthrough(t *testing.T) {
	app := &models.ComposeApplication{
		Services: map[string]models.ComposeService{
			"web": {
				Image:     "nginx",
				XComposey: map[string]interface{}{"min_scale": 2, "max_scale": 10},
			},
		},
	}

	result, err := Normalize(app, "myapp")
	if err != nil {
		t.Fatalf("Normalize failed: %v", err)
	}

	if result.Services[0].MinScale != 2 {
		t.Errorf("MinScale = %d, want 2", result.Services[0].MinScale)
	}
	if result.Services[0].MaxScale != 10 {
		t.Errorf("MaxScale = %d, want 10", result.Services[0].MaxScale)
	}
}

func TestNormalizeMissingImageFallsBackToPlaceholder(t *testing.T) {
	// A missing image must not crash the compiler — it degrades to a
	// placeholder rather than failing normalization outright.
	app := &models.ComposeApplication{
		Services: map[string]models.ComposeService{
			"web": {},
		},
	}

	result, err := Normalize(app, "myapp")
	if err != nil {
		t.Fatalf("Normalize failed: %v", err)
	}
	if result.Services[0].Image != "placeholder" {
		t.Errorf("Image = %q, want %q", result.Services[0].Image, "placeholder")
	}
}

// --- test_volumes.py: named volumes are refused, mounts are not ------------

func TestNamedVolumeIsRefused(t *testing.T) {
	app := &models.ComposeApplication{
		Services: map[string]models.ComposeService{
			"web": {
				Image: "nginx",
				Volumes: []interface{}{
					models.VolumeDefinition{Type: "volume", Source: "db-data", Target: "/data"},
				},
			},
		},
	}

	_, err := Normalize(app, "myapp")
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if !strings.Contains(err.Error(), "mounts named volume(s) db-data") {
		t.Errorf("error = %q, want it to mention 'mounts named volume(s) db-data'", err.Error())
	}
	if !strings.Contains(err.Error(), "minio") {
		t.Errorf("error = %q, want it to suggest minio as the alternative", err.Error())
	}
}

func TestEveryNamedVolumeIsListed(t *testing.T) {
	app := &models.ComposeApplication{
		Services: map[string]models.ComposeService{
			"web": {
				Image: "nginx",
				Volumes: []interface{}{
					models.VolumeDefinition{Type: "volume", Source: "media", Target: "/media"},
					models.VolumeDefinition{Type: "volume", Source: "assets", Target: "/assets"},
				},
			},
		},
	}

	_, err := Normalize(app, "myapp")
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if !strings.Contains(err.Error(), "assets, media") {
		t.Errorf("error = %q, want both volumes listed sorted", err.Error())
	}
}

func TestNamedVolumeOnSubstitutedServiceIsAccepted(t *testing.T) {
	// A managed database brings its own storage; the named volume is moot,
	// not an error, once the service is substituted for a managed service.
	app := &models.ComposeApplication{
		Services: map[string]models.ComposeService{
			"db": {
				Image: "postgres:16",
				Volumes: []interface{}{
					models.VolumeDefinition{Type: "volume", Source: "db-data", Target: "/var/lib/postgresql/data"},
				},
			},
		},
	}

	result, err := Normalize(app, "myapp")
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if result.Services[0].Capability != models.CapabilityDatabase {
		t.Errorf("Capability = %q, want database", result.Services[0].Capability)
	}
}

func TestLocalOnlyMountsAreIgnored(t *testing.T) {
	cases := []struct {
		name   string
		volume interface{}
	}{
		{"short-form bind", "./local:/data"},
		{"absolute bind", "/etc/hosts:/etc/hosts:ro"},
		{"home-relative bind", "~/config:/config"},
		{"anonymous volume struct", models.VolumeDefinition{Type: "volume", Target: "/data"}},
		{"bind struct", models.VolumeDefinition{Type: "bind", Source: "./local", Target: "/data"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			app := &models.ComposeApplication{
				Services: map[string]models.ComposeService{
					"web": {Image: "nginx", Volumes: []interface{}{tc.volume}},
				},
			}
			if _, err := Normalize(app, "myapp"); err != nil {
				t.Errorf("expected no error for a local-only mount, got: %v", err)
			}
		})
	}
}

func TestBindMountAlongsideNamedVolumeStillFails(t *testing.T) {
	app := &models.ComposeApplication{
		Services: map[string]models.ComposeService{
			"web": {
				Image: "nginx",
				Volumes: []interface{}{
					"./local:/scratch",
					models.VolumeDefinition{Type: "volume", Source: "media", Target: "/media"},
				},
			},
		},
	}

	_, err := Normalize(app, "myapp")
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if !strings.Contains(err.Error(), "media") {
		t.Errorf("error = %q, want it to mention the named volume 'media'", err.Error())
	}
}

// --- test_capability_detection.py: x-composey validation --------------------

func TestCapabilityCanBeDeclaredExplicitly(t *testing.T) {
	app := &models.ComposeApplication{
		Services: map[string]models.ComposeService{
			"thing": {
				Image:     "acme/private-thing:1",
				XComposey: map[string]interface{}{"capability": "database"},
			},
		},
	}

	result, err := Normalize(app, "test-project")
	if err != nil {
		t.Fatalf("Normalize failed: %v", err)
	}
	if result.Services[0].Capability != models.CapabilityDatabase {
		t.Errorf("Capability = %q, want database", result.Services[0].Capability)
	}
}

func TestCapabilityOverrideBeatsInference(t *testing.T) {
	app := &models.ComposeApplication{
		Services: map[string]models.ComposeService{
			"db": {
				Image:     "postgres:16",
				XComposey: map[string]interface{}{"capability": "container"},
			},
		},
	}

	result, err := Normalize(app, "p")
	if err != nil {
		t.Fatalf("Normalize failed: %v", err)
	}
	if result.Services[0].Capability != models.CapabilityContainer {
		t.Errorf("Capability = %q, want container (explicit override)", result.Services[0].Capability)
	}
}

func TestUnknownCapabilityIsRejectedByName(t *testing.T) {
	app := &models.ComposeApplication{
		Services: map[string]models.ComposeService{
			"thing": {
				Image:     "acme/private-thing:1",
				XComposey: map[string]interface{}{"capability": "databse"},
			},
		},
	}

	_, err := Normalize(app, "test-project")
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if !strings.Contains(err.Error(), "service 'thing' has an invalid x-composey") {
		t.Errorf("error = %q, want it to mention service 'thing' has an invalid x-composey", err.Error())
	}
}

func TestMisspelledKeyIsRejectedRatherThanIgnored(t *testing.T) {
	// The failure this validation exists for: `capabilty` was silently
	// dropped, and the service deployed as whatever the compiler guessed.
	app := &models.ComposeApplication{
		Services: map[string]models.ComposeService{
			"thing": {
				Image:     "acme/private-thing:1",
				XComposey: map[string]interface{}{"capabilty": "database"},
			},
		},
	}

	_, err := Normalize(app, "test-project")
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if !strings.Contains(err.Error(), "capabilty") {
		t.Errorf("error = %q, want it to mention the misspelled key 'capabilty'", err.Error())
	}
}

func TestMisspelledPublicIsRejected(t *testing.T) {
	app := &models.ComposeApplication{
		Services: map[string]models.ComposeService{
			"thing": {
				Image:     "acme/private-thing:1",
				XComposey: map[string]interface{}{"publik": true},
			},
		},
	}

	_, err := Normalize(app, "test-project")
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if !strings.Contains(err.Error(), "publik") {
		t.Errorf("error = %q, want it to mention 'publik'", err.Error())
	}
}

func TestOutOfRangeValuesAreRejected(t *testing.T) {
	cases := []struct {
		name    string
		setting map[string]interface{}
	}{
		{"size", map[string]interface{}{"size": "enormous"}},
		{"min_scale", map[string]interface{}{"min_scale": -1}},
		{"max_scale", map[string]interface{}{"max_scale": 0}},
		{"cpu", map[string]interface{}{"cpu": 0}},
		{"startup_grace_period", map[string]interface{}{"startup_grace_period": -5}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			app := &models.ComposeApplication{
				Services: map[string]models.ComposeService{
					"thing": {Image: "acme/private-thing:1", XComposey: tc.setting},
				},
			}
			_, err := Normalize(app, "test-project")
			if err == nil {
				t.Fatal("expected an error, got nil")
			}
			if !strings.Contains(err.Error(), "invalid x-composey") {
				t.Errorf("error = %q, want it to mention 'invalid x-composey'", err.Error())
			}
		})
	}
}

func publicTestApp(frontendXC, backendXC map[string]interface{}) *models.ComposeApplication {
	return &models.ComposeApplication{
		Services: map[string]models.ComposeService{
			"frontend": {
				Image:     "frontend",
				Ports:     []models.PortConfig{{Target: 8081, Published: "8081"}},
				XComposey: frontendXC,
			},
			"backend": {
				Image:     "backend",
				Ports:     []models.PortConfig{{Target: 8080, Published: "8080"}},
				XComposey: backendXC,
			},
		},
	}
}

func TestNoPublicServiceIsDetectedFromNonStandardPorts(t *testing.T) {
	// The behaviour that left two real applications deployed but unreachable.
	result, err := Normalize(publicTestApp(nil, nil), "p")
	if err != nil {
		t.Fatalf("Normalize failed: %v", err)
	}
	if len(result.PublicServices()) != 0 {
		t.Errorf("expected no public services, got %v", result.PublicServices())
	}
}

func TestPublicCanBeDeclaredExplicitly(t *testing.T) {
	result, err := Normalize(
		publicTestApp(map[string]interface{}{"ingress": map[string]interface{}{}}, nil), "p",
	)
	if err != nil {
		t.Fatalf("Normalize failed: %v", err)
	}
	names := serviceNames(result.PublicServices())
	if len(names) != 1 || names[0] != "frontend" {
		t.Errorf("public services = %v, want [frontend]", names)
	}
}

func TestTwoServicesMayBothBePublicOnDistinctPaths(t *testing.T) {
	result, err := Normalize(
		publicTestApp(
			map[string]interface{}{"ingress": map[string]interface{}{"path": "/"}},
			map[string]interface{}{"ingress": map[string]interface{}{"path": "/api"}},
		),
		"p",
	)
	if err != nil {
		t.Fatalf("Normalize failed: %v", err)
	}
	names := serviceNames(result.PublicServices())
	if len(names) != 2 {
		t.Errorf("expected 2 public services, got %v", names)
	}
}

func TestTwoServicesOnTheSamePathAreRejected(t *testing.T) {
	_, err := Normalize(
		publicTestApp(
			map[string]interface{}{"ingress": map[string]interface{}{}},
			map[string]interface{}{"ingress": map[string]interface{}{}},
		),
		"p",
	)
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if !strings.Contains(err.Error(), "both serve") {
		t.Errorf("error = %q, want it to mention 'both serve'", err.Error())
	}
}

// --- test_ingress.py: port/ingress declaration semantics -------------------

func TestPublishing80DoesNotImplyExposure(t *testing.T) {
	app := &models.ComposeApplication{
		Services: map[string]models.ComposeService{
			"web": {Image: "nginx", Ports: []models.PortConfig{{Target: 80, Published: "80"}}},
		},
	}
	result, err := Normalize(app, "p")
	if err != nil {
		t.Fatalf("Normalize failed: %v", err)
	}
	if len(result.PublicServices()) != 0 {
		t.Errorf("expected no public services just from publishing port 80, got %v", result.PublicServices())
	}
}

func TestIngressPortDefaultsToTheServicePort(t *testing.T) {
	app := &models.ComposeApplication{
		Services: map[string]models.ComposeService{
			"web": {
				Image:     "nginx",
				Ports:     []models.PortConfig{{Target: 9090, Published: "9090"}},
				XComposey: map[string]interface{}{"ingress": map[string]interface{}{}},
			},
		},
	}
	result, err := Normalize(app, "p")
	if err != nil {
		t.Fatalf("Normalize failed: %v", err)
	}
	ingress := result.Services[0].Ingress
	if ingress == nil {
		t.Fatal("expected ingress to be set")
	}
	if ingress.Port == nil {
		// nil means "use the service's own port" downstream; either
		// behaviour is acceptable as long as it resolves to 9090 somewhere
		// downstream — but Python's contract set the port explicitly, so
		// assert that here too, matching the Go inference's expectation.
		if result.Services[0].Port == nil || *result.Services[0].Port != 9090 {
			t.Errorf("expected ingress to resolve to the service port 9090")
		}
	}
}

func TestIngressPortCanBeDeclared(t *testing.T) {
	app := &models.ComposeApplication{
		Services: map[string]models.ComposeService{
			"web": {
				Image: "nginx",
				Ports: []models.PortConfig{{Target: 8080, Published: "8080"}},
				XComposey: map[string]interface{}{
					"ingress": map[string]interface{}{"port": 9000},
				},
			},
		},
	}
	result, err := Normalize(app, "p")
	if err != nil {
		t.Fatalf("Normalize failed: %v", err)
	}
	ingress := result.Services[0].Ingress
	if ingress == nil || ingress.Port == nil || *ingress.Port != 9000 {
		t.Errorf("expected declared ingress port 9000, got %+v", ingress)
	}
}

func TestBareIngressKeyDeclaresADefaultRoute(t *testing.T) {
	// `ingress:` with nothing under it (parses as YAML null) must still
	// declare a default route — the only place a service can still end up
	// silently non-public if this is handled wrong.
	app := &models.ComposeApplication{
		Services: map[string]models.ComposeService{
			"web": {
				Image:     "nginx",
				Ports:     []models.PortConfig{{Target: 80, Published: "80"}},
				XComposey: map[string]interface{}{"ingress": nil},
			},
		},
	}
	result, err := Normalize(app, "p")
	if err != nil {
		t.Fatalf("Normalize failed: %v", err)
	}
	if len(result.PublicServices()) != 1 {
		t.Errorf("expected 1 public service from a bare 'ingress:' key, got %v", result.PublicServices())
	}
}

func TestThePublicShorthandIsGone(t *testing.T) {
	// The old x-composey.public: true shorthand was replaced by explicit
	// ingress declaration; it must be rejected, not silently ignored.
	app := &models.ComposeApplication{
		Services: map[string]models.ComposeService{
			"web": {
				Image:     "nginx",
				XComposey: map[string]interface{}{"public": true},
			},
		},
	}
	_, err := Normalize(app, "p")
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if !strings.Contains(err.Error(), "public") {
		t.Errorf("error = %q, want it to mention 'public'", err.Error())
	}
}

// --- test_platform_settings.py: grace period key aliasing -------------------

func TestStartupGracePeriodIsRead(t *testing.T) {
	app := &models.ComposeApplication{
		Services: map[string]models.ComposeService{
			"web": {Image: "nginx", XComposey: map[string]interface{}{"startup_grace_period": 120}},
		},
	}
	result, err := Normalize(app, "p")
	if err != nil {
		t.Fatalf("Normalize failed: %v", err)
	}
	if result.Services[0].StartupGracePeriod == nil || *result.Services[0].StartupGracePeriod != 120 {
		t.Errorf("StartupGracePeriod = %v, want 120", result.Services[0].StartupGracePeriod)
	}
}

func TestDeprecatedHealthCheckGracePeriodStillWorks(t *testing.T) {
	app := &models.ComposeApplication{
		Services: map[string]models.ComposeService{
			"web": {Image: "nginx", XComposey: map[string]interface{}{"health_check_grace_period": 90}},
		},
	}
	result, err := Normalize(app, "p")
	if err != nil {
		t.Fatalf("Normalize failed: %v", err)
	}
	if result.Services[0].StartupGracePeriod == nil || *result.Services[0].StartupGracePeriod != 90 {
		t.Errorf("StartupGracePeriod = %v, want 90 (from the deprecated key)", result.Services[0].StartupGracePeriod)
	}
}

func TestNeutralNameWinsWhenBothAreGiven(t *testing.T) {
	app := &models.ComposeApplication{
		Services: map[string]models.ComposeService{
			"web": {
				Image: "nginx",
				XComposey: map[string]interface{}{
					"startup_grace_period":      120,
					"health_check_grace_period": 90,
				},
			},
		},
	}
	result, err := Normalize(app, "p")
	if err != nil {
		t.Fatalf("Normalize failed: %v", err)
	}
	if result.Services[0].StartupGracePeriod == nil || *result.Services[0].StartupGracePeriod != 120 {
		t.Errorf("StartupGracePeriod = %v, want 120 (the neutral name wins)", result.Services[0].StartupGracePeriod)
	}
}

// --- test_database_name.py: derivation and sanitization --------------------

func TestDatabaseNameDefaultAvoidsReservedWord(t *testing.T) {
	// The bug that broke acceptance: the bare service name "db" collided
	// with the engine's own reserved word.
	result := DatabaseName("doctor", "db", map[string]string{})
	if result != "doctor_db" {
		t.Errorf("DatabaseName(doctor, db, {}) = %q, want doctor_db", result)
	}
}

func TestDatabaseNameStatedIsHonouredVerbatim(t *testing.T) {
	result := DatabaseName("shop", "db", map[string]string{"POSTGRES_DB": "orders"})
	if result != "orders" {
		t.Errorf("DatabaseName with POSTGRES_DB=orders = %q, want orders", result)
	}
}

func TestDatabaseNameOnlyReferencedIsNotUsed(t *testing.T) {
	// An unresolved ${POSTGRES_DB} is absent from the environment dict
	// entirely by this point (declaredEnvironment strips it out) — so this
	// tests the fallback behaviour when the key is simply not present.
	result := DatabaseName("shop", "db", map[string]string{})
	if result != "shop_db" {
		t.Errorf("DatabaseName with no env = %q, want shop_db", result)
	}
}

func TestDatabaseNameSanitization(t *testing.T) {
	cases := []struct {
		raw      string
		expected string
	}{
		{"my-app", "my_app"},
		{"My_App", "my_app"},
		{"2fast", "fast"},
		{"_", "app"},
	}
	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			result := SanitizeDatabaseName(tc.raw)
			if result != tc.expected {
				t.Errorf("SanitizeDatabaseName(%q) = %q, want %q", tc.raw, result, tc.expected)
			}
		})
	}
}

func TestDatabaseNameLengthCap(t *testing.T) {
	long := strings.Repeat("a", 80)
	result := SanitizeDatabaseName(long)
	if len(result) > 63 {
		t.Errorf("SanitizeDatabaseName length = %d, want <= 63", len(result))
	}
}

// --- test_schedule.py: cron/rate parsing, expanded --------------------------

func TestParseScheduleCronNormalization(t *testing.T) {
	cases := []struct {
		input    string
		expected string
	}{
		{"0 2 * * *", "0 2 * * *"},
		{"cron(0 2 * * ? *)", "0 2 * * *"},
		{"cron(0 2 ? * MON *)", "0 2 * * MON"},
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			schedule, err := ParseSchedule(tc.input)
			if err != nil {
				t.Fatalf("ParseSchedule(%q) failed: %v", tc.input, err)
			}
			cron, ok := schedule.(*models.CronSchedule)
			if !ok {
				t.Fatalf("expected a CronSchedule, got %T", schedule)
			}
			if cron.Expression != tc.expected {
				t.Errorf("Expression = %q, want %q", cron.Expression, tc.expected)
			}
		})
	}
}

func TestParseScheduleIntervals(t *testing.T) {
	cases := []struct {
		input string
		value int
		unit  string
	}{
		{"every 1 hour", 1, "hours"},
		{"every hour", 1, "hours"},
		{"rate(15 minutes)", 15, "minutes"},
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			schedule, err := ParseSchedule(tc.input)
			if err != nil {
				t.Fatalf("ParseSchedule(%q) failed: %v", tc.input, err)
			}
			rate, ok := schedule.(*models.RateSchedule)
			if !ok {
				t.Fatalf("expected a RateSchedule, got %T", schedule)
			}
			if rate.Value != tc.value {
				t.Errorf("Value = %d, want %d", rate.Value, tc.value)
			}
			if string(rate.Unit) != tc.unit {
				t.Errorf("Unit = %q, want %q", rate.Unit, tc.unit)
			}
		})
	}
}

func TestParseScheduleRejectsMalformed(t *testing.T) {
	cases := []string{"", "0 2 * *", "* * * * * * *", "hourly", "every fortnight"}
	for _, input := range cases {
		t.Run(input, func(t *testing.T) {
			if _, err := ParseSchedule(input); err == nil {
				t.Errorf("ParseSchedule(%q) expected an error, got nil", input)
			}
		})
	}
}

func TestNormalizerReadsScheduleFromComposeFile(t *testing.T) {
	app := &models.ComposeApplication{
		Services: map[string]models.ComposeService{
			"cleanup": {
				Image:     "myapp/cleanup",
				XComposey: map[string]interface{}{"schedule": "every 6 hours"},
			},
		},
	}
	result, err := Normalize(app, "p")
	if err != nil {
		t.Fatalf("Normalize failed: %v", err)
	}
	rate, ok := result.Services[0].Schedule.(*models.RateSchedule)
	if !ok {
		t.Fatalf("expected a RateSchedule, got %T", result.Services[0].Schedule)
	}
	if rate.Value != 6 || string(rate.Unit) != "hours" {
		t.Errorf("Schedule = %+v, want every 6 hours", rate)
	}
}

// --- test_networks.py: segment sorting, cap, external rejection ------------

func TestNetworkSegmentsAreSortedForDeterminism(t *testing.T) {
	service := &models.ComposeService{
		Networks: map[string]interface{}{"zebra": nil, "alpha": nil},
	}
	segments, err := NetworkSegmentsFor("web", service, 0)
	if err != nil {
		t.Fatalf("NetworkSegmentsFor failed: %v", err)
	}
	if len(segments) != 2 || segments[0] != "alpha" || segments[1] != "zebra" {
		t.Errorf("segments = %v, want [alpha zebra]", segments)
	}
}

func TestTooManyNetworkSegmentsIsRejected(t *testing.T) {
	networks := map[string]interface{}{}
	for _, n := range []string{"a", "b", "c", "d", "e", "f"} {
		networks[n] = nil
	}
	service := &models.ComposeService{Networks: networks}
	_, err := NetworkSegmentsFor("web", service, 0)
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if !strings.Contains(err.Error(), "joins 6 network segments") {
		t.Errorf("error = %q, want it to mention 'joins 6 network segments'", err.Error())
	}
}

func TestExternalNetworksAreRejected(t *testing.T) {
	app := &models.ComposeApplication{
		Services: map[string]models.ComposeService{
			"web": {Image: "nginx"},
		},
		Networks: map[string]*models.NetworkDefinition{
			"shared": {External: true},
		},
	}
	_, err := Normalize(app, "p")
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if !strings.Contains(err.Error(), "declared external") {
		t.Errorf("error = %q, want it to mention 'declared external'", err.Error())
	}
}

// --- test_build.py: build context extraction --------------------------

func TestNormalizeExtractsBuildContext(t *testing.T) {
	app := &models.ComposeApplication{
		Services: map[string]models.ComposeService{
			"web": {
				Build: &models.BuildConfig{Context: "app"},
				Ports: []models.PortConfig{{Target: 80, Published: "80"}},
			},
		},
	}
	result, err := Normalize(app, "prod")
	if err != nil {
		t.Fatalf("Normalize failed: %v", err)
	}
	if result.Services[0].BuildContext == nil || *result.Services[0].BuildContext != "app" {
		t.Errorf("BuildContext = %v, want \"app\"", result.Services[0].BuildContext)
	}
}

// --- helpers -----------------------------------------------------------

func strPtr(s string) *string { return &s }

func serviceNames(services []models.Service) []string {
	names := make([]string, len(services))
	for i, s := range services {
		names[i] = s.Name
	}
	return names
}
