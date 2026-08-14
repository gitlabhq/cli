//go:build !integration

package view

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"testing"
	"time"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/acarl005/stripansi"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/commands/mr/mrutils"
	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/run"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
	"gitlab.com/gitlab-org/cli/internal/utils"
	"gitlab.com/gitlab-org/cli/test"
)

var (
	f         cmdutils.Factory
	stdout    *bytes.Buffer
	stderr    *bytes.Buffer
	ioStreams *iostreams.IOStreams
)

var testConfig = config.NewFromString(heredoc.Doc(`
	hosts:
	  gitlab.com:
	    username: monalisa
	    token: OTOKEN
`))

func TestMain(m *testing.M) {
	ioStreams, _, stdout, stderr = cmdtest.TestIOStreams(cmdtest.WithTestIOStreamsAsTTY(true))
	client, _ := gitlab.NewClient("")
	f = cmdtest.NewTestFactory(
		ioStreams,
		cmdtest.WithConfig(config.NewFromString(heredoc.Doc(`
			hosts:
			  gitlab.com:
			    username: monalisa
			    token: OTOKEN
		`))),
		cmdtest.WithGitLabClient(client),
	)

	timer, _ := time.Parse(time.RFC3339, "2014-11-12T11:45:26.371Z")
	api.GetMR = func(client *gitlab.Client, projectID any, mrID int64, opts *gitlab.GetMergeRequestsOptions) (*gitlab.MergeRequest, error) {
		if projectID == "" || projectID == "WRONG_REPO" || projectID == "expected_err" {
			return nil, fmt.Errorf("error expected")
		}

		// Use projectID directly instead of f.BaseRepo() to support per-test factories
		repoPath, ok := projectID.(string)
		if !ok {
			return nil, fmt.Errorf("unexpected projectID type: %T", projectID)
		}

		return &gitlab.MergeRequest{
			BasicMergeRequest: gitlab.BasicMergeRequest{
				ID:          mrID,
				IID:         mrID,
				Title:       "mrTitle",
				Labels:      gitlab.Labels{"test", "bug"},
				State:       "opened",
				Description: "mrBody",
				Author: &gitlab.BasicUser{
					ID:       mrID,
					Name:     "John Dev Wick",
					Username: "jdwick",
				},
				Assignees: []*gitlab.BasicUser{
					{
						Username: "mona",
					},
					{
						Username: "lisa",
					},
				},
				Reviewers: []*gitlab.BasicUser{
					{
						Username: "lisa",
					},
					{
						Username: "mona",
					},
				},
				WebURL:         fmt.Sprintf("https://gitlab.com/%s/-/merge_requests/%d", repoPath, mrID),
				CreatedAt:      &timer,
				UserNotesCount: 2,
				SourceBranch:   "feature-branch",
				TargetBranch:   "main",
				Milestone: &gitlab.Milestone{
					Title: "MilestoneTitle",
				},
			},
		}, nil
	}
	cmdtest.InitTest(m, "mr_view_test")
}

