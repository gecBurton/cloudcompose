package compiler

import (
	"fmt"
	"net"
	"regexp"
)

// Generate Terraform for shared environment infrastructure, mirroring
// composey/environment_generator.py.
//
// This module creates the "platform" infrastructure (VPC, ALB, ECS
// Cluster, etc.) that multiple applications share, used by `composey
// init` to set up environments that developers then deploy to.
//
// Every generator function here (like Azure/GCP's own inference
// generators) has no sort_keys=True equivalent on the Python side --
// json.dumps(manifest, indent=2) preserves insertion order exactly --
// so this uses the same PyOrdered/PyDumpsIndent machinery as
// generator_azure.go/generator_gcp.go, not encoding/json directly.

var tfNameInvalidChars = regexp.MustCompile(`[^a-zA-Z0-9_]`)

// tfName converts an environment name to a valid Terraform resource name,
// mirroring _tf_name: Terraform resource names must start with a letter
// or underscore and contain only letters, digits, and underscores.
func tfName(name string) string {
	result := tfNameInvalidChars.ReplaceAllString(name, "_")
	if result != "" && result[0] >= '0' && result[0] <= '9' {
		result = "_" + result
	}
	return result
}

// cidrsubnet calculates a subnet CIDR using Terraform's cidrsubnet logic,
// mirroring _cidrsubnet. Simplified implementation that works for the
// common cases this module actually uses (IPv4, single-digit newbits).
func cidrsubnet(baseCIDR string, newbits, netnum int) (string, error) {
	_, network, err := net.ParseCIDR(baseCIDR)
	if err != nil {
		return "", fmt.Errorf("parse CIDR %q: %w", baseCIDR, err)
	}
	ones, _ := network.Mask.Size()
	newPrefix := ones + newbits

	networkInt := ipToUint32(network.IP)
	subnetSize := uint32(1) << (32 - newPrefix)
	subnetInt := networkInt + uint32(netnum)*subnetSize

	return fmt.Sprintf("%s/%d", uint32ToIP(subnetInt), newPrefix), nil
}

func ipToUint32(ip net.IP) uint32 {
	ip4 := ip.To4()
	return uint32(ip4[0])<<24 | uint32(ip4[1])<<16 | uint32(ip4[2])<<8 | uint32(ip4[3])
}

func uint32ToIP(n uint32) net.IP {
	return net.IPv4(byte(n>>24), byte(n>>16), byte(n>>8), byte(n))
}

// mergedTags merges caller-supplied tags with the fixed Name/Environment
// (or just Environment) pair every resource in this file tags itself
// with, in the same key order Python's {**tags, "Name": ..., ...} dict
// literal produces: caller tags first (in the order given), then the
// fixed keys appended last, overwriting any caller tag of the same name.
func mergedTags(tags map[string]string, tagOrder []string, extra ...PyPair) PyOrdered {
	result := PyOrdered{}
	seen := map[string]bool{}
	for _, k := range tagOrder {
		result = append(result, p(k, tags[k]))
		seen[k] = true
	}
	for _, e := range extra {
		// A caller tag with the same key as a fixed one is overwritten,
		// matching Python's dict unpacking semantics -- the fixed key
		// simply replaces the earlier entry's value while keeping its
		// original position, exactly like dict literal overwrite
		// semantics in Python (**tags, "Name": name overwrites in place,
		// not appends a duplicate key).
		replaced := false
		for i, pair := range result {
			if pair.Key == e.Key {
				result[i] = e
				replaced = true
				break
			}
		}
		if !replaced {
			result = append(result, e)
		}
	}
	return result
}

