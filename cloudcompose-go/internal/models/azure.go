package models

// Azure resource models.
//
// Field names and JSON tags match Terraform's own attribute names exactly,
// since these marshal straight into Terraform's JSON syntax.

type ContainerApp struct {
	Name                      string                 `json:"name"`
	ResourceGroupName         string                 `json:"resource_group_name"`
	ContainerAppEnvironmentID string                 `json:"container_app_environment_id"`
	RevisionMode              string                 `json:"revision_mode"`
	Template                  ContainerAppTemplate   `json:"template"`
	Ingress                   *ContainerAppIngress   `json:"ingress,omitempty"`
	Identity                  *ManagedIdentity       `json:"identity,omitempty"`
	Secret                    []ContainerAppSecret   `json:"secret,omitempty"`
	Registry                  []ContainerAppRegistry `json:"registry,omitempty"`
	Tags                      map[string]string      `json:"tags,omitempty"`
}

func NewContainerApp() ContainerApp {
	return ContainerApp{RevisionMode: "Single"}
}

// ContainerAppTemplate is azurerm_container_app's `template` block: the
// container(s) to run, replica bounds, and any HTTP-based scale rules.
type ContainerAppTemplate struct {
	Container       []ContainerAppContainer       `json:"container"`
	MinReplicas     int                           `json:"min_replicas,omitempty"`
	MaxReplicas     int                           `json:"max_replicas,omitempty"`
	HTTPScaleRule   []ContainerAppHTTPScaleRule   `json:"http_scale_rule,omitempty"`
	CustomScaleRule []ContainerAppCustomScaleRule `json:"custom_scale_rule,omitempty"`
}

// ContainerAppContainer is one entry in a template's `container` block.
type ContainerAppContainer struct {
	Name           string               `json:"name"`
	Image          string               `json:"image"`
	CPU            float64              `json:"cpu"`
	Memory         string               `json:"memory"`
	Args           []string             `json:"args,omitempty"`
	Env            []ContainerAppEnvVar `json:"env"`
	LivenessProbe  []ContainerAppProbe  `json:"liveness_probe,omitempty"`
	ReadinessProbe []ContainerAppProbe  `json:"readiness_probe,omitempty"`
	StartupProbe   []ContainerAppProbe  `json:"startup_probe,omitempty"`
}

// ContainerAppProbe mirrors the liveness_probe/readiness_probe/
// startup_probe blocks, which all share the same shape. `path` only
// applies to HTTP/HTTPS transport; left empty for TCP.
//
// The three probe fields on ContainerAppContainer are slices, not bare
// structs: azurerm allows multiple probes per container even though
// this codebase only ever sets one.
type ContainerAppProbe struct {
	Transport             string `json:"transport"`
	Port                  int    `json:"port"`
	Path                  string `json:"path,omitempty"`
	IntervalSeconds       int    `json:"interval_seconds,omitempty"`
	FailureCountThreshold int    `json:"failure_count_threshold,omitempty"`
}

// ContainerAppEnvVar is one entry in a container's `env` block: either a
// literal Value, or a SecretName referencing a `secret` block entry
// (mutually exclusive; Value is ignored when SecretName is set).
type ContainerAppEnvVar struct {
	Name       string `json:"name"`
	Value      string `json:"value,omitempty"`
	SecretName string `json:"secret_name,omitempty"`
}

type ContainerAppHTTPScaleRule struct {
	Name               string `json:"name"`
	ConcurrentRequests string `json:"concurrent_requests"`
}

// ContainerAppCustomScaleRule is one entry in the `custom_scale_rule`
// block: azurerm's generic KEDA scaler wiring, used here for the `cpu`
// and `memory` scalers.
type ContainerAppCustomScaleRule struct {
	Name           string            `json:"name"`
	CustomRuleType string            `json:"custom_rule_type"`
	Metadata       map[string]string `json:"metadata"`
}

type ContainerAppIngress struct {
	ExternalEnabled bool                        `json:"external_enabled"`
	TargetPort      int                         `json:"target_port"`
	Transport       string                      `json:"transport"`
	TrafficWeight   []ContainerAppTrafficWeight `json:"traffic_weight"`
}

// ContainerAppTrafficWeight is one entry in `ingress.traffic_weight`.
// The field is a slice, not a bare struct: azurerm's schema allows any
// number of these (e.g. canary/blue-green splits), even though
// cloudcompose only ever emits one, weighted 100% to the latest revision.
type ContainerAppTrafficWeight struct {
	LatestRevision bool `json:"latest_revision"`
	Percentage     int  `json:"percentage"`
}

