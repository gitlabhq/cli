package utils

import (
	"fmt"
	"sort"
	"strings"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
)

// IssueType enum values from GitLab GraphQL API
// Used in workItems(types: [IssueType!]) filter parameter
// NOTE: These types will become customizable in future GitLab releases.
// This list is for reference only - API is source of truth for validation.
const (
	// Group-level work items
	TypeEpic      = "EPIC"
	TypeObjective = "OBJECTIVE"
	TypeKeyResult = "KEY_RESULT"
	// Project-level work items
	TypeIssue       = "ISSUE"
	TypeTask        = "TASK"
	TypeIncident    = "INCIDENT"
	TypeTicket      = "TICKET"
	TypeRequirement = "REQUIREMENT"
	TypeTestCase    = "TEST_CASE"
)

// AllKnownTypes contains current default work item types for reference.
// NOTE: This list will become outdated when work item types become customizable.
// Do not use for validation - API is the source of truth.
var AllKnownTypes = []string{
	TypeEpic,
	TypeObjective,
	TypeKeyResult,
	TypeIssue,
	TypeTask,
	TypeIncident,
	TypeTicket,
	TypeRequirement,
	TypeTestCase,
}

// WorkItemTypeIDs contains the current default work item type IDs
// NOTE: This list will become outdated when work items become customizable.
var workItemTypeIDs = map[string]gitlab.WorkItemTypeID{
	"epic":        gitlab.WorkItemTypeEpic,
	"issue":       gitlab.WorkItemTypeIssue,
	"task":        gitlab.WorkItemTypeTask,
	"incident":    gitlab.WorkItemTypeIncident,
	"ticket":      gitlab.WorkItemTypeTicket,
	"requirement": gitlab.WorkItemTypeRequirement,
	"test_case":   gitlab.WorkItemTypeTestCase,
	"objective":   gitlab.WorkItemTypeObjective,
	"key_result":  gitlab.WorkItemTypeKeyResult,
}

// ValidateTypes performs minimal format validation on work item types.
// Only checks that types are non-empty and non-whitespace.
// The GraphQL API is responsible for validating actual type names.
func ValidateTypes(types []string) error {
	for _, t := range types {
		if strings.TrimSpace(t) == "" {
			return fmt.Errorf("empty work item type not allowed")
		}
	}
	return nil
}

// ResolveTypeID will resolve the work item ID based on the type provided
func ResolveTypeID(t string) (gitlab.WorkItemTypeID, error) {
	wiType := strings.ToLower(strings.TrimSpace(t))

	v, ok := workItemTypeIDs[wiType]
	if !ok {
		return "", fmt.Errorf("--type must be one of %s", strings.Join(ValidTypeNames(), ", "))
	}
	return v, nil
}

// ValidTypeNames provides a list of the available type names as needed
func ValidTypeNames() []string {
	types := make([]string, 0, len(workItemTypeIDs))
	for i := range workItemTypeIDs {
		types = append(types, i)
	}

	sort.Strings(types)
	return types
}
