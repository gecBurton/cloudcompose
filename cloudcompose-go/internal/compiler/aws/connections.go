package aws

import (
	"github.com/gecburton/cloudcompose/internal/models"
)

// DefaultPort is the port a client reaches a service on, for firewall-style
// rules.
func DefaultPort(connection *models.Connection, fallback int) int {
	if connection == nil || connection.Port == nil {
		return fallback
	}
	return *connection.Port
}
