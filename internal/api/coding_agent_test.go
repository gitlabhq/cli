package api

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDetectCodingAgent(t *testing.T) {
	tests := []struct {
		name     string
		envVars  map[string]string
		expected string
	}{
		{
			name:     "no agent detected",
			envVars:  map[string]string{},
			expected: "",
		},
		{
			name:     "AI_AGENT takes priority over all others",
			envVars:  map[string]string{"AI_AGENT": "claude-code_2-1-140_agent", "CLAUDECODE": "1", "CURSOR_AGENT": "1"},
			expected: "claude-code_2-1-140_agent",
		},
		{
			name:     "AI_AGENT passes through raw value",
			envVars:  map[string]string{"AI_AGENT": "some-custom-agent"},
			expected: "some-custom-agent",
		},
		{
			name:     "Claude Code",
			envVars:  map[string]string{"CLAUDECODE": "1"},
			expected: "claude-code",
		},
		{
			name:     "CLAUDECODE wrong value ignored",
			envVars:  map[string]string{"CLAUDECODE": "true"},
			expected: "",
		},
		{
			name:     "Codex CLI",
			envVars:  map[string]string{"CODEX_THREAD_ID": "thread_abc123"},
			expected: "codex",
		},
		{
			name:     "OpenCode",
			envVars:  map[string]string{"OPENCODE": "1"},
			expected: "opencode",
		},
		{
			name:     "Cursor",
			envVars:  map[string]string{"CURSOR_AGENT": "1"},
			expected: "cursor",
		},
		{
			name:     "Gemini CLI",
			envVars:  map[string]string{"GEMINI_CLI": "1"},
			expected: "gemini",
		},
		{
			name:     "Roo Code",
			envVars:  map[string]string{"ROO_CLI_RUNTIME": "1"},
			expected: "roo-code",
		},
		{
			name:     "ROO_CLI_RUNTIME wrong value ignored",
			envVars:  map[string]string{"ROO_CLI_RUNTIME": "true"},
			expected: "",
		},
		{
			name:     "TERM_PROGRAM=cursor falls back to cursor-terminal",
			envVars:  map[string]string{"TERM_PROGRAM": "cursor"},
			expected: "cursor-terminal",
		},
		{
			name:     "TERM_PROGRAM=kiro falls back to kiro-terminal",
			envVars:  map[string]string{"TERM_PROGRAM": "kiro"},
			expected: "kiro-terminal",
		},
		{
			name:     "TERM_PROGRAM=Kiro is case-insensitive",
			envVars:  map[string]string{"TERM_PROGRAM": "Kiro"},
			expected: "kiro-terminal",
		},
		{
			name:     "TERM_PROGRAM=kiroshi is ignored",
			envVars:  map[string]string{"TERM_PROGRAM": "kiroshi"},
			expected: "",
		},
		{
			name:     "TERM_PROGRAM=Windsurf is case-insensitive",
			envVars:  map[string]string{"TERM_PROGRAM": "Windsurf"},
			expected: "windsurf-terminal",
		},
		{
			name:     "TERM_PROGRAM=zed falls back to zed-terminal",
			envVars:  map[string]string{"TERM_PROGRAM": "zed"},
			expected: "zed-terminal",
		},
		{
			name:     "TERM_PROGRAM=ghostty is ignored",
			envVars:  map[string]string{"TERM_PROGRAM": "ghostty"},
			expected: "",
		},
		{
			name:     "CURSOR_AGENT wins over TERM_PROGRAM=cursor",
			envVars:  map[string]string{"CURSOR_AGENT": "1", "TERM_PROGRAM": "cursor"},
			expected: "cursor",
		},
		{
			name:     "AI_AGENT with spaces is ignored",
			envVars:  map[string]string{"AI_AGENT": "has spaces", "CLAUDECODE": "1"},
			expected: "claude-code",
		},
		{
			name:     "AI_AGENT with special chars is ignored",
			envVars:  map[string]string{"AI_AGENT": "agent/name!@#"},
			expected: "",
		},
		{
			name:     "AI_AGENT exceeding max length is ignored",
			envVars:  map[string]string{"AI_AGENT": "abcdefghijklmnopqrstuvwxyz-ABCDEFGHIJKLMNOPQRSTUVWXYZ-0123456789XYZ"},
			expected: "",
		},
		{
			name:     "AI_AGENT at exactly max length is accepted",
			envVars:  map[string]string{"AI_AGENT": "abcdefghijklmnopqrstuvwxyz-ABCDEFGHIJKLMNOPQRSTUVWXYZ-0123456789"},
			expected: "abcdefghijklmnopqrstuvwxyz-ABCDEFGHIJKLMNOPQRSTUVWXYZ-0123456789",
		},
	}

	allAgentVars := []string{
		"AI_AGENT", "CLAUDECODE", "CODEX_THREAD_ID",
		"OPENCODE", "CURSOR_AGENT", "GEMINI_CLI",
		"ROO_CLI_RUNTIME", "TERM_PROGRAM",
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, key := range allAgentVars {
				t.Setenv(key, "")
			}
			for key, value := range tt.envVars {
				t.Setenv(key, value)
			}

			result := DetectCodingAgent()
			assert.Equal(t, tt.expected, result)
		})
	}
}
