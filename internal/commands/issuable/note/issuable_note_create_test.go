//go:build !integration

package note

import (
	"errors"
	"net/http"
	"testing"

	"git.sr.ht/~timofurrer/ugh"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/commands/issuable"
	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func Test_NewCmdNote(t *testing.T) {
	t.Parallel()

	commands := []struct {
		name      string
		issueType issuable.IssueType
	}{
		{"issue", issuable.TypeIssue},
		{"incident", issuable.TypeIncident},
	}

	for _, cc := range commands {
		t.Run(cc.name+"_message_flag_specified", func(t *testing.T) {
			t.Parallel()

			testClient := gitlabtesting.NewTestClient(t)

			// Mock GetIssue
			testClient.MockIssues.EXPECT().
				GetIssue("OWNER/REPO", int64(1), gomock.Any()).
				Return(&gitlab.Issue{
					ID:        1,
					IID:       1,
					IssueType: new(string(cc.issueType)),
					WebURL:    "https://gitlab.com/OWNER/REPO/issues/1",
				}, nil, nil)

			// Mock CreateIssueNote
			testClient.MockNotes.EXPECT().
				CreateIssueNote("OWNER/REPO", int64(1), gomock.Any()).
				DoAndReturn(func(pid any, issueIID int64, opts *gitlab.CreateIssueNoteOptions, options ...gitlab.RequestOptionFunc) (*gitlab.Note, *gitlab.Response, error) {
					assert.Equal(t, "Here is my note", *opts.Body)
					return &gitlab.Note{
						ID:           301,
						NoteableID:   1,
						NoteableType: "Issue",
						NoteableIID:  1,
					}, nil, nil
				})

			exec := cmdtest.SetupCmdForTest(t, func(f cmdutils.Factory) *cobra.Command {
				return NewCmdNote(f, cc.issueType)
			}, true,
				cmdtest.WithGitLabClient(testClient.Client),
				cmdtest.WithBaseRepo("OWNER", "REPO", ""),
				cmdtest.WithConfig(config.NewFromString("editor: vi")),
			)

			output, err := exec(`1 --message "Here is my note"`)
			require.NoError(t, err)
			assert.Empty(t, output.Stderr())
			assert.Equal(t, "https://gitlab.com/OWNER/REPO/issues/1#note_301\n", output.String())
		})

		t.Run(cc.name+"_issue_not_found", func(t *testing.T) {
			t.Parallel()

			testClient := gitlabtesting.NewTestClient(t)

			// Mock GetIssue - returns 404
			notFoundResp := &gitlab.Response{
				Response: &http.Response{StatusCode: http.StatusNotFound},
			}
			testClient.MockIssues.EXPECT().
				GetIssue("OWNER/REPO", int64(122), gomock.Any()).
				Return(nil, notFoundResp, gitlab.ErrNotFound)

			exec := cmdtest.SetupCmdForTest(t, func(f cmdutils.Factory) *cobra.Command {
				return NewCmdNote(f, cc.issueType)
			}, true,
				cmdtest.WithGitLabClient(testClient.Client),
				cmdtest.WithBaseRepo("OWNER", "REPO", ""),
				cmdtest.WithConfig(config.NewFromString("editor: vi")),
			)

			_, err := exec(`122`)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "Not Found")
		})
	}
}

func Test_NewCmdNote_error(t *testing.T) {
	t.Parallel()

	commands := []struct {
		name      string
		issueType issuable.IssueType
	}{
		{"issue", issuable.TypeIssue},
		{"incident", issuable.TypeIncident},
	}

	for _, cc := range commands {
		t.Run(cc.name+"_note_could_not_be_created", func(t *testing.T) {
			t.Parallel()

			testClient := gitlabtesting.NewTestClient(t)

			// Mock GetIssue
			testClient.MockIssues.EXPECT().
				GetIssue("OWNER/REPO", int64(1), gomock.Any()).
				Return(&gitlab.Issue{
					ID:        1,
					IID:       1,
					IssueType: new(string(cc.issueType)),
					WebURL:    "https://gitlab.com/OWNER/REPO/issues/1",
				}, nil, nil)

			// Mock CreateIssueNote - returns 401
			unauthorizedResp := &gitlab.Response{
				Response: &http.Response{StatusCode: http.StatusUnauthorized},
			}
			testClient.MockNotes.EXPECT().
				CreateIssueNote("OWNER/REPO", int64(1), gomock.Any()).
				Return(nil, unauthorizedResp, errors.New("401 Unauthorized"))

			exec := cmdtest.SetupCmdForTest(t, func(f cmdutils.Factory) *cobra.Command {
				return NewCmdNote(f, cc.issueType)
			}, true,
				cmdtest.WithGitLabClient(testClient.Client),
				cmdtest.WithBaseRepo("OWNER", "REPO", ""),
				cmdtest.WithConfig(config.NewFromString("editor: vi")),
			)

			_, err := exec(`1 -m "Some message"`)
			require.Error(t, err)
		})
	}

	t.Run("using incident note command with issue ID", func(t *testing.T) {
		t.Parallel()

		testClient := gitlabtesting.NewTestClient(t)

		// Mock GetIssue - returns an issue (not incident)
		testClient.MockIssues.EXPECT().
			GetIssue("OWNER/REPO", int64(1), gomock.Any()).
			Return(&gitlab.Issue{
				ID:        1,
				IID:       1,
				IssueType: new("issue"), // Not an incident
				WebURL:    "https://gitlab.com/OWNER/REPO/issues/1",
			}, nil, nil)

		exec := cmdtest.SetupCmdForTest(t, func(f cmdutils.Factory) *cobra.Command {
			return NewCmdNote(f, issuable.TypeIncident)
		}, true,
			cmdtest.WithGitLabClient(testClient.Client),
			cmdtest.WithBaseRepo("OWNER", "REPO", ""),
			cmdtest.WithConfig(config.NewFromString("editor: vi")),
		)

		output, err := exec(`1 -m "Some message"`)
		require.NoError(t, err)
		assert.Equal(t, "Incident not found, but an issue with the provided ID exists. Run `glab issue comment <id>` to comment.\n", output.String())
	})
}

