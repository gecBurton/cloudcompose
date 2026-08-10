package models

// Azure resource models.
//
// Field names and JSON tags match Terraform's own attribute names exactly,
// since these marshal straight into Terraform's JSON syntax: whatever key a
// struct here emits is the literal Terraform resource attribute name.

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
// cpu/memory sit directly on it; azurerm has no nested "resources" block
// the way ECS does.
type ContainerAppContainer struct {
	Name   string               `json:"name"`
	Image  string               `json:"image"`
	CPU    float64              `json:"cpu"`
	Memory string               `json:"memory"`
	Args   []string             `json:"args,omitempty"`
	Env    []ContainerAppEnvVar `json:"env"`
}

// ContainerAppEnvVar is one entry in a container's `env` block: either a
// literal Value, or a SecretName referencing a `secret` block entry
// (Terraform's schema treats these as mutually exclusive -- Value is
// ignored when SecretName is set). Managed-service credentials use
// SecretName (see azure/permissions.go); everything else uses Value.
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
// and `memory` scalers (added 2026-08-08, see
// docs/azure-aws-parity-todo.md's Priority 2 item 5 -- previously the
// only metric type Azure's inference handled was
// AutoScalingMetricRequestsPerTarget, via ContainerAppHTTPScaleRule
// instead of this type). Metadata's exact keys are scaler-specific; the
// cpu/memory scalers both want {"type": "Utilization", "value":
// "<percentage>"} -- see
// https://keda.sh/docs/2.14/scalers/cpu/ (memory's scaler is identical
// in shape).
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
// azurerm's schema allows any number of these (no max_items cap) --
// cloudcompose only ever emits one, weighted 100% to the latest revision,
// but the field is a slice because the schema genuinely supports more
// (e.g. canary/blue-green splits across multiple revisions), not just as
// single-item JSON-array shorthand. Confirmed against the real azurerm
// provider schema via `go run ./cmd/schema-check` (nesting_mode=list,
// no max_items), not assumed from provider docs.
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
// literal Value (used for the ACR admin password, which has no
// Key-Vault-ordering problem -- see registryAuthAzure's own doc comment
// for why it deliberately isn't RBAC-based), or a KeyVaultSecretID +
// Identity pair that has Azure fetch the value from Key Vault using the
// named identity at resolve time (used for managed-service credentials --
// see azure/permissions.go). These are mutually exclusive per Terraform's
// own schema.
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

// ContainerAppJob mirrors ContainerAppJob: a container that runs to
// completion on a trigger, for services with a schedule. A Container App
// is always-on, so a nightly task would run continuously, and one that
// exits when its work is done would be restarted indefinitely.
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
// ContainerApp's template, a Job has no replica bounds or scale rules --
// it runs to completion on its trigger and stops.
type ContainerAppJobTemplate struct {
	Container []ContainerAppContainer `json:"container"`
}

func NewContainerAppJob() ContainerAppJob {
	return ContainerAppJob{ReplicaTimeoutInSeconds: 1800, ReplicaRetryLimit: 1}
}

// ContainerAppEnvironment mirrors ContainerAppEnvironment. Defined for
// completeness, but never instantiated by inference: the environment is
// platform-owned and referenced via a data source instead (see
// AzureResources doc comment and generator_azure.go).
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

