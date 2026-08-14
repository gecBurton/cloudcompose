package main

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"

	"github.com/gecburton/cloudcompose/internal/compiler/aws"
)

func TestLogLine_Format(t *testing.T) {
	line := logLine(aws.LogEvent{
		Service:   "web",
		Timestamp: 1700000000000,
		Message:   "hello world",
	})
	if !strings.Contains(line, "web") || !strings.Contains(line, "hello world") {
		t.Errorf("expected service name and message in line, got %q", line)
	}
}

func TestPrintLogEvents_MultipleServicesInterleaved(t *testing.T) {
	var buf bytes.Buffer
	printLogEvents(&buf, []aws.LogEvent{
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
