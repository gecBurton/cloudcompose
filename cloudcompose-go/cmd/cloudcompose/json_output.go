package main

import (
	"encoding/json"
	"io"
	"os"
)

// printJSONArray writes rows as a single JSON array to w -- always an
// array, even for a nil or zero-length slice, so a caller (jq/python3
// in scripts/smoke-test.sh) never needs to special-case "no output" vs
// "empty array". Shared by ps.go's printPsJSON and logs.go's
// printLogsJSON, which were previously identical functions
// (`json.NewEncoder(w); enc.SetIndent(...); enc.Encode(rows)`)
// differing only in the row type (psRowJSON vs. logEventJSON) -- now
// one generic implementation instead of two hand-copied ones.
func printJSONArray[T any](w io.Writer, rows []T) {
	if rows == nil {
		rows = []T{}
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(rows); err != nil {
		printUnexpectedError(err)
		os.Exit(1)
	}
}
