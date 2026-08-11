//go:build !integration

package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestParseDomains covers that a trailing, leading, or doubled comma must
// not produce a blank domain: an empty string reaching
// dockercredhelper.Register would write "": "glab" into the user's
// config.json.
func TestParseDomains(t *testing.T) {
	tests := map[string]struct {
		domains string
		want    []string
	}{
		"empty":          {domains: "", want: nil},
		"single":         {domains: "registry.example.com", want: []string{"registry.example.com"}},
		"multiple":       {domains: "registry.example.com, registry.other.example.com", want: []string{"registry.example.com", "registry.other.example.com"}},
		"trailing comma": {domains: "registry.example.com,", want: []string{"registry.example.com"}},
		"leading comma":  {domains: ",registry.example.com", want: []string{"registry.example.com"}},
		"doubled comma":  {domains: "registry.example.com,,registry.other.example.com", want: []string{"registry.example.com", "registry.other.example.com"}},
		"all whitespace": {domains: "   ", want: nil},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tt.want, ParseDomains(tt.domains))
		})
	}
}
