package serve

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"os"
	"os/exec"
	"slices"
	"strings"
	"unicode"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
)

const (
	// Parameter names for the nested MCP tool structure
	argsParam   = "args"
	flagsParam  = "flags"
	limitParam  = "limit"
	offsetParam = "offset"

	// Default response limit in runes (balances usefulness vs token consumption)
	defaultResponseLimit = 50000
)

// mcpServer wraps the MCP server with GitLab client access
type mcpServer struct {
	server  *mcp.Server
	rootCmd *cobra.Command
}

// serverInstructions is the guidance every connecting client receives. The cap
// is interpolated from defaultResponseLimit so tuning the constant cannot leave
// this quoting a number nothing enforces.
func serverInstructions() string {
	return fmt.Sprintf(`GitLab CLI MCP Server - Provides access to GitLab functionality through glab commands.

General Usage:
- Use --help flag with any tool to get detailed usage information
- Responses are capped at %d characters and say so when cut. List commands
  routinely exceed this: narrow them with per_page, page, or jq rather than
  paging, because a cut JSON response is a fragment that will not parse.
  For plain-text output, "offset" and "limit" read further into the response.
- Most tools support common flags like --output for formatting`, defaultResponseLimit)
}

// newMCPServer creates a new MCP server instance
func newMCPServer(rootCmd *cobra.Command) *mcpServer {
	instructions := serverInstructions()

	mcpSrv := mcp.NewServer(
		&mcp.Implementation{
			Name:    "glab-mcp-server",
			Version: "1.0.0",
		},
		&mcp.ServerOptions{
			Instructions: instructions,
			Capabilities: &mcp.ServerCapabilities{
				Tools: &mcp.ToolCapabilities{
					ListChanged: false,
				},
			},
		},
	)

	glabServer := &mcpServer{
		server:  mcpSrv,
		rootCmd: rootCmd,
	}

	// Register all GitLab tools dynamically
	glabServer.registerToolsFromCommands()

	return glabServer
}

// Run starts the MCP server with stdio transport
func (s *mcpServer) Run(ctx context.Context) error {
	return s.server.Run(ctx, &mcp.StdioTransport{})
}

// registerToolsFromCommands automatically registers all glab commands as MCP tools
func (s *mcpServer) registerToolsFromCommands() {
	for cmd, path := range s.iterCommands(s.rootCmd, []string{}) {
		// Only register leaf commands that have RunE and are not the root command
		if cmd.RunE == nil || cmd == s.rootCmd {
			continue
		}

		// Skip commands that don't have explicit MCP annotations (require opt-in)
		if !mcpannotations.HasAnnotation(cmd.Annotations) {
			continue
		}

		// Skip commands marked as interactive or excluded
		if val := cmd.Annotations[mcpannotations.Interactive]; val == "true" {
			continue
		}
		if val := cmd.Annotations[mcpannotations.Exclude]; val == "true" {
			continue
		}

		toolName := "glab_" + strings.Join(path, "_")
		description := s.buildEnhancedDescription(cmd)
		if description == "" {
			description = fmt.Sprintf("Execute glab %s command", strings.Join(path, " "))
		}

		// Resolved once: cobra fills in inherited flags by mutating the command,
		// which concurrent tool invocations must not do.
		flags := commandFlags(cmd)

		// Build the tool with dynamic schema
		tool := s.buildToolFromCommand(toolName, description, cmd, flags)

		handler := s.createCommandHandler(path, flags)

		s.server.AddTool(tool, handler)
	}
}

func (s *mcpServer) iterCommands(cmd *cobra.Command, path []string) iter.Seq2[*cobra.Command, []string] {
	return func(yield func(*cobra.Command, []string) bool) {
		cmdName := strings.Fields(cmd.Use)[0]

		// Skip root "glab" command from path - remove binary name earlier
		var currentPath []string
		if len(path) == 0 && cmdName == "glab" {
			// This is the root command, start with empty path
			currentPath = []string{}
		} else {
			currentPath = append(slices.Clone(path), cmdName)
		}

		// Process current command
		if !yield(cmd, currentPath) {
			return
		}

		// Recursively process subcommands
		for _, subCmd := range cmd.Commands() {
			for c, p := range s.iterCommands(subCmd, currentPath) {
				if !yield(c, p) {
					return
				}
			}
		}
	}
}

