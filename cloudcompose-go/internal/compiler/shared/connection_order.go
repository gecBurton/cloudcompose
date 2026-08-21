package shared

import "github.com/gecburton/cloudcompose/internal/models"

// ConnectionOrder returns connection keys in a fixed, deterministic order:
// every database-capability service first (in app.Services declaration
// order), then cache-capability, then object-storage-capability, each
// filtered to services that actually have an entry in connections.
//
// Go map iteration order is randomized, so callers building a container's
// env vars from connections need a stable order; the database-then-cache-
// then-storage grouping is deliberate, not just any deterministic order.
func ConnectionOrder(app *models.Application, connections map[string]models.Connection) []string {
	order := make([]string, 0, len(connections))
	for _, capability := range []models.Capability{
		models.CapabilityDatabase,
		models.CapabilityCache,
		models.CapabilityObjectStorage,
	} {
		for i := range app.Services {
			name := app.Services[i].Name
			if app.Services[i].Capability != capability {
				continue
			}
			if _, ok := connections[name]; ok {
				order = append(order, name)
			}
		}
	}
	return order
}