// PostgreSQLFlexibleServer mirrors PostgreSQLFlexibleServer.
//
// Lifecycle defaults to ignoring the "zone" attribute: Azure assigns the
// availability zone itself, and nothing in this model configures it.
// Without ignoring it, any later plan sees a "change" from unset to
// whatever Azure actually picked and tries to write it back, which the API
// rejects outright (confirmed against real Azure, open on and off in the
// azurerm provider since 2022, e.g. hashicorp/terraform-provider-azurerm#16888).
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
// PostgreSQLFlexibleServer's flat storage_mb/storage_tier attributes --
// confirmed against the real provider schema via `go run
// ./cmd/schema-check` after `terraform validate` caught this as a real
// bug (2026-08-08, see docs/azure-aws-parity-todo.md): the field used to
// be a flat StorageMb int emitting a nonexistent "storage_mb" attribute,
// which `terraform validate` rejects outright as an "Extraneous JSON
// object property" -- this had gone unnoticed until the MariaDB-
// detection fix started routing more example apps through the MySQL
// Flexible Server path for the first time.
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
	// bool PostgreSQLFlexibleServer's equivalent field is --
	// public_network_access_enabled on this resource is
	// computed-only (Terraform rejects a config-supplied value for it
	// outright: "Value for unconfigurable attribute"), confirmed against
	// the real provider schema after `terraform validate` caught this as
	// a real bug (2026-08-08, see docs/azure-aws-parity-todo.md).
	// Omitted entirely (nil) when VNet-integrated: the provider docs
	// state it's automatically set to Disabled whenever
	// delegated_subnet_id + private_dns_zone_id are both set, so setting
	// it explicitly in that case would just be redundant, not wrong --
	// but there's no reason to also carry the redundant value.
	PublicNetworkAccess *string `json:"public_network_access,omitempty"`

	BackupRetentionDays int               `json:"backup_retention_days,omitempty"`
	HighAvailability    map[string]string `json:"high_availability,omitempty"`
	DependsOn           []string          `json:"depends_on,omitempty"`
	Tags                map[string]string `json:"tags,omitempty"`
}

// MySQLFlexibleServerStorage is the `storage` block's contents.
// size_gb, not storage_mb -- MySQL Flexible Server's storage is sized in
// GB, unlike PostgreSQL Flexible Server's storage_mb (confirmed against
// the real provider schema, not assumed from the naming symmetry with
// PostgreSQL's own field).
type MySQLFlexibleServerStorage struct {
	SizeGB int `json:"size_gb"`
}

func NewMySQLFlexibleServer() MySQLFlexibleServer {
	return MySQLFlexibleServer{
		// "8.0.21" is the actual valid version string, not "8.0" --
		// the provider's version attribute requires an exact match
		// against one of "5.7"/"8.0.21"/"8.4", found the same way as the
		// other MySQL Flexible Server bugs above.
		Version: "8.0.21",
		SkuName: "B_Standard_B1ms",
		Storage: []MySQLFlexibleServerStorage{{SizeGB: 32}},
	}
}

// MySQLFlexibleDatabase mirrors azurerm_mysql_flexible_database, which
// (unlike azurerm_postgresql_flexible_server_database's server_id)
// identifies its parent server by resource_group_name + server_name,
// not a single reference attribute -- confirmed against the real
// provider schema after the same terraform validate failure that found
// MySQLFlexibleServer's storage_mb bug above.
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

// PrivateDnsZone mirrors PrivateDnsZone: a server on a delegated subnet is
// unreachable by name without one, and Azure refuses to create the server
// otherwise (EmptyPrivateDnsZoneArmResourceId).
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

// PrivateEndpoint mirrors azurerm_private_endpoint. Added 2026-08-08 (see
// docs/azure-aws-parity-todo.md's Priority 3 item on Redis/Blob private
// networking) for Azure Managed Redis: unlike PostgreSQL/MySQL Flexible
// Server (which take a delegated_subnet_id/private_dns_zone_id directly
// on the server resource itself), Managed Redis's private connectivity
// is a genuinely separate resource -- azurerm_managed_redis has no
// networking-related attributes/blocks at all beyond public_network_access
// (confirmed against the real provider schema). A private endpoint
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
// which private DNS zone(s) get an A-record for this endpoint's IP,
// so the resource's own FQDN resolves privately from inside the VNet
// without any application-level configuration change.
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
	// RBAC mode, not the classic access-policy model: every consumer of
	// a secret this Key Vault holds (see azure/permissions.go) is granted
	// access via azurerm_role_assignment, the same primitive used for
	// storage access -- one access-control mechanism, not two.
	return KeyVault{SkuName: "standard", SoftDeleteRetentionDays: 7, RbacAuthorizationEnabled: true}
}

// KeyVaultSecret mirrors KeyVaultSecret. Lifecycle defaults to ignoring
// "value" so the secret's value never shows in Terraform's own plan/apply
// output.
type KeyVaultSecret struct {
	Name       string              `json:"name"`
	KeyVaultID string              `json:"key_vault_id"`
	Value      string              `json:"value"`
	Lifecycle  map[string][]string `json:"lifecycle"`
}

