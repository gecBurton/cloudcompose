package shared

import (
	"fmt"
	"regexp"
	"strings"
)

// backendKeyPrefix namespaces every state key this project's backend feature writes.
const backendKeyPrefix = "cloudcompose"

// backendNameInvalidChars matches any character not allowed in a backend name.
var backendNameInvalidChars = regexp.MustCompile(`[^a-zA-Z0-9_-]`)

// ValidateBackendName rejects any name containing "/" or any other character
// outside [a-zA-Z0-9_-], before it is used to build a backend state key
// (BackendKeyForEnvironment/BackendKeyForApp). Those keys are built by plain
// string concatenation with no escaping, so an unvalidated "/" could make two
// unrelated environments' or apps' keys collide.
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
// environment's main.tf.json uses, derived from the environment's name.
func BackendKeyForEnvironment(envName string) string {
	return fmt.Sprintf("%s/%s/environment.tfstate", backendKeyPrefix, envName)
}

// BackendKeyForApp returns the Terraform backend state key an app's
// main.tf.json uses: nested under the environment's prefix and its own
// "apps/" segment, so a project literally named "environment" can never
// collide with BackendKeyForEnvironment's reserved key.
func BackendKeyForApp(envName, projectName string) string {
	return fmt.Sprintf("%s/%s/apps/%s.tfstate", backendKeyPrefix, envName, projectName)
}

// BackendAppsPrefix returns the prefix every app's backend key
// (BackendKeyForApp) is nested under for envName. Listing objects under
// this prefix enumerates every app still depending on the environment.
func BackendAppsPrefix(envName string) string {
	return fmt.Sprintf("%s/%s/apps/", backendKeyPrefix, envName)
}

// ProjectNameFromAppKey recovers the project name encoded in an app's
// backend key (the inverse of BackendKeyForApp), or ("", false) if key
// doesn't have envName's apps/ prefix and .tfstate suffix.
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
