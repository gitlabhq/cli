package issuable

import (
	"fmt"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
)

type IssueType string

const (
	TypeIssue    IssueType = "issue"
	TypeIncident IssueType = "incident"
)

// ValidateIncidentCmd returns an error when incident command is used with non-incident's IDs.
//
// Issues and incidents are the same kind, but with different issueType.
//
// For example:
// `issue view` can view issues of all types including incidents
// `incident view` on the other hand, should view only incidents, and treat all other issue types as not found
func ValidateIncidentCmd(cmd IssueType, subcmd string, issue *gitlab.Issue) (bool, string) {
	if cmd == TypeIncident && *issue.IssueType != string(TypeIncident) {
		return false, fmt.Sprintf(
			"Incident not found, but an issue with the provided ID exists. Run `glab issue %[1]s <id>` to %[1]s.",
			subcmd,
		)
	}

	return true, ""
}
