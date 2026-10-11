package shared

import (
	"testing"

	"github.com/gecburton/cloudcompose/internal/models"
)

func TestResolveLocalhostURL(t *testing.T) {
	t.Parallel()
	p8000, p80 := 8000, 80
	app := &models.Application{Services: []models.Service{
		{Name: "iam", Ingress: &models.Ingress{Path: "/", PublishedPort: &p8000}},
		{Name: "web", Ingress: &models.Ingress{Path: "/", PublishedPort: &p80}},
		{Name: "worker"},
	}}
	publicURL := func(s *models.Service) string { return "https://" + s.Name + ".example" }

	for value, want := range map[string]string{
		"http://localhost:8000/o/login/":  "https://iam.example/o/login/",
		"http://127.0.0.1:8000?next=/x":   "https://iam.example?next=/x",
		"http://localhost:8000":           "https://iam.example",
		"http://localhost/":               "https://web.example/",   // default port 80
		"http://localhost:9999/":          "http://localhost:9999/", // no public service publishes 9999
		"http://localhost.evil.com:8000/": "http://localhost.evil.com:8000/",
		"localhost,iam":                   "localhost,iam",
	} {
		if got, _ := ResolveLocalhostURL(value, app, publicURL); got != want {
			t.Errorf("ResolveLocalhostURL(%q) = %q, want %q", value, got, want)
		}
	}
}
