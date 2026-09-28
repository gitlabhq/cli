package api

import "os"

const (
	// InvocationSourceEnv marks a glab process that another glab process
	// started on a caller's behalf.
	InvocationSourceEnv = "GLAB_INVOCATION_SOURCE"
	// InvocationSourceMCP is stamped on the subprocesses the MCP server spawns
	// for tool calls. Without it a tool call and the same command typed into a
	// terminal are indistinguishable in telemetry: both carry the same command
	// path, and both inherit the coding agent's environment.
	InvocationSourceMCP = "mcp"
)

// DetectInvocationSource reads the marker set by whichever glab process spawned
// this one, returning "" when glab was invoked directly. The value is validated
// because anything in the environment is caller-controlled.
func DetectInvocationSource() string {
	if v := os.Getenv(InvocationSourceEnv); envTokenRE.MatchString(v) {
		return v
	}
	return ""
}
