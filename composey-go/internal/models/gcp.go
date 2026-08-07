package models

// GCP resource models mirroring composey/models/gcp.py.
//
// Field names and JSON tags match the Python Pydantic models exactly,
// since both marshal straight into Terraform's JSON syntax.
//
// Unlike AWS/Azure, this port has essentially no test surface to verify
// against (no golden examples, no dedicated unit tests existed in Python
// either -- only 4 smoke tests via `composey init` scaffolding). Ported
// directly from the Python source with light sanity-checking against live
// Python output for a couple of hand-built cases, not the exhaustive
// golden-example/coverage-survey discipline AWS and Azure got, since there
// is no equivalent verification surface to check against or survey.

type CloudRunService struct {
	Name      string `json:"name"`
	Location  string `json:"location"`
	ProjectID string `json:"project_id"`

	Template  any `json:"template,omitempty"`
	Traffic   any `json:"traffic,omitempty"`
	VpcAccess any `json:"vpc_access,omitempty"`

	AutogenerateRevisionName bool     `json:"autogenerate_revision_name"`
	Ingress                  string   `json:"ingress"`
	DependsOn                []string `json:"depends_on,omitempty"`
}

func NewCloudRunService() CloudRunService {
	return CloudRunService{AutogenerateRevisionName: true, Ingress: "all"}
}

type CloudRunServiceIamMember struct {
	Service   string `json:"service"`
	Location  string `json:"location"`
	ProjectID string `json:"project_id"`
	Role      string `json:"role"`
	Member    string `json:"member"`
}

type CloudSqlInstance struct {
	Name      string `json:"name"`
	ProjectID string `json:"project_id"`
	Region    string `json:"region"`

	DatabaseVersion string `json:"database_version"`
	Tier            string `json:"tier"`

	StorageAutoResize      bool `json:"storage_auto_resize"`
	StorageAutoResizeLimit int  `json:"storage_auto_resize_limit"`

	AvailabilityType string `json:"availability_type"`

	BackupConfiguration any `json:"backup_configuration,omitempty"`
	IpConfiguration     any `json:"ip_configuration"`

	RootPassword *string `json:"root_password,omitempty"`
	DatabaseName *string `json:"database_name,omitempty"`
}

// NewCloudSqlInstance returns a CloudSqlInstance with Python's own
// Pydantic scalar defaults reproduced. BackupConfiguration/IpConfiguration
// (Python's two default_factory dicts) are left nil here and populated by
// the compiler package's inference code instead, which builds them as
// compiler.PyOrdered to preserve Python's dict-literal key order --
// models cannot import compiler (would be an import cycle), so the
// ordered-dict construction for any field on this file's structs that
// needs one lives in azure_managed.go-style inference code, not here.
func NewCloudSqlInstance() CloudSqlInstance {
	return CloudSqlInstance{
		DatabaseVersion:        "POSTGRES_14",
		Tier:                   "db-f1-micro",
		StorageAutoResize:      true,
		StorageAutoResizeLimit: 100,
		AvailabilityType:       "ZONAL",
	}
}

type CloudSqlDatabase struct {
	Name      string `json:"name"`
	Instance  string `json:"instance"`
	ProjectID string `json:"project_id"`
}

type RedisInstance struct {
	Name      string `json:"name"`
	ProjectID string `json:"project_id"`
	Region    string `json:"region"`

	Tier         string `json:"tier"`
	MemorySizeGb int    `json:"memory_size_gb"`
	RedisVersion string `json:"redis_version"`

	AuthorizedNetwork *string `json:"authorized_network,omitempty"`
	ConnectMode       string  `json:"connect_mode"`
}

func NewRedisInstance() RedisInstance {
	return RedisInstance{
		Tier:         "BASIC",
		MemorySizeGb: 1,
		RedisVersion: "REDIS_6_X",
		ConnectMode:  "DIRECT_PEERING",
	}
}

type StorageBucket struct {
	Name      string `json:"name"`
	ProjectID string `json:"project_id"`
	Location  string `json:"location"`

	StorageClass string `json:"storage_class"`
	Versioning   any    `json:"versioning"`

	LifecycleRule []map[string]any `json:"lifecycle_rule,omitempty"`

	UniformBucketLevelAccess bool `json:"uniform_bucket_level_access"`
	ForceDestroy             bool `json:"force_destroy"`
}

