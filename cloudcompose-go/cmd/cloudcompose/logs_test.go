package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gecburton/cloudcompose/internal/compiler/aws"
	"github.com/gecburton/cloudcompose/internal/compiler/azure"
)

func TestAwsLogLine_Format(t *testing.T) {
	line := awsLogLine(aws.LogEvent{
		Service:   "web",
		Timestamp: 1700000000000,
		Message:   "hello world",
	})
	if !strings.Contains(line, "web") || !strings.Contains(line, "hello world") {
		t.Errorf("expected service name and message in line, got %q", line)
	}
}

func TestPrintAwsLogEvents_MultipleServicesInterleaved(t *testing.T) {
	var buf bytes.Buffer
	printAwsLogEvents(&buf, []aws.LogEvent{
		{Service: "web", Timestamp: 1000, Message: "first"},
		{Service: "worker", Timestamp: 2000, Message: "second"},
	})

	out := buf.String()
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d:\n%s", len(lines), out)
	}
	if !strings.Contains(lines[0], "web") || !strings.Contains(lines[0], "first") {
		t.Errorf("expected first line to be web/first, got %q", lines[0])
	}
	if !strings.Contains(lines[1], "worker") || !strings.Contains(lines[1], "second") {
		t.Errorf("expected second line to be worker/second, got %q", lines[1])
	}
}

func TestAzureLogLine_Format(t *testing.T) {
	line := azureLogLine(azure.LogEvent{
		Service:   "web",
		Timestamp: time.Unix(1700000000, 0),
		Message:   "hello world",
	})
	if !strings.Contains(line, "web") || !strings.Contains(line, "hello world") {
		t.Errorf("expected service name and message in line, got %q", line)
	}
}

func TestPrintAzureLogEvents_MultipleServicesInterleaved(t *testing.T) {
	var buf bytes.Buffer
	printAzureLogEvents(&buf, []azure.LogEvent{
		{Service: "web", Timestamp: time.Unix(1000, 0), Message: "first"},
		{Service: "worker", Timestamp: time.Unix(2000, 0), Message: "second"},
	})

	out := buf.String()
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d:\n%s", len(lines), out)
	}
	if !strings.Contains(lines[0], "web") || !strings.Contains(lines[0], "first") {
		t.Errorf("expected first line to be web/first, got %q", lines[0])
	}
	if !strings.Contains(lines[1], "worker") || !strings.Contains(lines[1], "second") {
		t.Errorf("expected second line to be worker/second, got %q", lines[1])
	}
}

// TestMain_LogsRequiresEnv confirms `cloudcompose logs` has no default
// environment, matching `ps`'s and `main`'s own requirement.
func TestMain_LogsRequiresEnv(t *testing.T) {
	t.Parallel()
	bin := buildCloudComposeBinary(t)

	cmd := exec.Command(bin, "logs", "-f", "../../../examples/hello/compose.yml")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected a non-zero exit when --env is not given, got success:\n%s", out)
	}
	if !contains(string(out), "--env is required") {
		t.Errorf("expected the error to name --env, got:\n%s", out)
	}
}

func TestAwsLogEventsJSON(t *testing.T) {
	rows := awsLogEventsJSON([]aws.LogEvent{
		{Service: "web", Timestamp: 1700000000000, Message: "hello"},
	})
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0].Service != "web" || rows[0].Message != "hello" {
		t.Errorf("got %+v", rows[0])
	}
	if rows[0].Timestamp != "2023-11-14T22:13:20Z" {
		t.Errorf("Timestamp = %q, want 2023-11-14T22:13:20Z", rows[0].Timestamp)
	}
}

func TestAzureLogEventsJSON(t *testing.T) {
	rows := azureLogEventsJSON([]azure.LogEvent{
		{Service: "web", Timestamp: time.Unix(1700000000, 0), Message: "hello"},
	})
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0].Service != "web" || rows[0].Message != "hello" {
		t.Errorf("got %+v", rows[0])
	}
	if rows[0].Timestamp != "2023-11-14T22:13:20Z" {
		t.Errorf("Timestamp = %q, want 2023-11-14T22:13:20Z", rows[0].Timestamp)
	}
}

func TestPrintLogsJSON_IsValidJSONArray(t *testing.T) {
	var buf bytes.Buffer
	printJSONArray(&buf, []logEventJSON{
		{Service: "web", Timestamp: "2026-01-01T00:00:00Z", Message: "hello"},
	})

	var decoded []logEventJSON
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
		t.Fatalf("output is not valid JSON: %v\noutput:\n%s", err, buf.String())
	}
	if len(decoded) != 1 || decoded[0].Service != "web" || decoded[0].Message != "hello" {
		t.Errorf("got %+v", decoded)
	}
}

// TestPrintLogsJSON_EmptyIsStillAnArray mirrors ps_test.go's own
// TestPrintPsJSON_EmptyIsStillAnArray rationale: `logs --json` against
// zero events should still emit `[]`, not `null`. Exercised via
// printJSONArray directly (ps.go/logs.go's shared JSON printer) rather
// than a since-removed printLogsJSON wrapper.
func TestPrintLogsJSON_EmptyIsStillAnArray(t *testing.T) {
	var buf bytes.Buffer
	printJSONArray(&buf, []logEventJSON{})

	if strings.TrimSpace(buf.String()) != "[]" {
		t.Errorf("got %q, want []", buf.String())
	}
}

// TestMain_LogsJSONFlag confirms `--json` is a real, wired-up flag,
// mirroring TestMain_PsJSONFlag's own rationale.
func TestMain_LogsJSONFlag(t *testing.T) {
	t.Parallel()
	bin := buildCloudComposeBinary(t)

	cmd := exec.Command(bin, "logs", "--json", "-f", "../../../examples/hello/compose.yml")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected a non-zero exit when --env is not given, got success:\n%s", out)
	}
	if !contains(string(out), "--env is required") {
		t.Errorf("expected the error to name --env even with --json set, got:\n%s", out)
	}
}

// TestLogs_RejectsGcpBeforeParsingCompose mirrors ps_test.go's own
// TestPs_RejectsGcpBeforeParsingCompose: `logs` used to only reject a
// GCP environment after also parsing/normalizing compose.yml. Confirmed
// via a compose.yml with invalid YAML -- if `logs` were still trying
// to parse it first, this would see a YAML parse error instead of the
// target rejection.
func TestLogs_RejectsGcpBeforeParsingCompose(t *testing.T) {
	t.Parallel()
	bin := buildCloudComposeBinary(t)

	envDir := writeGcpEnvironmentFixture(t, "demo")
	invalidComposeFile := filepath.Join(t.TempDir(), "compose.yml")
	if err := os.WriteFile(invalidComposeFile, []byte("not: [valid, yaml: at all"), 0644); err != nil {
		t.Fatalf("write invalid compose.yml: %v", err)
	}

	cmd := exec.Command(bin, "logs", "-f", invalidComposeFile, "-e", envDir)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected cloudcompose logs to fail for a gcp environment, got:\n%s", out)
	}
	if !contains(string(out), "does not support gcp") {
		t.Errorf("expected the gcp rejection message, got:\n%s", out)
	}
}
