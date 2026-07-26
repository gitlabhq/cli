//go:build !integration

package serve

import (
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
)

// Mock command creation helpers

func createMockCommand(name, short, long, example string) *cobra.Command {
	cmd := &cobra.Command{
		Use:     name,
		Short:   short,
		Long:    long,
		Example: example,
		RunE: func(cmd *cobra.Command, args []string) error {
			return nil
		},
	}
	return cmd
}

func createMockCommandWithAnnotations(name, short string, annotations map[string]string) *cobra.Command {
	cmd := &cobra.Command{
		Use:         name,
		Short:       short,
		Annotations: annotations,
		RunE: func(cmd *cobra.Command, args []string) error {
			return nil
		},
	}
	return cmd
}

func createMockCommandHierarchy() (*cobra.Command, *cobra.Command, *cobra.Command) {
	root := createMockCommand("root", "Root command", "Root long description", "root example")
	parent := createMockCommand("parent", "Parent command", "Parent long description", "parent example")
	parent.Annotations = map[string]string{
		"help:arguments": "parent help arguments",
	}
	child := createMockCommand("child", "Child command", "", "")

	root.AddCommand(parent)
	parent.AddCommand(child)

	return root, parent, child
}

func createMockCommandWithFlags() *cobra.Command {
	cmd := createMockCommand("test", "Test command", "", "")

	// Add various flag types
	cmd.Flags().Bool("verbose", false, "Enable verbose output")
	cmd.Flags().String("output", "text", "Output format")
	cmd.Flags().Int("count", 10, "Number of items")
	cmd.Flags().StringSlice("labels", []string{}, "List of labels")
	cmd.Flags().Uint("port", 8080, "Port number")
	cmd.Flags().IntSlice("ids", []int{}, "List of IDs")

	return cmd
}

// Tests for server capabilities

func TestServerCapabilities(t *testing.T) {
	t.Parallel()

	// Create a mock root command for testing
	rootCmd := &cobra.Command{
		Use:   "glab",
		Short: "GitLab CLI",
	}

	// Create the MCP server
	server := newMCPServer(rootCmd)

	// Verify the server was created successfully
	require.NotNil(t, server)
	require.NotNil(t, server.server)

	// The server should be configured with tools capability
	// This is verified by the server creation not panicking and being able
	// to register tools successfully
	assert.NotNil(t, server.rootCmd)
}

func TestRegisterToolsFromCommands_RequiresMCPAnnotation(t *testing.T) {
	t.Parallel()

	rootCmd := &cobra.Command{
		Use:   "glab",
		Short: "GitLab CLI",
	}

	// Command with MCP annotation - should be registered
	annotatedCmd := &cobra.Command{
		Use:   "annotated",
		Short: "Annotated command",
		Annotations: map[string]string{
			mcpannotations.Safe: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return nil
		},
	}

	// Command without MCP annotation - should NOT be registered
	unannotatedCmd := &cobra.Command{
		Use:   "unannotated",
		Short: "Unannotated command",
		RunE: func(cmd *cobra.Command, args []string) error {
			return nil
		},
	}

	// Command with non-MCP annotation only - should NOT be registered
	otherAnnotationCmd := &cobra.Command{
		Use:   "other",
		Short: "Other annotation command",
		Annotations: map[string]string{
			"help:arguments": "some help text",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return nil
		},
	}

	rootCmd.AddCommand(annotatedCmd)
	rootCmd.AddCommand(unannotatedCmd)
	rootCmd.AddCommand(otherAnnotationCmd)

	server := newMCPServer(rootCmd)

	// Count registered tools by iterating through commands the same way the server does
	registeredTools := make(map[string]bool)
	for cmd := range server.iterCommands(server.rootCmd, []string{}) {
		if cmd.RunE == nil || cmd == server.rootCmd {
			continue
		}
		if !mcpannotations.HasAnnotation(cmd.Annotations) {
			continue
		}
		if val := cmd.Annotations[mcpannotations.Interactive]; val == "true" {
			continue
		}
		if val := cmd.Annotations[mcpannotations.Exclude]; val == "true" {
			continue
		}
		toolName := "glab_" + cmd.Use
		registeredTools[toolName] = true
	}

	// Only the annotated command should be registered
	assert.True(t, registeredTools["glab_annotated"], "annotated command should be registered")
	assert.False(t, registeredTools["glab_unannotated"], "unannotated command should NOT be registered")
	assert.False(t, registeredTools["glab_other"], "command with only non-MCP annotations should NOT be registered")
	assert.Len(t, registeredTools, 1, "only one tool should be registered")
}

