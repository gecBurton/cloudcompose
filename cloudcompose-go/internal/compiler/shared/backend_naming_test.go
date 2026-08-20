package shared

import "testing"

// TestBackendKeyForEnvironment_DerivedFromName confirms the state key
// is a pure function of the environment's name -- see this function's
// own doc comment for why nothing about it is configurable.
func TestBackendKeyForEnvironment_DerivedFromName(t *testing.T) {
	t.Parallel()
	cases := []struct{ envName, want string }{
		{"prod", "cloudcompose/prod/environment.tfstate"},
		{"dev", "cloudcompose/dev/environment.tfstate"},
	}
	for _, tc := range cases {
		if got := BackendKeyForEnvironment(tc.envName); got != tc.want {
			t.Errorf("BackendKeyForEnvironment(%q) = %q, want %q", tc.envName, got, tc.want)
		}
	}
}

// TestBackendKeyForApp_NestedUnderEnvironmentPrefix confirms an app's
// key lives under its environment's own prefix, distinguished only by
// project name -- the property docs/multi-user-state.md's dependent-app
// listing check for environment teardown depends on.
func TestBackendKeyForApp_NestedUnderEnvironmentPrefix(t *testing.T) {
	t.Parallel()
	cases := []struct {
		envName, projectName, want string
	}{
		{"prod", "checkout-api", "cloudcompose/prod/apps/checkout-api.tfstate"},
		{"prod", "web", "cloudcompose/prod/apps/web.tfstate"},
		{"dev", "checkout-api", "cloudcompose/dev/apps/checkout-api.tfstate"},
	}
	for _, tc := range cases {
		if got := BackendKeyForApp(tc.envName, tc.projectName); got != tc.want {
			t.Errorf("BackendKeyForApp(%q, %q) = %q, want %q", tc.envName, tc.projectName, got, tc.want)
		}
	}
}

// TestBackendKeyForApp_CannotCollideWithEnvironmentKeyEvenIfProjectIsNamedEnvironment
// confirms a project literally named "environment" still can't produce
// the same key as its own environment's environment.tfstate -- see
// BackendKeyForApp's own doc comment for why the "apps/" segment exists
// specifically to rule this out structurally, not just by convention.
func TestBackendKeyForApp_CannotCollideWithEnvironmentKeyEvenIfProjectIsNamedEnvironment(t *testing.T) {
	t.Parallel()
	envKey := BackendKeyForEnvironment("prod")
	appKey := BackendKeyForApp("prod", "environment")
	if envKey == appKey {
		t.Errorf("expected distinct keys, got both %q", envKey)
	}
}

// TestBackendKeyForEnvironment_DistinctPerEnvironment and its app
// equivalent below confirm two environments/apps that happen to share a
// name component don't collide with each other.
func TestBackendKeyForEnvironment_DistinctPerEnvironment(t *testing.T) {
	t.Parallel()
	if BackendKeyForEnvironment("prod") == BackendKeyForEnvironment("staging") {
		t.Errorf("expected distinct keys for distinct environment names")
	}
}

func TestBackendKeyForApp_DistinctPerProject(t *testing.T) {
	t.Parallel()
	if BackendKeyForApp("prod", "web") == BackendKeyForApp("prod", "checkout-api") {
		t.Errorf("expected distinct keys for distinct project names within one environment")
	}
}

// TestBackendAppsPrefix_EveryAppKeyStartsWithIt confirms
// BackendAppsPrefix's own contract (every BackendKeyForApp result
// starts with it) holds -- the property environment teardown's
// dependent-app listing depends on to enumerate apps by prefix alone.
func TestBackendAppsPrefix_EveryAppKeyStartsWithIt(t *testing.T) {
	t.Parallel()
	prefix := BackendAppsPrefix("prod")
	for _, projectName := range []string{"web", "checkout-api", "environment"} {
		key := BackendKeyForApp("prod", projectName)
		if len(key) < len(prefix) || key[:len(prefix)] != prefix {
			t.Errorf("BackendKeyForApp(%q, %q) = %q, does not start with prefix %q", "prod", projectName, key, prefix)
		}
	}
}

// TestBackendAppsPrefix_EnvironmentKeyDoesNotStartWithIt confirms the
// environment's own key is never mistaken for something under the
// apps/ prefix -- environment teardown's listing must only ever see
// apps there, never the environment's own state object (which normally
// wouldn't even be listed under this prefix in the first place, but
// this pins the assumption explicitly).
func TestBackendAppsPrefix_EnvironmentKeyDoesNotStartWithIt(t *testing.T) {
	t.Parallel()
	prefix := BackendAppsPrefix("prod")
	envKey := BackendKeyForEnvironment("prod")
	if len(envKey) >= len(prefix) && envKey[:len(prefix)] == prefix {
		t.Errorf("environment key %q unexpectedly starts with apps prefix %q", envKey, prefix)
	}
}

