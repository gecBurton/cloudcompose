package models

// AWS resource models.
//
// Field names and JSON tags match Terraform's own attribute names exactly,
// since these are marshalled straight into Terraform's JSON syntax.
//
// Optional fields use pointers so `omitempty` distinguishes "not set" from
// the zero value (e.g. a security group rule legitimately has
// from_port=0).

type TerraformLifecycle struct {
	CreateBeforeDestroy *bool    `json:"create_before_destroy,omitempty"`
	PreventDestroy      *bool    `json:"prevent_destroy,omitempty"`
	IgnoreChanges       []string `json:"ignore_changes,omitempty"`
}

type SecurityGroupRule struct {
	Type                  string   `json:"type"`
	FromPort              int      `json:"from_port"`
	ToPort                int      `json:"to_port"`
	Protocol              string   `json:"protocol"`
	CidrBlocks            []string `json:"cidr_blocks,omitempty"`
	SourceSecurityGroupID *string  `json:"source_security_group_id,omitempty"`
	SecurityGroupID       string   `json:"security_group_id"`
	Description           *string  `json:"description,omitempty"`
}

type ContainerDefinition struct {
	Name             string              `json:"name"`
	Image            string              `json:"image"`
	Essential        bool                `json:"essential"`
	Command          []string            `json:"command,omitempty"`
	PortMappings     []map[string]any    `json:"portMappings"`
	Environment      []map[string]string `json:"environment"`
	Secrets          []map[string]string `json:"secrets"`
	LogConfiguration map[string]any      `json:"logConfiguration,omitempty"`
}

type EcsTaskDefinition struct {
	Family                  string            `json:"family"`
	NetworkMode             string            `json:"network_mode"`
	RequiresCompatibilities []string          `json:"requires_compatibilities"`
	CPU                     string            `json:"cpu"`
	Memory                  string            `json:"memory"`
	ContainerDefinitions    string            `json:"container_definitions"`
	ExecutionRoleArn        string            `json:"execution_role_arn"`
	TaskRoleArn             *string           `json:"task_role_arn,omitempty"`
	Tags                    map[string]string `json:"tags,omitempty"`
}

func NewEcsTaskDefinition() EcsTaskDefinition {
	return EcsTaskDefinition{
		NetworkMode:             "awsvpc",
		RequiresCompatibilities: []string{"FARGATE"},
	}
}

type EcsService struct {
	Name                       string         `json:"name"`
	Cluster                    string         `json:"cluster"`
	TaskDefinition             string         `json:"task_definition"`
	DesiredCount               int            `json:"desired_count"`
	LaunchType                 string         `json:"launch_type"`
	HealthCheckGracePeriodSecs *int           `json:"health_check_grace_period_seconds,omitempty"`
	NetworkConfiguration       map[string]any `json:"network_configuration"`
	ServiceRegistries          map[string]any `json:"service_registries,omitempty"`
	// No omitempty: the output always includes "load_balancer": [] for a
	// service with no ingress.
	LoadBalancer []map[string]any    `json:"load_balancer"`
	Lifecycle    *TerraformLifecycle `json:"lifecycle,omitempty"`
	Tags         map[string]string   `json:"tags,omitempty"`
}

func NewEcsService() EcsService {
	return EcsService{
		DesiredCount: 1,
		LaunchType:   "FARGATE",
		LoadBalancer: []map[string]any{},
	}
}

type ServiceDiscoveryPrivateDnsNamespace struct {
	Name        string            `json:"name"`
	Vpc         string            `json:"vpc"`
	Description *string           `json:"description,omitempty"`
	Tags        map[string]string `json:"tags,omitempty"`
}

type ServiceDiscoveryService struct {
	Name                    string            `json:"name"`
	DnsConfig               map[string]any    `json:"dns_config"`
	HealthCheckCustomConfig map[string]any    `json:"health_check_custom_config"`
	Tags                    map[string]string `json:"tags,omitempty"`
}

func NewServiceDiscoveryService() ServiceDiscoveryService {
	return ServiceDiscoveryService{
		HealthCheckCustomConfig: map[string]any{"failure_threshold": 1},
	}
}

type SecurityGroup struct {
	Name        string            `json:"name"`
	VpcID       string            `json:"vpc_id"`
	Description string            `json:"description"`
	Tags        map[string]string `json:"tags,omitempty"`
}

type S3Bucket struct {
	Bucket       string            `json:"bucket"`
	ForceDestroy bool              `json:"force_destroy"`
	Tags         map[string]string `json:"tags,omitempty"`
}

func NewS3Bucket() S3Bucket {
	return S3Bucket{ForceDestroy: true}
}

type EcrRepository struct {
	Name               string            `json:"name"`
	ForceDelete        bool              `json:"force_delete"`
	ImageTagMutability string            `json:"image_tag_mutability"`
	Tags               map[string]string `json:"tags,omitempty"`
}

