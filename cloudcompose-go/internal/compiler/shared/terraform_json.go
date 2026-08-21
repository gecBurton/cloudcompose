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
// it has at least one instance.
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
// "~\u003e 5.0", which Terraform tolerates but has no reason to be there).
//
// v is round-tripped through a plain map first when it's a struct, since
// Go's encoding/json does not sort struct fields the way it sorts map
// keys; callers that already build a plain map[string]any skip straight
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
	// lexicographically; the trailing newline Encode appends is trimmed.
	return strings.TrimSuffix(buf.String(), "\n"), nil
}

// MarshalJSONStringPlain renders v as compact (non-indented),
// alphabetically key-sorted JSON, for embedding as a string value inside
// another JSON document.
func MarshalJSONStringPlain(v any) (string, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("marshal: %w", err)
	}
	return string(raw), nil
}