// ManagedIdentity is the `identity` block shared by ContainerApp and
// ContainerAppJob: either a system-assigned identity (Azure creates and
// manages it) or one or more user-assigned identities the caller already
// created.
type ManagedIdentity struct {
	Type        string   `json:"type"`
	IdentityIDs []string `json:"identity_ids,omitempty"`
}

// ContainerAppSecret is one entry in the `secret` block: either a
// literal Value, or a KeyVaultSecretID + Identity pair that has Azure
// fetch the value from Key Vault using the named identity. These are
// mutually exclusive per Terraform's own schema.
type ContainerAppSecret struct {
	Name             string `json:"name"`
	Value            string `json:"value,omitempty"`
	KeyVaultSecretID string `json:"key_vault_secret_id,omitempty"`
	Identity         string `json:"identity,omitempty"`
}

// ContainerAppRegistry is one entry in the `registry` block: how to
// authenticate when pulling the image.
type ContainerAppRegistry struct {
	Server             string `json:"server"`
	Username           string `json:"username"`
	PasswordSecretName string `json:"password_secret_name"`
}

// ContainerAppJob is a container that runs to completion on a trigger,
// for services with a schedule. A Container App is always-on, so a
// scheduled task needs this separate resource instead.
type ContainerAppJob struct {
	Name                      string                           `json:"name"`
	ResourceGroupName         string                           `json:"resource_group_name"`
	Location                  string                           `json:"location"`
	ContainerAppEnvironmentID string                           `json:"container_app_environment_id"`
	ReplicaTimeoutInSeconds   int                              `json:"replica_timeout_in_seconds"`
	ReplicaRetryLimit         int                              `json:"replica_retry_limit"`
	ScheduleTriggerConfig     []ContainerAppJobScheduleTrigger `json:"schedule_trigger_config"`
	Template                  []ContainerAppJobTemplate        `json:"template"`
	Identity                  *ManagedIdentity                 `json:"identity,omitempty"`
	Secret                    []ContainerAppSecret             `json:"secret,omitempty"`
	Registry                  []ContainerAppRegistry           `json:"registry,omitempty"`
	Tags                      map[string]string                `json:"tags,omitempty"`
}

// ContainerAppJobScheduleTrigger is the `schedule_trigger_config` block:
// when the job runs.
type ContainerAppJobScheduleTrigger struct {
	CronExpression string `json:"cron_expression"`
}

// ContainerAppJobTemplate is a Job's `template` block. Unlike a
// ContainerApp's template, a Job has no replica bounds or scale rules.
type ContainerAppJobTemplate struct {
	Container []ContainerAppContainer `json:"container"`
}

func NewContainerAppJob() ContainerAppJob {
	return ContainerAppJob{ReplicaTimeoutInSeconds: 1800, ReplicaRetryLimit: 1}
}

// Subnet mirrors azurerm_subnet. Created per-app, one set of four per
// Container Apps Environment (infrastructure/postgresql/mysql/redis),
// carved out of the environment's own AppsCIDR at the app's own
// --subnet-index.
type Subnet struct {
	Name               string             `json:"name"`
	ResourceGroupName  string             `json:"resource_group_name"`
	VirtualNetworkName string             `json:"virtual_network_name"`
	AddressPrefixes    []string           `json:"address_prefixes"`
	Delegation         []SubnetDelegation `json:"delegation,omitempty"`
}

// SubnetDelegation mirrors azurerm_subnet's delegation block -- a
// repeatable list, so []SubnetDelegation is correct here, not a bare
// struct. Left nil for the redis subnet: azurerm_private_endpoint
// attaches to a plain subnet.
type SubnetDelegation struct {
	Name              string              `json:"name"`
	ServiceDelegation []ServiceDelegation `json:"service_delegation"`
}

// ServiceDelegation mirrors delegation's own nested service_delegation
// block, capped at exactly one entry.
type ServiceDelegation struct {
	Name    string   `json:"name"`
	Actions []string `json:"actions"`
}

// ContainerAppEnvironment mirrors azurerm_container_app_environment.
// Created per-app: a Container Apps Environment is Azure's actual
// isolation boundary, so cloudcompose main creates its own rather than
// referencing a shared one.
type ContainerAppEnvironment struct {
	Name                        string            `json:"name"`
	ResourceGroupName           string            `json:"resource_group_name"`
	Location                    string            `json:"location"`
	LogAnalyticsWorkspaceID     string            `json:"log_analytics_workspace_id"`
	InfrastructureSubnetID      *string           `json:"infrastructure_subnet_id,omitempty"`
	InternalLoadBalancerEnabled bool              `json:"internal_load_balancer_enabled"`
	Tags                        map[string]string `json:"tags,omitempty"`
}

type ContainerRegistry struct {
	Name              string            `json:"name"`
	ResourceGroupName string            `json:"resource_group_name"`
	Location          string            `json:"location"`
	Sku               string            `json:"sku"`
	AdminEnabled      bool              `json:"admin_enabled"`
	Tags              map[string]string `json:"tags,omitempty"`
}

