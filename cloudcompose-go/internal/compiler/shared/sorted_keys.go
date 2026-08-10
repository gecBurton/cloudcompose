package shared

import "sort"

// SortedKeys returns the keys of a string-valued map in sorted order, for
// deterministic iteration over environment-variable-shaped maps (used
// wherever a map's keys need to be rendered in a stable order, e.g.
// building an ordered list of env var blocks for a container definition).
func SortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
