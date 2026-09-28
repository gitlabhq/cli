package api

import (
	"os"
	"regexp"
	"strings"
)

// envTokenRE validates caller-supplied environment values before they reach
// telemetry: alphanumeric, dots, hyphens, underscores, max 64 chars.
var envTokenRE = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

// AI_AGENT is the universal escape hatch; hardcoded agents are alphabetical, no priority implied.
func DetectCodingAgent() string {
	if v := os.Getenv("AI_AGENT"); envTokenRE.MatchString(v) {
		return v
	}
	if os.Getenv("CLAUDECODE") == "1" {
		return "claude-code"
	}
	// CODEX_THREAD_ID is an opaque thread identifier, not a boolean flag.
	if os.Getenv("CODEX_THREAD_ID") != "" {
		return "codex"
	}
	if os.Getenv("CURSOR_AGENT") == "1" {
		return "cursor"
	}
	if os.Getenv("GEMINI_CLI") == "1" {
		return "gemini"
	}
	if os.Getenv("OPENCODE") == "1" {
		return "opencode"
	}
	if os.Getenv("ROO_CLI_RUNTIME") == "1" {
		return "roo-code"
	}
	// TERM_PROGRAM is a weaker signal: it fires for any command run in those IDE
	// terminals, not only agent-driven invocations. The `-terminal` suffix lets
	// analytics distinguish these from explicit agent env vars handled above.
	switch strings.ToLower(os.Getenv("TERM_PROGRAM")) {
	case "cursor":
		return "cursor-terminal"
	case "windsurf":
		return "windsurf-terminal"
	case "zed":
		return "zed-terminal"
	}
	return ""
}
