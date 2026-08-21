package main

import (
	"encoding/json"
	"io"
	"os"
)

// printJSONArray writes rows as a single JSON array to w -- always an
// array, even for a nil or zero-length slice, so callers never need to
// special-case "no output" vs "empty array".
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
