//go:build !integration

package view

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/acarl005/stripansi"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/commands/issuable"
	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

var (
	f      cmdutils.Factory
	stdout *bytes.Buffer
	stderr *bytes.Buffer
	io     *iostreams.IOStreams
)

var testConfig = config.NewFromString(heredoc.Doc(`
	hosts:
	  gitlab.com:
	    username: monalisa
	    token: OTOKEN
`))

type issuableData struct {
	title       string
	description string
	issueType   issuable.IssueType
	labels      gitlab.Labels
}

var testIssuables = map[int]issuableData{
	13: {
		title:       "Incident title",
		description: "Incident body",
		issueType:   issuable.TypeIncident,
		labels:      gitlab.Labels{"test", "incident"},
	},
	14: {
		title:       "Issue title",
		description: "Issue body",
		issueType:   issuable.TypeIssue,
		labels:      gitlab.Labels{"test", "bug"},
	},
	225: {
		title:       "Incident title",
		description: "Incident body",
		issueType:   issuable.TypeIncident,
		labels:      gitlab.Labels{"test", "incident"},
	},
}

func TestMain(m *testing.M) {
	io, _, stdout, stderr = cmdtest.TestIOStreams(cmdtest.WithTestIOStreamsAsTTY(true))
	f = cmdtest.NewTestFactory(io, cmdtest.WithConfig(config.NewFromString(heredoc.Doc(`
		hosts:
		  gitlab.com:
		    username: monalisa
		    token: OTOKEN
	`))))

	timer, _ := time.Parse(time.RFC3339, "2014-11-12T11:45:26.371Z")
	api.GetIssue = func(client *gitlab.Client, projectID any, issueID int64) (*gitlab.Issue, error) {
		if projectID == "" || projectID == "WRONG_REPO" || projectID == "expected_err" {
			return nil, fmt.Errorf("error expected")
		}

		// Use projectID directly instead of f.BaseRepo() to support per-test factories
		repoPath, ok := projectID.(string)
		if !ok {
			return nil, fmt.Errorf("unexpected projectID type: %T", projectID)
		}

		testIssuable := testIssuables[int(issueID)]
		issueType := string(testIssuable.issueType)

		return &gitlab.Issue{
			ID:          issueID,
			IID:         issueID,
			Title:       testIssuable.title,
			Labels:      testIssuable.labels,
			State:       "opened",
			Description: testIssuable.description,
			References: &gitlab.IssueReferences{
				Full: fmt.Sprintf("%s#%d", repoPath, issueID),
			},
			Milestone: &gitlab.Milestone{
				Title: "MilestoneTitle",
			},
			Assignees: []*gitlab.IssueAssignee{
				{
					Username: "mona",
				},
				{
					Username: "lisa",
				},
			},
			Author: &gitlab.IssueAuthor{
				ID:       issueID,
				Name:     "John Dev Wick",
				Username: "jdwick",
			},
			WebURL:         fmt.Sprintf("https://gitlab.com/%s/-/issues/%d", repoPath, issueID),
			CreatedAt:      &timer,
			UserNotesCount: 2,
			IssueType:      &issueType,
		}, nil
	}
	cmdtest.InitTest(m, "mr_view_test")
}

