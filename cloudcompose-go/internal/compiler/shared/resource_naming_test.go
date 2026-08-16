package shared

import "testing"

// TestResourceNamer_CombinesEnvAppResource confirms the returned
// closure implements the env.Name-app.Name-resourceName convention
// every cloud's own InferAWS/InferAzure/InferGcp (and their FetchLogs/
// FetchStatus siblings) rely on to name generated cloud resources
// consistently -- see this function's own doc comment for why it
// exists as one shared implementation rather than 7 hand-copied
// closures.
func TestResourceNamer_CombinesEnvAppResource(t *testing.T) {
	t.Parallel()
	getName := ResourceNamer("prod", "web")

	cases := []struct{ resourceName, want string }{
		{"sg", "prod-web-sg"},
		{"cluster", "prod-web-cluster"},
		{"", "prod-web-"},
	}
	for _, tc := range cases {
		if got := getName(tc.resourceName); got != tc.want {
			t.Errorf("ResourceNamer(%q)(%q) = %q, want %q", "prod-web", tc.resourceName, got, tc.want)
		}
	}
}

// TestResourceNamer_ReturnsIndependentClosures confirms two different
// ResourceNamer calls don't share state -- each returned closure must
// only ever reflect the envName/appName it was constructed with.
func TestResourceNamer_ReturnsIndependentClosures(t *testing.T) {
	t.Parallel()
	first := ResourceNamer("dev", "api")
	second := ResourceNamer("prod", "web")

	if got := first("db"); got != "dev-api-db" {
		t.Errorf("first(%q) = %q, want %q", "db", got, "dev-api-db")
	}
	if got := second("db"); got != "prod-web-db" {
		t.Errorf("second(%q) = %q, want %q", "db", got, "prod-web-db")
	}
}