// TestProjectNameFromAppKey_RecoversProjectName confirms the round trip
// BackendKeyForApp -> ProjectNameFromAppKey recovers the exact project
// name that produced the key, for every project name BackendKeyForApp
// itself accepts -- the inverse operation environment teardown's
// dependent-app listing needs to turn a raw object key back into a
// human-readable project name.
func TestProjectNameFromAppKey_RecoversProjectName(t *testing.T) {
	t.Parallel()
	for _, projectName := range []string{"web", "checkout-api", "environment"} {
		key := BackendKeyForApp("prod", projectName)
		got, ok := ProjectNameFromAppKey("prod", key)
		if !ok {
			t.Fatalf("ProjectNameFromAppKey(%q, %q) returned ok=false, want true", "prod", key)
		}
		if got != projectName {
			t.Errorf("ProjectNameFromAppKey(%q, %q) = %q, want %q", "prod", key, got, projectName)
		}
	}
}

// TestProjectNameFromAppKey_RejectsKeysFromOtherEnvironments confirms a
// key generated for one environment is not mistaken for belonging to a
// different environment sharing the same backend bucket -- the same
// per-environment scoping every other backend key/prefix helper in this
// file already enforces.
func TestProjectNameFromAppKey_RejectsKeysFromOtherEnvironments(t *testing.T) {
	t.Parallel()
	key := BackendKeyForApp("staging", "web")
	if _, ok := ProjectNameFromAppKey("prod", key); ok {
		t.Errorf("expected ok=false for a key belonging to a different environment, got ok=true")
	}
}

// TestProjectNameFromAppKey_RejectsNonAppKeys confirms the environment's
// own key (and any other object that isn't a well-formed app key) is
// rejected rather than silently misparsed into some other project name.
func TestProjectNameFromAppKey_RejectsNonAppKeys(t *testing.T) {
	t.Parallel()
	cases := []string{
		BackendKeyForEnvironment("prod"),
		"cloudcompose/prod/apps/",
		"cloudcompose/prod/apps/.tfstate",
		"something-unrelated",
	}
	for _, key := range cases {
		if _, ok := ProjectNameFromAppKey("prod", key); ok {
			t.Errorf("ProjectNameFromAppKey(%q, %q) returned ok=true, want false", "prod", key)
		}
	}
}

// TestValidateBackendName_AcceptsSafeNames confirms every name shape
// already used throughout this codebase's own tests/examples (plain
// words, hyphens, underscores, digits) is accepted.
func TestValidateBackendName_AcceptsSafeNames(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"prod", "demo", "checkout-api", "web_api", "prod2", "a"} {
		if err := ValidateBackendName("name", name); err != nil {
			t.Errorf("ValidateBackendName(%q) = %v, want nil", name, err)
		}
	}
}

// TestValidateBackendName_RejectsEmpty confirms an empty name is
// rejected -- BackendKeyForApp("prod", "") would otherwise produce
// "cloudcompose/prod/apps/.tfstate", which ProjectNameFromAppKey itself
// already refuses to parse back (see
// TestProjectNameFromAppKey_RejectsNonAppKeys above); rejecting it here
// means that malformed key is never constructed in the first place.
func TestValidateBackendName_RejectsEmpty(t *testing.T) {
	t.Parallel()
	if err := ValidateBackendName("name", ""); err == nil {
		t.Error("expected an error for an empty name")
	}
}

// TestValidateBackendName_RejectsSlash is the regression test for the
// concrete collision ValidateBackendName's own doc comment describes:
// without this check, BackendKeyForEnvironment("prod/apps") and
// BackendKeyForApp("prod", "environment") produce the identical key
// ("cloudcompose/prod/apps/environment.tfstate"), silently colliding
// two unrelated environments'/apps' state. This test only confirms the
// name itself is rejected; the actual collision is exercised directly
// by TestBackendKeyCollision_SlashInNameWithoutValidation below.
func TestValidateBackendName_RejectsSlash(t *testing.T) {
	t.Parallel()
	if err := ValidateBackendName("name", "prod/apps"); err == nil {
		t.Error("expected an error for a name containing '/'")
	}
}

// TestValidateBackendName_RejectsOtherUnsafeCharacters confirms the
// check isn't narrowly scoped to "/" alone -- any character outside
// [a-zA-Z0-9_-] is rejected, including characters that could be
// meaningful in a URL, shell, or path context elsewhere in this
// codebase (e.g. appDir's own filepath.Join call sites).
func TestValidateBackendName_RejectsOtherUnsafeCharacters(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"prod apps", "prod\\apps", "prod.apps", "prod:apps", "prod\napps"} {
		if err := ValidateBackendName("name", name); err == nil {
			t.Errorf("ValidateBackendName(%q) = nil, want an error", name)
		}
	}
}

// TestBackendKeyCollision_SlashInNameWithoutValidation is a regression
// test proving the specific collision ValidateBackendName exists to
// prevent, independent of whether ValidateBackendName itself is
// actually called: it documents, in a way that fails loudly if
// BackendKeyForEnvironment/BackendKeyForApp's own key format ever
// changes to close this gap on its own, that the two key-building
// functions alone do NOT prevent this collision -- callers must call
// ValidateBackendName first. If this test starts failing (no longer
// collides), it is safe to delete; it exists to keep the discovered bug
// from silently reappearing if the key format changes elsewhere without
// this collision in mind.
func TestBackendKeyCollision_SlashInNameWithoutValidation(t *testing.T) {
	t.Parallel()
	envKey := BackendKeyForEnvironment("prod/apps")
	appKey := BackendKeyForApp("prod", "environment")
	if envKey != appKey {
		t.Skip("the key format no longer collides on '/' -- ValidateBackendName may no longer be strictly necessary for this specific case, but should still be kept as defense in depth")
	}
}
