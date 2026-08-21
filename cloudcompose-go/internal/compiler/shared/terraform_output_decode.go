package shared

// Helpers for decoding a Terraform output value (already parsed from
// JSON into map[string]any/[]any/string/float64/bool/nil) into the
// typed Go fields Aws/Azure/GcpEnvironment declare.

// ToStringMap converts a decoded JSON object into map[string]string,
// skipping any non-string values rather than erroring.
func ToStringMap(v any) map[string]string {
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	result := make(map[string]string, len(m))
	for k, val := range m {
		if s, ok := val.(string); ok {
			result[k] = s
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

// ToStringSlice converts a decoded JSON array into []string.
func ToStringSlice(v any) []string {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	result := make([]string, 0, len(arr))
	for _, item := range arr {
		if s, ok := item.(string); ok {
			result = append(result, s)
		}
	}
	return result
}

// ToStringPtr converts a decoded JSON value into *string, or nil if the
// value is absent/null/not a string.
func ToStringPtr(v any) *string {
	s, ok := v.(string)
	if !ok || s == "" {
		return nil
	}
	return &s
}
