package shared

import (
	"fmt"
	"regexp"
	"strings"
)

// backendKeyPrefix namespaces every state key this project's own
// backend: feature writes, the same way scripts/smoke-test.sh already
// namespaces its own CI-only backend keys under "acceptance/<NAME>/" --
// this is the equivalent prefix for real, non-CI use. See
// docs/multi-user-state.md.
const backendKeyPrefix = "cloudcompose"

// backendNameInvalidChars matches anything BackendKeyForEnvironment/
// BackendKeyForApp's own callers must reject before a name (environment
// name or project name) ever reaches key construction -- see
// ValidateBackendName's own doc comment for why. Deliberately stricter
// than TfName's own allowed set (environment_helpers.go): TfName exists
// to produce a *valid Terraform resource label* from an arbitrary
// string by substituting invalid characters, which is fine for that use
// (collisions there are cosmetic, not a state-safety problem); a
// backend key has no equivalent substitution step, so this instead
// rejects a name outright rather than mangling it into a different,
// possibly-colliding string.
var backendNameInvalidChars = regexp.MustCompile(`[^a-zA-Z0-9_-]`)

// ValidateBackendName rejects any name (an environment's own `name:`,
// or an app's own --project) containing "/" or any other character
// outside [a-zA-Z0-9_-], before that name is ever used to build a
// backend state key (BackendKeyForEnvironment/BackendKeyForApp).
//
// This matters because those two functions build keys by plain string
// concatenation with no escaping: without this check, a name containing
// "/" can make BackendKeyForEnvironment("prod/apps") produce the exact
// same string as BackendKeyForApp("prod", "environment") --
// "cloudcompose/prod/apps/environment.tfstate" -- silently colliding
// two completely unrelated environments'/apps' state. The "apps/"
// nesting BackendKeyForApp's own doc comment describes only rules out
// the *literal* project name "environment" colliding with an
// environment's reserved key; it does not defend against "/" appearing
// inside a name at all, which is what this function exists to close.
// Every caller that accepts an environment name or project name from a
// human (initconfig.Validate, cmd/cloudcompose's own
// resolveProjectName) must call this before that name reaches either
// key-building function.
func ValidateBackendName(kind, name string) error {
	if name == "" {
		return fmt.Errorf("%s must not be empty", kind)
	}
	if backendNameInvalidChars.MatchString(name) {
		return fmt.Errorf(
			"%s %q is invalid: only letters, digits, underscores, and hyphens are allowed (no \"/\" or other characters, which could otherwise collide with another environment's or app's own backend state key)",
			kind, name,
		)
	}
	return nil
}

// BackendKeyForEnvironment returns the Terraform backend state key an
// environment's own main.tf.json uses, derived mechanically from the
// environment's name -- never authored in environment.yaml, the same
// way env-<name>/'s own output directory name is never authored either
// (see docs/authored-environment-config.md). Two environments that
// happen to share a name must not silently share state, so this is a
// pure function of name, not configurable.
func BackendKeyForEnvironment(envName string) string {
	return fmt.Sprintf("%s/%s/environment.tfstate", backendKeyPrefix, envName)
}

// BackendKeyForApp returns the Terraform backend state key an app's own
// main.tf.json uses, mirroring ResourceNamer's env.Name-app.Name-...
// convention: an app's state lives under its environment's own prefix,
// distinguished from every other app sharing that environment by
// project name. Deliberately nested under its own "apps/" segment
// (rather than directly under the environment's prefix, alongside
// environment.tfstate) so a project literally named "environment"
// can never collide with the environment's own reserved state key --
// BackendKeyForEnvironment's "environment.tfstate" leaf name is not
// itself a valid project name's key shape, so no project name, however
// chosen, can produce the same key.
//
// This is also the key convention docs/multi-user-state.md's dependent-
// app check for environment teardown relies on: listing every object
// under BackendKeyForEnvironment's own prefix's "apps/" segment
// enumerates every app still depending on that environment.
func BackendKeyForApp(envName, projectName string) string {
	return fmt.Sprintf("%s/%s/apps/%s.tfstate", backendKeyPrefix, envName, projectName)
}

// BackendAppsPrefix returns the prefix every app compiled against
// envName's own backend key (BackendKeyForApp) is nested under --
// listing every object under this prefix (S3 ListObjectsV2/azurerm blob
// list/GCS list) enumerates every app still depending on this
// environment, which is exactly the check environment teardown's
// dependent-app safety check needs (see
// internal/compiler/{aws,azure,gcp}/backend_listing.go and
// docs/multi-user-state.md). Every BackendKeyForApp(envName, ...)
// result starts with this exact string, by construction.
func BackendAppsPrefix(envName string) string {
	return fmt.Sprintf("%s/%s/apps/", backendKeyPrefix, envName)
}

// ProjectNameFromAppKey recovers the project name encoded in an app's
// own backend key (the inverse of BackendKeyForApp), or ("", false) if
// key doesn't have envName's own apps/ prefix and .tfstate suffix --
// used by environment teardown's dependent-app listing to turn each
// listed object key back into a human-readable project name, without
// needing to open that app's state to find out what it's called (the
// name is already encoded in the key itself).
func ProjectNameFromAppKey(envName, key string) (string, bool) {
	prefix := BackendAppsPrefix(envName)
	if !strings.HasPrefix(key, prefix) || !strings.HasSuffix(key, ".tfstate") {
		return "", false
	}
	projectName := strings.TrimSuffix(strings.TrimPrefix(key, prefix), ".tfstate")
	if projectName == "" {
		return "", false
	}
	return projectName, true
}