// Tests for buildEnhancedDescription

func TestBuildEnhancedDescription(t *testing.T) {
	t.Parallel()

	server := &mcpServer{}

	tests := []struct {
		name     string
		cmd      *cobra.Command
		expected string
	}{
		{
			name:     "empty command",
			cmd:      createMockCommand("empty", "", "", ""),
			expected: "",
		},
		{
			name:     "short description only",
			cmd:      createMockCommand("short", "Short description", "", ""),
			expected: "Short description",
		},
		{
			name:     "short and long descriptions",
			cmd:      createMockCommand("both", "Short desc", "Long description here", ""),
			expected: "Short desc\n\nLong description here",
		},
		{
			name:     "long description truncation",
			cmd:      createMockCommand("truncated", "Short", "This is a very long description that should be truncated at one hundred characters because it exceeds the limit", ""),
			expected: "Short\n\nThis is a very long description that should be truncated at one hundred characters because it...",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result := server.buildEnhancedDescription(tt.cmd)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestBuildEnhancedDescriptionWithHierarchy(t *testing.T) {
	t.Parallel()

	server := &mcpServer{}
	root, _, child := createMockCommandHierarchy()
	server.rootCmd = root

	// Test child command with simplified format
	result := server.buildEnhancedDescription(child)
	expected := "Child command"
	assert.Equal(t, expected, result)
}

// Tests for truncateAtWordBoundary

func TestTruncateAtWordBoundary(t *testing.T) {
	t.Parallel()

	server := &mcpServer{}

	tests := []struct {
		name     string
		text     string
		maxChars int
		expected string
	}{
		{
			name:     "short text no truncation",
			text:     "Short text",
			maxChars: 20,
			expected: "Short text",
		},
		{
			name:     "truncate at word boundary",
			text:     "This is a long text that should be truncated",
			maxChars: 20,
			expected: "This is a long...",
		},
		{
			name:     "hard truncate if no spaces",
			text:     "Verylongtextwithnospaces",
			maxChars: 10,
			expected: "Verylon...",
		},
		{
			name:     "truncate at newline",
			text:     "First line\nSecond line that is longer",
			maxChars: 15,
			expected: "First line...",
		},
		{
			name:     "hard truncate Unicode without invalid UTF-8",
			text:     "界界界界界界界界界界",
			maxChars: 7,
			expected: "界界界界...",
		},
		{
			name:     "truncate Unicode at word boundary",
			text:     "界界界 界界界界界界",
			maxChars: 7,
			expected: "界界界...",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result := server.truncateAtWordBoundary(tt.text, tt.maxChars)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// Tests for addStandardGuidance

func TestAddStandardGuidance(t *testing.T) {
	t.Parallel()

	server := &mcpServer{}

	tests := []struct {
		name        string
		description string
		expected    string
	}{
		{
			name:        "empty description",
			description: "",
			expected:    "",
		},
		{
			name:        "add guidance to description",
			description: "Command description",
			expected:    "Command description",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result := server.addStandardGuidance(tt.description)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// Tests for buildFlagSchema

func TestBuildFlagSchema(t *testing.T) {
	t.Parallel()

	server := &mcpServer{}
	cmd := createMockCommandWithFlags()

	tests := []struct {
		flagName      string
		expectedType  string
		expectedItems map[string]any
	}{
		{
			flagName:     "verbose",
			expectedType: "boolean",
		},
		{
			flagName:     "output",
			expectedType: "string",
		},
		{
			flagName:     "count",
			expectedType: "number",
		},
		{
			flagName:      "labels",
			expectedType:  "array",
			expectedItems: map[string]any{"type": "string"},
		},
		{
			flagName:      "ids",
			expectedType:  "array",
			expectedItems: map[string]any{"type": "number"},
		},
		{
			flagName:     "port",
			expectedType: "number",
		},
	}

	for _, tt := range tests {
		t.Run(tt.flagName, func(t *testing.T) {
			t.Parallel()

			flag := cmd.Flags().Lookup(tt.flagName)
			require.NotNil(t, flag)

			schema := server.buildFlagSchema(flag)
			require.NotNil(t, schema)

			assert.Equal(t, tt.expectedType, schema["type"])

			// Check items property for array types
			if tt.expectedItems != nil {
				items, ok := schema["items"].(map[string]any)
				require.True(t, ok, "Array type must have items property for flag %s", tt.flagName)
				assert.Equal(t, tt.expectedItems, items, "Items mismatch for flag %s", tt.flagName)
			}

			// Minimal schema only contains type and items (for arrays)
			assert.NotContains(t, schema, "default")
			assert.NotContains(t, schema, "description")
			assert.NotContains(t, schema, "minimum")
		})
	}
}

// TestBuildFlagSchema_EnumValues verifies that flags backed by enumValue produce an "enum" constraint in the schema.
func TestBuildFlagSchema_EnumValues(t *testing.T) {
	t.Parallel()

	server := &mcpServer{}

	cmd := &cobra.Command{Use: "test", Short: "Test", RunE: func(cmd *cobra.Command, args []string) error { return nil }}

	var outputFormat string
	cmd.Flags().Var(cmdutils.NewEnumValue([]string{"json", "ndjson"}, "json", &outputFormat), "output", "Format output as: json, ndjson.")

	flag := cmd.Flags().Lookup("output")
	require.NotNil(t, flag)

	schema := server.buildFlagSchema(flag)
	require.NotNil(t, schema)

	assert.Equal(t, "string", schema["type"])
	assert.Equal(t, []string{"json", "ndjson"}, schema["enum"], "enum constraint should list allowed values in sorted order")
}

// TestBuildToolFromCommand_ArgsDescription verifies that the args description is derived from cmd.Use.
// Direct *cobra.Command construction is intentional: buildToolFromCommand operates purely on the
// cobra.Command struct (Use, Flags, Annotations) and requires no factory, IO, or GitLab API wiring,
// so cmdtest helpers would add overhead without benefit.
func TestBuildToolFromCommand_ArgsDescription(t *testing.T) {
	t.Parallel()

	server := &mcpServer{}

	tests := []struct {
		use          string
		expectedDesc string
	}{
		{
			use:          "api <endpoint>",
			expectedDesc: "Positional arguments: <endpoint>",
		},
		{
			use:          "view <id>",
			expectedDesc: "Positional arguments: <id>",
		},
		{
			use:          "list",
			expectedDesc: "Positional arguments",
		},
		{
			use:          "run [flags]",
			expectedDesc: "Positional arguments",
		},
	}

	for _, tt := range tests {
		t.Run(tt.use, func(t *testing.T) {
			t.Parallel()

			cmd := &cobra.Command{
				Use:         tt.use,
				Short:       "Test",
				Annotations: map[string]string{mcpannotations.Safe: "true"},
				RunE:        func(cmd *cobra.Command, args []string) error { return nil },
			}

			tool := server.buildToolFromCommand("test_tool", "desc", cmd)
			require.NotNil(t, tool)

			schema, ok := tool.InputSchema.(map[string]any)
			require.True(t, ok)
			properties, ok := schema["properties"].(map[string]any)
			require.True(t, ok)
			argsSchema, ok := properties["args"].(map[string]any)
			require.True(t, ok)

			assert.Equal(t, tt.expectedDesc, argsSchema["description"])
		})
	}
}

// TestBuildFlagSchema_AllArraysHaveItems validates that all array-type flags have the required items property

func TestBuildFlagSchema_AllArraysHaveItems(t *testing.T) {
	server := &mcpServer{}
	cmd := createMockCommandWithFlags()

	cmd.Flags().VisitAll(func(flag *pflag.Flag) {
		schema := server.buildFlagSchema(flag)

		// If type is array, items MUST exist
		if schema["type"] == "array" {
			items, ok := schema["items"].(map[string]any)
			require.True(t, ok,
				"Array type flag '%s' (type: %s) must have items property",
				flag.Name, flag.Value.Type())

			itemType, ok := items["type"].(string)
			require.True(t, ok,
				"Items for flag '%s' must have a type", flag.Name)

			// Validate item type is one of the valid JSON schema types
			validTypes := []string{"string", "number", "boolean", "object", "array"}
			require.Contains(t, validTypes, itemType,
				"Items type for flag '%s' must be a valid JSON schema type", flag.Name)
		}
	})
}

// Tests for isDestructiveCommand

func TestIsDestructiveCommand(t *testing.T) {
	t.Parallel()

	server := &mcpServer{}

	tests := []struct {
		name        string
		annotations map[string]string
		expected    bool
	}{
		{
			name:        "no annotations - defaults to destructive",
			annotations: nil,
			expected:    true,
		},
		{
			name:        "explicitly safe",
			annotations: map[string]string{mcpannotations.Safe: "true"},
			expected:    false,
		},
		{
			name:        "explicitly destructive",
			annotations: map[string]string{mcpannotations.Destructive: "true"},
			expected:    true,
		},
		{
			name:        "safe annotation false",
			annotations: map[string]string{mcpannotations.Safe: "false"},
			expected:    true,
		},
		{
			name:        "destructive annotation false",
			annotations: map[string]string{mcpannotations.Destructive: "false"},
			expected:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cmd := createMockCommandWithAnnotations("test", "Test", tt.annotations)
			result := server.isDestructiveCommand(cmd)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// Tests for convertParamsToArgs

func TestConvertParamsToArgs(t *testing.T) {
	t.Parallel()

	server := &mcpServer{}
	cmd := createMockCommandWithFlags()

	tests := []struct {
		name     string
		params   map[string]any
		expected []string
	}{
		{
			name:     "empty params",
			params:   map[string]any{},
			expected: []string{"--output", "json"}, // Auto-added for JSON output
		},
		{
			name: "boolean flag true",
			params: map[string]any{
				"flags": map[string]any{
					"verbose": true,
				},
			},
			expected: []string{"--verbose", "--output", "json"}, // Auto-added for JSON output
		},
		{
			name: "boolean flag false",
			params: map[string]any{
				"flags": map[string]any{
					"verbose": false,
				},
			},
			expected: []string{"--output", "json"}, // Auto-added for JSON output
		},
		{
			name: "string flag",
			params: map[string]any{
				"flags": map[string]any{
					"output": "json",
				},
			},
			expected: []string{"--output", "json"}, // User-specified, not auto-added
		},
		{
			name: "number flag",
			params: map[string]any{
				"flags": map[string]any{
					"count": float64(25),
				},
			},
			expected: []string{"--count", "25", "--output", "json"}, // Auto-added for JSON output
		},
		{
			name: "array flag",
			params: map[string]any{
				"flags": map[string]any{
					"labels": []any{"bug", "urgent"},
				},
			},
			expected: []string{"--labels", "bug", "--labels", "urgent", "--output", "json"}, // Auto-added for JSON output
		},
		{
			name: "positional args",
			params: map[string]any{
				"args": []any{"arg1", "arg2"},
			},
			expected: []string{"--output", "json", "arg1", "arg2"}, // Auto-added for JSON output, positionals at end
		},
		{
			name: "mixed params",
			params: map[string]any{
				"args": []any{"pos1"},
				"flags": map[string]any{
					"verbose": true,
					"output":  "json",
				},
			},
			expected: []string{"--verbose", "--output", "json", "pos1"}, // User-specified output, not auto-added
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			args, _ := server.convertParamsToArgs(tt.params, cmd)
			assert.ElementsMatch(t, tt.expected, args)
		})
	}
}

// Tests for processOutput

func TestProcessOutput(t *testing.T) {
	t.Parallel()

	server := &mcpServer{}

	tests := []struct {
		name           string
		output         string
		config         responseConfig
		expectedText   string
		expectedLength int
	}{
		{
			name:           "short output no limiting",
			output:         "hello world",
			config:         responseConfig{Limit: 100, Offset: 0},
			expectedText:   "hello world",
			expectedLength: 11,
		},
		{
			name:           "output with limiting",
			output:         "hello world",
			config:         responseConfig{Limit: 5, Offset: 0},
			expectedText:   "hello",
			expectedLength: 5,
		},
		{
			name:           "output with offset",
			output:         "hello world",
			config:         responseConfig{Limit: 5, Offset: 6},
			expectedText:   "world",
			expectedLength: 5,
		},
		{
			name:           "negative offset",
			output:         "hello world",
			config:         responseConfig{Limit: 5, Offset: -5},
			expectedText:   "world",
			expectedLength: 5,
		},
		{
			name:           "unicode handling",
			output:         "héllo wörld",
			config:         responseConfig{Limit: 5, Offset: 0},
			expectedText:   "héllo",
			expectedLength: 5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result := server.processOutput(tt.output, tt.config)

			assert.Equal(t, tt.expectedText, result)
			assert.Len(t, []rune(result), tt.expectedLength)
		})
	}
}

// Tests for buildToolFromCommand

func TestBuildToolFromCommand(t *testing.T) {
	t.Parallel()

	server := &mcpServer{}
	cmd := createMockCommandWithFlags()

	tests := []struct {
		name        string
		toolName    string
		description string
		wantSchema  bool
	}{
		{
			name:        "basic tool creation",
			toolName:    "test_tool",
			description: "Test tool description",
			wantSchema:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tool := server.buildToolFromCommand(tt.toolName, tt.description, cmd)

			// Verify basic tool fields
			assert.Equal(t, tt.toolName, tool.Name)
			assert.Equal(t, tt.description, tool.Description)

			// Verify input schema exists and has correct structure
			require.NotNil(t, tool.InputSchema)
			schema, ok := tool.InputSchema.(map[string]any)
			require.True(t, ok, "InputSchema should be a map")

			// Verify schema type
			assert.Equal(t, "object", schema["type"])

			// Verify properties exist
			properties, ok := schema["properties"].(map[string]any)
			require.True(t, ok, "properties should exist")

			// Verify expected parameters
			assert.Contains(t, properties, "args", "should have args parameter")
			assert.Contains(t, properties, "flags", "should have flags parameter")
			assert.Contains(t, properties, "limit", "should have limit parameter")
			assert.Contains(t, properties, "offset", "should have offset parameter")

			// Verify flags object structure
			flagsParam, ok := properties["flags"].(map[string]any)
			require.True(t, ok, "flags should be an object")
			flagsProperties, ok := flagsParam["properties"].(map[string]any)
			require.True(t, ok, "flags should have properties")

			// Verify flags from test command are present
			assert.Contains(t, flagsProperties, "verbose")
			assert.Contains(t, flagsProperties, "output")
			assert.Contains(t, flagsProperties, "count")
			assert.Contains(t, flagsProperties, "labels")
		})
	}
}

func TestBuildToolFromCommandWithDestructiveAnnotation(t *testing.T) {
	t.Parallel()

	server := &mcpServer{}

	tests := []struct {
		name            string
		annotations     map[string]string
		wantDestructive bool
	}{
		{
			name:            "safe command",
			annotations:     map[string]string{"mcp:safe": "true"},
			wantDestructive: false,
		},
		{
			name:            "destructive command",
			annotations:     map[string]string{"mcp:destructive": "true"},
			wantDestructive: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cmd := createMockCommandWithAnnotations("test", "Test", tt.annotations)
			tool := server.buildToolFromCommand("test_tool", "description", cmd)

			if tt.wantDestructive {
				require.NotNil(t, tool.Annotations, "destructive tool should have annotations")
				require.NotNil(t, tool.Annotations.DestructiveHint, "should have destructive hint")
				assert.True(t, *tool.Annotations.DestructiveHint, "destructive hint should be true")
			} else {
				// Safe commands might not have annotations set, or DestructiveHint might be nil
				if tool.Annotations != nil && tool.Annotations.DestructiveHint != nil {
					assert.False(t, *tool.Annotations.DestructiveHint, "safe command should not be marked destructive")
				}
			}
		})
	}
}

// Tests for JSON unmarshaling in tool handler

func TestToolHandlerJSONUnmarshal(t *testing.T) {
	t.Parallel()

	server := &mcpServer{}
	cmd := createMockCommandWithFlags()

	tests := []struct {
		name        string
		jsonArgs    string
		expectError bool
	}{
		{
			name:        "valid JSON",
			jsonArgs:    `{"args": ["test"], "flags": {"verbose": true}}`,
			expectError: false,
		},
		{
			name:        "invalid JSON",
			jsonArgs:    `{invalid json}`,
			expectError: true,
		},
		{
			name:        "empty JSON",
			jsonArgs:    `{}`,
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Simulate what the handler does
			var params map[string]any
			jsonBytes := []byte(tt.jsonArgs)

			err := json.Unmarshal(jsonBytes, &params)

			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)

				// If valid, try converting to args
				if err == nil {
					args, config := server.convertParamsToArgs(params, cmd)
					// Verify conversion works without error
					// args is a slice that can be nil or empty, both are valid
					_ = args
					assert.Equal(t, defaultResponseLimit, config.Limit)
					assert.Equal(t, 0, config.Offset)
				}
			}
		})
	}
}

// Tests for tool result structure

func TestToolResultStructure(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		result    *mcp.CallToolResult
		wantError bool
	}{
		{
			name: "success result",
			result: &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{
						Text: "success output",
					},
				},
				IsError: false,
			},
			wantError: false,
		},
		{
			name: "error result",
			result: &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{
						Text: "error message",
					},
				},
				IsError: true,
			},
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.wantError, tt.result.IsError)
			assert.NotNil(t, tt.result.Content)
			assert.NotEmpty(t, tt.result.Content)
		})
	}
}

