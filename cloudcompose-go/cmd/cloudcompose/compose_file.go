package main

import (
	"github.com/gecburton/cloudcompose/internal/compiler/shared"
)

// resolveComposeFile returns composeFile unchanged if explicitly given
// via -f/--file; otherwise it searches the current directory for a
// compose file using the same precedence `docker compose` uses.
func resolveComposeFile(composeFile string) (string, error) {
	if composeFile != "" {
		return composeFile, nil
	}
	return shared.FindComposeFile(".")
}
