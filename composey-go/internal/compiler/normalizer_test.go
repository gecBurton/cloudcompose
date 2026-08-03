package compiler

import (
	"testing"

	"github.com/gecburton/composey/internal/models"
)

func TestNormalizeFlaskExample(t *testing.T) {
	composeApp, err := ParseCompose("../../examples/flask/compose.yml")
	if err != nil {
		t.Fatalf("ParseCompose failed: %v", err)
	}

	semanticApp, err := Normalize(composeApp, "composey")
	if err != nil {
		t.Fatalf("Normalize failed: %v", err)
	}

	// Verify basic structure
	if semanticApp.Name != "composey" {
		t.Errorf("Expected name 'composey', got '%s'", semanticApp.Name)
	}

	if len(semanticApp.Services) != 3 {
		t.Errorf("Expected 3 services, got %d", len(semanticApp.Services))
	}

	// Find and verify database service
	var dbService *models.Service
	for i := range semanticApp.Services {
		if semanticApp.Services[i].Name == "db" {
			dbService = &semanticApp.Services[i]
			break
		}
	}

	if dbService == nil {
		t.Fatal("Database service not found")
	}

	if dbService.Capability != models.CapabilityDatabase {
		t.Errorf("Expected capability 'database', got '%s'", dbService.Capability)
	}

	if dbService.DatabaseName == nil || *dbService.DatabaseName != "example" {
		t.Errorf("Expected database_name 'example', got %v", dbService.DatabaseName)
	}

	if dbService.Image != "mariadb:10.6.4-focal" {
		t.Errorf("Expected image 'mariadb:10.6.4-focal', got '%s'", dbService.Image)
	}

	// Verify relationships
	if len(semanticApp.Relationships) != 2 {
		t.Errorf("Expected 2 relationships, got %d", len(semanticApp.Relationships))
	}
}

func TestNormalizeAllExamples(t *testing.T) {
	examples := []string{
		"../../examples/flask/compose.yml",
		"../../examples/flask-redis/compose.yml",
		"../../examples/minio-s3/compose.yml",
		"../../examples/hello/compose.yml",
		"../../examples/scaling/compose.yml",
	}

	for _, example := range examples {
		t.Run(example, func(t *testing.T) {
			composeApp, err := ParseCompose(example)
			if err != nil {
				t.Fatalf("ParseCompose failed: %v", err)
			}

			semanticApp, err := Normalize(composeApp, "composey")
			if err != nil {
				t.Fatalf("Normalize failed: %v", err)
			}

			if semanticApp.Name != "composey" {
				t.Errorf("Expected name 'composey', got '%s'", semanticApp.Name)
			}

			if len(semanticApp.Services) == 0 {
				t.Error("Expected at least one service")
			}

			// Validate all services
			for i := range semanticApp.Services {
				if err := semanticApp.Services[i].Validate(); err != nil {
					t.Errorf("Service validation failed: %v", err)
				}
			}
		})
	}
}

func TestCapabilityDetection(t *testing.T) {
	testCases := []struct {
		image    string
		expected models.Capability
	}{
		{"postgres:16", models.CapabilityDatabase},
		{"redis:7", models.CapabilityCache},
		{"minio/minio", models.CapabilityObjectStorage},
		{"nginx:latest", models.CapabilityContainer},
		{"flask-redis-web:latest", models.CapabilityContainer},
	}

	for _, tc := range testCases {
		result := InferCapability(tc.image)
		if result != string(tc.expected) {
			t.Errorf("InferCapability(%s) = %s, want %s", tc.image, result, tc.expected)
		}
	}
}

func TestDatabaseNameDerivation(t *testing.T) {
	tests := []struct {
		name        string
		appName     string
		serviceName string
		env         map[string]string
		expected    string
	}{
		{
			name:        "from env var",
			appName:     "myapp",
			serviceName: "db",
			env:         map[string]string{"POSTGRES_DB": "mydb"},
			expected:    "mydb",
		},
		{
			name:        "compound name",
			appName:     "myapp",
			serviceName: "db",
			env:         map[string]string{},
			expected:    "myapp_db",
		},
		{
			name:        "service name only",
			appName:     "",
			serviceName: "database",
			env:         map[string]string{},
			expected:    "database",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := DatabaseName(tc.appName, tc.serviceName, tc.env)
			if result != tc.expected {
				t.Errorf("DatabaseName() = %s, want %s", result, tc.expected)
			}
		})
	}
}

func TestScheduleParsing(t *testing.T) {
	tests := []struct {
		input    string
		kind     string
		hasError bool
	}{
		{"every 1 hour", "rate", false},
		{"rate(1 hour)", "rate", false},
		{"0 2 * * *", "cron", false},
		{"cron(0 2 * * *)", "cron", false},
		{"invalid", "", true},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			schedule, err := ParseSchedule(tc.input)
			if tc.hasError {
				if err == nil {
					t.Error("Expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}

			switch s := schedule.(type) {
			case *models.RateSchedule:
				if string(s.Kind) != tc.kind {
					t.Errorf("Expected kind %s, got %s", tc.kind, s.Kind)
				}
			case *models.CronSchedule:
				if string(s.Kind) != tc.kind {
					t.Errorf("Expected kind %s, got %s", tc.kind, s.Kind)
				}
			}
		})
	}
}
