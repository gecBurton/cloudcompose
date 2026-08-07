package aws

import (
	"crypto/sha256"
	"encoding/binary"
	"sort"
	"strings"

	"github.com/gecburton/composey/internal/compiler/shared"
	"github.com/gecburton/composey/internal/models"
)

// SafeTerraformIdentifier converts a name to a Terraform-safe identifier
// fragment, mirroring _common.py's safe_terraform_identifier: every
// character that isn't alphanumeric becomes an underscore, and leading/
// trailing underscores are trimmed.
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

// NamespaceFor creates a private DNS zone name for this application,
// mirroring _common.py's namespace_for. Scoped by environment and
// application because Cloud Map namespaces are unique per VPC, and several
// applications routinely share one.
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

// priorityBand calculates the priority band for an application, mirroring
// _common.py's _priority_band: a stable hash of the app name selects one of
// shared.PriorityBands bands, each shared.BandWidth wide.
func priorityBand(appName string) int {
	digest := sha256.Sum256([]byte(appName))
	n := binary.BigEndian.Uint32(digest[:4])
	return 1 + int(n%uint32(shared.PriorityBands))*shared.BandWidth
}

// CalculateListenerPriorities assigns each public service a stable, unique
// listener rule priority, mirroring _common.py's calculate_listener_
// priorities. Listener rule priorities must be unique across every
// application sharing a listener, so they cannot simply start at 1: each
// application gets a band derived from its name, and its routes are ordered
// within that band by path specificity, longest first, so that /api/admin
// is matched before /api.
//
// AWS-specific: GCP URL Maps use path specificity order, not numeric
// priority, so this lives in the AWS-specific inference package rather than
// anywhere shared across clouds.
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
			return len(pathI) > len(pathJ) // longest first
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
// beneath it, mirroring _common.py's path_patterns.
func PathPatterns(path string) []string {
	if path == "/" {
		return []string{"/*"}
	}
	trimmed := strings.TrimRight(path, "/")
	return []string{trimmed, trimmed + "/*"}
}
