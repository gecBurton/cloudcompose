package aws

import (
	"fmt"
	"strings"

	"github.com/gecburton/cloudcompose/internal/compiler/shared"
	"github.com/gecburton/cloudcompose/internal/models"
)

// InferComputeResources infers ECS Fargate compute resources.
//
// Returns a mapping of discoverable service names to their connections.
func InferComputeResources(
	resources *models.AWSResources,
	app *models.Application,
	env *models.AwsEnvironment,
	getName func(string) string,
	tags map[string]string,
	discard bool,
	priorities map[string]int,
	namespace string,
) map[string]models.Connection {
	connections := map[string]models.Connection{}

	for i := range app.Services {
		service := &app.Services[i]
		if service.Capability != models.CapabilityContainer {
			continue
		}

		// Get compute sizing.
		compute, ok := shared.SizeMappings[string(service.Size)]
		if !ok {
			compute = shared.SizeMappings["small"]
		}
		cpu := compute.CPU
		memory := compute.Memory
		if service.CPU != nil {
			cpu = *service.CPU
		}
		if service.Memory != nil {
			memory = *service.Memory
		}

		// Create log group.
		logGroupKey := service.Name + "_lg"
		logGroup := models.NewCloudWatchLogGroup()
		logGroup.Name = "/ecs/" + getName(service.Name)
		logGroup.RetentionInDays = env.LogRetentionDays
		logGroup.Tags = tags
		resources.CloudWatchLogGroup[logGroupKey] = logGroup

		// Create IAM roles.
		taskRoleKey, execRoleKey := createIamRoles(resources, service, getName, tags)

		// Grant exec role permission to write logs.
		createLogPolicy(resources, service, logGroupKey, getName, execRoleKey)

		// Handle build-from-source.
		containerImage := handleBuildContext(resources, service, env, getName, tags, discard, execRoleKey)

		// Handle secrets.
		containerSecrets := handleSecrets(resources, service, app, getName, tags, execRoleKey)

		// Handle platform config (env vars valued outside compose file).
		containerSecrets = handlePlatformConfig(resources, service, getName, tags, containerSecrets, execRoleKey)

		// Create container definition.
		var portMappings []map[string]any
		if service.Port != nil {
			portMappings = []map[string]any{
				{
					"containerPort": *service.Port,
					"hostPort":      *service.Port,
					"protocol":      "tcp",
				},
			}
		} else {
			portMappings = []map[string]any{}
		}

		environment := make([]map[string]string, 0, len(service.Env))
		for _, k := range shared.SortedKeys(service.Env) {
			environment = append(environment, map[string]string{"name": k, "value": service.Env[k]})
		}

		secretsList := make([]map[string]string, 0, len(containerSecrets))
		for _, s := range containerSecrets {
			secretsList = append(secretsList, map[string]string{"name": s["name"], "valueFrom": s["valueFrom"]})
		}

		container := models.ContainerDefinition{
			Name:         service.Name,
			Image:        containerImage,
			Essential:    true,
			Command:      service.Command,
			PortMappings: portMappings,
			Environment:  environment,
			Secrets:      secretsList,
			LogConfiguration: map[string]any{
				"logDriver": "awslogs",
				"options": map[string]any{
					"awslogs-group":         fmt.Sprintf("${aws_cloudwatch_log_group.%s.name}", logGroupKey),
					"awslogs-region":        env.Region,
					"awslogs-stream-prefix": shared.AWSLogsStreamPrefix,
				},
			},
		}

		containerJSON := marshalJSONString([]models.ContainerDefinition{container})

		// Create task definition.
		taskDefKey := service.Name + "_td"
		taskDef := models.NewEcsTaskDefinition()
		taskDef.Family = getName(service.Name)
		taskDef.CPU = fmt.Sprintf("%d", cpu)
		taskDef.Memory = fmt.Sprintf("%d", memory)
		taskDef.ContainerDefinitions = containerJSON
		taskDef.ExecutionRoleArn = fmt.Sprintf("${aws_iam_role.%s.arn}", execRoleKey)
		taskRoleArn := fmt.Sprintf("${aws_iam_role.%s.arn}", taskRoleKey)
		taskDef.TaskRoleArn = &taskRoleArn
		taskDef.Tags = tags
		resources.EcsTaskDefinition[taskDefKey] = taskDef

		// Create ECS service.
		ecsService := createEcsService(service, env, getName, tags, taskDefKey)

		// Handle public ingress.
		if service.Ingress != nil && env.AlbArn != nil && service.Schedule == nil {
			handleIngress(resources, service, env, getName, tags, priorities, &ecsService)
		}

		// Only create service if not scheduled.
		if service.Schedule == nil {
			resources.EcsService[service.Name+"_service"] = ecsService

			// Handle auto-scaling.
			if service.MaxScale > 1 {
				handleAutoscaling(resources, service, getName)
			}
		}

		// Add connection if discoverable.
		if IsDiscoverable(service) {
			connections[service.Name] = models.Connection{
				Host:        fmt.Sprintf("%s.%s", service.Name, namespace),
				Port:        service.Port,
				AddressedBy: "host",
			}
		}
	}

	return connections
}

