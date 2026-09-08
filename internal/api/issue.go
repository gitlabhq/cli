// This is a silly wrapper for gitlab client-go but helps maintain consistency
package api

import (
	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
)

// UpdateIssue updates an issue
// Attention: this is a global variable and may be overridden in tests.
var UpdateIssue = func(client *gitlab.Client, projectID any, issueID int64, opts *gitlab.UpdateIssueOptions) (*gitlab.Issue, error) {
	issue, _, err := client.Issues.UpdateIssue(projectID, issueID, opts)
	if err != nil {
		return nil, err
	}

	return issue, nil
}

// GetIssue returns an issue
// Attention: this is a global variable and may be overridden in tests.
var GetIssue = func(client *gitlab.Client, projectID any, issueID int64) (*gitlab.Issue, error) {
	issue, _, err := client.Issues.GetIssue(projectID, issueID)
	if err != nil {
		return nil, err
	}

	return issue, nil
}
