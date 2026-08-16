package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// TestPrintJSONArray_NilSliceIsStillAnArray confirms a nil slice (not
// just an explicitly-empty one, which ps_test.go's/logs_test.go's own
// TestPrintPsJSON_EmptyIsStillAnArray/TestPrintLogsJSON_EmptyIsStillAnArray
// already cover) still emits `[]`, not `null` -- json.Marshal itself
// would emit `null` for a nil slice with no help from printJSONArray,
// so this pins the nil-to-empty normalization specifically, not just
// the already-empty case.
func TestPrintJSONArray_NilSliceIsStillAnArray(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	var rows []psRowJSON // nil, not []psRowJSON{}
	printJSONArray(&buf, rows)

	if strings.TrimSpace(buf.String()) != "[]" {
		t.Errorf("got %q, want []", buf.String())
	}
}

// TestPrintJSONArray_WorksForAnyRowType confirms printJSONArray is
// genuinely generic -- both psRowJSON (ps.go) and logEventJSON (logs.go)
// use the exact same function, the reason it exists instead of the two
// near-identical printPsJSON/printLogsJSON wrappers this replaced.
func TestPrintJSONArray_WorksForAnyRowType(t *testing.T) {
	t.Parallel()

	var psBuf bytes.Buffer
	printJSONArray(&psBuf, []psRowJSON{{Name: "web", Found: true}})
	var psDecoded []psRowJSON
	if err := json.Unmarshal(psBuf.Bytes(), &psDecoded); err != nil {
		t.Fatalf("psRowJSON output is not valid JSON: %v", err)
	}
	if len(psDecoded) != 1 || psDecoded[0].Name != "web" {
		t.Errorf("got %+v", psDecoded)
	}

	var logBuf bytes.Buffer
	printJSONArray(&logBuf, []logEventJSON{{Service: "web", Message: "hello"}})
	var logDecoded []logEventJSON
	if err := json.Unmarshal(logBuf.Bytes(), &logDecoded); err != nil {
		t.Fatalf("logEventJSON output is not valid JSON: %v", err)
	}
	if len(logDecoded) != 1 || logDecoded[0].Message != "hello" {
		t.Errorf("got %+v", logDecoded)
	}
}
