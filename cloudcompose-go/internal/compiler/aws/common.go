package aws

import (
	"crypto/sha256"
	"encoding/binary"
	"sort"
	"strings"

	"github.com/gecburton/cloudcompose/internal/compiler/shared"
	"github.com/gecburton/cloudcompose/internal/models"
)

// SafeTerraformIdentifier converts a name to a Terraform-safe identifier
// fragment: every character that isn't alphanumeric becomes an underscore,
// and leading/trailing underscores are trimmed.
func SafeTerraformIdentifier(name string) string {
	var b strings.Builder
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return strings.Trim(b.String(), "_")
}

// NamespaceFor creates a private DNS zone name for this application.
// Scoped by environment and application because Cloud Map namespaces are
// unique per VPC, and several applications routinely share one.
func NamespaceFor(envName, appName string) string {
	parts := make([]string, 0, 2)
	if envName != "" {
		parts = append(parts, envName)
	}
	if appName != "" {
		parts = append(parts, appName)
	}
	label := strings.Join(parts, "-")

	var b strings.Builder
	for _, r := range label {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	trimmed := strings.Trim(b.String(), "-")
	return strings.ToLower(trimmed) + ".internal"
}

// priorityBand calculates the priority band for an application: a stable
// hash of the app name selects one of shared.PriorityBands bands, each
// shared.BandWidth wide.
func priorityBand(appName string) int {
	digest := sha256.Sum256([]byte(appName))
	n := binary.BigEndian.Uint32(digest[:4])
	return 1 + int(n%uint32(shared.PriorityBands))*shared.BandWidth
}

// CalculateListenerPriorities assigns each public service a stable, unique
// listener rule priority. Each application gets a priority band derived
// from its name, and its routes are ordered within that band by path
// specificity, longest first, so /api/admin is matched before /api.
func CalculateListenerPriorities(app *models.Application) map[string]int {
	band := priorityBand(app.Name)
	public := app.PublicServices()

	ordered := make([]models.Service, len(public))
	copy(ordered, public)
	sort.SliceStable(ordered, func(i, j int) bool {
		pathI, pathJ := "", ""
		if ordered[i].Ingress != nil {
			pathI = ordered[i].Ingress.Path
		}
		if ordered[j].Ingress != nil {
			pathJ = ordered[j].Ingress.Path
		}
		if len(pathI) != len(pathJ) {
			return len(pathI) > len(pathJ)
		}
		return ordered[i].Name < ordered[j].Name
	})

	priorities := make(map[string]int, len(ordered))
	for offset, service := range ordered {
		priorities[service.Name] = band + offset
	}
	return priorities
}

// PathPatterns generates ALB path patterns matching a prefix and everything
// beneath it.
func PathPatterns(path string) []string {
	if path == "/" {
		return []string{"/*"}
	}
	trimmed := strings.TrimRight(path, "/")
	return []string{trimmed, trimmed + "/*"}
}
