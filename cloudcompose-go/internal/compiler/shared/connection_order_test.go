package shared

import (
	"testing"

	"github.com/gecburton/cloudcompose/internal/models"
)

// TestConnectionOrder_DatabasesThenCachesThenStorage pins that
// connections are ordered databases-then-caches-then-storage (each group
// in service declaration order): alphabetically sorting connection keys
// puts a URL env var in the wrong relative position whenever a service
// references more than one connection -- confirmed against real Azure
// and GCP examples before this function existed as shared code (see its
// own doc comment); moved here from azure's own connections_test.go
// once azure/infer.go and gcp/infer.go stopped keeping separate copies
// of this logic.
func TestConnectionOrder_DatabasesThenCachesThenStorage(t *testing.T) {
	t.Parallel()
	app := &models.Application{
		Name: "app",
		Services: []models.Service{
			{Name: "web", Capability: models.CapabilityContainer},
			{Name: "blobs", Capability: models.CapabilityObjectStorage},
			{Name: "db", Capability: models.CapabilityDatabase},
			{Name: "cache", Capability: models.CapabilityCache},
		},
	}
	connections := map[string]models.Connection{
		"blobs": {},
		"db":    {},
		"cache": {},
	}

	order := ConnectionOrder(app, connections)
	want := []string{"db", "cache", "blobs"}
	if len(order) != len(want) {
		t.Fatalf("got %v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Errorf("order[%d] = %q, want %q (full: %v)", i, order[i], want[i], order)
		}
	}
}

// TestConnectionOrder_FiltersToServicesWithConnections confirms a
// capability-matching service with no actual entry in connections is
// excluded from the returned order, not just deprioritized -- both
// callers (azure/infer.go, gcp/infer.go) rely on the returned slice
// being safe to iterate directly against connections without a
// presence check of their own.
func TestConnectionOrder_FiltersToServicesWithConnections(t *testing.T) {
	t.Parallel()
	app := &models.Application{
		Name: "app",
		Services: []models.Service{
			{Name: "db1", Capability: models.CapabilityDatabase},
			{Name: "db2", Capability: models.CapabilityDatabase},
		},
	}
	connections := map[string]models.Connection{
		"db1": {},
		// db2 deliberately has no connection entry, e.g. inference
		// decided not to substitute a managed service for it.
	}

	order := ConnectionOrder(app, connections)
	want := []string{"db1"}
	if len(order) != len(want) || order[0] != want[0] {
		t.Errorf("got %v, want %v", order, want)
	}
}
