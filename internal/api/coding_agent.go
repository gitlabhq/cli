package api

import (
	"os"
	"regexp"
	"strings"
)

// envTokenRE validates caller-supplied environment values before they reach
// telemetry: alphanumeric, dots, hyphens, underscores, max 64 chars.
var envTokenRE = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

// AI_AGENT is the universal escape hatch; hardcoded agents are alphabetical,
// except where a fork inherits its parent's marker and must be checked first.
func DetectCodingAgent() string {
	if v := os.Getenv("AI_AGENT"); envTokenRE.MatchString(v) && !isBooleanWord(v) {
		return v
	}
	if os.Getenv("CLAUDECODE") == "1" {
		return "claude-code"
	}
	if os.Getenv("CLINE_ACTIVE") == "true" {
		return "cline"
	}
	// CODEX_THREAD_ID is an opaque thread identifier, not a boolean flag.
	if os.Getenv("CODEX_THREAD_ID") != "" {
		return "codex"
	}
	// GitHub Copilot CLI sets COPILOT_CLI=1
	if os.Getenv("COPILOT_CLI") == "1" {
		return "copilot"
	}
	if os.Getenv("CURSOR_AGENT") == "1" {
		return "cursor"
	}
	if os.Getenv("GEMINI_CLI") == "1" {
		return "gemini"
	}
	if os.Getenv("GOOSE_TERMINAL") == "1" {
		return "goose"
	}
	if os.Getenv("JUNIE_SHIM_PATH") != "" {
		return "junie"
	}
	// Kilo Code is an OpenCode fork and also sets OPENCODE=1.
	if os.Getenv("KILO") == "1" {
		return "kilo-code"
	}
	if os.Getenv("KIRO_CLI_IS_CHILD_PROCESS") == "1" {
		return "kiro-cli"
	}
	// Set in the Kiro IDE agent's shell but not in the user's Kiro terminal.
	if os.Getenv("KIRO_SESSION_ID") != "" {
		return "kiro"
	}
	// Kiro CLI also sets this, so it is checked after Kiro.
	if strings.Contains(os.Getenv("AWS_EXECUTION_ENV"), "AmazonQ-For-CLI") {
		return "amazon-q"
	}
	if os.Getenv("OPENCODE") == "1" {
		return "opencode"
	}
	if os.Getenv("QWEN_CODE") == "1" {
		return "qwen-code"
	}
	// ROO_CLI_RUNTIME is the Roo CLI; ROO_ACTIVE is the VS Code extension's terminals.
	if os.Getenv("ROO_CLI_RUNTIME") == "1" || os.Getenv("ROO_ACTIVE") == "true" {
		return "roo-code"
	}
	// Set by Warp's Oz cloud agent runs to the run's ID.
	if os.Getenv("OZ_RUN_ID") != "" {
		return "warp"
	}
	// TERM_PROGRAM is a weaker signal: it fires for any command run in those IDE
	// terminals, not only agent-driven invocations. The `-terminal` suffix lets
	// analytics distinguish these from explicit agent env vars handled above,
	// except for Zed's agent terminal, which its pager overrides identify.
	switch strings.ToLower(os.Getenv("TERM_PROGRAM")) {
	case "cursor":
		return "cursor-terminal"
	case "kiro":
		return "kiro-terminal"
	case "windsurf":
		return "windsurf-terminal"
	case "zed":
		// Zed's agent terminal disables pagers; its user terminal does not.
		if pager, ok := os.LookupEnv("PAGER"); ok && pager == "" && os.Getenv("GIT_PAGER") == "cat" {
			return "zed"
		}
		return "zed-terminal"
	}
	return ""
}

// isBooleanWord reports whether v is a flag value, which names no agent.
func isBooleanWord(v string) bool {
	switch strings.ToLower(v) {
	case "0", "1", "true", "false", "yes", "no":
		return true
	}
	return false
}