// createIamRoles creates IAM roles for ECS task and execution.
func createIamRoles(
	resources *models.AWSResources,
	service *models.Service,
	getName func(string) string,
	tags map[string]string,
) (taskRoleKey, execRoleKey string) {
	assumeRolePolicy := ecsTasksAssumeRolePolicy()

	taskRoleKey = service.Name + "_task_role"
	resources.IamRole[taskRoleKey] = models.IamRole{
		Name:             getName(service.Name + "-task-role"),
		AssumeRolePolicy: assumeRolePolicy,
		Tags:             tags,
	}

	execRoleKey = service.Name + "_exec_role"
	resources.IamRole[execRoleKey] = models.IamRole{
		Name:             getName(service.Name + "-exec-role"),
		AssumeRolePolicy: assumeRolePolicy,
		Tags:             tags,
	}

	return taskRoleKey, execRoleKey
}

func ecsTasksAssumeRolePolicy() string {
	return marshalJSONString(newIAMPolicyDocument(IAMPolicyStatement{
		Action:    "sts:AssumeRole",
		Effect:    "Allow",
		Principal: map[string]any{"Service": "ecs-tasks.amazonaws.com"},
	}))
}

// createLogPolicy grants exec role permission to push logs to CloudWatch.
func createLogPolicy(
	resources *models.AWSResources,
	service *models.Service,
	logGroupKey string,
	getName func(string) string,
	execRoleKey string,
) {
	policy := marshalJSONString(newIAMPolicyDocument(IAMPolicyStatement{
		Effect: "Allow",
		Action: []string{"logs:CreateLogStream", "logs:PutLogEvents"},
		Resource: []string{
			fmt.Sprintf("${aws_cloudwatch_log_group.%s.arn}:*", logGroupKey),
		},
	}))

	execLogPolicyKey := service.Name + "_exec_log_policy"
	resources.IamRolePolicy[execLogPolicyKey] = models.IamRolePolicy{
		Name:   getName(service.Name + "-exec-log-policy"),
		Role:   fmt.Sprintf("${aws_iam_role.%s.name}", execRoleKey),
		Policy: policy,
	}
}

// handleBuildContext handles build-from-source services.
func handleBuildContext(
	resources *models.AWSResources,
	service *models.Service,
	env *models.AwsEnvironment,
	getName func(string) string,
	tags map[string]string,
	discard bool,
	execRoleKey string,
) string {
	containerImage := service.Image

	if service.BuildContext == nil {
		return containerImage
	}

	// Create ECR repository.
	ecrKey := service.Name + "_ecr"
	ecr := models.NewEcrRepository()
	ecr.Name = strings.ToLower(getName(service.Name))
	ecr.ForceDelete = discard
	ecr.Tags = tags
	resources.EcrRepository[ecrKey] = ecr

	// Configure build.
	build := map[string]any{
		"context":  *service.BuildContext,
		"platform": "linux/amd64", // Match Fargate's X86_64.
	}
	if service.Dockerfile != nil {
		build["dockerfile"] = *service.Dockerfile
	}

	imageKey := service.Name + "_image"
	resources.DockerImage[imageKey] = models.DockerImage{
		Name:  fmt.Sprintf("${aws_ecr_repository.%s.repository_url}:latest", ecrKey),
		Build: build,
	}

	pushKey := service.Name + "_push"
	pushImage := models.NewDockerRegistryImage()
	pushImage.Name = fmt.Sprintf("${docker_image.%s.name}", imageKey)
	resources.DockerRegistryImage[pushKey] = pushImage

	containerImage = fmt.Sprintf(
		"${aws_ecr_repository.%s.repository_url}@${docker_registry_image.%s.sha256_digest}",
		ecrKey, pushKey,
	)

	// Grant ECR pull permissions.
	policy := marshalJSONString(newIAMPolicyDocument(IAMPolicyStatement{
		Effect: "Allow",
		Action: []string{
			"ecr:GetAuthorizationToken",
			"ecr:BatchCheckLayerAvailability",
			"ecr:GetDownloadUrlForLayer",
			"ecr:BatchGetImage",
		},
		Resource: "*",
	}))
	ecrPullPolicyKey := service.Name + "_exec_ecr_policy"
	resources.IamRolePolicy[ecrPullPolicyKey] = models.IamRolePolicy{
		Name:   getName(service.Name + "-exec-ecr-policy"),
		Role:   fmt.Sprintf("${aws_iam_role.%s.name}", execRoleKey),
		Policy: policy,
	}

	return containerImage
}