func NewStorageBucket() StorageBucket {
	return StorageBucket{
		Location:                 "US",
		StorageClass:             "STANDARD",
		UniformBucketLevelAccess: true,
	}
}

type StorageBucketIamMember struct {
	Bucket string `json:"bucket"`
	Role   string `json:"role"`
	Member string `json:"member"`
}

type VpcConnector struct {
	Name      string `json:"name"`
	ProjectID string `json:"project_id"`
	Region    string `json:"region"`

	Network     string `json:"network"`
	IpCidrRange string `json:"ip_cidr_range"`
	MachineType string `json:"machine_type"`

	MinInstances int `json:"min_instances"`
	MaxInstances int `json:"max_instances"`
}

func NewVpcConnector() VpcConnector {
	return VpcConnector{
		Network:      "default",
		IpCidrRange:  "10.8.0.0/28",
		MachineType:  "f1-micro",
		MinInstances: 2,
		MaxInstances: 10,
	}
}

type GlobalAddress struct {
	Name      string `json:"name"`
	ProjectID string `json:"project_id"`

	AddressType string `json:"address_type"`
	IpVersion   string `json:"ip_version"`
}

func NewGlobalAddress() GlobalAddress {
	return GlobalAddress{AddressType: "EXTERNAL", IpVersion: "IPV4"}
}

type ComputeManagedSslCertificate struct {
	Name      string `json:"name"`
	ProjectID string `json:"project_id"`

	Managed any `json:"managed"`
}

func NewComputeManagedSslCertificate() ComputeManagedSslCertificate {
	return ComputeManagedSslCertificate{}
}

type ComputeUrlMap struct {
	Name      string `json:"name"`
	ProjectID string `json:"project_id"`

	DefaultService string           `json:"default_service"`
	HostRule       []map[string]any `json:"host_rule,omitempty"`
	PathMatcher    []map[string]any `json:"path_matcher,omitempty"`
}

type ComputeBackendService struct {
	Name      string `json:"name"`
	ProjectID string `json:"project_id"`

	Backend []map[string]any `json:"backend"`
}

type ComputeTargetHttpsProxy struct {
	Name      string `json:"name"`
	ProjectID string `json:"project_id"`

	UrlMap          string   `json:"url_map"`
	SslCertificates []string `json:"ssl_certificates"`
}

type ComputeForwardingRule struct {
	Name      string `json:"name"`
	ProjectID string `json:"project_id"`

	Target    string  `json:"target"`
	IpAddress *string `json:"ip_address,omitempty"`
	PortRange string  `json:"port_range"`
}

func NewComputeForwardingRule() ComputeForwardingRule {
	return ComputeForwardingRule{PortRange: "443"}
}

type ComputeRegionNetworkEndpointGroup struct {
	Name      string `json:"name"`
	ProjectID string `json:"project_id"`
	Region    string `json:"region"`

	NetworkEndpointType string            `json:"network_endpoint_type"`
	CloudRun            map[string]string `json:"cloud_run"`
}

func NewComputeRegionNetworkEndpointGroup() ComputeRegionNetworkEndpointGroup {
	return ComputeRegionNetworkEndpointGroup{NetworkEndpointType: "SERVERLESS"}
}

type SecretManagerSecret struct {
	SecretID  string `json:"secret_id"`
	ProjectID string `json:"project_id"`

	Replication any `json:"replication"`
}

func NewSecretManagerSecret() SecretManagerSecret {
	return SecretManagerSecret{}
}

type SecretManagerSecretVersion struct {
	Secret     string `json:"secret"`
	SecretData string `json:"secret_data"`
}

