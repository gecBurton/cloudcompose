package azure

import (
	"regexp"
	"testing"
)

const azureLongAppName = "nginx-flask-mysql-with-a-very-long-application-name"

var (
	alphanumericOnly  = regexp.MustCompile(`^[a-zA-Z0-9]+$`)
	lowerAlphanumeric = regexp.MustCompile(`^[a-z0-9]+$`)
	keyVaultShape     = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9-]*[a-zA-Z0-9]$`)
)

func TestContainerRegistryName_ObeysAzureRules(t *testing.T) {
	t.Parallel()
	for _, app := range []string{"flask", azureLongAppName, "a"} {
		name := ContainerRegistryName("prod", app)
		if !alphanumericOnly.MatchString(name) {
			t.Errorf("ContainerRegistryName(prod, %q) = %q, want alphanumeric only", app, name)
		}
		if len(name) < 5 || len(name) > 50 {
			t.Errorf("ContainerRegistryName(prod, %q) = %q, want length in [5,50], got %d", app, name, len(name))
		}
	}
}

func TestContainerRegistryName_KeepsShortNamesReadable(t *testing.T) {
	t.Parallel()
	if got := ContainerRegistryName("prod", "flask"); got != "prodflaskacr" {
		t.Errorf("got %q, want prodflaskacr", got)
	}
}

func TestStorageAccountName_ObeysAzureRules(t *testing.T) {
	t.Parallel()
	for _, app := range []string{"flask-s3", azureLongAppName, "a"} {
		name := StorageAccountName("prod", app, "blobs")
		if !lowerAlphanumeric.MatchString(name) {
			t.Errorf("StorageAccountName(prod, %q, blobs) = %q, want lowercase alphanumeric only", app, name)
		}
		if len(name) < 3 || len(name) > 24 {
			t.Errorf("StorageAccountName(prod, %q, blobs) = %q, want length in [3,24], got %d", app, name, len(name))
		}
	}
}

func TestStorageAccountName_KeepsShortNamesReadable(t *testing.T) {
	t.Parallel()
	if got := StorageAccountName("prod", "flask", "blobs"); got != "prodflaskblobs" {
		t.Errorf("got %q, want prodflaskblobs", got)
	}
}

func TestKeyVaultName_ObeysAzureRules(t *testing.T) {
	t.Parallel()
	for _, app := range []string{"hello", "nginx-flask-mysql", azureLongAppName, "a"} {
		name := KeyVaultName("prod", app)
		if !keyVaultShape.MatchString(name) {
			t.Errorf("KeyVaultName(prod, %q) = %q, want key-vault shape", app, name)
		}
		if len(name) < 3 || len(name) > 24 {
			t.Errorf("KeyVaultName(prod, %q) = %q, want length in [3,24], got %d", app, name, len(name))
		}
	}
}

func TestKeyVaultName_KeepsShortNamesReadable(t *testing.T) {
	t.Parallel()
	if got := KeyVaultName("prod", "hello"); got != "prod-hello-kv" {
		t.Errorf("got %q, want prod-hello-kv", got)
	}
}

func TestKeyVaultName_StartsWithLetterEvenWhenEnvironmentDoesNot(t *testing.T) {
	t.Parallel()
	name := KeyVaultName("123", "app")
	if len(name) == 0 || !isAlpha(name[0]) {
		t.Errorf("KeyVaultName(123, app) = %q, want it to start with a letter", name)
	}
}

func TestKeyVaultName_ApplicationsSharingTruncatedPrefixStayDistinct(t *testing.T) {
	t.Parallel()
	a := KeyVaultName("prod", "nginx-flask-mysql-service")
	b := KeyVaultName("prod", "nginx-flask-mysql-serviceX")
	if a == b {
		t.Errorf("expected distinct names, both got %q", a)
	}
}

func TestStorageAccountName_SharingTruncatedPrefixStayDistinct(t *testing.T) {
	t.Parallel()
	a := StorageAccountName("prod", "a-very-long-application-name", "blobs")
	b := StorageAccountName("prod", "a-very-long-application-names", "blobs")
	if a == b {
		t.Errorf("expected distinct names, both got %q", a)
	}
}

func TestKeyVaultName_StableAcrossCalls(t *testing.T) {
	t.Parallel()
	if KeyVaultName("prod", azureLongAppName) != KeyVaultName("prod", azureLongAppName) {
		t.Error("expected KeyVaultName to be deterministic")
	}
}

// TestAzureNaming pins every naming function's output against known-good
// values, not just this implementation's own idea of what the
// transliteration should produce -- the AWS port's own review discipline
// applied here too.
func TestAzureNaming(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		got  string
		want string
	}{
		{"container_registry_name(prod, flask)", ContainerRegistryName("prod", "flask"), "prodflaskacr"},
		{"storage_account_name(prod, flask, blobs)", StorageAccountName("prod", "flask", "blobs"), "prodflaskblobs"},
		{"key_vault_name(prod, hello)", KeyVaultName("prod", "hello"), "prod-hello-kv"},
		{"key_vault_name(123, app)", KeyVaultName("123", "app"), "kv-123-app-kv"},
		{
			"container_registry_name(long, long)",
			ContainerRegistryName("this-is-a-very-long-environment-name-indeed", "this-is-a-very-long-application-name-too"),
			"thisisaverylongenvironmentnameindeedthisisav66d9f3",
		},
		{
			"storage_account_name(long, long, blobs)",
			StorageAccountName("this-is-a-very-long-environment-name-indeed", "this-is-a-very-long-application-name-too", "blobs"),
			"thisisaverylongenv614a24",
		},
		{
			"key_vault_name(long, long)",
			KeyVaultName("this-is-a-very-long-environment-name-indeed", "this-is-a-very-long-application-name-too"),
			"this-is-a-very-lo-ee9b9a",
		},
		{"frontdoor_profile_name(prod, hello)", FrontDoorProfileName("prod", "hello"), "prod-hello-fd"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Errorf("got %q, want %q", tc.got, tc.want)
			}
		})
	}
}