func NewContainerRegistry() ContainerRegistry {
	return ContainerRegistry{Sku: "Standard"}
}

// PostgreSQLFlexibleServer mirrors azurerm_postgresql_flexible_server.
//
// Lifecycle ignores the "zone" attribute: Azure assigns the
// availability zone itself, and without ignoring it, a later plan
// would try to write back the zone Azure picked, which the API rejects.
type PostgreSQLFlexibleServer struct {
	Name                       string              `json:"name"`
	ResourceGroupName          string              `json:"resource_group_name"`
	Location                   string              `json:"location"`
	Version                    string              `json:"version"`
	SkuName                    string              `json:"sku_name"`
	StorageMb                  int                 `json:"storage_mb"`
	AdministratorLogin         string              `json:"administrator_login"`
	AdministratorPassword      string              `json:"administrator_password"`
	DelegatedSubnetID          *string             `json:"delegated_subnet_id,omitempty"`
	PrivateDnsZoneID           *string             `json:"private_dns_zone_id,omitempty"`
	PublicNetworkAccessEnabled bool                `json:"public_network_access_enabled"`
	BackupRetentionDays        int                 `json:"backup_retention_days,omitempty"`
	HighAvailability           map[string]string   `json:"high_availability,omitempty"`
	DatabaseName               *string             `json:"database_name,omitempty"`
	DependsOn                  []string            `json:"depends_on,omitempty"`
	Lifecycle                  map[string][]string `json:"lifecycle"`
	Tags                       map[string]string   `json:"tags,omitempty"`
}

func NewPostgreSQLFlexibleServer() PostgreSQLFlexibleServer {
	return PostgreSQLFlexibleServer{
		Version:   "14",
		SkuName:   "B_Standard_B1ms",
		StorageMb: 32768,
		Lifecycle: map[string][]string{"ignore_changes": {"zone"}},
	}
}

// DiagnosticSetting mirrors azurerm_monitor_diagnostic_setting. Routes
// a resource's own logs to the shared Log Analytics workspace; log
// export is on by default for every database this compiler creates.
//
// EnabledLog is a slice, not a bare struct: the enabled_log block has
// nesting_mode "set" and is genuinely repeatable.
type DiagnosticSetting struct {
	Name                    string                        `json:"name"`
	TargetResourceID        string                        `json:"target_resource_id"`
	LogAnalyticsWorkspaceID string                        `json:"log_analytics_workspace_id"`
	EnabledLog              []DiagnosticSettingEnabledLog `json:"enabled_log"`
}

// DiagnosticSettingEnabledLog is one entry of the `enabled_log` set --
// just a log category name (e.g. "PostgreSQLLogs").
type DiagnosticSettingEnabledLog struct {
	Category string `json:"category"`
}

type PostgreSQLFlexibleDatabase struct {
	Name      string `json:"name"`
	ServerID  string `json:"server_id"`
	Charset   string `json:"charset"`
	Collation string `json:"collation"`
}

func NewPostgreSQLFlexibleDatabase() PostgreSQLFlexibleDatabase {
	return PostgreSQLFlexibleDatabase{Charset: "UTF8", Collation: "en_US.utf8"}
}

// MySQLFlexibleServer mirrors azurerm_mysql_flexible_server. Storage is
// a nested `storage { size_gb }` block here, unlike
// PostgreSQLFlexibleServer's flat storage_mb attribute.
type MySQLFlexibleServer struct {
	Name                  string                       `json:"name"`
	ResourceGroupName     string                       `json:"resource_group_name"`
	Location              string                       `json:"location"`
	Version               string                       `json:"version"`
	SkuName               string                       `json:"sku_name"`
	Storage               []MySQLFlexibleServerStorage `json:"storage"`
	AdministratorLogin    string                       `json:"administrator_login"`
	AdministratorPassword string                       `json:"administrator_password"`
	DelegatedSubnetID     *string                      `json:"delegated_subnet_id,omitempty"`
	PrivateDnsZoneID      *string                      `json:"private_dns_zone_id,omitempty"`

	// PublicNetworkAccess is a string ("Enabled"/"Disabled"), not the
	// bool PostgreSQLFlexibleServer's equivalent field is: this
	// attribute is computed-only, so it's omitted (nil) when
	// VNet-integrated, where the provider automatically sets it to
	// Disabled.
	PublicNetworkAccess *string `json:"public_network_access,omitempty"`

	BackupRetentionDays int               `json:"backup_retention_days,omitempty"`
	HighAvailability    map[string]string `json:"high_availability,omitempty"`
	DependsOn           []string          `json:"depends_on,omitempty"`
	Tags                map[string]string `json:"tags,omitempty"`
}

