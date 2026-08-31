package config

import "sync"

var knownKeys = sync.OnceValue(func() map[string]struct{} {
	out := map[string]struct{}{}
	for i := range KeySchema {
		if KeySchema[i].UserSettable {
			out[KeySchema[i].Name] = struct{}{}
		}
	}
	return out
})

func KnownKeys() map[string]struct{} {
	return knownKeys()
}

func IsKnownKey(key string) bool {
	_, ok := KnownKeys()[ConfigKeyEquivalence(key)]
	return ok
}