// GenerateAwsEnvironment generates Terraform JSON for a shared AWS
// environment, mirroring generate_aws_environment. Creates a VPC with
// public/private subnets across AZs, NAT Gateways, an optional ALB, and
// an ECS Cluster.
func GenerateAwsEnvironment(
	name, region, vpcCIDR string,
	azCount int,
	createALB bool,
	certificateARN *string,
	awsEndpoint *string,
	tags map[string]string,
	tagOrder []string,
	retainDataOnDestroy bool,
) (string, error) {
	tfn := tfName(name)

	listenerPort := 80
	listenerProtocol := "HTTP"
	if certificateARN != nil {
		listenerPort = 443
		listenerProtocol = "HTTPS"
	}

	publicCIDRs := make([]string, azCount)
	privateCIDRs := make([]string, azCount)
	for i := 0; i < azCount; i++ {
		c, err := cidrsubnet(vpcCIDR, 4, i)
		if err != nil {
			return "", err
		}
		publicCIDRs[i] = c
	}
	for i := 0; i < azCount; i++ {
		c, err := cidrsubnet(vpcCIDR, 4, i+azCount)
		if err != nil {
			return "", err
		}
		privateCIDRs[i] = c
	}

	requiredProviders := PyOrdered{
		p("aws", PyOrdered{p("source", "hashicorp/aws"), p("version", "~> 5.0")}),
		p("local", PyOrdered{p("source", "hashicorp/local"), p("version", "~> 2.4")}),
	}
	terraform := PyOrdered{p("required_version", ">= 1.5"), p("required_providers", requiredProviders)}

	awsProvider := PyOrdered{p("region", region)}
	if awsEndpoint != nil {
		awsProvider = append(awsProvider, p("endpoints", PyOrdered{
			p("ec2", *awsEndpoint), p("ecs", *awsEndpoint), p("elbv2", *awsEndpoint),
			p("iam", *awsEndpoint), p("sts", *awsEndpoint), p("logs", *awsEndpoint),
			p("s3", *awsEndpoint), p("secretsmanager", *awsEndpoint),
		}))
	}
	provider := PyOrdered{p("aws", awsProvider)}

	dataSources := PyOrdered{p("aws_availability_zones", PyOrdered{p("available", PyOrdered{p("state", "available")})})}

	resource := PyOrdered{}

	resource = append(resource, p("aws_vpc", PyOrdered{p(tfn, PyOrdered{
		p("cidr_block", vpcCIDR),
		p("enable_dns_support", true),
		p("enable_dns_hostnames", true),
		p("tags", mergedTags(tags, tagOrder, p("Name", name), p("Environment", name))),
	})}))

	resource = append(resource, p("aws_internet_gateway", PyOrdered{p(tfn, PyOrdered{
		p("vpc_id", fmt.Sprintf("${aws_vpc.%s.id}", tfn)),
		p("tags", mergedTags(tags, tagOrder, p("Name", name), p("Environment", name))),
	})}))

	subnets := PyOrdered{}
	for i := 0; i < azCount; i++ {
		subnets = append(subnets, p(fmt.Sprintf("%s_public_%d", tfn, i), PyOrdered{
			p("vpc_id", fmt.Sprintf("${aws_vpc.%s.id}", tfn)),
			p("cidr_block", publicCIDRs[i]),
			p("availability_zone", fmt.Sprintf("${data.aws_availability_zones.available.names[%d]}", i)),
			p("map_public_ip_on_launch", true),
			p("tags", mergedTags(tags, tagOrder, p("Name", fmt.Sprintf("%s-public-%d", name, i)), p("Environment", name))),
		}))
	}
	for i := 0; i < azCount; i++ {
		subnets = append(subnets, p(fmt.Sprintf("%s_private_%d", tfn, i), PyOrdered{
			p("vpc_id", fmt.Sprintf("${aws_vpc.%s.id}", tfn)),
			p("cidr_block", privateCIDRs[i]),
			p("availability_zone", fmt.Sprintf("${data.aws_availability_zones.available.names[%d]}", i)),
			p("map_public_ip_on_launch", false),
			p("tags", mergedTags(tags, tagOrder, p("Name", fmt.Sprintf("%s-private-%d", name, i)), p("Environment", name))),
		}))
	}
	resource = append(resource, p("aws_subnet", subnets))

	eips := PyOrdered{}
	for i := 0; i < azCount; i++ {
		eips = append(eips, p(fmt.Sprintf("%s_nat_%d", tfn, i), PyOrdered{
			p("domain", "vpc"),
			p("depends_on", []string{fmt.Sprintf("aws_internet_gateway.%s", tfn)}),
			p("tags", mergedTags(tags, tagOrder, p("Name", fmt.Sprintf("%s-nat-%d", name, i)), p("Environment", name))),
		}))
	}
	resource = append(resource, p("aws_eip", eips))

	nats := PyOrdered{}
	for i := 0; i < azCount; i++ {
		nats = append(nats, p(fmt.Sprintf("%s_%d", tfn, i), PyOrdered{
			p("allocation_id", fmt.Sprintf("${aws_eip.%s_nat_%d.id}", tfn, i)),
			p("subnet_id", fmt.Sprintf("${aws_subnet.%s_public_%d.id}", tfn, i)),
			p("depends_on", []string{fmt.Sprintf("aws_internet_gateway.%s", tfn)}),
			p("tags", mergedTags(tags, tagOrder, p("Name", fmt.Sprintf("%s-%d", name, i)), p("Environment", name))),
		}))
	}
	resource = append(resource, p("aws_nat_gateway", nats))

	routeTables := PyOrdered{p(tfn+"_public", PyOrdered{
		p("vpc_id", fmt.Sprintf("${aws_vpc.%s.id}", tfn)),
		p("tags", mergedTags(tags, tagOrder, p("Name", name+"-public"), p("Environment", name))),
	})}

	routes := PyOrdered{p(tfn+"_public", PyOrdered{
		p("route_table_id", fmt.Sprintf("${aws_route_table.%s_public.id}", tfn)),
		p("destination_cidr_block", "0.0.0.0/0"),
		p("gateway_id", fmt.Sprintf("${aws_internet_gateway.%s.id}", tfn)),
	})}

	rtAssociations := PyOrdered{}
	for i := 0; i < azCount; i++ {
		rtAssociations = append(rtAssociations, p(fmt.Sprintf("%s_public_%d", tfn, i), PyOrdered{
			p("subnet_id", fmt.Sprintf("${aws_subnet.%s_public_%d.id}", tfn, i)),
			p("route_table_id", fmt.Sprintf("${aws_route_table.%s_public.id}", tfn)),
		}))
	}

	for i := 0; i < azCount; i++ {
		routeTables = append(routeTables, p(fmt.Sprintf("%s_private_%d", tfn, i), PyOrdered{
			p("vpc_id", fmt.Sprintf("${aws_vpc.%s.id}", tfn)),
			p("tags", mergedTags(tags, tagOrder, p("Name", fmt.Sprintf("%s-private-%d", name, i)), p("Environment", name))),
		}))
	}
	for i := 0; i < azCount; i++ {
		routes = append(routes, p(fmt.Sprintf("%s_private_%d", tfn, i), PyOrdered{
			p("route_table_id", fmt.Sprintf("${aws_route_table.%s_private_%d.id}", tfn, i)),
			p("destination_cidr_block", "0.0.0.0/0"),
			p("nat_gateway_id", fmt.Sprintf("${aws_nat_gateway.%s_%d.id}", tfn, i)),
		}))
	}
	for i := 0; i < azCount; i++ {
		rtAssociations = append(rtAssociations, p(fmt.Sprintf("%s_private_%d", tfn, i), PyOrdered{
			p("subnet_id", fmt.Sprintf("${aws_subnet.%s_private_%d.id}", tfn, i)),
			p("route_table_id", fmt.Sprintf("${aws_route_table.%s_private_%d.id}", tfn, i)),
		}))
	}

	resource = append(resource, p("aws_route_table", routeTables))
	resource = append(resource, p("aws_route", routes))
	resource = append(resource, p("aws_route_table_association", rtAssociations))

	resource = append(resource, p("aws_ecs_cluster", PyOrdered{p(tfn, PyOrdered{
		p("name", name),
		p("setting", []any{PyOrdered{p("name", "containerInsights"), p("value", "enabled")}}),
		p("tags", mergedTags(tags, tagOrder, p("Environment", name))),
	})}))

	resource = append(resource, p("aws_ecs_cluster_capacity_providers", PyOrdered{p(tfn, PyOrdered{
		p("cluster_name", fmt.Sprintf("${aws_ecs_cluster.%s.name}", tfn)),
		p("capacity_providers", []string{"FARGATE", "FARGATE_SPOT"}),
		p("default_capacity_provider_strategy", []any{PyOrdered{p("capacity_provider", "FARGATE"), p("weight", 1)}}),
	})}))

	if createALB {
		resource = append(resource, p("aws_security_group", PyOrdered{p(tfn+"_alb", PyOrdered{
			p("name", name+"-alb"),
			p("description", fmt.Sprintf("Ingress to the shared ALB for %s", name)),
			p("vpc_id", fmt.Sprintf("${aws_vpc.%s.id}", tfn)),
			p("tags", mergedTags(tags, tagOrder, p("Environment", name))),
		})}))

		resource = append(resource, p("aws_security_group_rule", PyOrdered{
			p(tfn+"_alb_ingress", PyOrdered{
				p("type", "ingress"), p("from_port", listenerPort), p("to_port", listenerPort),
				p("protocol", "tcp"), p("cidr_blocks", []string{"0.0.0.0/0"}),
				p("security_group_id", fmt.Sprintf("${aws_security_group.%s_alb.id}", tfn)),
				p("description", "Allow ingress to ALB"),
			}),
			p(tfn+"_alb_egress", PyOrdered{
				p("type", "egress"), p("from_port", 0), p("to_port", 0),
				p("protocol", "-1"), p("cidr_blocks", []string{"0.0.0.0/0"}),
				p("security_group_id", fmt.Sprintf("${aws_security_group.%s_alb.id}", tfn)),
				p("description", "Allow all outbound"),
			}),
		}))

		publicSubnetIDs := make([]string, azCount)
		for i := 0; i < azCount; i++ {
			publicSubnetIDs[i] = fmt.Sprintf("${aws_subnet.%s_public_%d.id}", tfn, i)
		}
		resource = append(resource, p("aws_lb", PyOrdered{p(tfn, PyOrdered{
			p("name", name+"-alb"),
			p("load_balancer_type", "application"),
			p("subnets", publicSubnetIDs),
			p("security_groups", []string{fmt.Sprintf("${aws_security_group.%s_alb.id}", tfn)}),
			p("tags", mergedTags(tags, tagOrder, p("Environment", name))),
		})}))

		listenerConfig := PyOrdered{
			p("load_balancer_arn", fmt.Sprintf("${aws_lb.%s.arn}", tfn)),
			p("port", listenerPort),
			p("protocol", listenerProtocol),
			p("default_action", []any{PyOrdered{
				p("type", "fixed-response"),
				p("fixed_response", PyOrdered{
					p("content_type", "text/plain"),
					p("message_body", "Not Found"),
					p("status_code", "404"),
				}),
			}}),
			p("tags", mergedTags(tags, tagOrder, p("Environment", name))),
		}
		if certificateARN != nil {
			listenerConfig = append(listenerConfig,
				p("ssl_policy", "ELBSecurityPolicy-TLS13-1-2-2021-06"),
				p("certificate_arn", *certificateARN),
			)
		}
		resource = append(resource, p("aws_lb_listener", PyOrdered{p(tfn, listenerConfig)}))
	}

	publicSubnetRefs := make([]string, azCount)
	privateSubnetRefs := make([]string, azCount)
	for i := 0; i < azCount; i++ {
		publicSubnetRefs[i] = fmt.Sprintf("${aws_subnet.%s_public_%d.id}", tfn, i)
		privateSubnetRefs[i] = fmt.Sprintf("${aws_subnet.%s_private_%d.id}", tfn, i)
	}

	environmentConfig := PyOrdered{
		p("target", "aws"),
		p("name", name),
		p("region", region),
		p("vpc_id", fmt.Sprintf("${aws_vpc.%s.id}", tfn)),
		p("public_subnets", publicSubnetRefs),
		p("private_subnets", privateSubnetRefs),
		p("ecs_cluster_arn", fmt.Sprintf("${aws_ecs_cluster.%s.arn}", tfn)),
		p("retain_data_on_destroy", retainDataOnDestroy),
	}
	if createALB {
		environmentConfig = append(environmentConfig,
			p("alb_arn", fmt.Sprintf("${aws_lb.%s.arn}", tfn)),
			p("alb_listener_arn", fmt.Sprintf("${aws_lb_listener.%s.arn}", tfn)),
			p("alb_security_group_id", fmt.Sprintf("${aws_security_group.%s_alb.id}", tfn)),
		)
	}
	if len(tags) > 0 {
		environmentConfig = append(environmentConfig, p("tags", tagsAsOrdered(tags, tagOrder)))
	}
	if awsEndpoint != nil {
		environmentConfig = append(environmentConfig, p("aws_endpoint", *awsEndpoint))
	}

	resource = append(resource, p("local_file", PyOrdered{p(tfn+"_environment", PyOrdered{
		p("filename", "${path.module}/environment.yml"),
		p("content", PyDumps(environmentConfig)),
		p("file_permission", "0644"),
	})}))

	outputs := PyOrdered{p("environment", PyOrdered{
		p("description", "Values matching composey's Environment model."),
		p("value", environmentConfig),
	})}
	if createALB {
		outputs = append(outputs, p("alb_dns_name", PyOrdered{
			p("description", "DNS name of the shared ALB."),
			p("value", fmt.Sprintf("${aws_lb.%s.dns_name}", tfn)),
		}))
	}

	manifest := PyOrdered{
		p("terraform", terraform),
		p("provider", provider),
		p("data", dataSources),
		p("resource", resource),
		p("output", outputs),
	}

	return PyDumpsIndent(manifest, 2), nil
}

