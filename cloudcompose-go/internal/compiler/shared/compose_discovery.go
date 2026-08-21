package shared

import (
	"fmt"
	"os"
	"path/filepath"
)

// ComposeFileCandidates is the filename precedence order used when no
// -f/--file is given, matching `docker compose`'s default file search
// order.
var ComposeFileCandidates = []string{
	"compose.yaml",
	"compose.yml",
	"docker-compose.yaml",
	"docker-compose.yml",
}

// FindComposeFile looks in dir for each of ComposeFileCandidates in
// order and returns the path to the first one that exists.
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
