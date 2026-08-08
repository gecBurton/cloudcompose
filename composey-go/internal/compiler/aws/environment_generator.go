package aws

import (
	"fmt"

	"github.com/gecburton/composey/internal/compiler/shared"
)

// GenerateAwsEnvironment generates Terraform JSON for a shared AWS
// environment, mirroring environment_generator.py. Creates a VPC with
// public/private subnets across AZs, NAT Gateways, an optional ALB, and
// an ECS Cluster.
//
// This is the "platform" infrastructure (VPC, ALB, ECS Cluster, etc.)
// that multiple applications share, used by `composey init` to set up
// environments that developers then deploy to.
//
// The environment's facts (VPC ID, ALB ARN, cluster ARN) are exposed as
// a plain Terraform `output "environment"` block only -- `composey main`
// reads them directly via `terraform output -json` (see
// internal/compiler/shared/terraform_outputs.go), rather than through a
// generated file a local_file resource writes as a side effect. See
// docs/authored-environment-config.md for why: a generated file
// duplicated exactly what `terraform output` already tracks, and reading
// live state instead means there's nothing that can go stale.
func GenerateAwsEnvironment(
	name, region, vpcCIDR string,
	azCount int,
	createALB bool,
	certificateARN *string,
	awsEndpoint *string,
	tags map[string]string,
	retainDataOnDestroy bool,
) (string, error) {
	tfn := shared.TfName(name)
	envTag := map[string]string{"Environment": name}
	nameEnvTag := func(nameValue string) map[string]string {
		return map[string]string{"Name": nameValue, "Environment": name}
	}

	listenerPort := 80
	listenerProtocol := "HTTP"
	if certificateARN != nil {
		listenerPort = 443
		listenerProtocol = "HTTPS"
	}

	publicCIDRs := make([]string, azCount)
	privateCIDRs := make([]string, azCount)
	for i := 0; i < azCount; i++ {
		c, err := shared.Cidrsubnet(vpcCIDR, 4, i)
		if err != nil {
			return "", err
		}
		publicCIDRs[i] = c
	}
	for i := 0; i < azCount; i++ {
		c, err := shared.Cidrsubnet(vpcCIDR, 4, i+azCount)
		if err != nil {
			return "", err
		}
		privateCIDRs[i] = c
	}

	requiredProviders := map[string]any{
		"aws": map[string]any{"source": "hashicorp/aws", "version": "~> 5.0"},
	}
	terraform := map[string]any{"required_version": ">= 1.5", "required_providers": requiredProviders}

	awsProvider := map[string]any{"region": region}
	if awsEndpoint != nil {
		awsProvider["endpoints"] = map[string]any{
			"ec2": *awsEndpoint, "ecs": *awsEndpoint, "elbv2": *awsEndpoint,
			"iam": *awsEndpoint, "sts": *awsEndpoint, "logs": *awsEndpoint,
			"s3": *awsEndpoint, "secretsmanager": *awsEndpoint,
		}
	}
	provider := map[string]any{"aws": awsProvider}

	dataSources := map[string]any{
		"aws_availability_zones": map[string]any{
			"available": map[string]any{"state": "available"},
		},
	}

	resource := map[string]any{}

	resource["aws_vpc"] = map[string]any{
		tfn: map[string]any{
			"cidr_block":           vpcCIDR,
			"enable_dns_support":   true,
			"enable_dns_hostnames": true,
			"tags":                 shared.MergedTags(tags, nameEnvTag(name)),
		},
	}

	resource["aws_internet_gateway"] = map[string]any{
		tfn: map[string]any{
			"vpc_id": fmt.Sprintf("${aws_vpc.%s.id}", tfn),
			"tags":   shared.MergedTags(tags, nameEnvTag(name)),
		},
	}

	subnets := map[string]any{}
	for i := 0; i < azCount; i++ {
		subnets[fmt.Sprintf("%s_public_%d", tfn, i)] = map[string]any{
			"vpc_id":                  fmt.Sprintf("${aws_vpc.%s.id}", tfn),
			"cidr_block":              publicCIDRs[i],
			"availability_zone":       fmt.Sprintf("${data.aws_availability_zones.available.names[%d]}", i),
			"map_public_ip_on_launch": true,
			"tags":                    shared.MergedTags(tags, nameEnvTag(fmt.Sprintf("%s-public-%d", name, i))),
		}
	}
	for i := 0; i < azCount; i++ {
		subnets[fmt.Sprintf("%s_private_%d", tfn, i)] = map[string]any{
			"vpc_id":                  fmt.Sprintf("${aws_vpc.%s.id}", tfn),
			"cidr_block":              privateCIDRs[i],
			"availability_zone":       fmt.Sprintf("${data.aws_availability_zones.available.names[%d]}", i),
			"map_public_ip_on_launch": false,
			"tags":                    shared.MergedTags(tags, nameEnvTag(fmt.Sprintf("%s-private-%d", name, i))),
		}
	}
	resource["aws_subnet"] = subnets

	eips := map[string]any{}
	for i := 0; i < azCount; i++ {
		eips[fmt.Sprintf("%s_nat_%d", tfn, i)] = map[string]any{
			"domain":     "vpc",
			"depends_on": []string{fmt.Sprintf("aws_internet_gateway.%s", tfn)},
			"tags":       shared.MergedTags(tags, nameEnvTag(fmt.Sprintf("%s-nat-%d", name, i))),
		}
	}
	resource["aws_eip"] = eips

	nats := map[string]any{}
	for i := 0; i < azCount; i++ {
		nats[fmt.Sprintf("%s_%d", tfn, i)] = map[string]any{
			"allocation_id": fmt.Sprintf("${aws_eip.%s_nat_%d.id}", tfn, i),
			"subnet_id":     fmt.Sprintf("${aws_subnet.%s_public_%d.id}", tfn, i),
			"depends_on":    []string{fmt.Sprintf("aws_internet_gateway.%s", tfn)},
			"tags":          shared.MergedTags(tags, nameEnvTag(fmt.Sprintf("%s-%d", name, i))),
		}
	}
	resource["aws_nat_gateway"] = nats

	routeTables := map[string]any{
		tfn + "_public": map[string]any{
			"vpc_id": fmt.Sprintf("${aws_vpc.%s.id}", tfn),
			"tags":   shared.MergedTags(tags, nameEnvTag(name+"-public")),
		},
	}

	routes := map[string]any{
		tfn + "_public": map[string]any{
			"route_table_id":         fmt.Sprintf("${aws_route_table.%s_public.id}", tfn),
			"destination_cidr_block": "0.0.0.0/0",
			"gateway_id":             fmt.Sprintf("${aws_internet_gateway.%s.id}", tfn),
		},
	}

	rtAssociations := map[string]any{}
	for i := 0; i < azCount; i++ {
		rtAssociations[fmt.Sprintf("%s_public_%d", tfn, i)] = map[string]any{
			"subnet_id":      fmt.Sprintf("${aws_subnet.%s_public_%d.id}", tfn, i),
			"route_table_id": fmt.Sprintf("${aws_route_table.%s_public.id}", tfn),
		}
	}

	for i := 0; i < azCount; i++ {
		routeTables[fmt.Sprintf("%s_private_%d", tfn, i)] = map[string]any{
			"vpc_id": fmt.Sprintf("${aws_vpc.%s.id}", tfn),
			"tags":   shared.MergedTags(tags, nameEnvTag(fmt.Sprintf("%s-private-%d", name, i))),
		}
	}
	for i := 0; i < azCount; i++ {
		routes[fmt.Sprintf("%s_private_%d", tfn, i)] = map[string]any{
			"route_table_id":         fmt.Sprintf("${aws_route_table.%s_private_%d.id}", tfn, i),
			"destination_cidr_block": "0.0.0.0/0",
			"nat_gateway_id":         fmt.Sprintf("${aws_nat_gateway.%s_%d.id}", tfn, i),
		}
	}
	for i := 0; i < azCount; i++ {
		rtAssociations[fmt.Sprintf("%s_private_%d", tfn, i)] = map[string]any{
			"subnet_id":      fmt.Sprintf("${aws_subnet.%s_private_%d.id}", tfn, i),
			"route_table_id": fmt.Sprintf("${aws_route_table.%s_private_%d.id}", tfn, i),
		}
	}

	resource["aws_route_table"] = routeTables
	resource["aws_route"] = routes
	resource["aws_route_table_association"] = rtAssociations

	resource["aws_ecs_cluster"] = map[string]any{
		tfn: map[string]any{
			"name":    name,
			"setting": []any{map[string]any{"name": "containerInsights", "value": "enabled"}},
			"tags":    shared.MergedTags(tags, envTag),
		},
	}

	resource["aws_ecs_cluster_capacity_providers"] = map[string]any{
		tfn: map[string]any{
			"cluster_name":                       fmt.Sprintf("${aws_ecs_cluster.%s.name}", tfn),
			"capacity_providers":                 []string{"FARGATE", "FARGATE_SPOT"},
			"default_capacity_provider_strategy": []any{map[string]any{"capacity_provider": "FARGATE", "weight": 1}},
		},
	}

	if createALB {
		resource["aws_security_group"] = map[string]any{
			tfn + "_alb": map[string]any{
				"name":        name + "-alb",
				"description": fmt.Sprintf("Ingress to the shared ALB for %s", name),
				"vpc_id":      fmt.Sprintf("${aws_vpc.%s.id}", tfn),
				"tags":        shared.MergedTags(tags, envTag),
			},
		}

		resource["aws_security_group_rule"] = map[string]any{
			tfn + "_alb_ingress": map[string]any{
				"type": "ingress", "from_port": listenerPort, "to_port": listenerPort,
				"protocol": "tcp", "cidr_blocks": []string{"0.0.0.0/0"},
				"security_group_id": fmt.Sprintf("${aws_security_group.%s_alb.id}", tfn),
				"description":       "Allow ingress to ALB",
			},
			tfn + "_alb_egress": map[string]any{
				"type": "egress", "from_port": 0, "to_port": 0,
				"protocol": "-1", "cidr_blocks": []string{"0.0.0.0/0"},
				"security_group_id": fmt.Sprintf("${aws_security_group.%s_alb.id}", tfn),
				"description":       "Allow all outbound",
			},
		}

		publicSubnetIDs := make([]string, azCount)
		for i := 0; i < azCount; i++ {
			publicSubnetIDs[i] = fmt.Sprintf("${aws_subnet.%s_public_%d.id}", tfn, i)
		}
		resource["aws_lb"] = map[string]any{
			tfn: map[string]any{
				"name":               name + "-alb",
				"load_balancer_type": "application",
				"subnets":            publicSubnetIDs,
				"security_groups":    []string{fmt.Sprintf("${aws_security_group.%s_alb.id}", tfn)},
				"tags":               shared.MergedTags(tags, envTag),
			},
		}

		listenerConfig := map[string]any{
			"load_balancer_arn": fmt.Sprintf("${aws_lb.%s.arn}", tfn),
			"port":              listenerPort,
			"protocol":          listenerProtocol,
			"default_action": []any{map[string]any{
				"type": "fixed-response",
				"fixed_response": map[string]any{
					"content_type": "text/plain",
					"message_body": "Not Found",
					"status_code":  "404",
				},
			}},
			"tags": shared.MergedTags(tags, envTag),
		}
		if certificateARN != nil {
			listenerConfig["ssl_policy"] = "ELBSecurityPolicy-TLS13-1-2-2021-06"
			listenerConfig["certificate_arn"] = *certificateARN
		}
		resource["aws_lb_listener"] = map[string]any{tfn: listenerConfig}
	}

	publicSubnetRefs := make([]string, azCount)
	privateSubnetRefs := make([]string, azCount)
	for i := 0; i < azCount; i++ {
		publicSubnetRefs[i] = fmt.Sprintf("${aws_subnet.%s_public_%d.id}", tfn, i)
		privateSubnetRefs[i] = fmt.Sprintf("${aws_subnet.%s_private_%d.id}", tfn, i)
	}

	environmentConfig := map[string]any{
		"target":                 "aws",
		"name":                   name,
		"region":                 region,
		"vpc_id":                 fmt.Sprintf("${aws_vpc.%s.id}", tfn),
		"public_subnets":         publicSubnetRefs,
		"private_subnets":        privateSubnetRefs,
		"ecs_cluster_arn":        fmt.Sprintf("${aws_ecs_cluster.%s.arn}", tfn),
		"retain_data_on_destroy": retainDataOnDestroy,
	}
	if createALB {
		environmentConfig["alb_arn"] = fmt.Sprintf("${aws_lb.%s.arn}", tfn)
		environmentConfig["alb_listener_arn"] = fmt.Sprintf("${aws_lb_listener.%s.arn}", tfn)
		environmentConfig["alb_security_group_id"] = fmt.Sprintf("${aws_security_group.%s_alb.id}", tfn)
	}
	if len(tags) > 0 {
		environmentConfig["tags"] = tags
	}
	if awsEndpoint != nil {
		environmentConfig["aws_endpoint"] = *awsEndpoint
	}

	outputs := map[string]any{
		"environment": map[string]any{
			"description": "Values matching composey's Environment model.",
			"value":       environmentConfig,
		},
	}
	if createALB {
		outputs["alb_dns_name"] = map[string]any{
			"description": "DNS name of the shared ALB.",
			"value":       fmt.Sprintf("${aws_lb.%s.dns_name}", tfn),
		}
	}

	manifest := map[string]any{
		"terraform": terraform,
		"provider":  provider,
		"data":      dataSources,
		"resource":  resource,
		"output":    outputs,
	}

	return shared.MarshalIndentedJSON(manifest)
}
