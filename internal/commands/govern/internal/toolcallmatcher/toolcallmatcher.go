// Package toolcallmatcher decides a tool call against a policy snapshot. It
// implements the AI Tool Call Control rule from
// https://gitlab.com/gitlab-org/gitlab/-/work_items/634673 that GitLab also
// transpiles to Rego. The shared test vectors describe the expected decision
// after normalisation.
package toolcallmatcher

import (
	"errors"
	"fmt"
	"path"
	"regexp"

	"gitlab.com/gitlab-org/cli/internal/commands/govern/internal/policysnapshot"
)

const (
	DecisionAllow = "allow"
	DecisionAsk   = "ask"
	DecisionDeny  = "deny"

	TargetCommand = "command"
	TargetFile    = "file"
	TargetMCP     = "mcp"

	OperationRead   = "read"
	OperationWrite  = "write"
	OperationEdit   = "edit"
	OperationDelete = "delete"
	operationAny    = "any"

	modeEnforce = "enforce"
)

var decisionFor = map[string]string{
	"block":    DecisionDeny,
	"ask_user": DecisionAsk,
}

// errNoMCPPatterns is for a rule that GitLab refuses to save, which would
// otherwise match every MCP call.
var errNoMCPPatterns = errors.New("an MCP rule needs a server, tool, or input pattern")

// Verdict is the decision on a call, and every policy that matched it.
type Verdict struct {
	Decision string
	// Matches includes policies that do not decide the call, such as one that
	// asks when another blocks, so that each can be reported.
	Matches []Match
}

// Match is a policy with a rule that matched the call. Decision is what the
// policy decides on its own.
type Match struct {
	PolicyID      uint64
	PolicyVersion int
	PolicyName    string
	Mode          string
	Action        string
	Message       string
	RuleIndex     int
	Target        string
	Decision      string
}

// Deciding returns the matches that produced the verdict's decision.
func (v Verdict) Deciding() []Match {
	var deciding []Match
	for _, match := range v.Matches {
		if match.Decision == v.Decision {
			deciding = append(deciding, match)
		}
	}
	return deciding
}

// Call is the part of a tool call that a rule reads. Only the fields of its
// Target are set. A rule matches when it matches any of Commands or FilePaths,
// so they can hold normalised forms next to the value the agent sent.
type Call struct {
	Target        string
	Commands      []string
	FileOperation string
	FilePaths     []string
	MCPServer     string
	MCPTool       string
	MCPInput      string
}

type Matcher struct {
	policies []compiledPolicy
}

type compiledPolicy struct {
	policy   policysnapshot.Policy
	decision string
	rules    []compiledRule
}

type compiledRule struct {
	index          int
	target         string
	operations     []string
	patterns       []*regexp.Regexp
	serverPatterns []*regexp.Regexp
	toolPatterns   []*regexp.Regexp
	inputPatterns  []*regexp.Regexp
}

// Compile skips each rule it cannot enforce, returns an error for it, and
// keeps the rest of the snapshot, so that one rule cannot switch every policy
// off. GitLab validates patterns as RE2 on save, so a pattern Go cannot
// compile means the two sides disagree on the syntax. AI tool-call policies are
// enforce-only and either block or ask, so Compile drops a policy in any
// other mode or without an action, which then never matches. An action this
// glab does not know is reported too, since GitLab sends only these two.
func Compile(snapshot *policysnapshot.Snapshot) (*Matcher, []error) {
	matcher := &Matcher{}
	var skipped []error
	for _, policy := range snapshot.Policies {
		if policy.Mode != modeEnforce {
			continue
		}
		decision, ok := decisionFor[policy.Action]
		if !ok {
			if policy.Action != "" {
				skipped = append(skipped, fmt.Errorf("policy %d (%q): unknown action %q", policy.ID, policy.Name, policy.Action))
			}
			continue
		}
		compiled := compiledPolicy{policy: policy, decision: decision}
		for _, rule := range policy.Rules {
			compiledRule, err := compileRule(rule)
			if err != nil {
				skipped = append(skipped, fmt.Errorf("policy %d (%q) rule %d: %w", policy.ID, policy.Name, rule.RuleIndex, err))
				continue
			}
			compiled.rules = append(compiled.rules, compiledRule)
		}
		matcher.policies = append(matcher.policies, compiled)
	}
	return matcher, skipped
}

