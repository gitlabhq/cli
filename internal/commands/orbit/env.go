package orbit

import "strings"

func environWith(base, overrides []string) []string {
	blocked := make(map[string]struct{}, len(overrides))
	for _, kv := range overrides {
		if key, _, ok := strings.Cut(kv, "="); ok {
			blocked[key] = struct{}{}
		}
	}

	merged := make([]string, 0, len(base)+len(overrides))
	for _, kv := range base {
		key, _, _ := strings.Cut(kv, "=")
		if _, replaced := blocked[key]; replaced {
			continue
		}
		merged = append(merged, kv)
	}
	return append(merged, overrides...)
}
