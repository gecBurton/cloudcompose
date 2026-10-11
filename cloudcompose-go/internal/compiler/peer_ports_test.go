package compiler

import (
	"encoding/json"
	"testing"

	"github.com/gecburton/cloudcompose/internal/compiler/aws"
	"github.com/gecburton/cloudcompose/internal/compiler/azure"
	"github.com/gecburton/cloudcompose/internal/models"
)

// peerPortsCompose is the shape that exposed #156/#157: one container
// (Ory Hydra) serving two APIs on two ports, called by another container.
const peerPortsCompose = `name: mp
x-cloud:
  azure:
    subnet_index: 0
services:
  hydra:
    image: oryd/hydra:v2.3.0
    ports: ["4444:4444", "4445:4445"]
  iam:
    image: nginx
    ports: ["8000:8000"]
    environment:
      HYDRA_ADMIN_URL: http://hydra:4445
      HYDRA_PUBLIC_URL: http://hydra
    x-cloud:
      ingress: {}
`

func peerPortsApp(t *testing.T) *models.Application {
	t.Helper()
	composeApp, err := ParseCompose(writeComposeFixture(t, peerPortsCompose))
	if err != nil {
		t.Fatalf("ParseCompose failed: %v", err)
	}
	app, err := Normalize(composeApp, composeApp.Name)
	if err != nil {
		t.Fatalf("Normalize failed: %v", err)
	}
	return app
}

func TestPeerContainer_AWSKeepsAuthoredPort(t *testing.T) {
	t.Parallel()
	env := models.NewAwsEnvironment()
	env.Name = "prod"
	resources, err := aws.InferAWS(peerPortsApp(t), &env)
	if err != nil {
		t.Fatalf("InferAWS failed: %v", err)
	}
	var containers []struct {
		Environment []map[string]string `json:"environment"`
	}
	if err := json.Unmarshal([]byte(resources.EcsTaskDefinition["iam_td"].ContainerDefinitions), &containers); err != nil {
		t.Fatalf("container_definitions: %v", err)
	}
	got := map[string]string{}
	for _, e := range containers[0].Environment {
		got[e["name"]] = e["value"]
	}
	if want := "http://hydra.prod-mp.internal:4445"; got["HYDRA_ADMIN_URL"] != want {
		t.Errorf("HYDRA_ADMIN_URL = %q, want %q", got["HYDRA_ADMIN_URL"], want)
	}
	if want := "http://hydra.prod-mp.internal:4444"; got["HYDRA_PUBLIC_URL"] != want {
		t.Errorf("HYDRA_PUBLIC_URL = %q, want %q", got["HYDRA_PUBLIC_URL"], want)
	}
}

func TestPeerContainer_AzureAddressesByAppNameWithInternalIngress(t *testing.T) {
	t.Parallel()
	env := models.NewAzureEnvironment()
	env.Name = "prod"
	env.ResourceGroupName = "prod"
	env.AppsCIDR = "10.0.128.0/17"
	env.VnetID = "/subscriptions/0/resourceGroups/prod/providers/Microsoft.Network/virtualNetworks/v"
	env.VnetName = "v"
	resources, err := azure.InferAzure(peerPortsApp(t), &env)
	if err != nil {
		t.Fatalf("InferAzure failed: %v", err)
	}

	got := map[string]string{}
	for _, e := range resources.ContainerApp["iam"].Template.Container[0].Env {
		got[e.Name] = e.Value
	}
	// Port dropped: the environment proxy serves HTTP ingress only on
	// 80/443 and forwards to target_port itself.
	for _, name := range []string{"HYDRA_ADMIN_URL", "HYDRA_PUBLIC_URL"} {
		if got[name] != "http://prod-mp-hydra" {
			t.Errorf("%s = %q, want %q", name, got[name], "http://prod-mp-hydra")
		}
	}

	hydra := resources.ContainerApp["hydra"].Ingress
	if hydra == nil || hydra.ExternalEnabled || hydra.TargetPort != 4444 {
		t.Errorf("hydra ingress = %+v, want internal-only on 4444", hydra)
	}
	if iam := resources.ContainerApp["iam"].Ingress; iam == nil || !iam.ExternalEnabled {
		t.Errorf("iam ingress = %+v, want external (x-cloud: ingress)", iam)
	}
}
