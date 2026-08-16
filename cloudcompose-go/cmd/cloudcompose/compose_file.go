package main

import (
	"github.com/gecburton/cloudcompose/internal/compiler/shared"
)

// resolveComposeFile returns composeFile unchanged if the user gave one
// explicitly via -f/--file; otherwise it searches the current directory
// for a compose file using the same filename precedence `docker compose`
// itself uses (see shared.FindComposeFile), so -f is only required when
// there's genuine ambiguity, matching the local `docker compose`
// experience these commands otherwise mirror.
func resolveComposeFile(composeFile string) (string, error) {
	if composeFile != "" {
		return composeFile, nil
	}
	return shared.FindComposeFile(".")
}