// MySQLFlexibleServerStorage is the `storage` block's contents.
// size_gb, not storage_mb -- MySQL Flexible Server's storage is sized
// in GB, unlike PostgreSQL Flexible Server's storage_mb.
type MySQLFlexibleServerStorage struct {
	SizeGB int `json:"size_gb"`
}

func NewMySQLFlexibleServer() MySQLFlexibleServer {
	return MySQLFlexibleServer{
		// "8.0.21" is the actual valid version string; the provider's
		// version attribute requires an exact match against one of
		// "5.7"/"8.0.21"/"8.4".
		Version: "8.0.21",
		SkuName: "B_Standard_B1ms",
		Storage: []MySQLFlexibleServerStorage{{SizeGB: 32}},
	}
}

// MySQLFlexibleDatabase mirrors azurerm_mysql_flexible_database, which
// (unlike azurerm_postgresql_flexible_server_database's server_id)
// identifies its parent server by resource_group_name + server_name.
type MySQLFlexibleDatabase struct {
	Name              string `json:"name"`
	ResourceGroupName string `json:"resource_group_name"`
	ServerName        string `json:"server_name"`
	Charset           string `json:"charset"`
	Collation         string `json:"collation"`
}

func NewMySQLFlexibleDatabase() MySQLFlexibleDatabase {
	return MySQLFlexibleDatabase{Charset: "utf8mb4", Collation: "utf8mb4_unicode_ci"}
}

// PrivateDnsZone mirrors azurerm_private_dns_zone: a server on a
// delegated subnet is unreachable by name without one.
type PrivateDnsZone struct {
	Name              string            `json:"name"`
	ResourceGroupName string            `json:"resource_group_name"`
	Tags              map[string]string `json:"tags,omitempty"`
}

type PrivateDnsZoneVirtualNetworkLink struct {
	Name                string            `json:"name"`
	ResourceGroupName   string            `json:"resource_group_name"`
	PrivateDnsZoneName  string            `json:"private_dns_zone_name"`
	VirtualNetworkID    string            `json:"virtual_network_id"`
	RegistrationEnabled bool              `json:"registration_enabled"`
	Tags                map[string]string `json:"tags,omitempty"`
}

// PrivateEndpoint mirrors azurerm_private_endpoint. Used for Azure
// Managed Redis: unlike PostgreSQL/MySQL Flexible Server (which take a
// delegated_subnet_id/private_dns_zone_id directly on the server),
// Managed Redis's private connectivity is a separate resource. It
// attaches to a plain (non-delegated) subnet and references the target
// resource by ID + subresource name.
type PrivateEndpoint struct {
	Name                     string                       `json:"name"`
	ResourceGroupName        string                       `json:"resource_group_name"`
	Location                 string                       `json:"location"`
	SubnetID                 string                       `json:"subnet_id"`
	PrivateServiceConnection []PrivateServiceConnection   `json:"private_service_connection"`
	PrivateDnsZoneGroup      *PrivateEndpointDnsZoneGroup `json:"private_dns_zone_group,omitempty"`
	Tags                     map[string]string            `json:"tags,omitempty"`
}

// PrivateServiceConnection is the `private_service_connection` block:
// which resource this endpoint connects to.
type PrivateServiceConnection struct {
	Name                        string   `json:"name"`
	IsManualConnection          bool     `json:"is_manual_connection"`
	PrivateConnectionResourceID string   `json:"private_connection_resource_id"`
	SubresourceNames            []string `json:"subresource_names,omitempty"`
}

// PrivateEndpointDnsZoneGroup is the `private_dns_zone_group` block:
// which private DNS zone(s) get an A-record for this endpoint's IP.
type PrivateEndpointDnsZoneGroup struct {
	Name              string   `json:"name"`
	PrivateDnsZoneIDs []string `json:"private_dns_zone_ids"`
}

type KeyVault struct {
	Name                     string            `json:"name"`
	ResourceGroupName        string            `json:"resource_group_name"`
	Location                 string            `json:"location"`
	TenantID                 string            `json:"tenant_id"`
	SkuName                  string            `json:"sku_name"`
	SoftDeleteRetentionDays  int               `json:"soft_delete_retention_days"`
	PurgeProtectionEnabled   bool              `json:"purge_protection_enabled"`
	RbacAuthorizationEnabled bool              `json:"rbac_authorization_enabled"`
	Tags                     map[string]string `json:"tags,omitempty"`
}

func NewKeyVault() KeyVault {
	// RBAC mode, not the classic access-policy model: access is granted
	// via azurerm_role_assignment, the same primitive used for storage
	// access.
	return KeyVault{SkuName: "standard", SoftDeleteRetentionDays: 7, RbacAuthorizationEnabled: true}
}

