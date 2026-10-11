package compiler

import (
	"strings"
	"testing"

	"github.com/gecburton/cloudcompose/internal/compiler/aws"
	"github.com/gecburton/cloudcompose/internal/compiler/azure"
	"github.com/gecburton/cloudcompose/internal/compiler/gcp"
	"github.com/gecburton/cloudcompose/internal/models"
)

// TestTwoPublicServicesAtRoot is #161: two public services at / collide
// only on AWS, where they share one ALB listener routed by path. Azure
// and GCP give each its own hostname.
func TestTwoPublicServicesAtRoot(t *testing.T) {
	t.Parallel()
	composeApp, err := ParseCompose(writeComposeFixture(t, `name: two
x-cloud:
  azure:
    subnet_index: 0
services:
  hydra:
    image: oryd/hydra
    ports: ["4444:4444"]
    x-cloud:
      ingress: {}
  iam:
    image: nginx
    ports: ["8000:8000"]
    x-cloud:
      ingress: {}
`))
	if err != nil {
		t.Fatalf("ParseCompose failed: %v", err)
	}
	app, err := Normalize(composeApp, composeApp.Name)
	if err != nil {
		t.Fatalf("Normalize failed: %v", err)
	}

	found := false
	for _, d := range Explain(composeApp, app) {
		found = found || (d.Source == SourceWarning && d.Decision == "ingress paths overlap")
	}
	if !found {
		t.Error("expected --explain to warn that ingress paths overlap (an error on AWS)")
	}

	awsEnv := models.NewAwsEnvironment()
	if _, err := aws.InferAWS(app, &awsEnv); err == nil || !strings.Contains(err.Error(), "both serve") {
		t.Errorf("InferAWS error = %v, want a 'both serve' rejection", err)
	}

	azureEnv := models.NewAzureEnvironment()
	azureEnv.Name, azureEnv.ResourceGroupName, azureEnv.AppsCIDR = "prod", "prod", "10.0.128.0/17"
	azureEnv.VnetID = "/subscriptions/0/resourceGroups/prod/providers/Microsoft.Network/virtualNetworks/v"
	resources, err := azure.InferAzure(app, &azureEnv)
	if err != nil {
		t.Fatalf("InferAzure failed: %v", err)
	}
	for _, name := range []string{"hydra", "iam"} {
		if ingress := resources.ContainerApp[name].Ingress; ingress == nil || !ingress.ExternalEnabled {
			t.Errorf("azure %s ingress = %+v, want external", name, ingress)
		}
	}

	gcpEnv := models.NewGcpEnvironment()
	gcpResources := gcp.InferGcp(app, &gcpEnv)
	for _, name := range []string{"hydra", "iam"} {
		if got := gcpResources.CloudRunService[name].Ingress; got != "all" {
			t.Errorf("gcp %s ingress = %q, want public (all)", name, got)
		}
	}
}