// handleSecrets handles compose secrets and creates Secrets Manager
// resources.
func handleSecrets(
	resources *models.AWSResources,
	service *models.Service,
	app *models.Application,
	getName func(string) string,
	tags map[string]string,
	execRoleKey string,
) []map[string]string {
	containerSecrets := []map[string]string{}

	for _, secretName := range service.Secrets {
		secretKey := fmt.Sprintf("%s_%s_secret", service.Name, secretName)
		desc := fmt.Sprintf("Secret %s for %s service %s", secretName, app.Name, service.Name)
		resources.SecretsmanagerSecret[secretKey] = models.SecretsManagerSecret{
			Name:        getName(fmt.Sprintf("%s-%s", service.Name, secretName)),
			Description: &desc,
			Tags:        tags,
		}

		ignoreChanges := []string{"secret_string"}
		resources.SecretsmanagerSecretVersion[secretKey+"_v1"] = models.SecretsManagerSecretVersion{
			SecretID:     fmt.Sprintf("${aws_secretsmanager_secret.%s.id}", secretKey),
			SecretString: shared.SecretsPlaceholderValue,
			Lifecycle:    &models.TerraformLifecycle{IgnoreChanges: ignoreChanges},
		}

		containerSecrets = append(containerSecrets, map[string]string{
			"name":      strings.ReplaceAll(strings.ToUpper(secretName), "-", "_"),
			"valueFrom": fmt.Sprintf("${aws_secretsmanager_secret.%s.arn}", secretKey),
		})

		// Grant read access.
		secretPolicyKey := fmt.Sprintf("%s_%s_policy", service.Name, secretName)
		policy := marshalJSONString(newIAMPolicyDocument(IAMPolicyStatement{
			Effect:   "Allow",
			Action:   []string{"secretsmanager:GetSecretValue"},
			Resource: []string{fmt.Sprintf("${aws_secretsmanager_secret.%s.arn}", secretKey)},
		}))
		resources.IamRolePolicy[secretPolicyKey] = models.IamRolePolicy{
			Name:   getName(fmt.Sprintf("%s-%s-policy", service.Name, secretName)),
			Role:   fmt.Sprintf("${aws_iam_role.%s.name}", execRoleKey),
			Policy: policy,
		}
	}

	return containerSecrets
}

// handlePlatformConfig handles platform-supplied configuration (env vars
// not valued in the compose file).
func handlePlatformConfig(
	resources *models.AWSResources,
	service *models.Service,
	getName func(string) string,
	tags map[string]string,
	containerSecrets []map[string]string,
	execRoleKey string,
) []map[string]string {
	if len(service.Config) == 0 {
		return containerSecrets
	}

	configKey := service.Name + "_config"
	desc := fmt.Sprintf("Platform-supplied configuration for %s", service.Name)
	resources.SecretsmanagerSecret[configKey] = models.SecretsManagerSecret{
		Name:        getName(service.Name + "-config"),
		Description: &desc,
		Tags:        tags,
	}

	// Key order in the resulting JSON string has no observable effect
	// (Secrets Manager stores it as an opaque blob until read back out by
	// key), but service.Config's own order is preserved anyway since
	// that's the natural iteration order here.
	placeholders := make(map[string]string, len(service.Config))
	for _, key := range service.Config {
		placeholders[key] = shared.SecretsPlaceholderValue
	}
	secretString := marshalJSONString(placeholders)

	resources.SecretsmanagerSecretVersion[configKey+"_v1"] = models.SecretsManagerSecretVersion{
		SecretID:     fmt.Sprintf("${aws_secretsmanager_secret.%s.id}", configKey),
		SecretString: secretString,
		Lifecycle:    &models.TerraformLifecycle{IgnoreChanges: []string{"secret_string"}},
	}

	for _, key := range service.Config {
		containerSecrets = append(containerSecrets, map[string]string{
			"name":      key,
			"valueFrom": fmt.Sprintf("${aws_secretsmanager_secret.%s.arn}:%s::", configKey, key),
		})
	}

	// Grant config read access.
	policy := marshalJSONString(newIAMPolicyDocument(IAMPolicyStatement{
		Effect:   "Allow",
		Action:   []string{"secretsmanager:GetSecretValue"},
		Resource: []string{fmt.Sprintf("${aws_secretsmanager_secret.%s.arn}", configKey)},
	}))
	resources.IamRolePolicy[service.Name+"_config_policy"] = models.IamRolePolicy{
		Name:   getName(service.Name + "-config-policy"),
		Role:   fmt.Sprintf("${aws_iam_role.%s.name}", execRoleKey),
		Policy: policy,
	}

	return containerSecrets
}