func TestMRView(t *testing.T) {
	oldListAllDiscussions := mrutils.ListAllDiscussions
	timer, _ := time.Parse(time.RFC3339, "2014-11-12T11:45:26.371Z")
	mrutils.ListAllDiscussions = func(_ context.Context, client *gitlab.Client, projectID any, mrID int64, opts *gitlab.ListMergeRequestDiscussionsOptions) ([]*gitlab.Discussion, error) {
		if projectID == "PROJECT_MR_WITH_EMPTY_NOTE" {
			return []*gitlab.Discussion{}, nil
		}
		return []*gitlab.Discussion{
			{
				ID:             "disc1",
				IndividualNote: true,
				Notes: []*gitlab.Note{
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
				},
			},
			{
				ID:             "disc2",
				IndividualNote: true,
				Notes: []*gitlab.Note{
					{
						ID:    2,
						Body:  "Marked MR as ready",
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
				},
			},
		}, nil
	}

	t.Run("show", func(t *testing.T) {
		client, _ := gitlab.NewClient("")
		exec := cmdtest.SetupCmdForTest(t, func(f cmdutils.Factory) *cobra.Command {
			cmd := NewCmdView(f)
			cmdutils.EnableRepoOverride(cmd, f)
			return cmd
		}, true,
			cmdtest.WithConfig(testConfig),
			cmdtest.WithGitLabClient(client),
		)

		result, err := exec("13 -c -s -R cli-automated-testing/test")
		require.NoError(t, err)

		out := stripansi.Strip(result.String())
		outErr := stripansi.Strip(result.Stderr())

		require.Contains(t, out, "mrTitle !13")
		require.Contains(t, out, "@jdwick opened")
		require.Contains(t, out, "merge: feature-branch → main")
		require.Empty(t, outErr)
		assert.Contains(t, out, "https://gitlab.com/cli-automated-testing/test/-/merge_requests/13")
		assert.Contains(t, out, "johnwick Marked MR as ready")
		assert.NotContains(t, out, "[discussion:")
	})

	t.Run("no_tty", func(t *testing.T) {
		client, _ := gitlab.NewClient("")
		exec := cmdtest.SetupCmdForTest(t, func(f cmdutils.Factory) *cobra.Command {
			cmd := NewCmdView(f)
			cmdutils.EnableRepoOverride(cmd, f)
			return cmd
		}, false, // non-TTY
			cmdtest.WithConfig(testConfig),
			cmdtest.WithGitLabClient(client),
		)

		result, err := exec("13 -c -s -R cli-automated-testing/test")
		require.NoError(t, err)

		out := stripansi.Strip(result.String())
		outErr := stripansi.Strip(result.Stderr())

		expectedOutputs := []string{
			`title:\tmrTitle`,
			`assignees:\tmona, lisa`,
			`reviewers:\tlisa, mona`,
			`author:\tjdwick`,
			`state:\topen`,
			`comments:\t2`,
			`labels:\ttest, bug`,
			`milestone:\tMilestoneTitle\n`,
			`source_branch:\tfeature-branch`,
			`target_branch:\tmain`,
			`--`,
			`mrBody`,
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
	})
	mrutils.ListAllDiscussions = oldListAllDiscussions
}

func Test_rawMRPreview(t *testing.T) {
	// NOTE: we need to force disable colors, otherwise we'd need ANSI sequences in our test output assertions.
	t.Setenv("NO_COLOR", "true")

	fakeNote1 := &gitlab.Note{}
	fakeNote1.Author.Username = "bob"
	fakeNote2 := &gitlab.Note{}
	fakeNote2.Author.Username = "alice"

	time1, _ := time.Parse(time.RFC3339, "2023-03-09T16:50:20.111Z")
	time2, _ := time.Parse(time.RFC3339, "2023-03-09T16:52:30.222Z")

	mr := &gitlab.MergeRequest{
		BasicMergeRequest: gitlab.BasicMergeRequest{
			IID:            503,
			Title:          "MR title",
			Description:    "MR description",
			State:          "merged",
			Author:         &gitlab.BasicUser{Username: "alice"},
			Labels:         gitlab.Labels{"label1", "label2"},
			Assignees:      []*gitlab.BasicUser{{Username: "alice"}, {Username: "bob"}},
			Reviewers:      []*gitlab.BasicUser{{Username: "john"}, {Username: "paul"}},
			UserNotesCount: 2,
			Milestone:      &gitlab.Milestone{Title: "Some milestone"},
			SourceBranch:   "topic",
			TargetBranch:   "main",
			WebURL:         "https://gitlab.com/OWNER/REPO/-/merge_requests/503",
		},
	}

	discussions := []*gitlab.Discussion{
		{
			ID:             "disc1",
			IndividualNote: true,
			Notes: []*gitlab.Note{
				{
					System:    true,
					Author:    fakeNote1.Author,
					Body:      "assigned to @alice",
					CreatedAt: &time1,
				},
			},
		},
		{
			ID:             "disc2",
			IndividualNote: true,
			Notes: []*gitlab.Note{
				{
					ID:        100,
					System:    false,
					Author:    fakeNote1.Author,
					Body:      "Some comment",
					CreatedAt: &time1,
				},
			},
		},
		{
			ID:             "disc3",
			IndividualNote: true,
			Notes: []*gitlab.Note{
				{
					ID:        200,
					System:    false,
					Author:    fakeNote2.Author,
					Body:      "Another comment",
					CreatedAt: &time2,
				},
			},
		},
	}

	// The raw preview is only produced for non-TTY output, so render bodies verbatim.
	ioStreams, _, _, _ = cmdtest.TestIOStreams(cmdtest.WithTestIOStreamsAsTTY(false))

	tests := []struct {
		name        string
		opts        *options
		mr          *gitlab.MergeRequest
		discussions []*gitlab.Discussion
		want        []string
		notWant     []string
	}{
		{
			"mr_default",
			&options{
				io: ioStreams,
			},
			mr,
			discussions,
			[]string{
				"title:\tMR title",
				"state:\tmerged",
				"author:\talice",
				"labels:\tlabel1, label2",
				"assignees:\talice, bob",
				"reviewers:\tjohn, paul",
				"source_branch:\ttopic",
				"target_branch:\tmain",
				"comments:\t2",
				"milestone:\tSome milestone",
				"number:\t503",
				"url:\thttps://gitlab.com/OWNER/REPO/-/merge_requests/503",
				"--",
				"MR description",
			},
			nil,
		},
		{
			"mr_show_comments_no_comments",
			&options{
				io:             ioStreams,
				showComments:   true,
				showSystemLogs: true,
			},
			mr,
			[]*gitlab.Discussion{},
			[]string{
				"title:\tMR title",
				"state:\tmerged",
				"author:\talice",
				"labels:\tlabel1, label2",
				"assignees:\talice, bob",
				"reviewers:\tjohn, paul",
				"source_branch:\ttopic",
				"target_branch:\tmain",
				"comments:\t2",
				"milestone:\tSome milestone",
				"number:\t503",
				"url:\thttps://gitlab.com/OWNER/REPO/-/merge_requests/503",
				"--",
				"MR description",
				"This merge request has no comments.",
			},
			nil,
		},
		{
			"mr_with_comments_and_notes",
			&options{
				io:             ioStreams,
				showComments:   true,
				showSystemLogs: true,
			},
			mr,
			discussions,
			[]string{
				"title:\tMR title",
				"state:\tmerged",
				"author:\talice",
				"labels:\tlabel1, label2",
				"assignees:\talice, bob",
				"reviewers:\tjohn, paul",
				"source_branch:\ttopic",
				"target_branch:\tmain",
				"comments:\t2",
				"milestone:\tSome milestone",
				"number:\t503",
				"url:\thttps://gitlab.com/OWNER/REPO/-/merge_requests/503",
				"--",
				"MR description",
				"@bob assigned to @alice",
				"@bob commented",
				"[note #100]",
				"Some comment",
				"@alice commented",
				"[note #200]",
				"Another comment",
			},
			[]string{"[discussion:"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := rawMRPreview(tt.opts, tt.mr, tt.discussions)
			for _, want := range tt.want {
				assert.Contains(t, got, want)
			}
			for _, notWant := range tt.notWant {
				assert.NotContains(t, got, notWant)
			}
		})
	}
}

func Test_labelsList(t *testing.T) {
	tests := []struct {
		name string
		mr   *gitlab.MergeRequest
		want string
	}{
		{
			"no labels",
			&gitlab.MergeRequest{BasicMergeRequest: gitlab.BasicMergeRequest{
				Labels: gitlab.Labels{},
			}},
			"",
		},
		{
			"one label",
			&gitlab.MergeRequest{BasicMergeRequest: gitlab.BasicMergeRequest{
				Labels: gitlab.Labels{"label1"},
			}},
			"label1",
		},
		{
			"two labels",
			&gitlab.MergeRequest{BasicMergeRequest: gitlab.BasicMergeRequest{
				Labels: gitlab.Labels{"label1", "label2"},
			}},
			"label1, label2",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := labelsList(test.mr)

			if test.want != got {
				t.Errorf(`want "%s"; got "%s"`, test.want, got)
			}
		})
	}
}

func Test_assigneesList(t *testing.T) {
	tests := []struct {
		name string
		mr   *gitlab.MergeRequest
		want string
	}{
		{
			"no assignee",
			&gitlab.MergeRequest{BasicMergeRequest: gitlab.BasicMergeRequest{
				Assignees: []*gitlab.BasicUser{},
			}},
			"",
		},
		{
			"one assignee",
			&gitlab.MergeRequest{BasicMergeRequest: gitlab.BasicMergeRequest{
				Assignees: []*gitlab.BasicUser{{Username: "Alice"}},
			}},
			"Alice",
		},
		{
			"two assignees",
			&gitlab.MergeRequest{BasicMergeRequest: gitlab.BasicMergeRequest{
				Assignees: []*gitlab.BasicUser{{Username: "Alice"}, {Username: "Bob"}},
			}},
			"Alice, Bob",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := assigneesList(test.mr)

			if test.want != got {
				t.Errorf(`want "%s"; got "%s"`, test.want, got)
			}
		})
	}
}