// buildEnhancedDescription creates an optimized description with truncated content and standard guidance
func (s *mcpServer) buildEnhancedDescription(cmd *cobra.Command) string {
	var parts []string

	// Start with the command's short description
	if cmd.Short != "" {
		parts = append(parts, cmd.Short)
	}

	// Add truncated long description if present
	if cmd.Long != "" {
		truncatedLong := s.truncateAtWordBoundary(cmd.Long, 100)
		parts = append(parts, "", truncatedLong)
	}

	// Add standard guidance
	description := strings.Join(parts, "\n")
	return s.addStandardGuidance(description)
}

// truncateAtWordBoundary truncates text to maxChars at the nearest word boundary
func (s *mcpServer) truncateAtWordBoundary(text string, maxChars int) string {
	runes := []rune(text)
	if len(runes) <= maxChars {
		return text
	}

	// Find the last space within the limit, accounting for "..." suffix
	for i := maxChars - 4; i >= 0; i-- {
		if runes[i] == ' ' || runes[i] == '\n' {
			return strings.TrimSpace(string(runes[:i])) + "..."
		}
	}

	// If no space found, hard truncate accounting for "..." suffix
	return string(runes[:maxChars-3]) + "..."
}

// addStandardGuidance is no longer needed since guidance is provided at server level
// Keeping this as a no-op for now in case we want tool-specific guidance later
func (s *mcpServer) addStandardGuidance(description string) string {
	return description
}

// commandFlags collects every flag a command accepts. Cobra folds inherited
// flags into cmd.Flags() only while a command executes, which never happens
// here, so a flag a parent noun registers for its subtree (such as -R/--repo
// from cmdutils.EnableRepoOverride) needs the explicit InheritedFlags() pass.
func commandFlags(cmd *cobra.Command) *pflag.FlagSet {
	flags := pflag.NewFlagSet(cmd.Name(), pflag.ContinueOnError)
	flags.AddFlagSet(cmd.Flags())
	flags.AddFlagSet(cmd.PersistentFlags())
	flags.AddFlagSet(cmd.InheritedFlags())
	return flags
}

// buildToolFromCommand creates a tool with dynamic schema
func (s *mcpServer) buildToolFromCommand(toolName, description string, cmd *cobra.Command, flags *pflag.FlagSet) *mcp.Tool {
	// Create nested flags object schema with all available flags
	flagsProperties := make(map[string]any)

	flags.VisitAll(func(flag *pflag.Flag) {
		// Hidden is how a command opts out of an inherited flag: the root-level
		// --repo is hidden, and only subtrees that resolve a project unhide it.
		if flag.Hidden || flag.Name == "help" {
			return
		}
		flagName := strings.ReplaceAll(flag.Name, "-", "_")
		if flagSchema := s.buildFlagSchema(flag); flagSchema != nil {
			flagsProperties[flagName] = flagSchema
		}
	})

	argsSchema := map[string]any{
		"type":  "array",
		"items": map[string]any{"type": "string"},
	}
	if hint := positionalArgsHint(cmd); hint != "" {
		argsSchema["description"] = hint
	}

	// limit and offset are deliberately absent: convertParamsToArgs still honours
	// them, but advertising them on all ~200 tools costs more context than the
	// truncation notice that points an agent at them when output is actually cut.
	inputSchema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			argsParam: argsSchema,
			flagsParam: map[string]any{
				"type":       "object",
				"properties": flagsProperties,
			},
		},
	}

	tool := &mcp.Tool{
		Name:        toolName,
		Description: description,
		InputSchema: inputSchema,
	}

	// No readOnlyHint for the rest: mcp:safe only means "not destructive", and
	// that set still contains commands that reconfigure a runner or write files
	// to disk. Claiming read-only would let clients auto-approve those. Saying
	// nothing leaves destructiveHint at its spec default of true, which
	// over-prompts rather than under-prompts.
	if s.isDestructiveCommand(cmd) {
		destructiveHint := true
		tool.Annotations = &mcp.ToolAnnotations{DestructiveHint: &destructiveHint}
	}

	return tool
}

