//go:build !integration

package toolcallmatcher

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/cli/internal/commands/govern/internal/policysnapshot"
)

func snapshotWith(policies ...policysnapshot.Policy) *policysnapshot.Snapshot {
	return &policysnapshot.Snapshot{SchemaVersion: policysnapshot.SchemaVersion, Policies: policies}
}

func policyWith(name, mode, action, message string, rules ...policysnapshot.Rule) policysnapshot.Policy {
	return policysnapshot.Policy{Name: name, Mode: mode, Action: action, Message: message, Rules: rules}
}

func commandRule(patterns ...string) policysnapshot.Rule {
	return policysnapshot.Rule{Target: TargetCommand, BlockedPatterns: patterns}
}

func commandPolicy(name, mode, action, message string, patterns ...string) policysnapshot.Policy {
	return policyWith(name, mode, action, message, commandRule(patterns...))
}

func rawCommandCall(command string) Call {
	return Call{Target: TargetCommand, Commands: []string{command}}
}

func decidingNames(verdict Verdict) []string {
	var names []string
	for _, match := range verdict.Deciding() {
		names = append(names, match.PolicyName)
	}
	return names
}

func TestDecide_Command(t *testing.T) {
	t.Parallel()

	publish := `npm\s+publish`
	tests := []struct {
		name         string
		policies     []policysnapshot.Policy
		wantDecision string
		wantDeciding []string
	}{
		{
			name:         "an enforced block policy denies",
			policies:     []policysnapshot.Policy{commandPolicy("No publishing", "enforce", "block", "Use CI/CD", publish)},
			wantDecision: DecisionDeny,
			wantDeciding: []string{"No publishing"},
		},
		{
			name:         "an enforced ask_user policy asks",
			policies:     []policysnapshot.Policy{commandPolicy("Confirm", "enforce", "ask_user", "Confirm the publish", publish)},
			wantDecision: DecisionAsk,
			wantDeciding: []string{"Confirm"},
		},
		{
			name: "a deny wins over an ask",
			policies: []policysnapshot.Policy{
				commandPolicy("Confirm", "enforce", "ask_user", "Confirm the publish", publish),
				commandPolicy("No publishing", "enforce", "block", "Use CI/CD", publish),
			},
			wantDecision: DecisionDeny,
			wantDeciding: []string{"No publishing"},
		},
		{
			name: "several denying policies all decide",
			policies: []policysnapshot.Policy{
				commandPolicy("First", "enforce", "block", "First reason", publish),
				commandPolicy("Second", "enforce", "block", "Second reason", `publish`),
			},
			wantDecision: DecisionDeny,
			wantDeciding: []string{"First", "Second"},
		},
		{
			name:         "a policy whose patterns do not match allows",
			policies:     []policysnapshot.Policy{commandPolicy("No pushing", "enforce", "block", "No", `docker\s+push`)},
			wantDecision: DecisionAllow,
		},
		{
			name: "a rule for another target is ignored",
			policies: []policysnapshot.Policy{policyWith("Files", "enforce", "block", "No",
				policysnapshot.Rule{Target: TargetFile, Operations: []string{operationAny}, PathPatterns: []string{publish}})},
			wantDecision: DecisionAllow,
		},
		{
			name:         "a rule with an unknown target is ignored",
			policies:     []policysnapshot.Policy{policyWith("Future", "enforce", "block", "No", policysnapshot.Rule{Target: "network"})},
			wantDecision: DecisionAllow,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			matcher, skipped := Compile(snapshotWith(tt.policies...))
			require.Empty(t, skipped)

			verdict := matcher.Decide(rawCommandCall("npm publish"))

			assert.Equal(t, tt.wantDecision, verdict.Decision)
			assert.Equal(t, tt.wantDeciding, decidingNames(verdict))
		})
	}
}

