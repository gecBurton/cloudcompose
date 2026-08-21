package shared

import "sort"

// SortedKeys returns the keys of a string-valued map in sorted order, for
// deterministic iteration over environment-variable-shaped maps.
func SortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