// positionalArgsHint derives an args description from cmd.Use
// (e.g. "api <endpoint>" → "Positional arguments: <endpoint>"), returning ""
// when cmd.Use names no arguments.
func positionalArgsHint(cmd *cobra.Command) string {
	use := strings.Fields(cmd.Use)
	if len(use) < 2 {
		return ""
	}
	hint := strings.TrimSpace(strings.ReplaceAll(strings.Join(use[1:], " "), "[flags]", ""))
	if hint == "" {
		return ""
	}
	return "Positional arguments: " + hint
}

// buildFlagSchema creates a JSON schema object for a flag (used in nested flags object)
func (s *mcpServer) buildFlagSchema(flag *pflag.Flag) map[string]any {
	flagType := flag.Value.Type()
	schema := make(map[string]any)

	// Removed descriptions and defaults to minimize token usage
	// LLMs can infer flag purpose from flag names

	// Minimal type information only
	switch flagType {
	case "bool":
		schema["type"] = "boolean"

	// String array types
	case "stringSlice", "stringArray":
		schema["type"] = "array"
		schema["items"] = map[string]any{"type": "string"}

	// Numeric array types
	case "intSlice", "int32Slice", "int64Slice",
		"uintSlice", "uint32Slice", "uint64Slice",
		"float32Slice", "float64Slice":
		schema["type"] = "array"
		schema["items"] = map[string]any{"type": "number"}

	// Boolean array type
	case "boolSlice":
		schema["type"] = "array"
		schema["items"] = map[string]any{"type": "boolean"}

	// Special types that serialize as strings
	case "durationSlice", "ipSlice", "ipNetSlice":
		schema["type"] = "array"
		schema["items"] = map[string]any{"type": "string"}

	case "int", "int8", "int16", "int32", "int64",
		"uint", "uint8", "uint16", "uint32", "uint64",
		"float32", "float64":
		schema["type"] = "number"

	default:
		schema["type"] = "string"
	}

	// Add enum constraint if the flag value has a fixed set of allowed values.
	if av, ok := flag.Value.(cmdutils.AllowedValuer); ok {
		schema["enum"] = av.AllowedValues()
	}

	return schema
}

// createCommandHandler creates a handler function for a specific glab command
func (s *mcpServer) createCommandHandler(cmdPath []string, flags *pflag.FlagSet) mcp.ToolHandler {
	return func(ctx context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		// Get parameters from the request - need to unmarshal json.RawMessage
		var params map[string]any
		if len(request.Params.Arguments) > 0 {
			if err := json.Unmarshal(request.Params.Arguments, &params); err != nil {
				return &mcp.CallToolResult{
					Content: []mcp.Content{
						&mcp.TextContent{
							Text: fmt.Sprintf("failed to parse arguments: %v", err),
						},
					},
					IsError: true,
				}, nil
			}
		}

		// Convert MCP parameters to command line arguments and extract response config
		args, config := s.convertParamsToArgs(params, flags)

		output, err := s.executeGlabCommand(cmdPath, args)
		if err != nil {
			// Return the error as content so the user can see what went wrong
			return &mcp.CallToolResult{ //nolint:nilerr // MCP surfaces tool errors via IsError on the result, not by returning a Go error
				Content: []mcp.Content{
					&mcp.TextContent{
						Text: output, // This includes the actual error message from the command
					},
				},
				IsError: true, // Mark this as an error response
			}, nil
		}

		// Process output with rune-based limiting
		result := s.processOutput(output, config)

		structuredContent := s.buildStructuredContent(result)

		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{
					Text: result.Text + result.truncationNotice(),
				},
			},
			StructuredContent: structuredContent,
		}, nil
	}
}

