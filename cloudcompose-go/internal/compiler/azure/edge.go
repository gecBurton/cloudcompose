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
	}
}