func TestDecide_ListsEveryMatchedPolicy(t *testing.T) {
	t.Parallel()
	blocking := policysnapshot.Policy{
		ID: 12, Version: 3, Name: "No publishing", Mode: "enforce", Action: "block", Message: "Use CI/CD",
		Rules: []policysnapshot.Rule{
			{RuleIndex: 0, Target: TargetCommand, BlockedPatterns: []string{`docker\s+push`}},
			{RuleIndex: 2, Target: TargetCommand, BlockedPatterns: []string{`npm\s+publish`}},
		},
	}
	asking := policysnapshot.Policy{
		ID: 7, Version: 1, Name: "Confirm npm", Mode: "enforce", Action: "ask_user",
		Rules: []policysnapshot.Rule{{RuleIndex: 1, Target: TargetCommand, BlockedPatterns: []string{`^npm\b`}}},
	}
	unrelated := commandPolicy("No pushing", "enforce", "block", "No", `git\s+push`)
	matcher, skipped := Compile(snapshotWith(blocking, unrelated, asking))
	require.Empty(t, skipped)

	verdict := matcher.Decide(rawCommandCall("npm publish"))

	assert.Equal(t, DecisionDeny, verdict.Decision)
	require.Len(t, verdict.Matches, 2)
	assert.Equal(t, Match{
		PolicyID: 12, PolicyVersion: 3, PolicyName: "No publishing", Mode: "enforce", Action: "block",
		Message: "Use CI/CD", RuleIndex: 2, Target: TargetCommand, Decision: DecisionDeny,
	}, verdict.Matches[0])
	assert.Equal(t, Match{
		PolicyID: 7, PolicyVersion: 1, PolicyName: "Confirm npm", Mode: "enforce", Action: "ask_user",
		RuleIndex: 1, Target: TargetCommand, Decision: DecisionAsk,
	}, verdict.Matches[1])
}

func TestCompile_IgnoresPoliciesThatDoNotEnforceABlockOrAnAsk(t *testing.T) {
	t.Parallel()

	tests := []struct {
		mode   string
		action string
	}{
		{mode: "warn", action: "block"},
		{mode: "audit", action: "block"},
		{mode: "", action: "block"},
		{mode: "enforce", action: ""},
	}
	for _, tt := range tests {
		t.Run(tt.mode+" "+tt.action, func(t *testing.T) {
			t.Parallel()
			matcher, skipped := Compile(snapshotWith(
				commandPolicy("Ignored", tt.mode, tt.action, "No", `npm\s+publish`),
				commandPolicy("Confirm", "enforce", "ask_user", "Confirm the publish", `npm\s+publish`),
			))
			require.Empty(t, skipped)

			verdict := matcher.Decide(rawCommandCall("npm publish"))

			assert.Equal(t, DecisionAsk, verdict.Decision)
			assert.Equal(t, []string{"Confirm"}, decidingNames(verdict))
			require.Len(t, verdict.Matches, 1)
		})
	}
}

func TestCompile_ReportsAnUnknownAction(t *testing.T) {
	t.Parallel()

	for _, action := range []string{"log_audit_trail", "Block"} {
		t.Run(action, func(t *testing.T) {
			t.Parallel()
			unknown := commandPolicy("Unknown", "enforce", action, "No", `npm\s+publish`)
			unknown.ID = 7
			matcher, skipped := Compile(snapshotWith(unknown))

			require.Len(t, skipped, 1)
			require.ErrorContains(t, skipped[0], fmt.Sprintf(`policy 7 ("Unknown"): unknown action %q`, action))
			assert.Empty(t, matcher.Decide(rawCommandCall("npm publish")).Matches)
		})
	}
}

func TestDecide_MatchesAnyCommandForm(t *testing.T) {
	t.Parallel()
	matcher, skipped := Compile(snapshotWith(commandPolicy("No publishing", "enforce", "block", "No", `^npm\s+publish$`)))
	require.Empty(t, skipped)

	call := Call{Target: TargetCommand, Commands: []string{"cd app && npm publish", "cd app", "npm publish"}}

	assert.Equal(t, DecisionDeny, matcher.Decide(call).Decision)
}

