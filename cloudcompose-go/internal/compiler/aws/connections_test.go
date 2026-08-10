package aws

import (
	"testing"

	"github.com/gecburton/cloudcompose/internal/models"
)

// ResolveValue itself, and its own tests, moved to
// internal/compiler/shared/connections.go +
// internal/compiler/shared/connections_test.go once Azure needed the
// identical substitution (docs/azure-aws-parity-todo.md's "generalize
// Azure's connection-string rendering" item). DefaultPort stayed here:
// it's unused outside this package today and is about security-group
// port rules, an AWS-specific concept with no Azure equivalent to share
// it with.

func TestDefaultPort_PrefersTheConnection(t *testing.T) {
	t.Parallel()
	intPtr := func(i int) *int { return &i }
	db := models.Connection{Host: "h", Port: intPtr(5432)}
	bucket := models.Connection{Host: "h2"} // declares no port

	if got := DefaultPort(&db, 80); got != 5432 {
		t.Errorf("got %d, want 5432", got)
	}
	if got := DefaultPort(&bucket, 443); got != 443 {
		t.Errorf("got %d, want 443", got)
	}
	if got := DefaultPort(nil, 8080); got != 8080 {
		t.Errorf("got %d, want 8080", got)
	}
}
