package shared

import (
	"github.com/gecburton/composey/internal/models"
)

// --- helpers -----------------------------------------------------------

func strPtr(s string) *string { return &s }

func serviceNames(services []models.Service) []string {
	names := make([]string, len(services))
	for i, s := range services {
		names[i] = s.Name
	}
	return names
}

func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
