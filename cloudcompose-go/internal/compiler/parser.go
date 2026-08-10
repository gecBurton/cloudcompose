package compiler

import (
	"github.com/gecburton/cloudcompose/internal/compiler/shared"
	"github.com/gecburton/cloudcompose/internal/models"
)

// ParseCompose parses a Docker Compose file using compose-go. Thin
// re-export of shared.ParseCompose: the implementation lives in the
// cloud-agnostic shared package so aws/azure/gcp package tests (which
// exercise real compose fixtures through the parser -> normalizer
// boundary) can call it without an import cycle back to this root
// orchestration package.
func ParseCompose(filePath string) (*models.ComposeApplication, error) {
	return shared.ParseCompose(filePath)
}

// ParseComposeJSON parses a compose file and returns JSON output.
func ParseComposeJSON(filePath string) (string, error) {
	return shared.ParseComposeJSON(filePath)
}

// Normalize transforms a parsed Compose application into the
// cloud-agnostic semantic model. Thin re-export of shared.Normalize, for
// the same reason as ParseCompose above.
func Normalize(composeApp *models.ComposeApplication, projectName string) (*models.Application, error) {
	return shared.Normalize(composeApp, projectName)
}

// SemanticToJSON renders the semantic model as JSON.
func SemanticToJSON(app *models.Application) (string, error) {
	return shared.SemanticToJSON(app)
}