func NewEcrRepository() EcrRepository {
	return EcrRepository{ForceDelete: true, ImageTagMutability: "MUTABLE"}
}

type IamRole struct {
	Name             string            `json:"name"`
	AssumeRolePolicy string            `json:"assume_role_policy"`
	Tags             map[string]string `json:"tags,omitempty"`
}

type IamRolePolicy struct {
	Name   string `json:"name"`
	Role   string `json:"role"`
	Policy string `json:"policy"`
}

// DbInstance mirrors aws_db_instance. MultiAz/BackupRetentionPeriod are
// environment-level decisions (AwsEnvironment.HighAvailabilityEnabled/
// BackupRetentionDays), applied uniformly to every database this app
// creates, not a per-service x-cloud setting. `omitempty` on both is
// cosmetic, not semantic: false/0 are real, valid values, and every
// call site sets them explicitly.
type DbInstance struct {
	Identifier              string   `json:"identifier"`
	Engine                  string   `json:"engine"`
	DbName                  string   `json:"db_name"`
	InstanceClass           string   `json:"instance_class"`
	AllocatedStorage        int      `json:"allocated_storage"`
	DbSubnetGroupName       string   `json:"db_subnet_group_name"`
	VpcSecurityGroupIds     []string `json:"vpc_security_group_ids"`
	SkipFinalSnapshot       bool     `json:"skip_final_snapshot"`
	FinalSnapshotIdentifier *string  `json:"final_snapshot_identifier,omitempty"`
	PubliclyAccessible      bool     `json:"publicly_accessible"`
	MultiAz                 bool     `json:"multi_az"`
	BackupRetentionPeriod   int      `json:"backup_retention_period"`
	Username                *string  `json:"username,omitempty"`
	Password                *string  `json:"password,omitempty"`
	// EnabledCloudwatchLogsExports opts this instance's own log types
	// into CloudWatch Logs, an engine-specific list of log-type names
	// (e.g. `["postgresql", "upgrade"]` for Postgres). Always set
	// (never nil): log export is on by default for every database this
	// compiler creates.
	EnabledCloudwatchLogsExports []string          `json:"enabled_cloudwatch_logs_exports"`
	Tags                         map[string]string `json:"tags,omitempty"`
}

func NewDbInstance() DbInstance {
	return DbInstance{SkipFinalSnapshot: true}
}

type DbSubnetGroup struct {
	Name      string            `json:"name"`
	SubnetIds []string          `json:"subnet_ids"`
	Tags      map[string]string `json:"tags,omitempty"`
}

type ElastiCacheCluster struct {
	ClusterID        string            `json:"cluster_id"`
	Engine           string            `json:"engine"`
	NodeType         string            `json:"node_type"`
	NumCacheNodes    int               `json:"num_cache_nodes"`
	SubnetGroupName  string            `json:"subnet_group_name"`
	SecurityGroupIds []string          `json:"security_group_ids"`
	Tags             map[string]string `json:"tags,omitempty"`
}

type ElastiCacheSubnetGroup struct {
	Name      string            `json:"name"`
	SubnetIds []string          `json:"subnet_ids"`
	Tags      map[string]string `json:"tags,omitempty"`
}

type LbTargetGroup struct {
	Name        string            `json:"name"`
	Port        int               `json:"port"`
	Protocol    string            `json:"protocol"`
	VpcID       string            `json:"vpc_id"`
	TargetType  string            `json:"target_type"`
	HealthCheck map[string]any    `json:"health_check,omitempty"`
	Tags        map[string]string `json:"tags,omitempty"`
}

type LbListenerRule struct {
	ListenerArn string            `json:"listener_arn"`
	Priority    int               `json:"priority"`
	Action      []map[string]any  `json:"action"`
	Condition   []map[string]any  `json:"condition"`
	Tags        map[string]string `json:"tags,omitempty"`
}

type SecretsManagerSecret struct {
	Name                 string            `json:"name"`
	Description          *string           `json:"description,omitempty"`
	RecoveryWindowInDays int               `json:"recovery_window_in_days"`
	Tags                 map[string]string `json:"tags,omitempty"`
}

type SecretsManagerSecretVersion struct {
	SecretID     string              `json:"secret_id"`
	SecretString string              `json:"secret_string"`
	Lifecycle    *TerraformLifecycle `json:"lifecycle,omitempty"`
}

type RandomId struct {
	ByteLength int `json:"byte_length"`
}

func NewRandomId() RandomId {
	return RandomId{ByteLength: 4}
}

type CloudWatchLogGroup struct {
	Name            string            `json:"name"`
	RetentionInDays int               `json:"retention_in_days"`
	Tags            map[string]string `json:"tags,omitempty"`
}

