//go:build !integration

package issuable

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
)

func TestValidateIncidentCmd(t *testing.T) {
	issueTypeIssue := "issue"
	issueTypeIncident := "incident"
	tests := []struct {
		name   string
		cmd    IssueType
		subcmd string
		issue  *gitlab.Issue
		valid  bool
	}{
		{
			name:   "valid_incident_view_command",
			cmd:    TypeIncident,
			subcmd: "view",
			issue: &gitlab.Issue{
				IssueType: &issueTypeIncident,
			},
			valid: true,
		},
		{
			name:   "invalid_incident_view_command",
			cmd:    TypeIncident,
			subcmd: "view",
			issue: &gitlab.Issue{
				IssueType: &issueTypeIssue,
			},
			valid: false,
		},
		{
			name:   "valid_issue_view_command_for_issue",
			cmd:    TypeIssue,
			subcmd: "view",
			issue: &gitlab.Issue{
				IssueType: &issueTypeIssue,
			},
			valid: true,
		},
		{
			name:   "valid_issue_view_command_for_incident",
			cmd:    TypeIssue,
			subcmd: "view",
			issue: &gitlab.Issue{
				IssueType: &issueTypeIncident,
			},
			valid: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			valid, msg := ValidateIncidentCmd(tt.cmd, tt.subcmd, tt.issue)
			assert.Equal(t, tt.valid, valid)

			if !valid {
				assert.Equal(
					t,
					fmt.Sprintf("Incident not found, but an issue with the provided ID exists. Run `glab issue %[1]s <id>` to %[1]s.", tt.subcmd),
					msg,
				)
			}
		})
	}
}
