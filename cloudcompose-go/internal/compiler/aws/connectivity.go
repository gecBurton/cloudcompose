package aws

import (
	"fmt"
	"sort"

	"github.com/gecburton/cloudcompose/internal/models"
)

// sgKey generates a Terraform-safe security group key from a network name.
func sgKey(network string) string {
	return SafeTerraformIdentifier(network) + "_sg"
}

// SecurityGroupIDs gets Terraform references to security groups for given
// network segments.
func SecurityGroupIDs(serviceSegments []string) []string {
	sorted := make([]string, len(serviceSegments))
	copy(sorted, serviceSegments)
	sort.Strings(sorted)

	ids := make([]string, len(sorted))
	for i, n := range sorted {
		ids[i] = fmt.Sprintf("${aws_security_group.%s.id}", sgKey(n))
	}
	return ids
}

// IsDiscoverable checks if a service can be discovered by other services. A
// scheduled task runs and exits rather than being a service, and a container
// with no port publishes nothing to reach.
func IsDiscoverable(service *models.Service) bool {
	return service.Capability == models.CapabilityContainer &&
		service.Port != nil &&
		service.Schedule == nil
}

// InferNetworking infers security groups and network rules for the
// application. Creates one security group per network isolation segment.
// Services sharing a segment can reach each other, and services on disjoint
// segments cannot. This maps to AWS security groups; other clouds use
// equivalent mechanisms.
func InferNetworking(
	resources *models.AWSResources,
	app *models.Application,
	env *models.AwsEnvironment,
	getName func(string) string,
	tags map[string]string,
) {
	networkSet := map[string]struct{}{}
	for _, service := range app.Services {
		for _, n := range service.NetworkIsolationSegments {
			networkSet[n] = struct{}{}
		}
	}
	networks := make([]string, 0, len(networkSet))
	for n := range networkSet {
		networks = append(networks, n)
	}
	sort.Strings(networks)

	for _, network := range networks {
		key := sgKey(network)
		resources.SecurityGroup[key] = models.SecurityGroup{
			Name:        getName(network),
			VpcID:       env.VpcID,
			Description: fmt.Sprintf("Network %s of %s in %s", network, app.Name, env.Name),
			Tags:        tags,
		}

		selfRef := fmt.Sprintf("${aws_security_group.%s.id}", key)

		// Members of a network reach each other on any port.
		desc := fmt.Sprintf("Allow traffic within network %s", network)
		selfRefCopy := selfRef
		resources.SecurityGroupRule[key+"_internal_rule"] = models.SecurityGroupRule{
			Type:                  "ingress",
			FromPort:              0,
			ToPort:                0,
			Protocol:              "-1",
			SecurityGroupID:       selfRef,
			SourceSecurityGroupID: &selfRefCopy,
			Description:           &desc,
		}

		// Allow outbound access (needed for pulling images, writing logs).
		egressDesc := fmt.Sprintf("Allow all outbound from network %s", network)
		resources.SecurityGroupRule[key+"_egress_rule"] = models.SecurityGroupRule{
			Type:            "egress",
			FromPort:        0,
			ToPort:          0,
			Protocol:        "-1",
			SecurityGroupID: selfRef,
			CidrBlocks:      []string{"0.0.0.0/0"},
			Description:     &egressDesc,
		}
	}
}

// InferServiceDiscovery creates the service discovery namespace and
// services. Returns the namespace name for use in connection strings.
func InferServiceDiscovery(
	resources *models.AWSResources,
	app *models.Application,
	env *models.AwsEnvironment,
	getName func(string) string,
	tags map[string]string,
) string {
	namespace := NamespaceFor(env.Name, app.Name)

	var discoverable []models.Service
	for i := range app.Services {
		if IsDiscoverable(&app.Services[i]) {
			discoverable = append(discoverable, app.Services[i])
		}
	}

	if len(discoverable) > 0 {
		desc := fmt.Sprintf("Service discovery for %s in %s", app.Name, env.Name)
		resources.ServiceDiscoveryPrivateDnsNamespace["app"] = models.ServiceDiscoveryPrivateDnsNamespace{
			Name:        namespace,
			Vpc:         env.VpcID,
			Description: &desc,
			Tags:        tags,
		}
	}

	for _, service := range discoverable {
		svc := models.NewServiceDiscoveryService()
		svc.Name = service.Name
		svc.DnsConfig = map[string]any{
			"namespace_id":   "${aws_service_discovery_private_dns_namespace.app.id}",
			"dns_records":    []map[string]any{{"ttl": 10, "type": "A"}},
			"routing_policy": "MULTIVALUE",
		}
		svc.Tags = tags
		resources.ServiceDiscoveryService[SafeTerraformIdentifier(service.Name)+"_discovery"] = svc
	}

	return namespace
}