func Test_reviewersList(t *testing.T) {
	tests := []struct {
		name string
		mr   *gitlab.MergeRequest
		want string
	}{
		{
			"no assignee",
			&gitlab.MergeRequest{BasicMergeRequest: gitlab.BasicMergeRequest{
				Reviewers: []*gitlab.BasicUser{},
			}},
			"",
		},
		{
			"one assignee",
			&gitlab.MergeRequest{BasicMergeRequest: gitlab.BasicMergeRequest{
				Reviewers: []*gitlab.BasicUser{{Username: "Alice"}},
			}},
			"Alice",
		},
		{
			"two assignees",
			&gitlab.MergeRequest{BasicMergeRequest: gitlab.BasicMergeRequest{
				Reviewers: []*gitlab.BasicUser{{Username: "Alice"}, {Username: "Bob"}},
			}},
			"Alice, Bob",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := reviewersList(test.mr)

			if test.want != got {
				t.Errorf(`want "%s"; got "%s"`, test.want, got)
			}
		})
	}
}

func TestMrViewJSON(t *testing.T) {
	client, _ := gitlab.NewClient("")
	exec := cmdtest.SetupCmdForTest(t, NewCmdView, false,
		cmdtest.WithConfig(testConfig),
		cmdtest.WithGitLabClient(client),
	)

	output, err := exec("1 -F json")
	require.NoError(t, err)

	assert.True(t, json.Valid([]byte(output.String())))
	assert.Empty(t, output.Stderr())
}

