package shared

import "github.com/gecburton/cloudcompose/internal/models"

// ConnectionOrder returns connection keys in a fixed, deterministic
// order: every database-capability service first (in app.Services
// declaration order), then every cache-capability service, then every
// object-storage-capability service, each filtered to services that
// actually have an entry in connections.
//
// This was previously the identical function connectionOrderForAzure/
// connectionOrderForGcp, hand-copied into azure/infer.go and
// gcp/infer.go (gcp/infer.go's own comment on this already noted "the
// same bug class Azure's implementation hit and fixed the same way" --
// evidence this should have been unified the first time, not copied a
// second). Both callers use the returned order to iterate a service's
// connections when building its container's own env vars: Go map
// iteration order is randomized, and -- per those callers' own
// comments -- even a *stable* but wrong order (e.g. plain alphabetical
// key sort) has caused real examples to get DB_URL/BLOBS_URL and
// CACHE_URL in the wrong relative position, so the database-then-cache-
// then-storage grouping here is deliberate, not just "any deterministic
// order will do."
//
// Not used by AWS: aws/permissions.go's own connectionOrderFor serves a
// different purpose (disambiguating shared.ResolveValue's pattern
// search across connections, not fixing container env-var iteration
// order) and uses plain declaration order rather than this
// capability-grouped one -- a real, deliberate difference, not an
// oversight left unmerged.
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
