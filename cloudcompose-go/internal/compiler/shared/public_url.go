package shared

import (
	"regexp"
	"strconv"

	"github.com/gecburton/cloudcompose/internal/models"
)

// localhostURL matches a URL addressed to the developer's own machine:
// how a compose file names a service's published port, e.g. an OAuth
// redirect URI or issuer that a browser follows.
var localhostURL = regexp.MustCompile(`^(?P<scheme>https?)://(?:localhost|127\.0\.0\.1)(?::(?P<port>\d+))?(?P<rest>[/?#].*)?$`)

// LocalhostTarget reports whether value is a localhost URL, and if so,
// which public (ingress) service publishes the port it names. matched is
// true with a nil service when the URL names a port no public service
// publishes -- it would reach the container itself once deployed.
func LocalhostTarget(value string, app *models.Application) (service *models.Service, rest string, matched bool) {
	m := localhostURL.FindStringSubmatch(value)
	if m == nil {
		return nil, "", false
	}
	port := 80
	if m[1] == "https" {
		port = 443
	}
	if m[2] != "" {
		port, _ = strconv.Atoi(m[2])
	}
	for i := range app.Services {
		s := &app.Services[i]
		if s.Ingress != nil && s.Ingress.PublishedPort != nil && *s.Ingress.PublishedPort == port {
			return s, m[3], true
		}
	}
	return nil, "", true
}

// ResolveLocalhostURL rewrites a localhost URL to the public URL of the
// service that publishes its port (#149), keeping the path/query. The
// authored scheme is dropped: locally it's whatever the dev server spoke,
// deployed it's whatever the platform serves. publicURL returns "" when
// the cloud can't express a public URL for that service, in which case
// the value is left unchanged (and --explain warns).
func ResolveLocalhostURL(value string, app *models.Application, publicURL func(*models.Service) string) (string, bool) {
	service, rest, _ := LocalhostTarget(value, app)
	if service == nil {
		return value, false
	}
	base := publicURL(service)
	if base == "" {
		return value, false
	}
	return base + rest, true
}
