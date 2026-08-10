package azure

import (
	"testing"

	"github.com/gecburton/cloudcompose/internal/models"
)

// Tests for docs/azure-aws-parity-todo.md's Priority 4 backup/HA item:
// AzureEnvironment.HighAvailabilityEnabled/BackupRetentionDays wired
// into azurerm_postgresql_flexible_server/azurerm_mysql_flexible_server's
// high_availability/backup_retention_days.

func TestInferDatabasesAzure_HighAvailabilityDisabledByDefault(t *testing.T) {
	t.Parallel()
	app := &models.Application{
		Services: []models.Service{
			{Name: "db", Image: "postgres:16", Capability: models.CapabilityDatabase},
		},
	}
	env := mockAzureProdEnv()
	resources := models.NewAzureResources()
	inferDatabasesAzure(resources, app, &env, testGetNameAzure, nil)

	server := resources.PostgreSQLFlexibleServer["main"]
	if server.HighAvailability != nil {
		t.Errorf("expected HighAvailability = nil by default (HA doubles compute cost, so it's opt-in), got %v", server.HighAvailability)
	}
	if server.BackupRetentionDays != 7 {
		t.Errorf("BackupRetentionDays = %d, want 7 (the default both NewAwsEnvironment and NewAzureEnvironment share)", server.BackupRetentionDays)
	}
}

func TestInferDatabasesAzure_HighAvailabilityEnabledSetsZoneRedundant(t *testing.T) {
	t.Parallel()
	app := &models.Application{
		Services: []models.Service{
			{Name: "db", Image: "postgres:16", Capability: models.CapabilityDatabase},
		},
	}
	env := mockAzureProdEnv()
	env.HighAvailabilityEnabled = true
	env.BackupRetentionDays = 14
	resources := models.NewAzureResources()
	inferDatabasesAzure(resources, app, &env, testGetNameAzure, nil)

	server := resources.PostgreSQLFlexibleServer["main"]
	if server.HighAvailability == nil || server.HighAvailability["mode"] != "ZoneRedundant" {
		t.Errorf(`expected HighAvailability = {"mode": "ZoneRedundant"}, got %v`, server.HighAvailability)
	}
	if server.BackupRetentionDays != 14 {
		t.Errorf("BackupRetentionDays = %d, want 14", server.BackupRetentionDays)
	}
}

func TestInferDatabasesAzure_HighAvailabilityEnabledForMySQL(t *testing.T) {
	t.Parallel()
	app := &models.Application{
		Services: []models.Service{
			{Name: "db", Image: "mysql:8", Capability: models.CapabilityDatabase},
		},
	}
	env := mockAzureProdEnv()
	env.HighAvailabilityEnabled = true
	resources := models.NewAzureResources()
	inferDatabasesAzure(resources, app, &env, testGetNameAzure, nil)

	server := resources.MySQLFlexibleServer["main"]
	if server.HighAvailability == nil || server.HighAvailability["mode"] != "ZoneRedundant" {
		t.Errorf(`expected HighAvailability = {"mode": "ZoneRedundant"} on MySQL Flexible Server too, got %v`, server.HighAvailability)
	}
	if server.BackupRetentionDays != 7 {
		t.Errorf("BackupRetentionDays = %d, want 7", server.BackupRetentionDays)
	}
}

func TestHighAvailabilityAzure_ReturnsNilWhenDisabled(t *testing.T) {
	t.Parallel()
	env := &models.AzureEnvironment{HighAvailabilityEnabled: false}
	if got := highAvailabilityAzure(env); got != nil {
		t.Errorf("highAvailabilityAzure(disabled) = %v, want nil", got)
	}
}

func TestHighAvailabilityAzure_ReturnsZoneRedundantWhenEnabled(t *testing.T) {
	t.Parallel()
	env := &models.AzureEnvironment{HighAvailabilityEnabled: true}
	got := highAvailabilityAzure(env)
	if got == nil || got["mode"] != "ZoneRedundant" {
		t.Errorf(`highAvailabilityAzure(enabled) = %v, want {"mode": "ZoneRedundant"}`, got)
	}
}
