package serve

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"os"
	"os/exec"
	"strings"

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

// newMCPServer creates a new MCP server instance
func newMCPServer(rootCmd *cobra.Command) *mcpServer {
	// Create MCP server with usage instructions
	instructions := `GitLab CLI MCP Server - Provides access to GitLab functionality through glab commands.

General Usage:
- Use --help flag with any tool to get detailed usage information
- For large outputs, use limit/offset parameters for pagination
- Check 'total_size' in response metadata to navigate results
- Most tools support common flags like --output for formatting`

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

		// Build the tool with dynamic schema
		tool := s.buildToolFromCommand(toolName, description, cmd)

		// Create handler for this command
		handler := s.createCommandHandler(path, cmd)

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
			currentPath = append(path, cmdName)
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

// buildToolFromCommand creates a tool with dynamic schema
func (s *mcpServer) buildToolFromCommand(toolName, description string, cmd *cobra.Command) *mcp.Tool {
	// Create nested flags object schema with all available flags
	flagsProperties := make(map[string]any)

	// Add parameters for each flag to the flags object
	cmd.Flags().VisitAll(func(flag *pflag.Flag) {
		if !flag.Hidden && flag.Name != "help" {
			flagName := strings.ReplaceAll(flag.Name, "-", "_")
			flagSchema := s.buildFlagSchema(flag)
			if flagSchema != nil {
				flagsProperties[flagName] = flagSchema
			}
		}
	})

	// Add persistent flags from parent commands to the flags object
	cmd.PersistentFlags().VisitAll(func(flag *pflag.Flag) {
		if !flag.Hidden && flag.Name != "help" {
			flagName := strings.ReplaceAll(flag.Name, "-", "_")

			// Check if we already added this flag from local flags
			alreadyAdded := false
			cmd.Flags().VisitAll(func(localFlag *pflag.Flag) {
				if localFlag.Name == flag.Name {
					alreadyAdded = true
				}
			})

			if !alreadyAdded {
				flagSchema := s.buildFlagSchema(flag)
				if flagSchema != nil {
					flagsProperties[flagName] = flagSchema
				}
			}
		}
	})

	// Determine if this is a destructive command
	isDestructive := s.isDestructiveCommand(cmd)

	// Derive args description from cmd.Use (e.g. "api <endpoint>" → "Positional arguments: <endpoint>").
	// strings.Fields handles irregular whitespace; joining the tail reconstructs multi-word arg specs cleanly.
	argsDesc := "Positional arguments"
	if use := strings.Fields(cmd.Use); len(use) > 1 {
		hint := strings.TrimSpace(strings.ReplaceAll(strings.Join(use[1:], " "), "[flags]", ""))
		if hint != "" {
			argsDesc = "Positional arguments: " + hint
		}
	}

	// Build the input schema manually
	inputSchema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			argsParam: map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": argsDesc,
			},
			flagsParam: map[string]any{
				"type":        "object",
				"properties":  flagsProperties,
				"description": "Command flags",
			},
			limitParam: map[string]any{
				"type":        "number",
				"description": "Response size limit",
				"default":     float64(defaultResponseLimit),
			},
			offsetParam: map[string]any{
				"type":        "number",
				"description": "Pagination offset",
				"default":     float64(0),
			},
		},
	}

	// Create the tool
	tool := &mcp.Tool{
		Name:        toolName,
		Description: description,
		InputSchema: inputSchema,
	}

	// Add destructive annotation if needed
	if isDestructive {
		if tool.Annotations == nil {
			tool.Annotations = &mcp.ToolAnnotations{}
		}
		destructiveHint := true
		tool.Annotations.DestructiveHint = &destructiveHint
	}

	return tool
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
func (s *mcpServer) createCommandHandler(cmdPath []string, cmd *cobra.Command) mcp.ToolHandler {
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
		args, config := s.convertParamsToArgs(params, cmd)

		// Execute the glab command
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
		processedOutput := s.processOutput(output, config)

		structuredContent := s.buildStructuredContent(processedOutput)

		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{
					Text: processedOutput,
				},
			},
			StructuredContent: structuredContent,
		}, nil
	}
}

// buildStructuredContent builds structured content from command output.
// It always includes the raw text output under "content" and, when possible,
// also includes parsed JSON under "data".
func (s *mcpServer) buildStructuredContent(processedOutput string) map[string]any {
	structuredContent := map[string]any{
		"content": processedOutput,
	}

	var structuredData any
	if err := json.Unmarshal([]byte(processedOutput), &structuredData); err == nil {
		structuredContent["data"] = structuredData
	}

	return structuredContent
}

// responseConfig holds output processing configuration
type responseConfig struct {
	Limit  int
	Offset int
}

// processOutput handles rune-based output limiting
func (s *mcpServer) processOutput(output string, config responseConfig) string {
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

	return string(processedRunes)
}

// convertParamsToArgs converts MCP JSON parameters to command line arguments and extracts response config
func (s *mcpServer) convertParamsToArgs(params map[string]any, cmd *cobra.Command) ([]string, responseConfig) {
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
				flag := cmd.Flags().Lookup(flagName)
				if flag == nil {
					// Try original key name
					flag = cmd.Flags().Lookup(key)
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
	if outputFlag := cmd.Flags().Lookup("output"); outputFlag != nil {
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
	fullArgs := append(cmdPath, args...)

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
