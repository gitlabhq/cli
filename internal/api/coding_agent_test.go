package api

import (
	"os"
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
		{
			name:     "Copilot CLI",
			envVars:  map[string]string{"COPILOT_CLI": "1"},
			expected: "copilot",
		},
		{
			name:     "Copilot CLI with value true is ignored",
			envVars:  map[string]string{"COPILOT_CLI": "true"},
			expected: "",
		},
		{
			name:     "Copilot CLI with value 0 is ignored",
			envVars:  map[string]string{"COPILOT_CLI": "0"},
			expected: "",
		},
		{
			name:     "AI_AGENT=1 names no agent",
			envVars:  map[string]string{"AI_AGENT": "1"},
			expected: "",
		},
		{
			name:     "AI_AGENT=true names no agent and falls through",
			envVars:  map[string]string{"AI_AGENT": "true", "CLAUDECODE": "1"},
			expected: "claude-code",
		},
		{
			name:     "AI_AGENT boolean words are matched in any case",
			envVars:  map[string]string{"AI_AGENT": "TRUE"},
			expected: "",
		},
		{
			name:     "AI_AGENT=false names no agent",
			envVars:  map[string]string{"AI_AGENT": "false"},
			expected: "",
		},
		{
			name:     "AI_AGENT=0 names no agent",
			envVars:  map[string]string{"AI_AGENT": "0"},
			expected: "",
		},
		{
			name:     "Kilo Code wins over the OpenCode markers it inherits",
			envVars:  map[string]string{"KILO": "1", "OPENCODE": "1"},
			expected: "kilo-code",
		},
		{
			name:     "KILO with value true is ignored",
			envVars:  map[string]string{"KILO": "true", "OPENCODE": "1"},
			expected: "opencode",
		},
		{
			name:     "Cline",
			envVars:  map[string]string{"CLINE_ACTIVE": "true"},
			expected: "cline",
		},
		{
			name:     "CLINE_ACTIVE with value 1 is ignored",
			envVars:  map[string]string{"CLINE_ACTIVE": "1"},
			expected: "",
		},
		{
			name:     "Goose",
			envVars:  map[string]string{"GOOSE_TERMINAL": "1"},
			expected: "goose",
		},
		{
			name:     "GOOSE_TERMINAL with value true is ignored",
			envVars:  map[string]string{"GOOSE_TERMINAL": "true"},
			expected: "",
		},
		{
			name:     "Kiro CLI wins over TERM_PROGRAM=kiro",
			envVars:  map[string]string{"KIRO_CLI_IS_CHILD_PROCESS": "1", "TERM_PROGRAM": "kiro"},
			expected: "kiro-cli",
		},
		{
			name:     "Amazon Q Developer CLI",
			envVars:  map[string]string{"AWS_EXECUTION_ENV": "AmazonQ-For-CLI Version/1.16.0"},
			expected: "amazon-q",
		},
		{
			name:     "Kiro CLI wins over the Amazon Q marker it also sets",
			envVars:  map[string]string{"KIRO_CLI_IS_CHILD_PROCESS": "1", "AWS_EXECUTION_ENV": "AmazonQ-For-CLI Version/3.4.0 acp-client/kiro-cli-non-interactive"},
			expected: "kiro-cli",
		},
		{
			name:     "Other AWS_EXECUTION_ENV values are ignored",
			envVars:  map[string]string{"AWS_EXECUTION_ENV": "AWS_Lambda_go1.x"},
			expected: "",
		},
		{
			name:     "Kiro IDE agent wins over TERM_PROGRAM=kiro",
			envVars:  map[string]string{"KIRO_SESSION_ID": "sess_123", "TERM_PROGRAM": "kiro"},
			expected: "kiro",
		},
		{
			name:     "Kiro IDE agent without TERM_PROGRAM",
			envVars:  map[string]string{"KIRO_SESSION_ID": "sess_123"},
			expected: "kiro",
		},
		{
			name:     "Kiro CLI wins over the session ID it also sets",
			envVars:  map[string]string{"KIRO_CLI_IS_CHILD_PROCESS": "1", "KIRO_SESSION_ID": "sess_123"},
			expected: "kiro-cli",
		},
		{
			name:     "Junie CLI",
			envVars:  map[string]string{"JUNIE_SHIM_PATH": "/Users/me/.local/bin/junie"},
			expected: "junie",
		},
		{
			name:     "Warp cloud agent run",
			envVars:  map[string]string{"OZ_RUN_ID": "run_123"},
			expected: "warp",
		},
		{
			name:     "Qwen Code",
			envVars:  map[string]string{"QWEN_CODE": "1"},
			expected: "qwen-code",
		},
		{
			name:     "QWEN_CODE with value true is ignored",
			envVars:  map[string]string{"QWEN_CODE": "true"},
			expected: "",
		},
		{
			name:     "Roo Code extension terminal",
			envVars:  map[string]string{"ROO_ACTIVE": "true"},
			expected: "roo-code",
		},
		{
			name:     "ROO_ACTIVE with value 1 is ignored",
			envVars:  map[string]string{"ROO_ACTIVE": "1"},
			expected: "",
		},
		{
			name:     "Zed agent terminal",
			envVars:  map[string]string{"TERM_PROGRAM": "zed", "PAGER": "", "GIT_PAGER": "cat"},
			expected: "zed",
		},
		{
			name:     "Zed terminal with PAGER unset",
			envVars:  map[string]string{"TERM_PROGRAM": "zed", "GIT_PAGER": "cat"},
			expected: "zed-terminal",
		},
		{
			name:     "Zed terminal with PAGER empty and GIT_PAGER unset",
			envVars:  map[string]string{"TERM_PROGRAM": "zed", "PAGER": ""},
			expected: "zed-terminal",
		},
		{
			name:     "Zed terminal with PAGER empty and a user GIT_PAGER",
			envVars:  map[string]string{"TERM_PROGRAM": "zed", "PAGER": "", "GIT_PAGER": "delta"},
			expected: "zed-terminal",
		},
		{
			name:     "Zed terminal with a user pager",
			envVars:  map[string]string{"TERM_PROGRAM": "zed", "PAGER": "less", "GIT_PAGER": "cat"},
			expected: "zed-terminal",
		},
	}

	allAgentVars := []string{
		"AI_AGENT", "CLAUDECODE", "CODEX_THREAD_ID", "COPILOT_CLI",
		"OPENCODE", "CURSOR_AGENT", "GEMINI_CLI", "KILO",
		"CLINE_ACTIVE", "GOOSE_TERMINAL", "KIRO_CLI_IS_CHILD_PROCESS", "QWEN_CODE", "ROO_ACTIVE", "AWS_EXECUTION_ENV", "JUNIE_SHIM_PATH", "OZ_RUN_ID", "KIRO_SESSION_ID",
		"ROO_CLI_RUNTIME", "TERM_PROGRAM", "PAGER", "GIT_PAGER",
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, key := range allAgentVars {
				t.Setenv(key, "") // restores the original value after the test
				_ = os.Unsetenv(key)
			}
			for key, value := range tt.envVars {
				t.Setenv(key, value)
			}

			result := DetectCodingAgent()
			assert.Equal(t, tt.expected, result)
		})
	}
}