// KeyVaultSecret mirrors azurerm_key_vault_secret. Lifecycle ignores
// "value" so the secret's value never shows in Terraform's plan/apply
// output.
type KeyVaultSecret struct {
	Name       string              `json:"name"`
	KeyVaultID string              `json:"key_vault_id"`
	Value      string              `json:"value"`
	Lifecycle  map[string][]string `json:"lifecycle"`
	// DependsOn references the RBAC-propagation time_sleep, set
	// unconditionally since every secret here is created after
	// granting some identity read access to the vault.
	DependsOn []string `json:"depends_on,omitempty"`
}

func NewKeyVaultSecret() KeyVaultSecret {
	return KeyVaultSecret{
		Lifecycle: map[string][]string{"ignore_changes": {"value"}},
		DependsOn: []string{"time_sleep.kv_role_propagation"},
	}
}

// UserAssignedIdentity is created once per app that has any service
// consuming a managed-service credential, so it can be granted
// RoleAssignments before any Container App exists to reference it: a
// system-assigned identity's principal_id doesn't exist until its
// owning resource is created, which is too late for that resource's
// own credential lookups during creation.
type UserAssignedIdentity struct {
	Name              string            `json:"name"`
	ResourceGroupName string            `json:"resource_group_name"`
	Location          string            `json:"location"`
	Tags              map[string]string `json:"tags,omitempty"`
}

// RoleAssignment grants a scoped Azure RBAC role to a principal (here,
// always a UserAssignedIdentity's principal_id). Used for Key Vault
// Secrets User and Storage Blob Data Contributor.
type RoleAssignment struct {
	Scope              string `json:"scope"`
	RoleDefinitionName string `json:"role_definition_name"`
	PrincipalID        string `json:"principal_id"`
}

// TimeSleep mirrors time_sleep (hashicorp/time provider): a resource
// whose only purpose is to make Terraform wait. Used to work around a
// real gap in Azure's RBAC propagation -- azurerm_role_assignment
// reporting "created" does not mean the grant has actually propagated,
// which can take up to 10 minutes and causes 403/AuthorizationFailed on
// any Key Vault secret read attempted too soon.
//
// DependsOn must name azurerm_role_assignment.kv_role explicitly:
// nothing in this resource's own arguments references it, so Terraform
// has no other reason to order this after it.
type TimeSleep struct {
	CreateDuration string   `json:"create_duration"`
	DependsOn      []string `json:"depends_on"`
}

// KeyVaultRoleAssignmentPropagationDelay is the create_duration for the
// RBAC-propagation time_sleep. 90s balances against Microsoft's
// documented worst case of up to 10 minutes, which would impose a large
// delay on every deployment to guard against a rare race.
const KeyVaultRoleAssignmentPropagationDelay = "90s"

type StorageAccount struct {
	Name                    string            `json:"name"`
	ResourceGroupName       string            `json:"resource_group_name"`
	Location                string            `json:"location"`
	AccountTier             string            `json:"account_tier"`
	AccountReplicationType  string            `json:"account_replication_type"`
	AccountKind             string            `json:"account_kind"`
	MinTlsVersion           string            `json:"min_tls_version"`
	HttpsTrafficOnlyEnabled bool              `json:"https_traffic_only_enabled"`
	Tags                    map[string]string `json:"tags,omitempty"`
}

func NewStorageAccount() StorageAccount {
	return StorageAccount{
		AccountTier:             "Standard",
		AccountReplicationType:  "LRS",
		AccountKind:             "StorageV2",
		MinTlsVersion:           "TLS1_2",
		HttpsTrafficOnlyEnabled: true,
	}
}

type StorageContainer struct {
	Name                string `json:"name"`
	StorageAccountName  string `json:"storage_account_name"`
	ContainerAccessType string `json:"container_access_type"`
}

func NewStorageContainer() StorageContainer {
	return StorageContainer{ContainerAccessType: "private"}
}

// FrontDoorProfile mirrors azurerm_cdn_frontdoor_profile: the top-level
// container for an endpoint, origin groups and origins. Has no Location
// field: Front Door is a global resource.
type FrontDoorProfile struct {
	Name              string            `json:"name"`
	ResourceGroupName string            `json:"resource_group_name"`
	SkuName           string            `json:"sku_name"`
	Tags              map[string]string `json:"tags,omitempty"`
}

func NewFrontDoorProfile() FrontDoorProfile {
	return FrontDoorProfile{SkuName: "Standard_AzureFrontDoor"}
}

type FrontDoorEndpoint struct {
	Name                  string            `json:"name"`
	CdnFrontdoorProfileID string            `json:"cdn_frontdoor_profile_id"`
	Tags                  map[string]string `json:"tags,omitempty"`
}

