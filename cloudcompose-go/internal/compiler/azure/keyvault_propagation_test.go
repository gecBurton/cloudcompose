package azure

import (
	"strings"
	"testing"

	"github.com/gecburton/cloudcompose/internal/models"
)

// Tests for docs/azure-todo.md's "Key Vault role-assignment RBAC
// propagation" item: azurerm_role_assignment.kv_role reporting created
// does not mean the grant has actually propagated on Azure's side
// (confirmed against a real francecentral run, 2026-08-10) --
// grantKeyVaultAccessOnce now also creates a time_sleep every
// azurerm_key_vault_secret depends on.

func TestGrantKeyVaultAccessOnce_CreatesPropagationSleep(t *testing.T) {
	t.Parallel()
	resources := models.NewAzureResources()
	granted := false
	grantKeyVaultAccessOnce(resources, &granted, principalIDRefForIdentity())

	sleep, ok := resources.TimeSleep["kv_role_propagation"]
	if !ok {
		t.Fatalf("expected a kv_role_propagation TimeSleep resource")
	}
	if sleep.CreateDuration != models.KeyVaultRoleAssignmentPropagationDelay {
		t.Errorf("CreateDuration = %q, want %q", sleep.CreateDuration, models.KeyVaultRoleAssignmentPropagationDelay)
	}
	if len(sleep.DependsOn) != 1 || sleep.DependsOn[0] != "azurerm_role_assignment.kv_role" {
		t.Errorf("DependsOn = %v, want [azurerm_role_assignment.kv_role]", sleep.DependsOn)
	}
}

// TestGrantKeyVaultAccessOnce_SleepCreatedExactlyOnce is the TimeSleep
// counterpart to TestGrantManagedServicePermissions_DoesNotDuplicateKeyVaultRoleAssignment:
// the sleep is keyed the same way the role assignment is (one per app,
// not one per credential), so a second call must not create a second,
// differently-keyed sleep resource that other secrets might miss
// depending on.
func TestGrantKeyVaultAccessOnce_SleepCreatedExactlyOnce(t *testing.T) {
	t.Parallel()
	resources := models.NewAzureResources()
	granted := false
	grantKeyVaultAccessOnce(resources, &granted, principalIDRefForIdentity())
	grantKeyVaultAccessOnce(resources, &granted, principalIDRefForIdentity())

	if len(resources.TimeSleep) != 1 {
		t.Errorf("expected exactly 1 TimeSleep resource after two calls, got %d", len(resources.TimeSleep))
	}
}

// TestNewKeyVaultSecret_DependsOnPropagationSleep checks the fix at its
// actual source: every KeyVaultSecret this codebase creates goes
// through this one constructor (confirmed by grep across
// internal/compiler/azure -- see the constructor's own doc comment), so
// fixing it here, rather than at each of the 4 call sites individually,
// is what makes the fix apply everywhere without relying on every
// future call site remembering to set it.
func TestNewKeyVaultSecret_DependsOnPropagationSleep(t *testing.T) {
	t.Parallel()
	secret := models.NewKeyVaultSecret()
	if len(secret.DependsOn) != 1 || secret.DependsOn[0] != "time_sleep.kv_role_propagation" {
		t.Errorf("DependsOn = %v, want [time_sleep.kv_role_propagation]", secret.DependsOn)
	}
}

// TestGenerateAzure_TimeProviderOnlyDeclaredWhenNeeded checks the same
// "don't declare a provider you have no resource of" convention already
// used for the docker provider (see GenerateAzure's own comment): an app
// with no managed-service credentials never creates a Key Vault at all,
// and shouldn't pull in the time provider either.
func TestGenerateAzure_TimeProviderOnlyDeclaredWhenNeeded(t *testing.T) {
	t.Parallel()
	env := mockAzureProdEnv()

	withoutSleep := models.NewAzureResources()
	out, err := GenerateAzure(withoutSleep, &env)
	if err != nil {
		t.Fatalf("GenerateAzure failed: %v", err)
	}
	if strings.Contains(out, `"time":`) {
		t.Errorf("expected no time provider declared when nothing needs it, got:\n%s", out)
	}

	withSleep := models.NewAzureResources()
	granted := false
	grantKeyVaultAccessOnce(withSleep, &granted, principalIDRefForIdentity())
	out, err = GenerateAzure(withSleep, &env)
	if err != nil {
		t.Fatalf("GenerateAzure failed: %v", err)
	}
	if !strings.Contains(out, `"hashicorp/time"`) {
		t.Errorf("expected the time provider to be declared when a TimeSleep resource exists, got:\n%s", out)
	}
}
