package main

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"

	"github.com/gecburton/cloudcompose/internal/compiler/aws"
)

func TestPsRow_NotFound(t *testing.T) {
	row := psRow(aws.ServiceStatus{Name: "web", Found: false})
	if row != "web\tnot found\t-\t-" {
		t.Errorf("got %q", row)
	}
}

func TestPsRow_RunningWithIngress(t *testing.T) {
	row := psRow(aws.ServiceStatus{
		Name:         "web",
		Found:        true,
		Status:       "ACTIVE",
		DesiredCount: 2,
		RunningCount: 2,
		PendingCount: 0,
		HasIngress:   true,
		Healthy:      2,
		Unhealthy:    0,
	})
	want := "web\tACTIVE\t2/2 running, 0 pending\t2 healthy, 0 unhealthy"
	if row != want {
		t.Errorf("got %q, want %q", row, want)
	}
}

func TestPsRow_RunningWithoutIngress(t *testing.T) {
	row := psRow(aws.ServiceStatus{
		Name:         "worker",
		Found:        true,
		Status:       "ACTIVE",
		DesiredCount: 1,
		RunningCount: 1,
		PendingCount: 0,
		HasIngress:   false,
	})
	want := "worker\tACTIVE\t1/1 running, 0 pending\t-"
	if row != want {
		t.Errorf("got %q, want %q", row, want)
	}
}

func TestPrintPsTable_HeaderAndRows(t *testing.T) {
	var buf bytes.Buffer
	printPsTable(&buf, []aws.ServiceStatus{
		{Name: "web", Found: true, Status: "ACTIVE", DesiredCount: 1, RunningCount: 1, HasIngress: true, Healthy: 1},
		{Name: "worker", Found: false},
	})

	out := buf.String()
	if !strings.Contains(out, "NAME") || !strings.Contains(out, "STATUS") {
		t.Errorf("expected a header row, got:\n%s", out)
	}
	if !strings.Contains(out, "web") || !strings.Contains(out, "worker") {
		t.Errorf("expected both services listed, got:\n%s", out)
	}
	if !strings.Contains(out, "not found") {
		t.Errorf("expected worker's row to say 'not found', got:\n%s", out)
	}
}

// TestMain_PsRequiresEnv confirms `cloudcompose ps` has no default
// environment: like `main`, it fails with a helpful message rather
// than silently guessing which cluster to query.
func TestMain_PsRequiresEnv(t *testing.T) {
	t.Parallel()
	bin := buildCloudComposeBinary(t)

	cmd := exec.Command(bin, "ps", "-f", "../../../examples/hello/compose.yml")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected a non-zero exit when --env is not given, got success:\n%s", out)
	}
	if !contains(string(out), "--env is required") {
		t.Errorf("expected the error to name --env, got:\n%s", out)
	}
}
