package compiler

import (
	"github.com/gecburton/cloudcompose/internal/compiler/shared"
	"github.com/gecburton/cloudcompose/internal/models"
)

// ParseCompose parses a Docker Compose file using compose-go.
func ParseCompose(filePath string) (*models.ComposeApplication, error) {
	return shared.ParseCompose(filePath)
}

// ParseComposeJSON parses a compose file and returns JSON output.
func ParseComposeJSON(filePath string) (string, error) {
	return shared.ParseComposeJSON(filePath)
}

// Normalize transforms a parsed Compose application into the
// cloud-agnostic semantic model.
func Normalize(composeApp *models.ComposeApplication, projectName string) (*models.Application, error) {
	return shared.Normalize(composeApp, projectName)
}

// SemanticToJSON renders the semantic model as JSON.
func SemanticToJSON(app *models.Application) (string, error) {
	return shared.SemanticToJSON(app)
}
