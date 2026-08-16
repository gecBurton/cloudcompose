package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gecburton/cloudcompose/internal/compiler/aws"
	"github.com/gecburton/cloudcompose/internal/compiler/azure"
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

func TestPrintAwsPsTable_HeaderAndRows(t *testing.T) {
	var buf bytes.Buffer
	printAwsPsTable(&buf, []aws.ServiceStatus{
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

func TestAzurePsRow_NotFound(t *testing.T) {
	row := azurePsRow(azure.ServiceStatus{Name: "web", Found: false})
	if row != "web\tnot found\t-\t-" {
		t.Errorf("got %q", row)
	}
}

func TestAzurePsRow_Running(t *testing.T) {
	row := azurePsRow(azure.ServiceStatus{
		Name:              "web",
		Found:             true,
		ProvisioningState: "Succeeded",
		Replicas:          2,
		HasIngress:        true,
		HealthState:       "Healthy",
	})
	want := "web\tSucceeded\t2\tHealthy"
	if row != want {
		t.Errorf("got %q, want %q", row, want)
	}
}

func TestAzurePsRow_NoHealthState(t *testing.T) {
	row := azurePsRow(azure.ServiceStatus{
		Name:              "worker",
		Found:             true,
		ProvisioningState: "Succeeded",
		Replicas:          1,
	})
	want := "worker\tSucceeded\t1\t-"
	if row != want {
		t.Errorf("got %q, want %q", row, want)
	}
}

func TestPrintAzurePsTable_HeaderAndRows(t *testing.T) {
	var buf bytes.Buffer
	printAzurePsTable(&buf, []azure.ServiceStatus{
		{Name: "web", Found: true, ProvisioningState: "Succeeded", Replicas: 1, HealthState: "Healthy"},
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

func TestAwsPsRowsJSON(t *testing.T) {
	rows := awsPsRowsJSON([]aws.ServiceStatus{
		{Name: "web", Found: true, Status: "ACTIVE", RunningCount: 2, HasIngress: true, Healthy: 2, Unhealthy: 0},
		{Name: "worker", Found: false},
	})
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
	if rows[0] != (psRowJSON{Name: "web", Found: true, Status: "ACTIVE", Running: 2, Health: "2 healthy, 0 unhealthy"}) {
		t.Errorf("got %+v", rows[0])
	}
	if rows[1] != (psRowJSON{Name: "worker", Found: false}) {
		t.Errorf("got %+v", rows[1])
	}
}

func TestAzurePsRowsJSON(t *testing.T) {
	rows := azurePsRowsJSON([]azure.ServiceStatus{
		{Name: "web", Found: true, ProvisioningState: "Succeeded", Replicas: 3, HealthState: "Healthy"},
		{Name: "worker", Found: false},
	})
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
	if rows[0] != (psRowJSON{Name: "web", Found: true, Status: "Succeeded", Running: 3, Health: "Healthy"}) {
		t.Errorf("got %+v", rows[0])
	}
	if rows[1] != (psRowJSON{Name: "worker", Found: false}) {
		t.Errorf("got %+v", rows[1])
	}
}

func TestPrintPsJSON_IsValidJSONArray(t *testing.T) {
	var buf bytes.Buffer
	printJSONArray(&buf, []psRowJSON{
		{Name: "web", Found: true, Status: "ACTIVE", Running: 2, Health: "2 healthy, 0 unhealthy"},
		{Name: "worker", Found: false},
	})

	var decoded []psRowJSON
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
		t.Fatalf("output is not valid JSON: %v\noutput:\n%s", err, buf.String())
	}
	if len(decoded) != 2 {
		t.Fatalf("expected 2 decoded rows, got %d", len(decoded))
	}
	if decoded[0].Name != "web" || !decoded[0].Found {
		t.Errorf("got %+v", decoded[0])
	}
	if decoded[1].Name != "worker" || decoded[1].Found {
		t.Errorf("got %+v", decoded[1])
	}
}

// TestPrintPsJSON_EmptyIsStillAnArray confirms `ps --json` against zero
// services still emits `[]`, not `null` -- a caller (jq/python3) should
// never need to special-case "no output" vs "empty array". Exercised via
// printJSONArray directly (ps.go/logs.go's shared JSON printer, see its
// own doc comment) rather than a since-removed printPsJSON wrapper.
func TestPrintPsJSON_EmptyIsStillAnArray(t *testing.T) {
	var buf bytes.Buffer
	printJSONArray(&buf, []psRowJSON{})

	if strings.TrimSpace(buf.String()) != "[]" {
		t.Errorf("got %q, want []", buf.String())
	}
}

// TestMain_PsJSONFlag confirms `--json` is a real, wired-up flag (not
// just present in the two conversion functions above), via the same
// CLI-level pattern as TestMain_PsRequiresEnv.
func TestMain_PsJSONFlag(t *testing.T) {
	t.Parallel()
	bin := buildCloudComposeBinary(t)

	// --env is still required even with --json: this only proves the
	// flag itself parses, not that it does anything meaningful without
	// a real environment, which needs a real cloud (covered by
	// scripts/smoke-test.sh instead, not a unit test).
	cmd := exec.Command(bin, "ps", "--json", "-f", "../../../examples/hello/compose.yml")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected a non-zero exit when --env is not given, got success:\n%s", out)
	}
	if !contains(string(out), "--env is required") {
		t.Errorf("expected the error to name --env even with --json set, got:\n%s", out)
	}
}

// TestPs_RejectsGcpBeforeParsingCompose is a regression test: `ps`
// used to only reject a GCP environment after also parsing/normalizing
// compose.yml (a real, if harmless, waste of work -- LoadEnvironment
// itself has nothing to reject for GCP, since GCP is a real,
// supported cloudcompose target elsewhere). This confirms `ps` fails
// with the "does not support gcp" message even against a compose.yml
// with invalid YAML -- if `ps` were still trying to parse it first,
// this test would see a YAML parse error instead of the target
// rejection.
func TestPs_RejectsGcpBeforeParsingCompose(t *testing.T) {
	t.Parallel()
	bin := buildCloudComposeBinary(t)

	envDir := writeGcpEnvironmentFixture(t, "demo")
	invalidComposeFile := filepath.Join(t.TempDir(), "compose.yml")
	if err := os.WriteFile(invalidComposeFile, []byte("not: [valid, yaml: at all"), 0644); err != nil {
		t.Fatalf("write invalid compose.yml: %v", err)
	}

	cmd := exec.Command(bin, "ps", "-f", invalidComposeFile, "-e", envDir)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected cloudcompose ps to fail for a gcp environment, got:\n%s", out)
	}
	if !contains(string(out), "does not support gcp") {
		t.Errorf("expected the gcp rejection message, got:\n%s", out)
	}
}