func TestNewCmdView(t *testing.T) {
	tests := []struct {
		name          string
		issueID       int
		viewIssueType issuable.IssueType
		isTTY         bool
	}{
		{"incident_view", 13, issuable.TypeIncident, true},
		{"issue_view", 14, issuable.TypeIssue, true},
		{"incident_view_no_tty", 13, issuable.TypeIncident, false},
		{"issue_view_no_tty", 14, issuable.TypeIssue, false},
		{"incident_view_with_issue_id", 14, issuable.TypeIncident, true},
		{"issue_view_view_with_incident_id", 13, issuable.TypeIssue, true},
		{"incident_view_with_issue_id_no_tty", 14, issuable.TypeIncident, false},
		{"issue_view_view_with_incident_id_no_tty", 13, issuable.TypeIssue, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testIssuable := testIssuables[tt.issueID]
			oldListIssueNotes := listIssueNotes
			timer, _ := time.Parse(time.RFC3339, "2014-11-12T11:45:26.371Z")
			listIssueNotes = func(client *gitlab.Client, projectID any, issueID int64, opts *gitlab.ListIssueNotesOptions) ([]*gitlab.Note, error) {
				if projectID == "PROJECT_MR_WITH_EMPTY_NOTE" {
					return []*gitlab.Note{}, nil
				}
				return []*gitlab.Note{
					{
						ID:    1,
						Body:  "Note Body",
						Title: "Note Title",
						Author: gitlab.NoteAuthor{
							ID:       1,
							Username: "johnwick",
							Name:     "John Wick",
						},
						System:     false,
						CreatedAt:  &timer,
						NoteableID: 0,
					},
					{
						ID:    1,
						Body:  fmt.Sprintf("Marked %s as stale", testIssuable.issueType),
						Title: "",
						Author: gitlab.NoteAuthor{
							ID:       1,
							Username: "johnwick",
							Name:     "John Wick",
						},
						System:     true,
						CreatedAt:  &timer,
						NoteableID: 0,
					},
				}, nil
			}

			exec := cmdtest.SetupCmdForTest(t, func(f cmdutils.Factory) *cobra.Command {
				cmd := NewCmdView(f, tt.viewIssueType)
				cmdutils.EnableRepoOverride(cmd, f)
				return cmd
			}, tt.isTTY,
				cmdtest.WithConfig(testConfig),
			)

			result, err := exec(fmt.Sprintf("%d -c -s -R cli-automated-testing/test", tt.issueID))
			require.NoError(t, err)

			out := stripansi.Strip(result.String())
			outErr := stripansi.Strip(result.Stderr())

			viewIncidentWithIssueID := tt.viewIssueType == issuable.TypeIncident && testIssuable.issueType != issuable.TypeIncident
			wantErrorMsg := "Incident not found, but an issue with the provided ID exists. Run `glab issue view <id>` to view.\n"

			if tt.isTTY {
				if viewIncidentWithIssueID {
					require.Equal(t, wantErrorMsg, outErr)
				} else {
					require.Empty(t, outErr)
					require.Contains(t, out, fmt.Sprintf("%s #%d", testIssuable.title, tt.issueID))
					require.Contains(t, out, testIssuable.description)
					assert.Contains(t, out, fmt.Sprintf("https://gitlab.com/cli-automated-testing/test/-/issues/%d", tt.issueID))
					assert.Contains(t, out, fmt.Sprintf("johnwick Marked %s as stale", testIssuable.issueType))
				}
			} else {
				if viewIncidentWithIssueID {
					assert.Equal(t, wantErrorMsg, outErr)
				} else {
					expectedOutputs := []string{
						fmt.Sprintf(`title:\t%s`, testIssuable.title),
						`assignees:\tmona, lisa`,
						`author:\tjdwick`,
						`state:\topen`,
						`comments:\t2`,
						fmt.Sprintf(`labels:\t%s`, strings.Join([]string(testIssuable.labels), ", ")),
						`milestone:\tMilestoneTitle\n`,
						`--`,
						testIssuable.description,
					}

					assert.Empty(t, outErr)
					t.Helper()
					var r *regexp.Regexp
					for _, l := range expectedOutputs {
						r = regexp.MustCompile(l)
						if !r.MatchString(out) {
							t.Errorf("output did not match regexp /%s/\n> output\n%s\n", r, out)
							return
						}
					}
				}
			}

			listIssueNotes = oldListIssueNotes
		})
	}
}