func Test_printTTYMRPreview_closedMRWithNilClosedBy(t *testing.T) {
	// NOTE: we need to force disable colors, otherwise we'd need ANSI sequences in our test output assertions.
	t.Setenv("NO_COLOR", "true")

	ioStreams, _, stdout, _ := cmdtest.TestIOStreams(cmdtest.WithTestIOStreamsAsTTY(true))

	createdTime, _ := time.Parse(time.RFC3339, "2024-01-01T12:00:00Z")

	mr := &gitlab.MergeRequest{
		BasicMergeRequest: gitlab.BasicMergeRequest{
			IID:         505,
			Title:       "Test closed MR",
			Description: "Test description",
			State:       "closed", // Now test closed MR with nil ClosedBy
			Author:      &gitlab.BasicUser{Username: "testuser"},
			WebURL:      "https://gitlab.com/OWNER/REPO/-/merge_requests/505",
			CreatedAt:   &createdTime,
			ClosedBy:    nil,
		},
	}

	opts := &options{
		io:             ioStreams,
		showComments:   false,
		showSystemLogs: false,
	}

	// This should not panic - the bug would cause a nil pointer dereference here
	printTTYMRPreview(opts, mr, nil, []*gitlab.Discussion{})
	output := stdout.String()

	// Verify that it contains "Closed" but not "Closed by:" since ClosedBy is nil
	assert.Contains(t, output, "Closed")
	assert.NotContains(t, output, "Closed by:")
}

func Test_filterDiscussionsByResolution(t *testing.T) {
	timer, _ := time.Parse(time.RFC3339, "2014-11-12T11:45:26.371Z")

	tests := []struct {
		name        string
		discussions []*gitlab.Discussion
		state       string
		wantCount   int
		wantIDs     []string
	}{
		{
			name: "filter resolved discussions",
			discussions: []*gitlab.Discussion{
				{
					ID:             "disc1",
					IndividualNote: false,
					Notes: []*gitlab.Note{
						{ID: 1, Resolvable: true, Resolved: true, CreatedAt: &timer},
						{ID: 2, Resolvable: true, Resolved: true, CreatedAt: &timer},
					},
				},
				{
					ID:             "disc2",
					IndividualNote: false,
					Notes: []*gitlab.Note{
						{ID: 3, Resolvable: true, Resolved: false, CreatedAt: &timer},
					},
				},
				{
					ID:             "disc3",
					IndividualNote: true,
					Notes: []*gitlab.Note{
						{ID: 4, Resolvable: false, Resolved: false, CreatedAt: &timer},
					},
				},
			},
			state:     "resolved",
			wantCount: 1,
			wantIDs:   []string{"disc1"},
		},
		{
			name: "filter unresolved discussions",
			discussions: []*gitlab.Discussion{
				{
					ID:             "disc1",
					IndividualNote: false,
					Notes: []*gitlab.Note{
						{ID: 1, Resolvable: true, Resolved: true, CreatedAt: &timer},
						{ID: 2, Resolvable: true, Resolved: true, CreatedAt: &timer},
					},
				},
				{
					ID:             "disc2",
					IndividualNote: false,
					Notes: []*gitlab.Note{
						{ID: 3, Resolvable: true, Resolved: false, CreatedAt: &timer},
					},
				},
				{
					ID:             "disc3",
					IndividualNote: false,
					Notes: []*gitlab.Note{
						{ID: 4, Resolvable: true, Resolved: true, CreatedAt: &timer},
						{ID: 5, Resolvable: true, Resolved: false, CreatedAt: &timer},
					},
				},
			},
			state:     "unresolved",
			wantCount: 2,
			wantIDs:   []string{"disc2", "disc3"},
		},
		{
			name: "exclude non-resolvable discussions",
			discussions: []*gitlab.Discussion{
				{
					ID:             "disc1",
					IndividualNote: true,
					Notes: []*gitlab.Note{
						{ID: 1, Resolvable: false, Resolved: false, CreatedAt: &timer},
					},
				},
				{
					ID:             "disc2",
					IndividualNote: false,
					Notes: []*gitlab.Note{
						{ID: 2, Resolvable: true, Resolved: true, CreatedAt: &timer},
					},
				},
			},
			state:     "resolved",
			wantCount: 1,
			wantIDs:   []string{"disc2"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mrutils.FilterDiscussions(tt.discussions, mrutils.FilterOpts{State: tt.state})
			require.Len(t, got, tt.wantCount)

			gotIDs := []string{}
			for _, d := range got {
				gotIDs = append(gotIDs, d.ID)
			}
			assert.Equal(t, tt.wantIDs, gotIDs)
		})
	}
}

