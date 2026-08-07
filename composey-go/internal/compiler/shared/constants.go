package shared

import "strings"

const (
	Version       = "0.1.0"
	VersionString = "composey " + Version + " (pre-alpha)"
)

const (
	DatabaseDefaultUsername = "composey"
	DatabaseNameMaxLength   = 63
)

var DatabaseNameVariables = []string{"POSTGRES_DB", "MYSQL_DATABASE", "MARIADB_DATABASE"}

const SecretsPlaceholderValue = "PLACEHOLDER_VALUE_CHANGE_IN_AWS_CONSOLE"

const (
	MaxNetworksPerService = 5
	DefaultNetworkName    = "default"
)

const (
	PriorityBands = 500
	BandWidth     = 100
)

const (
	CloudFrontScopeRegion   = "us-east-1"
	CloudFrontProviderAlias = "us_east_1"
	CloudFrontProviderRef   = "aws." + CloudFrontProviderAlias
	ALBDataSourceKey        = "shared_alb"
)

const (
	DefaultPortPostgres          = 5432
	DefaultPortMySQL             = 3306
	DefaultPortMariaDB           = 3306
	DefaultPortRedis             = 6379
	DefaultPortAzureManagedRedis = 10000
)

func DefaultPortForDatabase(engine string) int {
	engineLower := strings.ToLower(engine)
	if strings.Contains(engineLower, "mysql") {
		return DefaultPortMySQL
	}
	if strings.Contains(engineLower, "mariadb") {
		return DefaultPortMariaDB
	}
	return DefaultPortPostgres
}

type SizeMapping struct {
	CPU    int
	Memory int
}

var SizeMappings = map[string]SizeMapping{
	"small":  {CPU: 256, Memory: 512},
	"medium": {CPU: 1024, Memory: 2048},
	"large":  {CPU: 4096, Memory: 8192},
}

var DBInstanceClasses = map[string]string{
	"small":  "db.t3.micro",
	"medium": "db.t3.medium",
	"large":  "db.m5.large",
}

var CacheNodeTypes = map[string]string{
	"small":  "cache.t3.micro",
	"medium": "cache.t3.medium",
	"large":  "cache.m5.large",
}

var CapabilityImages = map[string][]string{
	"database": {
		"postgres",
		"postgresql",
		"pgvector",
		"postgis",
		"timescaledb",
		"mysql",
		"mariadb",
		"percona",
		"percona-server-mysql",
	},
	"cache": {
		"redis",
		"redismod",
		"redis-stack",
		"valkey",
		"keydb",
	},
	"object-storage": {
		"minio",
	},
}

var BindSourcePrefixes = []string{"/", "./", "../", "~"}

const (
	DefaultScheduleUnit     = "hours"
	DefaultLogRetentionDays = 7
)

const IAMPolicyVersion = "2012-10-17"

const AWSLogsStreamPrefix = "ecs"

const (
	AutoScalingCPUTarget    = 70.0
	AutoScalingMemoryTarget = 80.0
)