func NewKeyVaultSecret() KeyVaultSecret {
	return KeyVaultSecret{Lifecycle: map[string][]string{"ignore_changes": {"value"}}}
}

// UserAssignedIdentity is created once per app that has any service
// consuming a managed-service credential (database/cache password,
// storage access), so it can be granted RoleAssignments *before* any
// Container App exists to reference it -- see azure/permissions.go's
// inferManagedServiceIdentity for why this must be user-assigned rather
// than the system-assigned identity Container Apps would otherwise use
// by default: a system-assigned identity's principal_id doesn't exist
// until the resource that owns it is created, so it can't be granted a
// role before that resource's own creation, but that resource's creation
// is exactly when the role is needed (to resolve a Key Vault secret
// reference). A pre-created, standalone user-assigned identity has no
// such ordering cycle.
type UserAssignedIdentity struct {
	Name              string            `json:"name"`
	ResourceGroupName string            `json:"resource_group_name"`
	Location          string            `json:"location"`
	Tags              map[string]string `json:"tags,omitempty"`
}

// RoleAssignment grants a scoped Azure RBAC role to a principal (here,
// always a UserAssignedIdentity's principal_id), mirroring
// aws/permissions.go's IamRolePolicy attachments. Used for Key Vault
// Secrets User (reading managed-service credentials) and Storage Blob
// Data Contributor (object-storage access) -- see
// azure/permissions.go.
type RoleAssignment struct {
	Scope              string `json:"scope"`
	RoleDefinitionName string `json:"role_definition_name"`
	PrincipalID        string `json:"principal_id"`
}

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

// FrontDoorProfile mirrors FrontDoorProfile: the top-level container for an
// endpoint, origin groups and origins. Replaces CdnProfile/CdnEndpoint
// (Azure CDN from Microsoft, classic), which no longer accepts new
// profiles. Has no Location field: Front Door is a global resource, unlike
// everything else this inference creates.
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
	Name                  string         `json:"name"`
	CdnFrontdoorProfileID string         `json:"cdn_frontdoor_profile_id"`
	LoadBalancing         map[string]any `json:"load_balancing"`
	HealthProbe           map[string]any `json:"health_probe,omitempty"`
}

// FrontDoorOrigin mirrors FrontDoorOrigin: the backend Front Door forwards
// traffic to -- a Container App's ingress FQDN, in this codebase's case.
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

// FrontDoorRoute mirrors FrontDoorRoute: ties an endpoint to an origin
// group and says which request paths and protocols reach it.
// CdnFrontdoorOriginIds is not sent to the Azure API -- Terraform uses it
// only to order creation and destruction against the FrontDoorOrigin(s) it
// lists, since the API itself infers origins from the origin group.
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

// ManagedRedis mirrors ManagedRedis: replaces Azure Cache for Redis
// (azurerm_redis_cache), which no longer accepts new instances. Only
// azurerm 4.x exposes this; the 3.x alternative,
// azurerm_redis_enterprise_cluster, rejects the Balanced SKUs outright and
// starts at Enterprise_E5. The connection details live on the nested
// DefaultDatabase block rather than on the cluster: port and
// primary_access_key both hang off it.
type ManagedRedis struct {
	Name                    string           `json:"name"`
	ResourceGroupName       string           `json:"resource_group_name"`
	Location                string           `json:"location"`
	SkuName                 string           `json:"sku_name"`
	HighAvailabilityEnabled bool             `json:"high_availability_enabled"`
	DefaultDatabase         []map[string]any `json:"default_database"`

	// PublicNetworkAccess is a string ("Enabled"/"Disabled"), matching
	// azurerm_managed_redis's own attribute name/type exactly -- unlike
	// the two Flexible Server resources, Managed Redis has only ever had
	// one shape for this (no bool-vs-string inconsistency to account
	// for here, since Managed Redis didn't exist as a *cloudcompose* target
	// before this field was added, 2026-08-08). Omitted (nil) when
	// public access is genuinely wanted, since the provider's own
	// default is already "Enabled" -- only set explicitly to "Disabled"
	// once a private endpoint exists, mirroring MySQLFlexibleServer's
	// own "only set when it deviates from the default" convention.
	PublicNetworkAccess *string `json:"public_network_access,omitempty"`

	Tags map[string]string `json:"tags,omitempty"`
}