func NewCloudWatchLogGroup() CloudWatchLogGroup {
	return CloudWatchLogGroup{RetentionInDays: 7}
}

type AppAutoscalingTarget struct {
	MaxCapacity       int    `json:"max_capacity"`
	MinCapacity       int    `json:"min_capacity"`
	ResourceID        string `json:"resource_id"`
	ScalableDimension string `json:"scalable_dimension"`
	ServiceNamespace  string `json:"service_namespace"`
}

func NewAppAutoscalingTarget() AppAutoscalingTarget {
	return AppAutoscalingTarget{
		ScalableDimension: "ecs:service:DesiredCount",
		ServiceNamespace:  "ecs",
	}
}

type AppAutoscalingPolicy struct {
	Name                                     string         `json:"name"`
	PolicyType                               string         `json:"policy_type"`
	ResourceID                               string         `json:"resource_id"`
	ScalableDimension                        string         `json:"scalable_dimension"`
	ServiceNamespace                         string         `json:"service_namespace"`
	TargetTrackingScalingPolicyConfiguration map[string]any `json:"target_tracking_scaling_policy_configuration"`
}

func NewAppAutoscalingPolicy() AppAutoscalingPolicy {
	return AppAutoscalingPolicy{
		PolicyType:        "TargetTrackingScaling",
		ScalableDimension: "ecs:service:DesiredCount",
		ServiceNamespace:  "ecs",
	}
}

type CloudwatchEventRule struct {
	Name               string            `json:"name"`
	ScheduleExpression string            `json:"schedule_expression"`
	Description        *string           `json:"description,omitempty"`
	State              string            `json:"state"`
	Tags               map[string]string `json:"tags,omitempty"`
}

func NewCloudwatchEventRule() CloudwatchEventRule {
	return CloudwatchEventRule{State: "ENABLED"}
}

type CloudwatchEventTarget struct {
	Rule      string         `json:"rule"`
	Arn       string         `json:"arn"`
	RoleArn   *string        `json:"role_arn,omitempty"`
	EcsTarget map[string]any `json:"ecs_target,omitempty"`
}

type CloudfrontDistribution struct {
	Enabled              bool              `json:"enabled"`
	Comment              *string           `json:"comment,omitempty"`
	Origin               []map[string]any  `json:"origin"`
	DefaultCacheBehavior map[string]any    `json:"default_cache_behavior"`
	Restrictions         map[string]any    `json:"restrictions"`
	ViewerCertificate    map[string]any    `json:"viewer_certificate"`
	WebAclID             *string           `json:"web_acl_id,omitempty"`
	Tags                 map[string]string `json:"tags,omitempty"`
}

func NewCloudfrontDistribution() CloudfrontDistribution {
	return CloudfrontDistribution{
		Enabled:           true,
		Restrictions:      map[string]any{"geo_restriction": map[string]any{"restriction_type": "none"}},
		ViewerCertificate: map[string]any{"cloudfront_default_certificate": true},
	}
}

type Wafv2WebAcl struct {
	Name             string            `json:"name"`
	Scope            string            `json:"scope"`
	Description      *string           `json:"description,omitempty"`
	DefaultAction    map[string]any    `json:"default_action"`
	VisibilityConfig map[string]any    `json:"visibility_config"`
	Rule             []map[string]any  `json:"rule,omitempty"`
	Tags             map[string]string `json:"tags,omitempty"`
	// Terraform meta-argument selecting a non-default provider configuration.
	Provider *string `json:"provider,omitempty"`
}

func NewWafv2WebAcl() Wafv2WebAcl {
	return Wafv2WebAcl{DefaultAction: map[string]any{"allow": map[string]any{}}}
}