// createEcsService creates the ECS service configuration.
func createEcsService(
	service *models.Service,
	env *models.AwsEnvironment,
	getName func(string) string,
	tags map[string]string,
	taskDefKey string,
) models.EcsService {
	svc := models.NewEcsService()
	svc.Name = getName(service.Name)
	svc.Cluster = env.EcsClusterArn
	svc.TaskDefinition = fmt.Sprintf("${aws_ecs_task_definition.%s.arn}", taskDefKey)
	svc.DesiredCount = service.MinScale
	if service.MaxScale > 1 {
		svc.Lifecycle = &models.TerraformLifecycle{IgnoreChanges: []string{"desired_count"}}
	}
	svc.HealthCheckGracePeriodSecs = service.StartupGracePeriod
	svc.NetworkConfiguration = map[string]any{
		"subnets":          env.PrivateSubnets,
		"security_groups":  SecurityGroupIDs(service.NetworkIsolationSegments),
		"assign_public_ip": false,
	}
	if IsDiscoverable(service) {
		svc.ServiceRegistries = map[string]any{
			"registry_arn": fmt.Sprintf(
				"${aws_service_discovery_service.%s_discovery.arn}",
				SafeTerraformIdentifier(service.Name),
			),
		}
	}
	svc.Tags = tags
	return svc
}

// handleIngress handles public ingress configuration (ALB).
func handleIngress(
	resources *models.AWSResources,
	service *models.Service,
	env *models.AwsEnvironment,
	getName func(string) string,
	tags map[string]string,
	priorities map[string]int,
	ecsService *models.EcsService,
) {
	ingress := service.Ingress
	ingressPort := 80
	if ingress.Port != nil {
		ingressPort = *ingress.Port
	} else if service.Port != nil {
		ingressPort = *service.Port
	}

	// Create target group.
	tgKey := service.Name + "_tg"
	healthCheckPath := "/"
	if ingress.HealthCheck.Path != "" {
		healthCheckPath = ingress.HealthCheck.Path
	}
	resources.LbTargetGroup[tgKey] = models.LbTargetGroup{
		Name:       getName(service.Name + "-tg"),
		Port:       ingressPort,
		Protocol:   "HTTP",
		VpcID:      env.VpcID,
		TargetType: "ip",
		HealthCheck: map[string]any{
			"enabled": true,
			"path":    healthCheckPath,
			"matcher": "200-399",
		},
		Tags: tags,
	}

	// Create listener rule.
	if env.AlbListenerArn != nil {
		ruleKey := service.Name + "_listener_rule"
		resources.LbListenerRule[ruleKey] = models.LbListenerRule{
			ListenerArn: *env.AlbListenerArn,
			Priority:    priorities[service.Name],
			Action: []map[string]any{
				{
					"type":             "forward",
					"target_group_arn": fmt.Sprintf("${aws_lb_target_group.%s.arn}", tgKey),
				},
			},
			Condition: []map[string]any{
				{"path_pattern": map[string]any{"values": PathPatterns(ingress.Path)}},
			},
		}
	}

	// Attach load balancer to service.
	ecsService.LoadBalancer = []map[string]any{
		{
			"target_group_arn": fmt.Sprintf("${aws_lb_target_group.%s.arn}", tgKey),
			"container_name":   service.Name,
			"container_port":   ingressPort,
		},
	}

	// Create dedicated security group for ingress.
	ingressSgKey := SafeTerraformIdentifier(service.Name) + "_ingress_sg"
	desc := fmt.Sprintf("Load balancer ingress to %s", service.Name)
	resources.SecurityGroup[ingressSgKey] = models.SecurityGroup{
		Name:        getName(service.Name + "-ingress"),
		VpcID:       env.VpcID,
		Description: desc,
		Tags:        tags,
	}

	ruleDesc := fmt.Sprintf("Allow the load balancer to reach %s", service.Name)
	resources.SecurityGroupRule["alb_to_"+service.Name+"_rule"] = models.SecurityGroupRule{
		Type:                  "ingress",
		FromPort:              ingressPort,
		ToPort:                ingressPort,
		Protocol:              "tcp",
		SecurityGroupID:       fmt.Sprintf("${aws_security_group.%s.id}", ingressSgKey),
		SourceSecurityGroupID: env.AlbSecurityGroupID,
		Description:           &ruleDesc,
	}

	existingSGs, _ := ecsService.NetworkConfiguration["security_groups"].([]string)
	ecsService.NetworkConfiguration["security_groups"] = append(
		append([]string{}, existingSGs...),
		fmt.Sprintf("${aws_security_group.%s.id}", ingressSgKey),
	)
}