func Test_rawIssuePreview(t *testing.T) {
	// NOTE: we need to force disable colors, otherwise we'd need ANSI sequences in our test output assertions.
	t.Setenv("NO_COLOR", "true")

	issueType := string(issuable.TypeIssue)
	incidentType := string(issuable.TypeIncident)

	fakeNote1 := &gitlab.Note{}
	fakeNote1.Author.Username = "bob"
	fakeNote2 := &gitlab.Note{}
	fakeNote2.Author.Username = "alice"

	time1, _ := time.Parse(time.RFC3339, "2023-03-09T16:50:20.111Z")
	time2, _ := time.Parse(time.RFC3339, "2023-03-09T16:52:30.222Z")

	io, _, _, _ = cmdtest.TestIOStreams(cmdtest.WithTestIOStreamsAsTTY(true))

	tests := []struct {
		name string
		opts *options
		want []string
	}{
		{
			"issue_default",
			&options{
				io: io,
				issue: &gitlab.Issue{
					Title:          "Issue title",
					State:          "opened",
					Author:         &gitlab.IssueAuthor{Username: "alice"},
					Labels:         gitlab.Labels{"label1", "label2"},
					Assignees:      []*gitlab.IssueAssignee{{Username: "Alice"}, {Username: "Bob"}},
					UserNotesCount: 2,
					Description:    "Issue description",
					IssueType:      &issueType,
					Milestone:      &gitlab.Milestone{Title: "Milestone 5"},
				},
				showComments: false,
			},
			[]string{
				"title:\tIssue title",
				"state:\topen",
				"author:\talice",
				"labels:\tlabel1, label2",
				"comments:\t2",
				"assignees:\tAlice, Bob",
				"milestone:\tMilestone 5",
				"--",
				"Issue description",
			},
		},
		{
			"issue_show_comments_no_comments",
			&options{
				io: io,
				issue: &gitlab.Issue{
					Title:          "Issue title",
					Author:         &gitlab.IssueAuthor{Username: "alice"},
					UserNotesCount: 2,
					Description:    "Issue description",
					IssueType:      &issueType,
					Milestone:      &gitlab.Milestone{Title: "Milestone 5"},
				},
				showComments: true,
			},
			[]string{
				"title:\tIssue title",
				"state:\t",
				"author:\talice",
				"labels:\t",
				"comments:\t2",
				"assignees:\t",
				"milestone:\tMilestone 5",
				"--",
				"Issue description",
				"\n--\ncomments/notes:\n",
				"There are no comments on this issue.",
			},
		},
		{
			"incident_show_comments_no_comments",
			&options{
				io: io,
				issue: &gitlab.Issue{
					Title:          "Incident title",
					Author:         &gitlab.IssueAuthor{Username: "alice"},
					UserNotesCount: 2,
					Description:    "Incident description",
					IssueType:      &incidentType,
					Milestone:      &gitlab.Milestone{Title: "Milestone 5"},
				},
				showComments: true,
			},
			[]string{
				"title:\tIncident title",
				"state:\t",
				"author:\talice",
				"labels:\t",
				"comments:\t2",
				"assignees:\t",
				"milestone:\tMilestone 5",
				"--",
				"Incident description",
				"\n--\ncomments/notes:\n",
				"There are no comments on this incident.",
			},
		},
		{
			"issue_show_comments_with_comments_and_system_notes",
			&options{
				io: io,
				issue: &gitlab.Issue{
					Title:          "Issue title",
					Author:         &gitlab.IssueAuthor{Username: "alice"},
					UserNotesCount: 2,
					Description:    "Issue description",
					IssueType:      &issueType,
					Milestone:      &gitlab.Milestone{Title: "Milestone 5"},
				},
				showComments:   true,
				showSystemLogs: true,
				notes: []*gitlab.Note{
					{
						System:    true,
						Author:    fakeNote1.Author,
						Body:      "assigned to @alice",
						CreatedAt: &time1,
					},
					{
						System:    false,
						Author:    fakeNote1.Author,
						Body:      "Some comment",
						CreatedAt: &time1,
					},
					{
						System:    false,
						Author:    fakeNote2.Author,
						Body:      "Another comment",
						CreatedAt: &time2,
					},
				},
			},
			[]string{
				"title:\tIssue title",
				"state:\t",
				"author:\talice",
				"labels:\t",
				"comments:\t2",
				"assignees:\t",
				"milestone:\tMilestone 5",
				"--",
				"Issue description",
				"\n--\ncomments/notes:\n",
				fmt.Sprintf("bob assigned to @alice %s", time1),
				"",
				fmt.Sprintf("bob commented %s", time1),
				"Some comment",
				"",
				fmt.Sprintf("alice commented %s", time2),
				"Another comment",
				"",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			want := strings.Join(tt.want, "\n") + "\n"
			got := rawIssuePreview(tt.opts)

			require.Equal(t, want, got)
		})
	}
}