// tagsAsOrdered renders a tags map in caller-supplied order, mirroring
// how Python's dict preserves the order the CLI's json.loads(tags) call
// produced (itself the order keys appeared in the --tags JSON string).
func tagsAsOrdered(tags map[string]string, tagOrder []string) PyOrdered {
	result := PyOrdered{}
	for _, k := range tagOrder {
		result = append(result, p(k, tags[k]))
	}
	return result
}

// GenerateAzureEnvironment generates Terraform JSON for a shared Azure
// environment, mirroring generate_azure_environment. Creates a Resource
// Group, Log Analytics Workspace, VNet with three delegated subnets
// (Container Apps, PostgreSQL, MySQL), and a Container Apps Environment.
func GenerateAzureEnvironment(
	name, location, vnetCIDR string,
	tags map[string]string,
	tagOrder []string,
	retainDataOnDestroy bool,
) (string, error) {
	tfn := tfName(name)

	requiredProviders := PyOrdered{
		p("azurerm", PyOrdered{p("source", "hashicorp/azurerm"), p("version", "~> 4.0")}),
		p("local", PyOrdered{p("source", "hashicorp/local"), p("version", "~> 2.4")}),
	}
	terraform := PyOrdered{p("required_version", ">= 1.5"), p("required_providers", requiredProviders)}
	provider := PyOrdered{p("azurerm", PyOrdered{p("features", PyOrdered{})})}
	dataSources := PyOrdered{p("azurerm_client_config", PyOrdered{p("current", PyOrdered{})})}

	resource := PyOrdered{}

	registerCmd := "az provider register --namespace Microsoft.OperationalInsights --wait && " +
		"az provider register --namespace Microsoft.ContainerInstance --wait && " +
		"az provider register --namespace Microsoft.App --wait && " +
		"az provider register --namespace Microsoft.Network --wait"
	resource = append(resource, p("null_resource", PyOrdered{p(tfn+"_register_providers", PyOrdered{
		p("provisioner", []any{PyOrdered{
			p("local-exec", PyOrdered{
				p("command", registerCmd),
				p("interpreter", []string{"/bin/sh", "-c"}),
			}),
		}}),
	})}))

	resource = append(resource, p("azurerm_resource_group", PyOrdered{p(tfn, PyOrdered{
		p("name", name),
		p("location", location),
		p("tags", mergedTags(tags, tagOrder, p("Environment", name))),
	})}))

	resource = append(resource, p("azurerm_log_analytics_workspace", PyOrdered{p(tfn, PyOrdered{
		p("name", name+"-logs"),
		p("location", location),
		p("resource_group_name", fmt.Sprintf("${azurerm_resource_group.%s.name}", tfn)),
		p("sku", "PerGB2018"),
		p("retention_in_days", 30),
		p("tags", mergedTags(tags, tagOrder, p("Environment", name))),
	})}))

	resource = append(resource, p("azurerm_virtual_network", PyOrdered{p(tfn, PyOrdered{
		p("name", name+"-vnet"),
		p("location", location),
		p("resource_group_name", fmt.Sprintf("${azurerm_resource_group.%s.name}", tfn)),
		p("address_space", []string{vnetCIDR}),
		p("tags", mergedTags(tags, tagOrder, p("Environment", name))),
	})}))

	infraCIDR, err := cidrsubnet(vnetCIDR, 5, 0)
	if err != nil {
		return "", err
	}
	pgCIDR, err := cidrsubnet(vnetCIDR, 5, 1)
	if err != nil {
		return "", err
	}
	mysqlCIDR, err := cidrsubnet(vnetCIDR, 5, 2)
	if err != nil {
		return "", err
	}

	delegation := func(delegationName, serviceName string) []any {
		return []any{PyOrdered{
			p("name", delegationName),
			p("service_delegation", []any{PyOrdered{
				p("name", serviceName),
				p("actions", []string{"Microsoft.Network/virtualNetworks/subnets/join/action"}),
			}}),
		}}
	}

	resource = append(resource, p("azurerm_subnet", PyOrdered{
		p(tfn+"_infrastructure", PyOrdered{
			p("name", "infrastructure"),
			p("resource_group_name", fmt.Sprintf("${azurerm_resource_group.%s.name}", tfn)),
			p("virtual_network_name", fmt.Sprintf("${azurerm_virtual_network.%s.name}", tfn)),
			p("address_prefixes", []string{infraCIDR}),
			p("delegation", delegation("container-apps", "Microsoft.App/environments")),
		}),
		p(tfn+"_postgresql", PyOrdered{
			p("name", "postgresql"),
			p("resource_group_name", fmt.Sprintf("${azurerm_resource_group.%s.name}", tfn)),
			p("virtual_network_name", fmt.Sprintf("${azurerm_virtual_network.%s.name}", tfn)),
			p("address_prefixes", []string{pgCIDR}),
			p("delegation", delegation("postgresql-flexible-server", "Microsoft.DBforPostgreSQL/flexibleServers")),
		}),
		p(tfn+"_mysql", PyOrdered{
			p("name", "mysql"),
			p("resource_group_name", fmt.Sprintf("${azurerm_resource_group.%s.name}", tfn)),
			p("virtual_network_name", fmt.Sprintf("${azurerm_virtual_network.%s.name}", tfn)),
			p("address_prefixes", []string{mysqlCIDR}),
			p("delegation", delegation("mysql-flexible-server", "Microsoft.DBforMySQL/flexibleServers")),
		}),
	}))

	resource = append(resource, p("azurerm_container_app_environment", PyOrdered{p(tfn, PyOrdered{
		p("name", name+"-env"),
		p("location", location),
		p("resource_group_name", fmt.Sprintf("${azurerm_resource_group.%s.name}", tfn)),
		p("log_analytics_workspace_id", fmt.Sprintf("${azurerm_log_analytics_workspace.%s.id}", tfn)),
		p("infrastructure_subnet_id", fmt.Sprintf("${azurerm_subnet.%s_infrastructure.id}", tfn)),
		p("tags", mergedTags(tags, tagOrder, p("Environment", name))),
	})}))

	environmentConfig := PyOrdered{
		p("target", "azure"),
		p("name", name),
		p("region", location),
		p("container_apps_environment_name", fmt.Sprintf("${azurerm_container_app_environment.%s.name}", tfn)),
		p("log_analytics_workspace_id", fmt.Sprintf("${azurerm_log_analytics_workspace.%s.id}", tfn)),
		p("vnet_id", fmt.Sprintf("${azurerm_virtual_network.%s.id}", tfn)),
		p("infrastructure_subnet_id", fmt.Sprintf("${azurerm_subnet.%s_infrastructure.id}", tfn)),
		p("postgresql_subnet_id", fmt.Sprintf("${azurerm_subnet.%s_postgresql.id}", tfn)),
		p("mysql_subnet_id", fmt.Sprintf("${azurerm_subnet.%s_mysql.id}", tfn)),
		p("retain_data_on_destroy", retainDataOnDestroy),
	}
	if len(tags) > 0 {
		environmentConfig = append(environmentConfig, p("tags", tagsAsOrdered(tags, tagOrder)))
	}

	resource = append(resource, p("local_file", PyOrdered{p(tfn+"_environment", PyOrdered{
		p("filename", "${path.module}/environment.yml"),
		p("content", PyDumps(environmentConfig)),
		p("file_permission", "0644"),
	})}))

	outputs := PyOrdered{p("environment", PyOrdered{
		p("description", "Values matching composey's Environment model."),
		p("value", environmentConfig),
	})}

	manifest := PyOrdered{
		p("terraform", terraform),
		p("provider", provider),
		p("data", dataSources),
		p("resource", resource),
		p("output", outputs),
	}

	return PyDumpsIndent(manifest, 2), nil
}