type FrontDoorOriginGroup struct {
	Name                  string                `json:"name"`
	CdnFrontdoorProfileID string                `json:"cdn_frontdoor_profile_id"`
	LoadBalancing         map[string]any        `json:"load_balancing"`
	HealthProbe           *FrontDoorHealthProbe `json:"health_probe,omitempty"`
}

// FrontDoorHealthProbe mirrors azurerm_cdn_frontdoor_origin_group's
// health_probe block, capped at one entry -- a bare struct, not a slice.
type FrontDoorHealthProbe struct {
	Protocol          string `json:"protocol"`
	IntervalInSeconds int    `json:"interval_in_seconds"`
	Path              string `json:"path,omitempty"`
	RequestType       string `json:"request_type,omitempty"`
}

// FrontDoorOrigin mirrors azurerm_cdn_frontdoor_origin: the backend
// Front Door forwards traffic to -- a Container App's ingress FQDN,
// in this codebase's case.
type FrontDoorOrigin struct {
	Name                        string  `json:"name"`
	CdnFrontdoorOriginGroupID   string  `json:"cdn_frontdoor_origin_group_id"`
	HostName                    string  `json:"host_name"`
	CertificateNameCheckEnabled bool    `json:"certificate_name_check_enabled"`
	OriginHostHeader            *string `json:"origin_host_header,omitempty"`
	HttpPort                    int     `json:"http_port"`
	HttpsPort                   int     `json:"https_port"`
}

func NewFrontDoorOrigin() FrontDoorOrigin {
	return FrontDoorOrigin{CertificateNameCheckEnabled: true, HttpPort: 80, HttpsPort: 443}
}

// FrontDoorRoute mirrors azurerm_cdn_frontdoor_route: ties an endpoint
// to an origin group and says which request paths and protocols reach
// it. CdnFrontdoorOriginIds is not sent to the Azure API -- Terraform
// uses it only to order creation/destruction against the origins.
type FrontDoorRoute struct {
	Name                      string   `json:"name"`
	CdnFrontdoorEndpointID    string   `json:"cdn_frontdoor_endpoint_id"`
	CdnFrontdoorOriginGroupID string   `json:"cdn_frontdoor_origin_group_id"`
	CdnFrontdoorOriginIds     []string `json:"cdn_frontdoor_origin_ids"`
	PatternsToMatch           []string `json:"patterns_to_match"`
	SupportedProtocols        []string `json:"supported_protocols"`
	ForwardingProtocol        string   `json:"forwarding_protocol"`
	HttpsRedirectEnabled      bool     `json:"https_redirect_enabled"`
}

func NewFrontDoorRoute() FrontDoorRoute {
	return FrontDoorRoute{
		PatternsToMatch:      []string{"/*"},
		SupportedProtocols:   []string{"Http", "Https"},
		ForwardingProtocol:   "HttpsOnly",
		HttpsRedirectEnabled: true,
	}
}

// FrontDoorFirewallPolicy mirrors azurerm_cdn_frontdoor_firewall_policy
// -- Front Door's WAF equivalent to AWS's aws_wafv2_web_acl.
//
// Not a like-for-like mirror of AWS's default: the Standard_AzureFrontDoor
// SKU (this codebase's current SKU) cannot use managed rule sets, which
// require Premium. What's created (see NewFrontDoorFirewallPolicy) is a
// rate-limit custom_rule instead.
type FrontDoorFirewallPolicy struct {
	Name              string            `json:"name"`
	ResourceGroupName string            `json:"resource_group_name"`
	SkuName           string            `json:"sku_name"`
	Mode              string            `json:"mode"`
	CustomRule        []map[string]any  `json:"custom_rule,omitempty"`
	Tags              map[string]string `json:"tags,omitempty"`
}

// NewFrontDoorFirewallPolicy returns a FrontDoorFirewallPolicy in
// Prevention mode (rules are actually enforced, not just logged) with
// one rate-limit custom_rule matching every request via a Host-header
// size check. 100 requests/minute per client IP is a starting default,
// not derived from any AWS equivalent.
func NewFrontDoorFirewallPolicy() FrontDoorFirewallPolicy {
	return FrontDoorFirewallPolicy{
		Mode: "Prevention",
		CustomRule: []map[string]any{
			{
				"name":                           "RateLimit",
				"enabled":                        true,
				"priority":                       1,
				"type":                           "RateLimitRule",
				"action":                         "Block",
				"rate_limit_duration_in_minutes": 1,
				"rate_limit_threshold":           100,
				"match_condition": []map[string]any{
					{
						"match_variable":     "RequestHeader",
						"selector":           "Host",
						"operator":           "GreaterThanOrEqual",
						"negation_condition": false,
						"match_values":       []string{"0"},
					},
				},
			},
		},
	}
}

