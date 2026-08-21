package azure

import (
	"fmt"

	"github.com/gecburton/cloudcompose/internal/models"
)

// inferCdnAzure puts Azure Front Door (Standard/Premium) in front of every
// service with both cdn:true and an ingress.
//
// One profile per application, shared by every CDN-enabled service in it.
// Everything below the profile (endpoint, origin group, origin, route) is
// one set per service, since each service's Container App has its own
// ingress FQDN to front.
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

		// The Container App's ingress FQDN. Both HostName and
		// OriginHostHeader need it: the Host header on the forwarded
		// request must match the origin it's listening for.
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
		// rather than one shared policy per profile, since two services
		// behind the same profile can have different legitimate traffic
		// volumes.
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

// frontDoorHealthProbeAzureIntervalSeconds is Front Door's documented
// default probe interval, made explicit here so it's visible in
// generated Terraform.
const frontDoorHealthProbeAzureIntervalSeconds = 30

// frontDoorHealthProbeFor builds a Front Door origin group's health_probe
// from the same health-check path used for the Container App's liveness
// probe. Protocol is always "Https" since Front Door's route always
// forwards over HTTPS. RequestType is always "HEAD" per Microsoft's
// guidance to lower load/cost on origins.
func frontDoorHealthProbeFor(service *models.Service) *models.FrontDoorHealthProbe {
	return &models.FrontDoorHealthProbe{
		Protocol:          "Https",
		IntervalInSeconds: frontDoorHealthProbeAzureIntervalSeconds,
		Path:              service.Ingress.HealthCheck.Path,
		RequestType:       "HEAD",
	}
}
