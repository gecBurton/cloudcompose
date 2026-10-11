package aws

import (
	"fmt"
	"strings"

	"github.com/gecburton/cloudcompose/internal/compiler/shared"
	"github.com/gecburton/cloudcompose/internal/models"
)

// PublicURL is the origin a browser reaches a public service at (#149):
// its CloudFront domain when cdn: true, otherwise the shared ALB's DNS
// name. Both are resolved by Terraform from resources that exist before
// any task definition, so no cycle. "" when the environment has no ALB.
//
// The ingress path is deliberately not appended: the ALB routes on it but
// doesn't strip it, so a localhost URL's own path already is the path the
// service sees (http://localhost:4444/oauth2/auth -> <alb>/oauth2/auth).
func PublicURL(service *models.Service, env *models.AwsEnvironment) string {
	if service.CDNEnabled {
		return fmt.Sprintf("https://${aws_cloudfront_distribution.%s_cdn.domain_name}", service.Name)
	}
	if env.AlbArn == nil {
		return ""
	}
	if env.AlbHTTPS {
		return "https://" + albDNSReference
	}
	return "http://" + albDNSReference
}

// albDNSReference is how PublicURL refers to the shared ALB, so the
// generator can tell whether that data source is needed.
var albDNSReference = fmt.Sprintf("${data.aws_lb.%s.dns_name}", shared.ALBDataSourceKey)

func referencesALB(resources *models.AWSResources) bool {
	for _, td := range resources.EcsTaskDefinition {
		if strings.Contains(td.ContainerDefinitions, albDNSReference) {
			return true
		}
	}
	return false
}

// DefaultPort is the port a client reaches a service on, for firewall-style
// rules.
func DefaultPort(connection *models.Connection, fallback int) int {
	if connection == nil || connection.Port == nil {
		return fallback
	}
	return *connection.Port
}