// GenerateGcpEnvironment generates Terraform JSON for a shared GCP
// environment, mirroring generate_gcp_environment. Creates a VPC
// Network, subnet, VPC connector for Cloud Run, and a service networking
// connection for Cloud SQL.
func GenerateGcpEnvironment(
	name, region, vpcCIDR string,
	tags map[string]string,
	tagOrder []string,
	retainDataOnDestroy bool,
) (string, error) {
	tfn := tfName(name)

	requiredProviders := PyOrdered{
		p("google", PyOrdered{p("source", "hashicorp/google"), p("version", "~> 5.0")}),
		p("local", PyOrdered{p("source", "hashicorp/local"), p("version", "~> 2.4")}),
	}
	terraform := PyOrdered{p("required_version", ">= 1.5"), p("required_providers", requiredProviders)}
	provider := PyOrdered{p("google", PyOrdered{p("region", region)})}

	resource := PyOrdered{}

	resource = append(resource, p("google_compute_network", PyOrdered{p(tfn, PyOrdered{
		p("name", name+"-vpc"),
		p("auto_create_subnetworks", false),
	})}))

	resource = append(resource, p("google_compute_subnetwork", PyOrdered{p(tfn, PyOrdered{
		p("name", name+"-subnet"),
		p("region", region),
		p("network", fmt.Sprintf("${google_compute_network.%s.id}", tfn)),
		p("ip_cidr_range", vpcCIDR),
		p("private_ip_google_access", true),
	})}))

	connectorCIDR, err := cidrsubnet(vpcCIDR, 4, 1)
	if err != nil {
		return "", err
	}
	resource = append(resource, p("google_vpc_access_connector", PyOrdered{p(tfn, PyOrdered{
		p("name", name+"-connector"),
		p("region", region),
		p("network", fmt.Sprintf("${google_compute_network.%s.id}", tfn)),
		p("ip_cidr_range", connectorCIDR),
		p("min_throughput", 200),
		p("max_throughput", 400),
	})}))

	resource = append(resource, p("google_compute_global_address", PyOrdered{p(tfn+"_service_networking", PyOrdered{
		p("name", name+"-service-networking"),
		p("purpose", "VPC_PEERING"),
		p("address_type", "INTERNAL"),
		p("prefix_length", 16),
		p("network", fmt.Sprintf("${google_compute_network.%s.id}", tfn)),
	})}))

	resource = append(resource, p("google_service_networking_connection", PyOrdered{p(tfn, PyOrdered{
		p("network", fmt.Sprintf("${google_compute_network.%s.id}", tfn)),
		p("service", "servicenetworking.googleapis.com"),
		p("reserved_peering_ranges", []string{fmt.Sprintf("${google_compute_global_address.%s_service_networking.name}", tfn)}),
	})}))

	environmentConfig := PyOrdered{
		p("target", "gcp"),
		p("name", name),
		p("region", region),
		p("vpc_id", fmt.Sprintf("${google_compute_network.%s.id}", tfn)),
		p("subnet_id", fmt.Sprintf("${google_compute_subnetwork.%s.id}", tfn)),
		p("vpc_connector_name", fmt.Sprintf("${google_vpc_access_connector.%s.name}", tfn)),
		p("retain_data_on_destroy", retainDataOnDestroy),
	}
	if len(tags) > 0 {
		environmentConfig = append(environmentConfig, p("labels", tagsAsOrdered(tags, tagOrder)))
	}

	resource = append(resource, p("local_file", PyOrdered{p(tfn+"_environment", PyOrdered{
		p("filename", "${path.module}/environment.yml"),
		p("content", PyDumps(environmentConfig)),
		p("file_permission", "0644"),
	})}))

	outputs := PyOrdered{p("environment", PyOrdered{
		p("description", "Values matching composey's Environment model."),
		p("value", environmentConfig),
	})}

	manifest := PyOrdered{
		p("terraform", terraform),
		p("provider", provider),
		p("resource", resource),
		p("output", outputs),
	}

	return PyDumpsIndent(manifest, 2), nil
}