// NewManagedRedis returns a ManagedRedis with the default_database default
// reproduced: a single-element list containing a 3-key dict in this exact
// order.
//
// Managed Redis can require Entra ID auth instead; this application wires
// a password into containers, so the access keys have to be available --
// confirmed as the deliberate reason for AccessKeysAuthenticationEnabled
// being true, not an oversight.
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
// supports, mirroring AzureResources.
type AzureResources struct {
	ContainerApp                     map[string]ContainerApp                     `json:"azurerm_container_app,omitempty"`
	ContainerAppJob                  map[string]ContainerAppJob                  `json:"azurerm_container_app_job,omitempty"`
	ContainerAppEnvironment          map[string]ContainerAppEnvironment          `json:"azurerm_container_app_environment,omitempty"`
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
	ManagedRedis                     map[string]ManagedRedis                     `json:"azurerm_managed_redis,omitempty"`
	StorageAccount                   map[string]StorageAccount                   `json:"azurerm_storage_account,omitempty"`
	StorageContainer                 map[string]StorageContainer                 `json:"azurerm_storage_container,omitempty"`
	CdnFrontdoorProfile              map[string]FrontDoorProfile                 `json:"azurerm_cdn_frontdoor_profile,omitempty"`
	CdnFrontdoorEndpoint             map[string]FrontDoorEndpoint                `json:"azurerm_cdn_frontdoor_endpoint,omitempty"`
	CdnFrontdoorOriginGroup          map[string]FrontDoorOriginGroup             `json:"azurerm_cdn_frontdoor_origin_group,omitempty"`
	CdnFrontdoorOrigin               map[string]FrontDoorOrigin                  `json:"azurerm_cdn_frontdoor_origin,omitempty"`
	CdnFrontdoorRoute                map[string]FrontDoorRoute                   `json:"azurerm_cdn_frontdoor_route,omitempty"`

	// Docker provider resources (same models as AWS: build locally, push
	// to ACR instead of ECR). See handleBuildContext in
	// compiler/azure_compute.go for how these get populated.
	DockerImage         map[string]DockerImage         `json:"docker_image,omitempty"`
	DockerRegistryImage map[string]DockerRegistryImage `json:"docker_registry_image,omitempty"`

	// Random resources for passwords. Typed as RandomPassword directly,
	// since every call site assigns a RandomPassword instance in
	// practice -- confirmed by grepping every resources.RandomPassword[...]
	// assignment in compiler/azure/managed.go.
	RandomPassword map[string]RandomPassword `json:"random_password,omitempty"`
}

// NewAzureResources returns an AzureResources with every map initialized,
// so inference functions can assign into resources.Foo[key] without a
// nil-map panic. Empty maps are still omitted from JSON output (see struct
// tags).
func NewAzureResources() *AzureResources {
	return &AzureResources{
		ContainerApp:                     map[string]ContainerApp{},
		ContainerAppJob:                  map[string]ContainerAppJob{},
		ContainerAppEnvironment:          map[string]ContainerAppEnvironment{},
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
		ManagedRedis:                     map[string]ManagedRedis{},
		StorageAccount:                   map[string]StorageAccount{},
		StorageContainer:                 map[string]StorageContainer{},
		CdnFrontdoorProfile:              map[string]FrontDoorProfile{},
		CdnFrontdoorEndpoint:             map[string]FrontDoorEndpoint{},
		CdnFrontdoorOriginGroup:          map[string]FrontDoorOriginGroup{},
		CdnFrontdoorOrigin:               map[string]FrontDoorOrigin{},
		CdnFrontdoorRoute:                map[string]FrontDoorRoute{},
		DockerImage:                      map[string]DockerImage{},
		DockerRegistryImage:              map[string]DockerRegistryImage{},
		RandomPassword:                   map[string]RandomPassword{},
	}
}