// handleAutoscaling handles auto-scaling configuration. Supports
// configurable metrics (CPU, memory, requests per target) with customizable
// target values and cooldown periods.
func handleAutoscaling(
	resources *models.AWSResources,
	service *models.Service,
	getName func(string) string,
) {
	serviceKey := service.Name + "_service"
	targetKey := service.Name + "_asg_target"

	target := models.NewAppAutoscalingTarget()
	target.MaxCapacity = service.MaxScale
	target.MinCapacity = service.MinScale
	target.ResourceID = fmt.Sprintf(
		`service/${split("/", "${aws_ecs_service.%s.cluster}")[1]}/${aws_ecs_service.%s.name}`,
		serviceKey, serviceKey,
	)
	resources.AppAutoscalingTarget[targetKey] = target

	// Get auto-scaling configuration (use defaults if not specified).
	//
	// A bare zero-value models.AutoScalingConfig{} has none of the
	// defaultAutoScalingConfig defaults (CPU 70%/Memory 80% metrics and
	// 300s/60s cooldowns) -- a real, silent divergence (not merely an
	// equivalent-empty-value one): a service relying on this default
	// (declaring max_scale > min_scale with no explicit auto_scaling
	// block) would otherwise get no autoscaling policies at all.
	config := service.AutoScaling
	if config == nil {
		config = defaultAutoScalingConfig()
	}

	metricMapping := map[models.AutoScalingMetricType]string{
		models.AutoScalingMetricCPU:               "ECSServiceAverageCPUUtilization",
		models.AutoScalingMetricMemory:            "ECSServiceAverageMemoryUtilization",
		models.AutoScalingMetricRequestsPerTarget: "ALBRequestCountPerTarget",
	}

	for i, metric := range config.Metrics {
		policyKey := fmt.Sprintf("%s_scaling_%d", service.Name, i)
		metricName, ok := metricMapping[metric.Type]
		if !ok {
			continue
		}

		predefinedMetricSpec := map[string]any{"predefined_metric_type": metricName}

		// For ALB requests, we need to specify the resource label.
		if metric.Type == models.AutoScalingMetricRequestsPerTarget {
			predefinedMetricSpec["resource_label"] = fmt.Sprintf("${aws_lb_target_group.%s_tg.arn_suffix}", service.Name)
		}

		policyConfig := map[string]any{
			"predefined_metric_specification": predefinedMetricSpec,
			"target_value":                    metric.TargetValue,
			"scale_in_cooldown":               config.ScaleInCooldown,
			"scale_out_cooldown":              config.ScaleOutCooldown,
		}

		policy := models.NewAppAutoscalingPolicy()
		policy.Name = getName(fmt.Sprintf("%s-scaling-%s", service.Name, metric.Type))
		policy.ResourceID = fmt.Sprintf("${aws_appautoscaling_target.%s.resource_id}", targetKey)
		policy.TargetTrackingScalingPolicyConfiguration = policyConfig
		resources.AppAutoscalingPolicy[policyKey] = policy
	}
}

// defaultAutoScalingConfig supplies the default auto-scaling configuration:
// CPU 70%, Memory 80%, 300s scale-in cooldown, 60s scale-out cooldown. Used
// whenever a scaling service declares no explicit auto_scaling block.
func defaultAutoScalingConfig() *models.AutoScalingConfig {
	return &models.AutoScalingConfig{
		Metrics: []models.AutoScalingMetric{
			{Type: models.AutoScalingMetricCPU, TargetValue: shared.AutoScalingCPUTarget},
			{Type: models.AutoScalingMetricMemory, TargetValue: shared.AutoScalingMemoryTarget},
		},
		ScaleInCooldown:  300,
		ScaleOutCooldown: 60,
	}
}
