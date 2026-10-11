package compiler

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/gecburton/cloudcompose/internal/compiler/aws"
	"github.com/gecburton/cloudcompose/internal/compiler/azure"
	"github.com/gecburton/cloudcompose/internal/compiler/gcp"
	"github.com/gecburton/cloudcompose/internal/models"
)

// publicURLCompose is the OIDC shape that motivated #149: an authorization
// server (hydra) and a login app (iam), each needing the other's public
// URL and its own, written the way they already are for local use.
const publicURLCompose = `name: oidc
x-cloud:
  azure:
    subnet_index: 0
services:
  hydra:
    image: oryd/hydra:v2.3.0
    ports: ["4444:4444"]
    environment:
      URLS_SELF_ISSUER: http://localhost:4444
      URLS_LOGIN: http://localhost:8000/o/login/
    x-cloud:
      ingress:
        path: /oauth2
  iam:
    image: nginx
    ports: ["8000:8000"]
    x-cloud:
      ingress: {}
`

func publicURLApp(t *testing.T) *models.Application {
	t.Helper()
	composeApp, err := ParseCompose(writeComposeFixture(t, publicURLCompose))
	if err != nil {
		t.Fatalf("ParseCompose failed: %v", err)
	}
	app, err := Normalize(composeApp, composeApp.Name)
	if err != nil {
		t.Fatalf("Normalize failed: %v", err)
	}
	return app
}

func TestPublicURL_AWS(t *testing.T) {
	t.Parallel()
	alb := "arn:aws:elasticloadbalancing:eu-west-2:0:loadbalancer/app/x/y"
	env := models.NewAwsEnvironment()
	env.Name = "prod"
	env.AlbArn = &alb
	resources, err := aws.InferAWS(publicURLApp(t), &env)
	if err != nil {
		t.Fatalf("InferAWS failed: %v", err)
	}
	var containers []struct {
		Environment []map[string]string `json:"environment"`
	}
	if err := json.Unmarshal([]byte(resources.EcsTaskDefinition["hydra_td"].ContainerDefinitions), &containers); err != nil {
		t.Fatalf("container_definitions: %v", err)
	}
	got := map[string]string{}
	for _, e := range containers[0].Environment {
		got[e["name"]] = e["value"]
	}
	alb_ := "http://${data.aws_lb.shared_alb.dns_name}"
	if got["URLS_SELF_ISSUER"] != alb_ {
		t.Errorf("URLS_SELF_ISSUER = %q, want %q", got["URLS_SELF_ISSUER"], alb_)
	}
	if got["URLS_LOGIN"] != alb_+"/o/login/" {
		t.Errorf("URLS_LOGIN = %q, want %q", got["URLS_LOGIN"], alb_+"/o/login/")
	}

	out, err := aws.GenerateAWS(resources, &env, "oidc")
	if err != nil {
		t.Fatalf("GenerateAWS failed: %v", err)
	}
	if !strings.Contains(out, `"shared_alb"`) {
		t.Error("expected the aws_lb data source the public URL references to be emitted")
	}
}

func TestPublicURL_Azure(t *testing.T) {
	t.Parallel()
	env := models.NewAzureEnvironment()
	env.Name = "prod"
	env.ResourceGroupName = "prod"
	env.AppsCIDR = "10.0.128.0/17"
	env.VnetID = "/subscriptions/0/resourceGroups/prod/providers/Microsoft.Network/virtualNetworks/v"
	env.VnetName = "v"
	resources, err := azure.InferAzure(publicURLApp(t), &env)
	if err != nil {
		t.Fatalf("InferAzure failed: %v", err)
	}
	got := map[string]string{}
	for _, e := range resources.ContainerApp["hydra"].Template.Container[0].Env {
		got[e.Name] = e.Value
	}
	// Built from the environment's default_domain, never from a Container
	// App's own ingress[0].fqdn: hydra referencing its own FQDN would be a
	// self-reference, and hydra/iam referencing each other a cycle.
	domain := "${azurerm_container_app_environment.main.default_domain}"
	for name, want := range map[string]string{
		"URLS_SELF_ISSUER": "https://prod-oidc-hydra." + domain,
		"URLS_LOGIN":       "https://prod-oidc-iam." + domain + "/o/login/",
	} {
		if got[name] != want {
			t.Errorf("%s = %q, want %q", name, got[name], want)
		}
		if strings.Contains(got[name], "azurerm_container_app.") {
			t.Errorf("%s = %q references a container app: dependency cycle", name, got[name])
		}
	}
}

func TestPublicURL_GCP(t *testing.T) {
	t.Parallel()
	env := models.NewGcpEnvironment()
	env.Name = "prod"
	env.ProjectID = "my-project"
	env.Region = "europe-west2"
	resources := gcp.InferGcp(publicURLApp(t), &env)
	got := map[string]string{}
	for _, e := range resources.CloudRunService["hydra"].Template.Spec.Containers[0].Env {
		got[e.Name] = e.Value
	}
	for name, want := range map[string]string{
		"URLS_SELF_ISSUER": "https://prod-oidc-hydra-${data.google_project.current.number}.europe-west2.run.app",
		"URLS_LOGIN":       "https://prod-oidc-iam-${data.google_project.current.number}.europe-west2.run.app/o/login/",
	} {
		if got[name] != want {
			t.Errorf("%s = %q, want %q", name, got[name], want)
		}
	}

	out, err := gcp.GenerateGcp(resources, &env, "oidc")
	if err != nil {
		t.Fatalf("GenerateGcp failed: %v", err)
	}
	if !strings.Contains(out, `"google_project"`) {
		t.Error("expected the google_project data source the public URL references to be emitted")
	}
}

func TestExplain_LocalhostDecisions(t *testing.T) {
	t.Parallel()
	composeApp, err := ParseCompose(writeComposeFixture(t, publicURLCompose+`  worker:
    image: busybox
    environment:
      ALLOWED_HOSTS: localhost,iam
      CALLBACK: http://localhost:9999/cb
`))
	if err != nil {
		t.Fatalf("ParseCompose failed: %v", err)
	}
	app, err := Normalize(composeApp, composeApp.Name)
	if err != nil {
		t.Fatalf("Normalize failed: %v", err)
	}
	got := map[string]DecisionSource{}
	for _, d := range Explain(composeApp, app) {
		got[d.Subject+": "+d.Decision] = d.Source
	}
	for decision, source := range map[string]DecisionSource{
		"hydra: URLS_LOGIN → public URL of iam":         SourceInferred,
		"hydra: URLS_SELF_ISSUER → public URL of hydra": SourceInferred,
		"worker: ALLOWED_HOSTS contains localhost":      SourceWarning,
		"worker: CALLBACK points at localhost":          SourceWarning,
	} {
		if got[decision] != source {
			t.Errorf("expected %s decision %q, got decisions %v", source, decision, got)
		}
	}
}