// FrontDoorSecurityPolicy mirrors azurerm_cdn_frontdoor_security_policy
// -- the resource that attaches a FrontDoorFirewallPolicy to a domain.
type FrontDoorSecurityPolicy struct {
	Name                  string           `json:"name"`
	CdnFrontdoorProfileID string           `json:"cdn_frontdoor_profile_id"`
	SecurityPolicies      []map[string]any `json:"security_policies"`
}

// NewFrontDoorSecurityPolicy attaches firewallPolicyID to every path
// (`/*`) on the endpoint domainID references.
func NewFrontDoorSecurityPolicy(name, profileID, firewallPolicyID, domainID string) FrontDoorSecurityPolicy {
	return FrontDoorSecurityPolicy{
		Name:                  name,
		CdnFrontdoorProfileID: profileID,
		SecurityPolicies: []map[string]any{
			{
				"firewall": []map[string]any{
					{
						"cdn_frontdoor_firewall_policy_id": firewallPolicyID,
						"association": []map[string]any{
							{
								"domain":            []map[string]any{{"cdn_frontdoor_domain_id": domainID}},
								"patterns_to_match": []string{"/*"},
							},
						},
					},
				},
			},
		},
	}
}

// ManagedRedis mirrors azurerm_managed_redis: replaces Azure Cache for
// Redis, which no longer accepts new instances. Connection details live
// on the nested DefaultDatabase block rather than on the cluster.
type ManagedRedis struct {
	Name                    string           `json:"name"`
	ResourceGroupName       string           `json:"resource_group_name"`
	Location                string           `json:"location"`
	SkuName                 string           `json:"sku_name"`
	HighAvailabilityEnabled bool             `json:"high_availability_enabled"`
	DefaultDatabase         []map[string]any `json:"default_database"`

	// PublicNetworkAccess is a string ("Enabled"/"Disabled"). Omitted
	// (nil) when public access is wanted (the provider default);
	// set explicitly to "Disabled" once a private endpoint exists.
	PublicNetworkAccess *string `json:"public_network_access,omitempty"`

	Tags map[string]string `json:"tags,omitempty"`
}

// NewManagedRedis returns a ManagedRedis with the default_database
// default reproduced. Managed Redis can require Entra ID auth instead,
// but this application wires a password into containers, so access
// keys must stay enabled.
func NewManagedRedis() ManagedRedis {
	return ManagedRedis{
		SkuName: "Balanced_B0",
		DefaultDatabase: []map[string]any{
			{
				"access_keys_authentication_enabled": true,
				"client_protocol":                    "Encrypted",
				"eviction_policy":                    "AllKeysLRU",
			},
		},
	}
}

