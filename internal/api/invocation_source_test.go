//go:build !integration

package api

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDetectInvocationSource(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  string
	}{
		{name: "unset", value: "", want: ""},
		{name: "mcp", value: InvocationSourceMCP, want: "mcp"},
		{name: "future source", value: "hooks", want: "hooks"},
		{name: "rejects whitespace", value: "mcp tool", want: ""},
		{name: "rejects overlong", value: strings.Repeat("a", 65), want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set unconditionally rather than only for non-empty values: a test
			// run under an MCP-spawned glab inherits the very variable under
			// test, which would make the unset case pass for the wrong reason.
			t.Setenv(InvocationSourceEnv, tt.value)

			assert.Equal(t, tt.want, DetectInvocationSource())
		})
	}
}