// Tests for metadata structure

// Tests for tool result response structure

func TestCallToolResultStructure(t *testing.T) {
	t.Parallel()

	// This test verifies that CallToolResult properly contains
	// Content (actual command output)
	tests := []struct {
		name            string
		output          string
		config          responseConfig
		wantContentText string
	}{
		{
			name:            "short output",
			output:          "test output content",
			config:          responseConfig{Limit: 50000, Offset: 0},
			wantContentText: "test output content",
		},
		{
			name:            "truncated output",
			output:          "This is a much longer output that will be truncated by the limit parameter",
			config:          responseConfig{Limit: 20, Offset: 0},
			wantContentText: "This is a much longe",
		},
		{
			name:            "output with offset",
			output:          "hello world",
			config:          responseConfig{Limit: 5, Offset: 6},
			wantContentText: "world",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			server := &mcpServer{}

			// Process output to get text (this is what the handler does)
			processedOutput := server.processOutput(tt.output, tt.config)

			// Create the result structure as the handler would
			result := &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{
						Text: processedOutput,
					},
				},
			}

			// Verify Content is present and populated
			require.NotNil(t, result.Content, "Content must not be nil")
			require.Len(t, result.Content, 1, "Content should have exactly one element")

			textContent, ok := result.Content[0].(*mcp.TextContent)
			require.True(t, ok, "Content[0] must be *TextContent")
			assert.Equal(t, tt.wantContentText, textContent.Text, "Content text should match expected output")

			// Verify Content is present in the response
			assert.NotNil(t, result.Content, "Content must be present in response")
			assert.NotEmpty(t, textContent.Text, "Content.Text must not be empty")
		})
	}
}

func TestCallToolResultErrorStructure(t *testing.T) {
	t.Parallel()

	// Test that error results also have proper Content
	result := &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{
				Text: "error message here",
			},
		},
		IsError: true,
	}

	require.NotNil(t, result.Content)
	require.Len(t, result.Content, 1)
	assert.True(t, result.IsError)

	textContent, ok := result.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	assert.Equal(t, "error message here", textContent.Text)
}

func TestStructuredContentIncludesToolOutput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		processedOutput string
		wantData        bool
	}{
		{
			name:            "non-json output includes content only",
			processedOutput: "plain text output",
			wantData:        false,
		},
		{
			name:            "json output includes content and data",
			processedOutput: `{"id":1,"name":"test"}`,
			wantData:        true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			server := &mcpServer{}

			structuredContent := server.buildStructuredContent(tt.processedOutput)

			result := &mcp.CallToolResult{StructuredContent: structuredContent}

			require.NotNil(t, result.StructuredContent)

			structuredMap, ok := result.StructuredContent.(map[string]any)
			require.True(t, ok, "structured content should be a map")
			assert.Equal(t, tt.processedOutput, structuredMap["content"])

			_, hasData := structuredMap["data"]
			assert.Equal(t, tt.wantData, hasData)
		})
	}
}