// buildStructuredContent builds structured content from command output.
// It always includes the raw text output under "content" and, when possible,
// also includes parsed JSON under "data".
func (s *mcpServer) buildStructuredContent(result outputResult) map[string]any {
	structuredContent := map[string]any{
		"content":    result.Text,
		"total_size": result.TotalSize,
	}
	if result.Truncated {
		structuredContent["next_offset"] = result.NextOffset
	}

	var structuredData any
	if err := json.Unmarshal([]byte(result.Text), &structuredData); err == nil {
		structuredContent["data"] = structuredData
	}

	return structuredContent
}

// responseConfig holds output processing configuration
type responseConfig struct {
	Limit  int
	Offset int
}

// outputResult is a window onto command output, sized by responseConfig.
type outputResult struct {
	Text       string
	TotalSize  int
	NextOffset int
	Truncated  bool
	JSONLike   bool
}

// truncationNotice tells an agent how to fetch the rest. The limit and offset
// parameters are not in any tool's input schema, so this is where they are
// advertised: once, to the caller that needs them.
//
// Slicing runs on runes, so a cut JSON document yields a fragment that never
// parses. Paging by offset recovers the bytes but not the structure, which is
// why JSON output is steered towards narrowing the query instead.
func (r outputResult) truncationNotice() string {
	if !r.Truncated {
		return ""
	}
	// NextOffset rather than the window length: it is the cumulative position,
	// so successive pages report progress instead of repeating the page size.
	if r.JSONLike {
		return fmt.Sprintf(
			"\n\n[Truncated: %d of %d characters read. This window is an incomplete JSON fragment and will not parse. "+
				"Narrow the query instead, with per_page, page, or jq. Passing \"offset\": %d returns the next raw chunk, not valid JSON.]",
			r.NextOffset, r.TotalSize, r.NextOffset,
		)
	}
	return fmt.Sprintf(
		"\n\n[Truncated: %d of %d characters read. Call this tool again with \"offset\": %d to continue.]",
		r.NextOffset, r.TotalSize, r.NextOffset,
	)
}

// processOutput handles rune-based output limiting
func (s *mcpServer) processOutput(output string, config responseConfig) outputResult {
	// Convert to runes for Unicode-safe processing
	runes := []rune(output)
	totalSize := len(runes)

	// Calculate slice bounds with support for negative offsets (counting from end)
	start := config.Offset
	if start < 0 {
		// Negative offset: count from the end like 'tail'
		start = max(totalSize+start, 0)
	}
	if start > totalSize {
		start = totalSize
	}

	end := min(start+config.Limit, totalSize)

	// Extract the slice
	var processedRunes []rune
	if start < totalSize {
		processedRunes = runes[start:end]
	}

	return outputResult{
		Text:       string(processedRunes),
		TotalSize:  totalSize,
		NextOffset: end,
		Truncated:  end < totalSize,
		JSONLike:   looksLikeJSON(output),
	}
}