func Test_labelsList(t *testing.T) {
	tests := []struct {
		name string
		opts *options
		want string
	}{
		{
			"no labels",
			&options{issue: &gitlab.Issue{Labels: gitlab.Labels{}}},
			"",
		},
		{
			"one label",
			&options{issue: &gitlab.Issue{Labels: gitlab.Labels{"label1"}}},
			"label1",
		},
		{
			"two labels",
			&options{issue: &gitlab.Issue{Labels: gitlab.Labels{"label1", "label2"}}},
			"label1, label2",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := labelsList(test.opts)

			if test.want != got {
				t.Errorf(`want "%s"; got "%s"`, test.want, got)
			}
		})
	}
}

func Test_assigneesList(t *testing.T) {
	tests := []struct {
		name string
		opts *options
		want string
	}{
		{
			"no assignee",
			&options{issue: &gitlab.Issue{Assignees: []*gitlab.IssueAssignee{}}},
			"",
		},
		{
			"one assignee",
			&options{issue: &gitlab.Issue{Assignees: []*gitlab.IssueAssignee{{Username: "Alice"}}}},
			"Alice",
		},
		{
			"two assignees",
			&options{issue: &gitlab.Issue{Assignees: []*gitlab.IssueAssignee{{Username: "Alice"}, {Username: "Bob"}}}},
			"Alice, Bob",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := assigneesList(test.opts)

			if test.want != got {
				t.Errorf(`want "%s"; got "%s"`, test.want, got)
			}
		})
	}
}

func Test_printTTYIssuePreview_closed(t *testing.T) {
	createdAt, _ := time.Parse(time.RFC3339, "2014-11-12T11:45:26.371Z")
	closedAt, _ := time.Parse(time.RFC3339, "2015-01-15T09:00:00.000Z")
	issueType := string(issuable.TypeIssue)

	tests := []struct {
		name         string
		issue        *gitlab.Issue
		wantContains []string
		wantOmits    []string
	}{
		{
			name: "closed_issue_with_nil_closed_by_does_not_panic",
			issue: &gitlab.Issue{
				Title:     "Closed issue",
				State:     "closed",
				Author:    &gitlab.IssueAuthor{Username: "alice"},
				CreatedAt: &createdAt,
				IssueType: &issueType,
				ClosedBy:  nil,
				ClosedAt:  nil,
			},
			wantOmits: []string{"Closed by:"},
		},
		{
			name: "closed_issue_with_closed_by_and_closed_at",
			issue: &gitlab.Issue{
				Title:     "Closed issue",
				State:     "closed",
				Author:    &gitlab.IssueAuthor{Username: "alice"},
				CreatedAt: &createdAt,
				IssueType: &issueType,
				ClosedBy:  &gitlab.IssueCloser{Username: "bob"},
				ClosedAt:  &closedAt,
			},
			wantContains: []string{"Closed by: bob "},
		},
		{
			name: "closed_issue_with_closed_by_but_no_closed_at",
			issue: &gitlab.Issue{
				Title:     "Closed issue",
				State:     "closed",
				Author:    &gitlab.IssueAuthor{Username: "alice"},
				CreatedAt: &createdAt,
				IssueType: &issueType,
				ClosedBy:  &gitlab.IssueCloser{Username: "bob"},
				ClosedAt:  nil,
			},
			wantContains: []string{"Closed by: bob\n"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ios, _, stdout, _ := cmdtest.TestIOStreams(cmdtest.WithTestIOStreamsAsTTY(true))
			opts := &options{io: ios, issue: tt.issue}

			require.NotPanics(t, func() { printTTYIssuePreview(opts) })

			out := stripansi.Strip(stdout.String())
			for _, want := range tt.wantContains {
				assert.Contains(t, out, want)
			}
			for _, omit := range tt.wantOmits {
				assert.NotContains(t, out, omit)
			}
		})
	}
}

func TestIssueViewJSON(t *testing.T) {
	exec := cmdtest.SetupCmdForTest(t, func(f cmdutils.Factory) *cobra.Command {
		return NewCmdView(f, issuable.TypeIssue)
	}, false,
		cmdtest.WithConfig(testConfig),
	)

	output, err := exec("1 -F json")
	require.NoError(t, err)

	assert.True(t, json.Valid([]byte(output.String())))
	assert.Empty(t, output.Stderr())
}
