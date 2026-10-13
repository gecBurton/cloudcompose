package compiler

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/gecburton/cloudcompose/internal/compiler/aws"
	"github.com/gecburton/cloudcompose/internal/compiler/azure"
	"github.com/gecburton/cloudcompose/internal/models"
)

// sharedConfigCompose is #168: two services (Ory Hydra's public and admin
// halves) that must agree on one platform-supplied value, plus a variable
// only one of them names.
const sharedConfigCompose = `name: shared
x-cloud:
  azure:
    subnet_index: 0
services:
  hydra:
    image: oryd/hydra:v2.3.0
    environment:
      SECRETS_SYSTEM: ${HYDRA_SECRETS_SYSTEM}
  hydra-admin:
    image: oryd/hydra:v2.3.0
    environment:
      SECRETS_SYSTEM: ${HYDRA_SECRETS_SYSTEM}
      ADMIN_ONLY: ${ADMIN_ONLY}
`

func sharedConfigApp(t *testing.T) *models.Application {
	t.Helper()
	composeApp, err := ParseCompose(writeComposeFixture(t, sharedConfigCompose))
	if err != nil {
		t.Fatalf("ParseCompose failed: %v", err)
	}
	app, err := Normalize(composeApp, composeApp.Name)
	if err != nil {
		t.Fatalf("Normalize failed: %v", err)
	}
	return app
}

func TestSharedPlatformConfig_AWS(t *testing.T) {
	t.Parallel()
	env := models.NewAwsEnvironment()
	env.Name = "prod"
	resources, err := aws.InferAWS(sharedConfigApp(t), &env)
	if err != nil {
		t.Fatalf("InferAWS failed: %v", err)
	}
	if got := len(resources.SecretsmanagerSecret); got != 2 {
		t.Errorf("expected 2 secrets (one per variable, not per service), got %d: %v", got, keysOf(resources.SecretsmanagerSecret))
	}
	shared := "${aws_secretsmanager_secret.config_secrets_system.arn}"
	for _, service := range []string{"hydra", "hydra-admin"} {
		var containers []struct {
			Secrets []map[string]string `json:"secrets"`
		}
		if err := json.Unmarshal([]byte(resources.EcsTaskDefinition[service+"_td"].ContainerDefinitions), &containers); err != nil {
			t.Fatalf("%s container_definitions: %v", service, err)
		}
		found := false
		for _, s := range containers[0].Secrets {
			found = found || (s["name"] == "SECRETS_SYSTEM" && s["valueFrom"] == shared)
		}
		if !found {
			t.Errorf("%s: expected SECRETS_SYSTEM from the shared secret %s, got %v", service, shared, containers[0].Secrets)
		}
	}
	// Each service may only read the variables it names.
	if policy := resources.IamRolePolicy["hydra_config_policy"].Policy; strings.Contains(policy, "config_admin_only") {
		t.Errorf("hydra's policy grants ADMIN_ONLY, which it doesn't name: %s", policy)
	}
	if policy := resources.IamRolePolicy["hydra-admin_config_policy"].Policy; !strings.Contains(policy, "config_admin_only") {
		t.Errorf("hydra-admin's policy should grant ADMIN_ONLY: %s", policy)
	}
}

func TestSharedPlatformConfig_Azure(t *testing.T) {
	t.Parallel()
	env := models.NewAzureEnvironment()
	env.Name, env.ResourceGroupName, env.AppsCIDR = "prod", "prod", "10.0.128.0/17"
	env.VnetID = "/subscriptions/0/resourceGroups/prod/providers/Microsoft.Network/virtualNetworks/v"
	resources, err := azure.InferAzure(sharedConfigApp(t), &env)
	if err != nil {
		t.Fatalf("InferAzure failed: %v", err)
	}
	var configSecrets []string
	for key := range resources.KeyVaultSecret {
		if strings.HasPrefix(key, "config_") {
			configSecrets = append(configSecrets, key)
		}
	}
	if len(configSecrets) != 2 {
		t.Errorf("expected 2 Key Vault config secrets (one per variable, not per service), got %v", configSecrets)
	}
	shared := "${azurerm_key_vault_secret.config_secrets_system_secret.versionless_id}"
	for _, service := range []string{"hydra", "hydra-admin"} {
		found := false
		for _, s := range resources.ContainerApp[service].Secret {
			found = found || s.KeyVaultSecretID == shared
		}
		if !found {
			t.Errorf("%s: expected a Container App secret referencing %s, got %+v", service, shared, resources.ContainerApp[service].Secret)
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

func TestExplain_SharedPlatformConfig(t *testing.T) {
	t.Parallel()
	composeApp, err := ParseCompose(writeComposeFixture(t, sharedConfigCompose))
	if err != nil {
		t.Fatalf("ParseCompose failed: %v", err)
	}
	app, err := Normalize(composeApp, composeApp.Name)
	if err != nil {
		t.Fatalf("Normalize failed: %v", err)
	}
	got := map[string]bool{}
	for _, d := range Explain(composeApp, app) {
		got[d.Subject+": "+d.Decision] = true
	}
	for _, want := range []string{
		"hydra: SECRETS_SYSTEM shared with hydra-admin",
		"hydra-admin: SECRETS_SYSTEM shared with hydra",
	} {
		if !got[want] {
			t.Errorf("expected decision %q, got %v", want, got)
		}
	}
	if got["hydra-admin: ADMIN_ONLY shared with hydra"] {
		t.Error("ADMIN_ONLY is only named by hydra-admin and must not be reported as shared")
	}
}