func TestMRViewWeb(t *testing.T) {
	tests := []struct {
		name              string
		cli               string
		wantURL           string
		wantGetMRCalls    int
		wantBranchLookups int
	}{
		{
			name: "by branch, resolved from the list response",
			cli:  "-w -R cli-automated-testing/test",
			// The web URL comes from the branch lookup, so the merge request
			// itself is never fetched.
			wantURL:           "https://gitlab.com/cli-automated-testing/test/-/merge_requests/42",
			wantGetMRCalls:    0,
			wantBranchLookups: 1,
		},
		{
			name:              "by ID",
			cli:               "13 -w -R cli-automated-testing/test",
			wantURL:           "https://gitlab.com/cli-automated-testing/test/-/merge_requests/13",
			wantGetMRCalls:    1,
			wantBranchLookups: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var getMRCalls, branchLookups int

			oldGetMR := api.GetMR
			api.GetMR = func(client *gitlab.Client, projectID any, mrID int64, opts *gitlab.GetMergeRequestsOptions) (*gitlab.MergeRequest, error) {
				getMRCalls++
				return oldGetMR(client, projectID, mrID, opts)
			}
			t.Cleanup(func() { api.GetMR = oldGetMR })

			oldGetMRForBranch := mrutils.GetMRForBranch
			mrutils.GetMRForBranch = func(_ context.Context, _ *iostreams.IOStreams, _ *gitlab.Client, mrOpts mrutils.MrOptions) (*gitlab.BasicMergeRequest, error) {
				branchLookups++
				return &gitlab.BasicMergeRequest{
					IID:    42,
					WebURL: fmt.Sprintf("https://gitlab.com/%s/-/merge_requests/42", mrOpts.BaseRepo.FullName()),
				}, nil
			}
			t.Cleanup(func() { mrutils.GetMRForBranch = oldGetMRForBranch })

			var browsedURL string
			restoreCmd := run.SetPrepareCmd(func(cmd *exec.Cmd) run.Runnable {
				browsedURL = cmd.Args[len(cmd.Args)-1]
				return &test.OutputStub{}
			})
			defer restoreCmd()

			client, _ := gitlab.NewClient("")
			execCmd := cmdtest.SetupCmdForTest(t, func(f cmdutils.Factory) *cobra.Command {
				cmd := NewCmdView(f)
				cmdutils.EnableRepoOverride(cmd, f)
				return cmd
			}, true,
				cmdtest.WithConfig(testConfig),
				cmdtest.WithGitLabClient(client),
				cmdtest.WithBranch("feature-branch"),
			)

			result, err := execCmd(tt.cli)
			require.NoError(t, err)

			assert.Equal(t, tt.wantURL, browsedURL)
			assert.Contains(t, stripansi.Strip(result.Stderr()), "Opening "+utils.DisplayURL(tt.wantURL)+" in your browser.")
			assert.Equal(t, tt.wantGetMRCalls, getMRCalls, "unexpected number of merge request fetches")
			assert.Equal(t, tt.wantBranchLookups, branchLookups, "unexpected number of branch lookups")
		})
	}
}