// GcpResources is a registry of the GCP resources the compiler supports,
// mirroring GcpResources.
type GcpResources struct {
	CloudRunService          map[string]CloudRunService          `json:"google_cloud_run_service,omitempty"`
	CloudRunServiceIamMember map[string]CloudRunServiceIamMember `json:"google_cloud_run_service_iam_member,omitempty"`

	SqlDatabaseInstance map[string]CloudSqlInstance `json:"google_sql_database_instance,omitempty"`
	SqlDatabase         map[string]CloudSqlDatabase `json:"google_sql_database,omitempty"`

	RedisInstance map[string]RedisInstance `json:"google_redis_instance,omitempty"`

	StorageBucket          map[string]StorageBucket          `json:"google_storage_bucket,omitempty"`
	StorageBucketIamMember map[string]StorageBucketIamMember `json:"google_storage_bucket_iam_member,omitempty"`

	VpcAccessConnector map[string]VpcConnector `json:"google_vpc_access_connector,omitempty"`

	ComputeGlobalAddress              map[string]GlobalAddress                     `json:"google_compute_global_address,omitempty"`
	ComputeManagedSslCertificate      map[string]ComputeManagedSslCertificate      `json:"google_compute_managed_ssl_certificate,omitempty"`
	ComputeRegionNetworkEndpointGroup map[string]ComputeRegionNetworkEndpointGroup `json:"google_compute_region_network_endpoint_group,omitempty"`
	ComputeBackendService             map[string]ComputeBackendService             `json:"google_compute_backend_service,omitempty"`
	ComputeUrlMap                     map[string]ComputeUrlMap                     `json:"google_compute_url_map,omitempty"`
	ComputeTargetHttpsProxy           map[string]ComputeTargetHttpsProxy           `json:"google_compute_target_https_proxy,omitempty"`
	ComputeForwardingRule             map[string]ComputeForwardingRule             `json:"google_compute_forwarding_rule,omitempty"`

	SecretManagerSecret        map[string]SecretManagerSecret        `json:"google_secret_manager_secret,omitempty"`
	SecretManagerSecretVersion map[string]SecretManagerSecretVersion `json:"google_secret_manager_secret_version,omitempty"`

	DockerImage         map[string]DockerImage         `json:"docker_image,omitempty"`
	DockerRegistryImage map[string]DockerRegistryImage `json:"docker_registry_image,omitempty"`

	// Typed as `any`, not RandomPassword: Python's own field is
	// Dict[str, Any], and _infer_databases assigns a bare
	// {"length": 20} dict literal, not a RandomPassword model instance
	// -- so it carries no "special" key at all, unlike AWS/Azure's own
	// random_password resources, which do assign a real RandomPassword
	// model. Confirmed by reading _infer_databases directly, not assumed
	// from the AWS/Azure pattern (which would have been wrong here).
	RandomPassword map[string]any `json:"random_password,omitempty"`
}

// NewGcpResources returns a GcpResources with every map initialized.
func NewGcpResources() *GcpResources {
	return &GcpResources{
		CloudRunService:                   map[string]CloudRunService{},
		CloudRunServiceIamMember:          map[string]CloudRunServiceIamMember{},
		SqlDatabaseInstance:               map[string]CloudSqlInstance{},
		SqlDatabase:                       map[string]CloudSqlDatabase{},
		RedisInstance:                     map[string]RedisInstance{},
		StorageBucket:                     map[string]StorageBucket{},
		StorageBucketIamMember:            map[string]StorageBucketIamMember{},
		VpcAccessConnector:                map[string]VpcConnector{},
		ComputeGlobalAddress:              map[string]GlobalAddress{},
		ComputeManagedSslCertificate:      map[string]ComputeManagedSslCertificate{},
		ComputeRegionNetworkEndpointGroup: map[string]ComputeRegionNetworkEndpointGroup{},
		ComputeBackendService:             map[string]ComputeBackendService{},
		ComputeUrlMap:                     map[string]ComputeUrlMap{},
		ComputeTargetHttpsProxy:           map[string]ComputeTargetHttpsProxy{},
		ComputeForwardingRule:             map[string]ComputeForwardingRule{},
		SecretManagerSecret:               map[string]SecretManagerSecret{},
		SecretManagerSecretVersion:        map[string]SecretManagerSecretVersion{},
		DockerImage:                       map[string]DockerImage{},
		DockerRegistryImage:               map[string]DockerRegistryImage{},
		RandomPassword:                    map[string]any{},
	}
}