func TestDecide_File(t *testing.T) {
	t.Parallel()

	envRule := policysnapshot.Rule{Target: TargetFile, Operations: []string{OperationRead, OperationEdit}, PathPatterns: []string{`(^|/)\.env$`}}
	anyRule := policysnapshot.Rule{Target: TargetFile, Operations: []string{operationAny}, PathPatterns: []string{`^/Users/dev/\.ssh/`}}
	matcher, skipped := Compile(snapshotWith(
		policyWith("No env files", "enforce", "block", "No .env", envRule),
		policyWith("No SSH keys", "enforce", "block", "No SSH keys", anyRule),
	))
	require.Empty(t, skipped)

	tests := []struct {
		name      string
		operation string
		paths     []string
		want      string
	}{
		{name: "a listed operation on a matching path", operation: OperationRead, paths: []string{"/repo/.env"}, want: DecisionDeny},
		{name: "an operation the rule does not list", operation: OperationWrite, paths: []string{"/repo/.env"}, want: DecisionAllow},
		{name: "a path that does not match", operation: OperationRead, paths: []string{"/repo/.env.example"}, want: DecisionAllow},
		{name: "any covers every operation", operation: OperationWrite, paths: []string{"/Users/dev/.ssh/id_rsa"}, want: DecisionDeny},
		{name: "any covers a delete", operation: OperationDelete, paths: []string{"/Users/dev/.ssh/id_rsa"}, want: DecisionDeny},
		{name: "a path that steps around the anchor", operation: OperationRead, paths: []string{"/repo/../Users/dev/.ssh/id_rsa"}, want: DecisionDeny},
		{name: "a path with repeated separators", operation: OperationRead, paths: []string{"/Users/dev//.ssh/id_rsa"}, want: DecisionDeny},
		{name: "a later form that matches", operation: OperationRead, paths: []string{"id_rsa", "/Users/dev/.ssh/id_rsa"}, want: DecisionDeny},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			verdict := matcher.Decide(Call{Target: TargetFile, FileOperation: tt.operation, FilePaths: tt.paths})

			assert.Equal(t, tt.want, verdict.Decision)
		})
	}
}

func TestDecide_MCP(t *testing.T) {
	t.Parallel()

	databaseRule := policysnapshot.Rule{
		Target:         TargetMCP,
		ServerPatterns: []string{`postgres`},
		InputPatterns:  []string{`(?i)\bdrop\s+table\b`},
	}
	toolRule := policysnapshot.Rule{Target: TargetMCP, ToolPatterns: []string{`^delete_`}}
	matcher, skipped := Compile(snapshotWith(
		policyWith("No dropped tables", "enforce", "block", "No DROP TABLE", databaseRule),
		policyWith("Confirm deletes", "enforce", "ask_user", "Confirm the delete", toolRule),
	))
	require.Empty(t, skipped)

	tests := []struct {
		name string
		call Call
		want string
	}{
		{
			name: "every populated field matches",
			call: Call{Target: TargetMCP, MCPServer: "postgres", MCPTool: "query", MCPInput: `{"sql":"DROP TABLE users;"}`},
			want: DecisionDeny,
		},
		{
			name: "the server matches but the input does not",
			call: Call{Target: TargetMCP, MCPServer: "postgres", MCPTool: "query", MCPInput: `{"sql":"SELECT 1"}`},
			want: DecisionAllow,
		},
		{
			name: "the input matches but the server does not",
			call: Call{Target: TargetMCP, MCPServer: "github", MCPTool: "query", MCPInput: `{"sql":"DROP TABLE users;"}`},
			want: DecisionAllow,
		},
		{
			name: "a rule with only tool patterns ignores the server and input",
			call: Call{Target: TargetMCP, MCPServer: "github", MCPTool: "delete_repository", MCPInput: `{}`},
			want: DecisionAsk,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, matcher.Decide(tt.call).Decision)
		})
	}
}

func TestCompile_SkipsARuleItCannotEnforceAndKeepsTheRest(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		rule policysnapshot.Rule
	}{
		{name: "a command pattern", rule: commandRule("(unclosed")},
		{name: "a path pattern", rule: policysnapshot.Rule{Target: TargetFile, Operations: []string{operationAny}, PathPatterns: []string{"(unclosed"}}},
		{name: "an MCP server pattern", rule: policysnapshot.Rule{Target: TargetMCP, ServerPatterns: []string{"(unclosed"}}},
		{name: "an MCP tool pattern", rule: policysnapshot.Rule{Target: TargetMCP, ToolPatterns: []string{"(unclosed"}}},
		{name: "an MCP input pattern", rule: policysnapshot.Rule{Target: TargetMCP, InputPatterns: []string{"(unclosed"}}},
		{name: "an MCP rule without patterns", rule: policysnapshot.Rule{Target: TargetMCP}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			broken := policyWith("Broken", "enforce", "block", "No", tt.rule, commandRule(`^npm\s+publish$`))
			broken.ID = 4
			matcher, skipped := Compile(snapshotWith(broken, commandPolicy("Working", "enforce", "block", "No", `^docker\s+push$`)))

			require.Len(t, skipped, 1)
			require.ErrorContains(t, skipped[0], `policy 4 ("Broken") rule 0`)
			assert.Equal(t, DecisionDeny, matcher.Decide(rawCommandCall("npm publish")).Decision, "the policy's other rules still apply")
			assert.Equal(t, DecisionDeny, matcher.Decide(rawCommandCall("docker push")).Decision, "other policies still apply")
		})
	}
}