func looksLikeJSON(output string) bool {
	trimmed := strings.TrimLeftFunc(output, unicode.IsSpace)
	return strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[")
}

// convertParamsToArgs converts MCP JSON parameters to command line arguments and extracts response config
func (s *mcpServer) convertParamsToArgs(params map[string]any, flags *pflag.FlagSet) ([]string, responseConfig) {
	var args []string
	var positionals []string
	config := responseConfig{
		Limit:  defaultResponseLimit,
		Offset: 0,
	}

	// Handle args (positional arguments)
	if argsParam, exists := params[argsParam]; exists {
		if argArray, ok := argsParam.([]any); ok {
			for _, arg := range argArray {
				if str, ok := arg.(string); ok && str != "" {
					positionals = append(positionals, str)
				}
			}
		}
	}

	// Handle limit parameter
	if limitParam, exists := params[limitParam]; exists {
		if f64, ok := limitParam.(float64); ok {
			config.Limit = int(f64)
		}
	}

	// Handle offset parameter
	if offsetParam, exists := params[offsetParam]; exists {
		if f64, ok := offsetParam.(float64); ok {
			config.Offset = int(f64)
		}
	}

	// Handle flags object
	if flagsParam, exists := params[flagsParam]; exists {
		if flagsObj, ok := flagsParam.(map[string]any); ok {
			for key, value := range flagsObj {
				if value == nil {
					continue
				}

				// Convert snake_case to kebab-case for CLI flags
				flagName := strings.ReplaceAll(key, "_", "-")

				// Check if this is a known flag
				flag := flags.Lookup(flagName)
				if flag == nil {
					// Try original key name
					flag = flags.Lookup(key)
				}

				// Process the parameter value
				switch v := value.(type) {
				case bool:
					if v && flag != nil {
						args = append(args, "--"+flagName)
					}
				case string:
					if v != "" {
						if flag != nil {
							args = append(args, "--"+flagName, v)
						}
					}
				case []any:
					// Handle arrays (like labels)
					for _, item := range v {
						if str, ok := item.(string); ok && str != "" {
							args = append(args, "--"+flagName, str)
						}
					}
				case float64:
					// Handle numbers from JSON
					if v != 0 {
						// For large integers (like pipeline IDs), format without decimals and avoid scientific notation
						var numStr string
						if v == float64(int64(v)) {
							// This is an integer value, format as int to avoid precision issues
							numStr = fmt.Sprintf("%d", int64(v))
						} else {
							// This is a float value
							numStr = fmt.Sprintf("%g", v)
						}

						if flag != nil {
							args = append(args, "--"+flagName, numStr)
						}
					}
				default:
					// Convert other types to string
					if str := fmt.Sprintf("%v", value); str != "" && str != "0" && str != "false" {
						if flag != nil {
							args = append(args, "--"+flagName, str)
						}
					}
				}
			}
		}
	}

	// Auto-enable JSON output for commands that support it (better for LLM parsing)
	// Only add if user hasn't already specified an output format via MCP params
	if outputFlag := flags.Lookup("output"); outputFlag != nil {
		// Check if output flag was already provided by user in their MCP parameters
		// (which would have been processed into args above)
		// Users could specify either long form ("output") or short form ("o")
		outputAlreadySet := false
		for _, arg := range args {
			// Check for all possible forms:
			// - "--output" or "--output=" (long form)
			// - "--o" or "--o=" (short form, our code adds -- prefix)
			// - "-o" (short form, for robustness)
			if arg == "--output" || strings.HasPrefix(arg, "--output=") ||
				arg == "--o" || strings.HasPrefix(arg, "--o=") ||
				arg == "-o" {
				outputAlreadySet = true
				break
			}
		}

		if !outputAlreadySet {
			args = append(args, "--output", "json")
		}
	}

	// Add positional arguments at the end
	args = append(args, positionals...)

	return args, config
}

// executeGlabCommand executes a glab command and captures its output
func (s *mcpServer) executeGlabCommand(cmdPath []string, args []string) (string, error) {
	// Get the current binary (same one running MCP server)
	currentBinary, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("failed to get current executable: %w", err)
	}

	// Build full command arguments
	fullArgs := slices.Concat(cmdPath, args)

	// Execute subprocess
	cmd := exec.Command(currentBinary, fullArgs...)

	output, err := cmd.CombinedOutput()
	if err != nil {
		// On failure, return the output (which includes stderr) with the error
		return string(output), err
	}

	// On success, return stdout content
	return string(output), nil
}

// isDestructiveCommand determines if a command is destructive based on annotations
func (s *mcpServer) isDestructiveCommand(cmd *cobra.Command) bool {
	// All executable commands should have annotations
	if cmd.Annotations != nil {
		if val, exists := cmd.Annotations[mcpannotations.Destructive]; exists {
			return val == "true"
		}
		if val, exists := cmd.Annotations[mcpannotations.Safe]; exists {
			return val != "true"
		}
	}

	// Default to destructive for safety if no annotation found (should not happen - unannotated commands are filtered out)
	return true
}