// AWSResources is a registry of the AWS resources the compiler supports.
// Every field is a map keyed by Terraform resource key. Empty maps are
// omitted from JSON output.
type AWSResources struct {
	EcsTaskDefinition                   map[string]EcsTaskDefinition                   `json:"aws_ecs_task_definition,omitempty"`
	EcsService                          map[string]EcsService                          `json:"aws_ecs_service,omitempty"`
	AppAutoscalingTarget                map[string]AppAutoscalingTarget                `json:"aws_appautoscaling_target,omitempty"`
	AppAutoscalingPolicy                map[string]AppAutoscalingPolicy                `json:"aws_appautoscaling_policy,omitempty"`
	CloudwatchEventRule                 map[string]CloudwatchEventRule                 `json:"aws_cloudwatch_event_rule,omitempty"`
	CloudwatchEventTarget               map[string]CloudwatchEventTarget               `json:"aws_cloudwatch_event_target,omitempty"`
	CloudfrontDistribution              map[string]CloudfrontDistribution              `json:"aws_cloudfront_distribution,omitempty"`
	Wafv2WebAcl                         map[string]Wafv2WebAcl                         `json:"aws_wafv2_web_acl,omitempty"`
	ServiceDiscoveryPrivateDnsNamespace map[string]ServiceDiscoveryPrivateDnsNamespace `json:"aws_service_discovery_private_dns_namespace,omitempty"`
	ServiceDiscoveryService             map[string]ServiceDiscoveryService             `json:"aws_service_discovery_service,omitempty"`
	SecurityGroup                       map[string]SecurityGroup                       `json:"aws_security_group,omitempty"`
	SecurityGroupRule                   map[string]SecurityGroupRule                   `json:"aws_security_group_rule,omitempty"`
	CloudWatchLogGroup                  map[string]CloudWatchLogGroup                  `json:"aws_cloudwatch_log_group,omitempty"`
	LbTargetGroup                       map[string]LbTargetGroup                       `json:"aws_lb_target_group,omitempty"`
	LbListenerRule                      map[string]LbListenerRule                      `json:"aws_lb_listener_rule,omitempty"`
	SecretsmanagerSecret                map[string]SecretsManagerSecret                `json:"aws_secretsmanager_secret,omitempty"`
	SecretsmanagerSecretVersion         map[string]SecretsManagerSecretVersion         `json:"aws_secretsmanager_secret_version,omitempty"`
	RandomPassword                      map[string]RandomPassword                      `json:"random_password,omitempty"`
	RandomID                            map[string]RandomId                            `json:"random_id,omitempty"`
	S3Bucket                            map[string]S3Bucket                            `json:"aws_s3_bucket,omitempty"`
	EcrRepository                       map[string]EcrRepository                       `json:"aws_ecr_repository,omitempty"`
	DockerImage                         map[string]DockerImage                         `json:"docker_image,omitempty"`
	DockerRegistryImage                 map[string]DockerRegistryImage                 `json:"docker_registry_image,omitempty"`
	IamRole                             map[string]IamRole                             `json:"aws_iam_role,omitempty"`
	IamRolePolicy                       map[string]IamRolePolicy                       `json:"aws_iam_role_policy,omitempty"`
	DbInstance                          map[string]DbInstance                          `json:"aws_db_instance,omitempty"`
	ElastiCacheCluster                  map[string]ElastiCacheCluster                  `json:"aws_elasticache_cluster,omitempty"`
	DbSubnetGroup                       map[string]DbSubnetGroup                       `json:"aws_db_subnet_group,omitempty"`
	ElastiCacheSubnetGroup              map[string]ElastiCacheSubnetGroup              `json:"aws_elasticache_subnet_group,omitempty"`
}

// NewAWSResources returns an AWSResources with every map initialized, so
// inference functions can assign into resources.Foo[key] without a nil-map
// panic.
func NewAWSResources() *AWSResources {
	return &AWSResources{
		EcsTaskDefinition:                   map[string]EcsTaskDefinition{},
		EcsService:                          map[string]EcsService{},
		AppAutoscalingTarget:                map[string]AppAutoscalingTarget{},
		AppAutoscalingPolicy:                map[string]AppAutoscalingPolicy{},
		CloudwatchEventRule:                 map[string]CloudwatchEventRule{},
		CloudwatchEventTarget:               map[string]CloudwatchEventTarget{},
		CloudfrontDistribution:              map[string]CloudfrontDistribution{},
		Wafv2WebAcl:                         map[string]Wafv2WebAcl{},
		ServiceDiscoveryPrivateDnsNamespace: map[string]ServiceDiscoveryPrivateDnsNamespace{},
		ServiceDiscoveryService:             map[string]ServiceDiscoveryService{},
		SecurityGroup:                       map[string]SecurityGroup{},
		SecurityGroupRule:                   map[string]SecurityGroupRule{},
		CloudWatchLogGroup:                  map[string]CloudWatchLogGroup{},
		LbTargetGroup:                       map[string]LbTargetGroup{},
		LbListenerRule:                      map[string]LbListenerRule{},
		SecretsmanagerSecret:                map[string]SecretsManagerSecret{},
		SecretsmanagerSecretVersion:         map[string]SecretsManagerSecretVersion{},
		RandomPassword:                      map[string]RandomPassword{},
		RandomID:                            map[string]RandomId{},
		S3Bucket:                            map[string]S3Bucket{},
		EcrRepository:                       map[string]EcrRepository{},
		DockerImage:                         map[string]DockerImage{},
		DockerRegistryImage:                 map[string]DockerRegistryImage{},
		IamRole:                             map[string]IamRole{},
		IamRolePolicy:                       map[string]IamRolePolicy{},
		DbInstance:                          map[string]DbInstance{},
		ElastiCacheCluster:                  map[string]ElastiCacheCluster{},
		DbSubnetGroup:                       map[string]DbSubnetGroup{},
		ElastiCacheSubnetGroup:              map[string]ElastiCacheSubnetGroup{},
	}
}
