package api

import (
	"os"
	"regexp"
	"strings"
)

// envTokenRE validates caller-supplied environment values before they reach
// telemetry: alphanumeric, dots, hyphens, underscores, max 64 chars.
var envTokenRE = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

// DetectCodingAgent returns the coding agent, or the IDE terminal, that glab
// runs in, for telemetry and the User-Agent.
func DetectCodingAgent() string {
	agent, _ := detectAgent()
	return agent
}

// DetectRunningAgent returns the coding agent that runs glab, or an empty
// string when there is none or only an IDE terminal is detected, where a
// person may be typing. Any AI_AGENT other than 0, false or no marks an
// agent, reported as unknown when it is not a valid agent name.
func DetectRunningAgent() string {
	if agent, explicit := detectAgent(); explicit {
		return agent
	}
	switch strings.ToLower(os.Getenv("AI_AGENT")) {
	case "", "0", "false", "no":
		return ""
	}
	return "unknown"
}

// detectAgent returns the agent DetectCodingAgent reports, and false when no
// agent is detected or the agent is only the IDE terminal glab runs in.
// AI_AGENT is the universal escape hatch; hardcoded agents are alphabetical,
// except where a fork inherits its parent's marker and must be checked first.
func detectAgent() (string, bool) {
	if v := os.Getenv("AI_AGENT"); envTokenRE.MatchString(v) && !isBooleanWord(v) {
		return v, true
	}
	if os.Getenv("CLAUDECODE") == "1" {
		return "claude-code", true
	}
	if os.Getenv("CLINE_ACTIVE") == "true" {
		return "cline", true
	}
	// CODEX_THREAD_ID is an opaque thread identifier, not a boolean flag.
	if os.Getenv("CODEX_THREAD_ID") != "" {
		return "codex", true
	}
	// GitHub Copilot CLI sets COPILOT_CLI=1
	if os.Getenv("COPILOT_CLI") == "1" {
		return "copilot", true
	}
	if os.Getenv("CURSOR_AGENT") == "1" {
		return "cursor", true
	}
	if os.Getenv("GEMINI_CLI") == "1" {
		return "gemini", true
	}
	if os.Getenv("GOOSE_TERMINAL") == "1" {
		return "goose", true
	}
	if os.Getenv("JUNIE_SHIM_PATH") != "" {
		return "junie", true
	}
	// Kilo Code is an OpenCode fork and also sets OPENCODE=1.
	if os.Getenv("KILO") == "1" {
		return "kilo-code", true
	}
	if os.Getenv("KIRO_CLI_IS_CHILD_PROCESS") == "1" {
		return "kiro-cli", true
	}
	// Set in the Kiro IDE agent's shell but not in the user's Kiro terminal.
	if os.Getenv("KIRO_SESSION_ID") != "" {
		return "kiro", true
	}
	// Kiro CLI also sets this, so it is checked after Kiro.
	if strings.Contains(os.Getenv("AWS_EXECUTION_ENV"), "AmazonQ-For-CLI") {
		return "amazon-q", true
	}
	if os.Getenv("OPENCODE") == "1" {
		return "opencode", true
	}
	if os.Getenv("QWEN_CODE") == "1" {
		return "qwen-code", true
	}
	// ROO_CLI_RUNTIME is the Roo CLI; ROO_ACTIVE is the VS Code extension's terminals.
	if os.Getenv("ROO_CLI_RUNTIME") == "1" || os.Getenv("ROO_ACTIVE") == "true" {
		return "roo-code", true
	}
	// Set by Warp's Oz cloud agent runs to the run's ID.
	if os.Getenv("OZ_RUN_ID") != "" {
		return "warp", true
	}
	// TERM_PROGRAM is a weaker signal: it fires for any command run in those IDE
	// terminals, not only agent-driven invocations. The `-terminal` suffix lets
	// analytics distinguish these from explicit agent env vars handled above,
	// except for Zed's agent terminal, which its pager overrides identify.
	switch strings.ToLower(os.Getenv("TERM_PROGRAM")) {
	case "cursor":
		return "cursor-terminal", false
	case "kiro":
		return "kiro-terminal", false
	case "windsurf":
		return "windsurf-terminal", false
	case "zed":
		// Zed's agent terminal disables pagers; its user terminal does not.
		if pager, ok := os.LookupEnv("PAGER"); ok && pager == "" && os.Getenv("GIT_PAGER") == "cat" {
			return "zed", true
		}
		return "zed-terminal", false
	}
	return "", false
}

// isBooleanWord reports whether v is a flag value, which names no agent.
func isBooleanWord(v string) bool {
	switch strings.ToLower(v) {
	case "0", "1", "true", "false", "yes", "no":
		return true
	}
	return false
}