func compileRule(rule policysnapshot.Rule) (compiledRule, error) {
	compiled := compiledRule{index: rule.RuleIndex, target: rule.Target, operations: rule.Operations}
	var err error
	switch rule.Target {
	case TargetCommand:
		compiled.patterns, err = compilePatterns(rule.BlockedPatterns)
	case TargetFile:
		compiled.patterns, err = compilePatterns(rule.PathPatterns)
	case TargetMCP:
		if len(rule.ServerPatterns)+len(rule.ToolPatterns)+len(rule.InputPatterns) == 0 {
			return compiled, errNoMCPPatterns
		}
		if compiled.serverPatterns, err = compilePatterns(rule.ServerPatterns); err != nil {
			return compiled, err
		}
		if compiled.toolPatterns, err = compilePatterns(rule.ToolPatterns); err != nil {
			return compiled, err
		}
		compiled.inputPatterns, err = compilePatterns(rule.InputPatterns)
	}
	return compiled, err
}

func compilePatterns(patterns []string) ([]*regexp.Regexp, error) {
	expressions := make([]*regexp.Regexp, 0, len(patterns))
	for _, pattern := range patterns {
		expression, err := regexp.Compile(pattern)
		if err != nil {
			return nil, err
		}
		expressions = append(expressions, expression)
	}
	return expressions, nil
}

// cleanPath collapses `..`, `.` and repeated separators as GitLab's
// ToolCallContext does with Pathname#cleanpath, on the string alone.
func cleanPath(filePath string) string {
	return path.Clean(filePath)
}

// matches follows the transpiled Rego: patterns are OR within a list, and an
// MCP rule needs a match in every field that has patterns.
func (rule compiledRule) matches(call Call) bool {
	if rule.target != call.Target {
		return false
	}
	switch rule.target {
	case TargetCommand:
		for _, command := range call.Commands {
			if anyMatches(rule.patterns, command) {
				return true
			}
		}
		return false
	case TargetFile:
		if !rule.coversOperation(call.FileOperation) {
			return false
		}
		for _, filePath := range call.FilePaths {
			if anyMatches(rule.patterns, cleanPath(filePath)) {
				return true
			}
		}
		return false
	case TargetMCP:
		return populatedMatches(rule.serverPatterns, call.MCPServer) &&
			populatedMatches(rule.toolPatterns, call.MCPTool) &&
			populatedMatches(rule.inputPatterns, call.MCPInput)
	default:
		return false
	}
}

func (rule compiledRule) coversOperation(operation string) bool {
	for _, covered := range rule.operations {
		if covered == operationAny || covered == operation {
			return true
		}
	}
	return false
}

func anyMatches(patterns []*regexp.Regexp, value string) bool {
	for _, pattern := range patterns {
		if pattern.MatchString(value) {
			return true
		}
	}
	return false
}

func populatedMatches(patterns []*regexp.Regexp, value string) bool {
	return len(patterns) == 0 || anyMatches(patterns, value)
}

func (policy compiledPolicy) matchingRule(call Call) (compiledRule, bool) {
	for _, rule := range policy.rules {
		if rule.matches(call) {
			return rule, true
		}
	}
	return compiledRule{}, false
}

// Decide ports Govern::PolicyEngine::ToolCallGate#decided_by: deny wins over
// ask.
func (matcher *Matcher) Decide(call Call) Verdict {
	verdict := Verdict{Decision: DecisionAllow}
	for _, compiled := range matcher.policies {
		rule, ok := compiled.matchingRule(call)
		if !ok {
			continue
		}
		policy := compiled.policy
		match := Match{
			PolicyID:      policy.ID,
			PolicyVersion: policy.Version,
			PolicyName:    policy.Name,
			Mode:          policy.Mode,
			Action:        policy.Action,
			Message:       policy.Message,
			RuleIndex:     rule.index,
			Target:        rule.target,
			Decision:      compiled.decision,
		}
		verdict.Matches = append(verdict.Matches, match)
		if precedence[match.Decision] > precedence[verdict.Decision] {
			verdict.Decision = match.Decision
		}
	}
	return verdict
}

var precedence = map[string]int{
	DecisionAllow: 0,
	DecisionAsk:   1,
	DecisionDeny:  2,
}
