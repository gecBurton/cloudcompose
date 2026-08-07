package shared

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// StructResourceBlocks converts any *Resources struct (AWSResources,
// AzureResources, GcpResources) into the map Terraform's JSON syntax
// expects under "resource": one entry per resource type, present only if
// it has at least one instance. Shared across all three clouds so each
// generator doesn't need its own hand-written resource-type-order list
// and empty-map type-switch -- the struct's own field order and
// `omitempty` tags are the single source of truth for both.
func StructResourceBlocks(resources any) (map[string]any, error) {
	raw, err := json.Marshal(resources)
	if err != nil {
		return nil, fmt.Errorf("marshal resources: %w", err)
	}

	var blocks map[string]any
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return nil, fmt.Errorf("unmarshal resources: %w", err)
	}

	return blocks, nil
}

// MarshalIndentedJSON renders v as indented, alphabetically key-sorted
// JSON, with literal "<"/">" characters left unescaped (encoding/json's
// default HTML-safe escaping would otherwise turn "~> 5.0" into
// "~\u003e 5.0", which Terraform's version-constraint parser tolerates
// but which has no reason to be there in output nobody's consuming as
// HTML).
//
// Struct field declaration order is not alphabetical, and Go's
// encoding/json does not sort struct fields the way it sorts map keys, so
// v is round-tripped through a plain map first when it's a struct (e.g.
// models.TerraformManifest); callers that already build a plain
// map[string]any (each cloud's environment_*.go generator) skip straight
// to the encode step with the same helper.
func MarshalIndentedJSON(v any) (string, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("marshal to intermediate form: %w", err)
	}

	var asMap map[string]any
	if err := json.Unmarshal(raw, &asMap); err != nil {
		return "", fmt.Errorf("unmarshal to map: %w", err)
	}

	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(asMap); err != nil {
		return "", fmt.Errorf("encode: %w", err)
	}

	// json.Marshal (and Encoder.Encode) already sort map keys
	// lexicographically. Encoder.Encode appends a trailing newline;
	// trimmed so output has no unexpected trailing whitespace.
	return strings.TrimSuffix(buf.String(), "\n"), nil
}

// MarshalJSONStringPlain renders v as compact (non-indented),
// alphabetically key-sorted JSON, for embedding as a string value inside
// another JSON document (e.g. the shared-infrastructure generators'
// local_file.content field, which itself holds the generated
// environment.yml's JSON-as-YAML contents).
func MarshalJSONStringPlain(v any) (string, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("marshal: %w", err)
	}
	return string(raw), nil
}
