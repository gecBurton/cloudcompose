package azure

import (
	"fmt"

	"github.com/gecburton/cloudcompose/internal/models"
)

// inferCdnAzure puts Azure Front Door (Standard/Premium) in front of every
// service with both cdn:true and an ingress.
//
// One profile per application, shared by every CDN-enabled service in it
// -- Front Door profiles are a real unit of billing and management, and
// nothing about this codebase's other per-app resources (Key Vault,
// Container Registry) suggests one per service. Everything below the
// profile (endpoint, origin group, origin, route) is one set per service,
// since each service's Container App has its own ingress FQDN to front.
func inferCdnAzure(
	resources *models.AzureResources,
	app *models.Application,
	env *models.AzureEnvironment,
	getName func(string) string,
	tags map[string]string,
) {
	var cdnServices []*models.Service
	for i := range app.Services {
		s := &app.Services[i]
		if s.CDNEnabled && s.Ingress != nil {
			cdnServices = append(cdnServices, s)
		}
	}
	if len(cdnServices) == 0 {
		return
	}

	profileKey := "main"
	profile := models.NewFrontDoorProfile()
	profile.Name = FrontDoorProfileName(env.Name, app.Name)
	profile.ResourceGroupName = env.Name
	profile.Tags = tags
	resources.CdnFrontdoorProfile[profileKey] = profile
	profileID := fmt.Sprintf("${azurerm_cdn_frontdoor_profile.%s.id}", profileKey)

	for _, service := range cdnServices {
		key := service.Name

		resources.CdnFrontdoorEndpoint[key] = models.FrontDoorEndpoint{
			Name:                  getName(service.Name + "-fd"),
			CdnFrontdoorProfileID: profileID,
			Tags:                  tags,
		}
		endpointID := fmt.Sprintf("${azurerm_cdn_frontdoor_endpoint.%s.id}", key)

		resources.CdnFrontdoorOriginGroup[key] = models.FrontDoorOriginGroup{
			Name:                  service.Name,
			CdnFrontdoorProfileID: profileID,
			LoadBalancing:         map[string]any{},
			HealthProbe:           frontDoorHealthProbeFor(service),
		}
		originGroupID := fmt.Sprintf("${azurerm_cdn_frontdoor_origin_group.%s.id}", key)

		// The Container App's ingress FQDN, exactly as output.fqdn already
		// references it in generator_azure.go. Both HostName and
		// OriginHostHeader need it: Container Apps, like most managed
		// backends, requires the Host header on the forwarded request to
		// match the origin it is actually listening for.
		fqdn := fmt.Sprintf("${azurerm_container_app.%s.ingress[0].fqdn}", service.Name)
		origin := models.NewFrontDoorOrigin()
		origin.Name = service.Name
		origin.CdnFrontdoorOriginGroupID = originGroupID
		origin.HostName = fqdn
		origin.OriginHostHeader = &fqdn
		resources.CdnFrontdoorOrigin[key] = origin
		originID := fmt.Sprintf("${azurerm_cdn_frontdoor_origin.%s.id}", key)

		route := models.NewFrontDoorRoute()
		route.Name = "default"
		route.CdnFrontdoorEndpointID = endpointID
		route.CdnFrontdoorOriginGroupID = originGroupID
		route.CdnFrontdoorOriginIds = []string{originID}
		resources.CdnFrontdoorRoute[key] = route

		// One Firewall Policy + Security Policy per CDN-enabled service,
		// matching aws/edge.go's own granularity (one WAF Web ACL per
		// service, wafKey := service.Name + "_waf") rather than one
		// shared policy per profile -- a per-service rate-limit threshold
		// is the right default even before per-service overrides exist,
		// since two services behind the same profile can have wildly
		// different legitimate traffic volumes.
		waf := models.NewFrontDoorFirewallPolicy()
		waf.Name = FrontDoorFirewallPolicyName(env.Name, app.Name, service.Name)
		waf.ResourceGroupName = env.Name
		waf.SkuName = profile.SkuName
		waf.Tags = tags
		resources.CdnFrontdoorFirewallPolicy[key] = waf
		firewallPolicyID := fmt.Sprintf("${azurerm_cdn_frontdoor_firewall_policy.%s.id}", key)

		resources.CdnFrontdoorSecurityPolicy[key] = models.NewFrontDoorSecurityPolicy(getName(service.Name+"-secpolicy"), profileID, firewallPolicyID, endpointID)
	}
}

// frontDoorHealthProbeAzureIntervalSeconds is Front Door's own
// documented default probe interval (learn.microsoft.com/azure/frontdoor/health-probes:
// "roughly estimate the health probe volume per minute... using the
// default probe frequency of 30 seconds") -- used explicitly here
// rather than left to the schema's own default so the value is visible
// in generated Terraform, not implicit.
const frontDoorHealthProbeAzureIntervalSeconds = 30

// frontDoorHealthProbeFor builds a Front Door origin group's
// health_probe from the same service.Ingress.HealthCheck.Path already
// collected for the service's Container App liveness_probe -- see
// FrontDoorHealthProbe's own doc comment for why this is a genuine
// capability addition, not an AWS-parity item.
//
// Protocol is always "Https", not derived from
// service.Ingress.HealthCheck.Type: Front Door's own route always
// forwards to the origin over HTTPS regardless
// (models.NewFrontDoorRoute's ForwardingProtocol is unconditionally
// "HttpsOnly"), so a probe checking any other protocol would be
// checking something real traffic never actually uses -- confirmed via
// a real `terraform validate` against the exact values used here, not
// assumed from the naming symmetry with Container Apps' HTTP/TCP
// transport choice. RequestType is always "HEAD" (the schema's own
// default, and Microsoft's own guidance: "To lower the load and cost to
// your origins, use HEAD requests for health probes").
func frontDoorHealthProbeFor(service *models.Service) *models.FrontDoorHealthProbe {
	return &models.FrontDoorHealthProbe{
		Protocol:          "Https",
		IntervalInSeconds: frontDoorHealthProbeAzureIntervalSeconds,
		Path:              service.Ingress.HealthCheck.Path,
		RequestType:       "HEAD",
	}
}
