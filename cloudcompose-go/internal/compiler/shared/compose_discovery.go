package shared

import (
	"fmt"
	"os"
	"path/filepath"
)

// ComposeFileCandidates is the filename precedence order used when no
// -f/--file is given, matching `docker compose`'s own default-file
// search order exactly (compose.yaml/compose.yml take precedence over
// the legacy docker-compose.yaml/docker-compose.yml names).
var ComposeFileCandidates = []string{
	"compose.yaml",
	"compose.yml",
	"docker-compose.yaml",
	"docker-compose.yml",
}

// FindComposeFile looks in dir for each of ComposeFileCandidates in
// order and returns the path to the first one that exists, mirroring
// `docker compose`'s own no-flag default-file behavior so cloudcompose
// only requires -f/--file when there's genuine ambiguity (more than one
// compose file, or a non-default name/location), not on every
// invocation.
func FindComposeFile(dir string) (string, error) {
	for _, name := range ComposeFileCandidates {
		candidate := filepath.Join(dir, name)
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}
	return "", fmt.Errorf(
		"no compose file found in %s (looked for %s) -- use -f/--file to specify one",
		dir, joinCandidates(),
	)
}

func joinCandidates() string {
	out := ""
	for i, name := range ComposeFileCandidates {
		if i > 0 {
			out += ", "
		}
		out += name
	}
	return out
}