func Test_IssuableNoteCreate_prompt(t *testing.T) {
	// NOTE: This test cannot run in parallel because the huh form library
	// uses global state (charmbracelet/bubbles runeutil sanitizer).
	commands := []struct {
		name      string
		issueType issuable.IssueType
	}{
		{"issue", issuable.TypeIssue},
		{"incident", issuable.TypeIncident},
	}

	for _, cc := range commands {
		t.Run(cc.name+"_message_provided", func(t *testing.T) {
			testClient := gitlabtesting.NewTestClient(t)

			// Mock GetIssue
			testClient.MockIssues.EXPECT().
				GetIssue("OWNER/REPO", int64(1), gomock.Any()).
				Return(&gitlab.Issue{
					ID:        1,
					IID:       1,
					IssueType: new(string(cc.issueType)),
					WebURL:    "https://gitlab.com/OWNER/REPO/issues/1",
				}, nil, nil)

			// Mock CreateIssueNote
			testClient.MockNotes.EXPECT().
				CreateIssueNote("OWNER/REPO", int64(1), gomock.Any()).
				DoAndReturn(func(pid any, issueIID int64, opts *gitlab.CreateIssueNoteOptions, options ...gitlab.RequestOptionFunc) (*gitlab.Note, *gitlab.Response, error) {
					// The prompt adds a trailing newline
					assert.Contains(t, *opts.Body, "some note message")
					return &gitlab.Note{
						ID:           301,
						NoteableID:   1,
						NoteableType: "Issue",
						NoteableIID:  1,
					}, nil, nil
				})

			c := ugh.New(t)
			c.Expect(ugh.Input("Message:")).
				Do(ugh.Type("some note message"))

			exec := cmdtest.SetupCmdForTest(t, func(f cmdutils.Factory) *cobra.Command {
				return NewCmdNote(f, cc.issueType)
			}, true,
				cmdtest.WithGitLabClient(testClient.Client),
				cmdtest.WithBaseRepo("OWNER", "REPO", ""),
				cmdtest.WithConfig(config.NewFromString("editor: vi")),
				cmdtest.WithConsole(t, c),
			)

			output, err := exec(`1`)
			require.NoError(t, err)
			assert.Empty(t, output.Stderr())
			assert.Contains(t, output.String(), "https://gitlab.com/OWNER/REPO/issues/1#note_301")
		})

		tests := []struct {
			name    string
			message string
		}{
			{"message is empty", ""},
			{"message contains only spaces", "   "},
			{"message contains only line breaks", "\n\n"},
		}

		for _, tt := range tests {
			t.Run(cc.name+"_"+tt.name, func(t *testing.T) {
				testClient := gitlabtesting.NewTestClient(t)

				// Mock GetIssue
				testClient.MockIssues.EXPECT().
					GetIssue("OWNER/REPO", int64(1), gomock.Any()).
					Return(&gitlab.Issue{
						ID:        1,
						IID:       1,
						IssueType: new(string(cc.issueType)),
						WebURL:    "https://gitlab.com/OWNER/REPO/issues/1",
					}, nil, nil)

				c := ugh.New(t)
				c.Expect(ugh.Input("Message:")).
					Do(ugh.Type(tt.message))

				exec := cmdtest.SetupCmdForTest(t, func(f cmdutils.Factory) *cobra.Command {
					return NewCmdNote(f, cc.issueType)
				}, true,
					cmdtest.WithGitLabClient(testClient.Client),
					cmdtest.WithBaseRepo("OWNER", "REPO", ""),
					cmdtest.WithConfig(config.NewFromString("editor: vi")),
					cmdtest.WithConsole(t, c),
				)

				_, err := exec(`1`)
				require.Error(t, err)
				assert.Equal(t, "aborted: note is empty", err.Error())
			})
		}
	}
}