// AzureResources is a registry of the Azure resources the compiler
// supports.
type AzureResources struct {
	ContainerApp                     map[string]ContainerApp                     `json:"azurerm_container_app,omitempty"`
	ContainerAppJob                  map[string]ContainerAppJob                  `json:"azurerm_container_app_job,omitempty"`
	ContainerAppEnvironment          map[string]ContainerAppEnvironment          `json:"azurerm_container_app_environment,omitempty"`
	Subnet                           map[string]Subnet                           `json:"azurerm_subnet,omitempty"`
	ContainerRegistry                map[string]ContainerRegistry                `json:"azurerm_container_registry,omitempty"`
	PostgreSQLFlexibleServer         map[string]PostgreSQLFlexibleServer         `json:"azurerm_postgresql_flexible_server,omitempty"`
	PostgreSQLFlexibleServerDatabase map[string]PostgreSQLFlexibleDatabase       `json:"azurerm_postgresql_flexible_server_database,omitempty"`
	MySQLFlexibleServer              map[string]MySQLFlexibleServer              `json:"azurerm_mysql_flexible_server,omitempty"`
	MySQLFlexibleDatabase            map[string]MySQLFlexibleDatabase            `json:"azurerm_mysql_flexible_database,omitempty"`
	PrivateDnsZone                   map[string]PrivateDnsZone                   `json:"azurerm_private_dns_zone,omitempty"`
	PrivateDnsZoneVirtualNetworkLink map[string]PrivateDnsZoneVirtualNetworkLink `json:"azurerm_private_dns_zone_virtual_network_link,omitempty"`
	PrivateEndpoint                  map[string]PrivateEndpoint                  `json:"azurerm_private_endpoint,omitempty"`
	KeyVault                         map[string]KeyVault                         `json:"azurerm_key_vault,omitempty"`
	KeyVaultSecret                   map[string]KeyVaultSecret                   `json:"azurerm_key_vault_secret,omitempty"`
	UserAssignedIdentity             map[string]UserAssignedIdentity             `json:"azurerm_user_assigned_identity,omitempty"`
	RoleAssignment                   map[string]RoleAssignment                   `json:"azurerm_role_assignment,omitempty"`
	TimeSleep                        map[string]TimeSleep                        `json:"time_sleep,omitempty"`
	ManagedRedis                     map[string]ManagedRedis                     `json:"azurerm_managed_redis,omitempty"`
	StorageAccount                   map[string]StorageAccount                   `json:"azurerm_storage_account,omitempty"`
	StorageContainer                 map[string]StorageContainer                 `json:"azurerm_storage_container,omitempty"`
	CdnFrontdoorProfile              map[string]FrontDoorProfile                 `json:"azurerm_cdn_frontdoor_profile,omitempty"`
	CdnFrontdoorEndpoint             map[string]FrontDoorEndpoint                `json:"azurerm_cdn_frontdoor_endpoint,omitempty"`
	CdnFrontdoorOriginGroup          map[string]FrontDoorOriginGroup             `json:"azurerm_cdn_frontdoor_origin_group,omitempty"`
	CdnFrontdoorOrigin               map[string]FrontDoorOrigin                  `json:"azurerm_cdn_frontdoor_origin,omitempty"`
	CdnFrontdoorRoute                map[string]FrontDoorRoute                   `json:"azurerm_cdn_frontdoor_route,omitempty"`
	CdnFrontdoorFirewallPolicy       map[string]FrontDoorFirewallPolicy          `json:"azurerm_cdn_frontdoor_firewall_policy,omitempty"`
	CdnFrontdoorSecurityPolicy       map[string]FrontDoorSecurityPolicy          `json:"azurerm_cdn_frontdoor_security_policy,omitempty"`
	DiagnosticSetting                map[string]DiagnosticSetting                `json:"azurerm_monitor_diagnostic_setting,omitempty"`

	// Docker provider resources (same models as AWS: build locally, push
	// to ACR instead of ECR).
	DockerImage         map[string]DockerImage         `json:"docker_image,omitempty"`
	DockerRegistryImage map[string]DockerRegistryImage `json:"docker_registry_image,omitempty"`

	// Random resources for passwords.
	RandomPassword map[string]RandomPassword `json:"random_password,omitempty"`
}

// NewAzureResources returns an AzureResources with every map initialized,
// so inference functions can assign into resources.Foo[key] without a
// nil-map panic.
func NewAzureResources() *AzureResources {
	return &AzureResources{
		ContainerApp:                     map[string]ContainerApp{},
		ContainerAppJob:                  map[string]ContainerAppJob{},
		ContainerAppEnvironment:          map[string]ContainerAppEnvironment{},
		Subnet:                           map[string]Subnet{},
		ContainerRegistry:                map[string]ContainerRegistry{},
		PostgreSQLFlexibleServer:         map[string]PostgreSQLFlexibleServer{},
		PostgreSQLFlexibleServerDatabase: map[string]PostgreSQLFlexibleDatabase{},
		MySQLFlexibleServer:              map[string]MySQLFlexibleServer{},
		MySQLFlexibleDatabase:            map[string]MySQLFlexibleDatabase{},
		PrivateDnsZone:                   map[string]PrivateDnsZone{},
		PrivateDnsZoneVirtualNetworkLink: map[string]PrivateDnsZoneVirtualNetworkLink{},
		PrivateEndpoint:                  map[string]PrivateEndpoint{},
		KeyVault:                         map[string]KeyVault{},
		KeyVaultSecret:                   map[string]KeyVaultSecret{},
		UserAssignedIdentity:             map[string]UserAssignedIdentity{},
		RoleAssignment:                   map[string]RoleAssignment{},
		TimeSleep:                        map[string]TimeSleep{},
		ManagedRedis:                     map[string]ManagedRedis{},
		StorageAccount:                   map[string]StorageAccount{},
		StorageContainer:                 map[string]StorageContainer{},
		CdnFrontdoorProfile:              map[string]FrontDoorProfile{},
		CdnFrontdoorEndpoint:             map[string]FrontDoorEndpoint{},
		CdnFrontdoorOriginGroup:          map[string]FrontDoorOriginGroup{},
		CdnFrontdoorOrigin:               map[string]FrontDoorOrigin{},
		CdnFrontdoorRoute:                map[string]FrontDoorRoute{},
		CdnFrontdoorFirewallPolicy:       map[string]FrontDoorFirewallPolicy{},
		CdnFrontdoorSecurityPolicy:       map[string]FrontDoorSecurityPolicy{},
		DiagnosticSetting:                map[string]DiagnosticSetting{},
		DockerImage:                      map[string]DockerImage{},
		DockerRegistryImage:              map[string]DockerRegistryImage{},
		RandomPassword:                   map[string]RandomPassword{},
	}
}
