//go:build !integration

package cmdutils

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"git.sr.ht/~timofurrer/ugh"
	"github.com/acarl005/stripansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/git"
	"gitlab.com/gitlab-org/cli/internal/glinstance"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
)

const (
	testConsoleWidth  = 120
	testConsoleHeight = 40
)

func testIOStreamsWithConsole(t *testing.T, c *ugh.Console) (*iostreams.IOStreams, context.CancelFunc) {
	t.Helper()

	appInR, appInW := io.Pipe()
	appOutR, appOutW := io.Pipe()

	ctx, cancel := context.WithCancel(t.Context())
	wait := c.Start(ctx, appOutR, appInW)

	ios := iostreams.New(
		iostreams.WithStdin(appInR, true),
		iostreams.WithStdout(appOutW, true),
		iostreams.WithStderr(io.Discard, true),
		iostreams.WithProgramOptions(tea.WithWindowSize(testConsoleWidth, testConsoleHeight)),
	)

	cleanup := func() {
		appInW.Close()
		appOutW.Close()
		appOutR.Close()
		cancel()
		wait()
	}

	return ios, cleanup
}

// testIOStreams creates IOStreams for testing (avoids import cycle with cmdtest)
func testIOStreams() *iostreams.IOStreams {
	in := &bytes.Buffer{}
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}

	return iostreams.New(
		iostreams.WithStdin(io.NopCloser(in), false),
		iostreams.WithStdout(out, false),
		iostreams.WithStderr(errOut, false),
	)
}

func Test_ParseAssignees(t *testing.T) {
	testCases := []struct {
		name        string
		input       []string
		wantAdd     []string
		wantRemove  []string
		wantReplace []string
	}{
		{
			name:        "simple replace",
			input:       []string{"foo"},
			wantAdd:     []string{},
			wantRemove:  []string{},
			wantReplace: []string{"foo"},
		},
		{
			name:        "only add",
			input:       []string{"+foo"},
			wantAdd:     []string{"foo"},
			wantRemove:  []string{},
			wantReplace: []string{},
		},
		{
			name:        "only remove",
			input:       []string{"-foo", "!bar"},
			wantAdd:     []string{},
			wantRemove:  []string{"foo", "bar"},
			wantReplace: []string{},
		},
		{
			name:        "only replace",
			input:       []string{"baz"},
			wantAdd:     []string{},
			wantRemove:  []string{},
			wantReplace: []string{"baz"},
		},
		{
			name:        "add and remove",
			input:       []string{"+qux", "-foo", "!bar"},
			wantAdd:     []string{"qux"},
			wantRemove:  []string{"foo", "bar"},
			wantReplace: []string{},
		},
		{
			name:        "add and replace",
			input:       []string{"+foo", "bar"},
			wantAdd:     []string{"foo"},
			wantRemove:  []string{},
			wantReplace: []string{"bar"},
		},
		{
			name:        "remove and replace",
			input:       []string{"-foo", "bar", "!baz"},
			wantAdd:     []string{},
			wantRemove:  []string{"foo", "baz"},
			wantReplace: []string{"bar"},
		},
		{
			name:        "ignore empty assignee",
			input:       []string{"", "foo"},
			wantAdd:     []string{},
			wantRemove:  []string{},
			wantReplace: []string{"foo"},
		},
	}
	for _, tC := range testCases {
		t.Run(tC.name, func(t *testing.T) {
			uaGot := ParseAssignees(tC.input)
			assert.ElementsMatch(t, uaGot.ToAdd, tC.wantAdd)
			assert.ElementsMatch(t, uaGot.ToRemove, tC.wantRemove)
			assert.ElementsMatch(t, uaGot.ToReplace, tC.wantReplace)
		})
	}
}

func Test_VerifyAssignees(t *testing.T) {
	testCases := []struct {
		name  string
		input UserAssignments
		want  string // expected error message
	}{
		{
			name: "empty, no errors",
			input: UserAssignments{
				ToAdd:     []string{},
				ToRemove:  []string{},
				ToReplace: []string{},
			},
		},
		{
			name: "simple addition, no errors",
			input: UserAssignments{
				ToAdd:     []string{"foo"},
				ToRemove:  []string{},
				ToReplace: []string{},
			},
		},
		{
			name: "simple removal, no errors",
			input: UserAssignments{
				ToAdd:     []string{},
				ToRemove:  []string{"foo"},
				ToReplace: []string{},
			},
		},
		{
			name: "simple replace, no errors",
			input: UserAssignments{
				ToAdd:     []string{},
				ToRemove:  []string{},
				ToReplace: []string{"foo"},
			},
		},
		{
			name: "add and removal with multiple elements, no errors",
			input: UserAssignments{
				ToAdd:     []string{"foo", "bar", "baz"},
				ToRemove:  []string{"qux", "quux", "quz"},
				ToReplace: []string{},
			},
		},
		{
			name: "multi replace, no errors",
			input: UserAssignments{
				ToAdd:     []string{},
				ToRemove:  []string{},
				ToReplace: []string{"foo", "bar"},
			},
		},
		{
			name: "replace with add, error",
			input: UserAssignments{
				ToAdd:     []string{"bar"},
				ToRemove:  []string{},
				ToReplace: []string{"foo"},
			},
			want: "mixing relative (+,!,-) and absolute assignments is forbidden",
		},
		{
			name: "replace with remove, error",
			input: UserAssignments{
				ToAdd:     []string{},
				ToRemove:  []string{"baz"},
				ToReplace: []string{"foo"},
			},
			want: "mixing relative (+,!,-) and absolute assignments is forbidden",
		},
		{
			name: "overlapping add and removal element, error",
			input: UserAssignments{
				ToAdd:     []string{"foo"},
				ToRemove:  []string{"foo"},
				ToReplace: []string{},
			},
			want: `1 element "foo" present in both add and remove, which is forbidden`,
		},
		{
			name: "overlapping add and removal elements, error",
			input: UserAssignments{
				ToAdd:     []string{"foo", "bar", "baz"},
				ToRemove:  []string{"foo", "baz"},
				ToReplace: []string{},
			},
			want: `2 elements "foo baz" present in both add and remove, which is forbidden`,
		},
	}
	for _, tC := range testCases {
		t.Run(tC.name, func(t *testing.T) {
			err := tC.input.VerifyAssignees()
			if tC.want == "" {
				if err != nil {
					t.Errorf("VerifyAssignees() unexpected error = %s", err)
				}
			} else {
				if tC.want != err.Error() {
					t.Errorf("VerifyAssignees() expected = %s, got = %s", tC.want, err.Error())
				}
			}
		})
	}
}

func Test_UsersFromReplaces(t *testing.T) {
	testCases := []struct {
		name           string
		users          []*gitlab.User
		expectedIDs    []int64
		expectedAction []string
	}{
		{
			name:           "nothingness",
			users:          []*gitlab.User{},
			expectedIDs:    []int64{},
			expectedAction: []string{},
		},
		{
			name: "single user named foo",
			users: []*gitlab.User{
				{ID: 1, Username: "foo"},
			},
			expectedIDs:    []int64{1},
			expectedAction: []string{`assigned to "@foo"`},
		},
		{
			name: "multiple users named foo, bar and baz",
			users: []*gitlab.User{
				{ID: 1, Username: "foo"},
				{ID: 3, Username: "bar"},
				{ID: 7, Username: "baz"},
			},
			expectedIDs:    []int64{1, 3, 7},
			expectedAction: []string{`assigned to "@foo @bar @baz"`},
		},
	}
	for _, tC := range testCases {
		t.Run(tC.name, func(t *testing.T) {
			ua := UserAssignments{}
			api.UsersByNames = func(apiClient *gitlab.Client, names []string) ([]*gitlab.User, error) {
				return tC.users, nil
			}
			var gotAction []string
			gotIDs, gotAction, err := ua.UsersFromReplaces(&gitlab.Client{}, gotAction)
			if err != nil {
				t.Errorf("UsersFromReplaces() unexpected error = %s", err)
			}
			assert.ElementsMatch(t, *gotIDs, tC.expectedIDs)
			assert.ElementsMatch(t, gotAction, tC.expectedAction)
		})
	}
}

func Test_UserAssignmentsAPIFailure(t *testing.T) {
	want := "failed to get users by their names" // Error message we want
	ua := UserAssignments{
		ToAdd: []string{"foo"},
	} // Fill `ToAdd` so `cmdutils.UsersFromAddRemove()` reaches the api call
	var err error

	api.UsersByNames = func(apiClient *gitlab.Client, names []string) ([]*gitlab.User, error) {
		return nil, fmt.Errorf("failed to get users by their names")
	}

	apiClient := gitlab.Client{} // Empty Client, it won't be used, just to satisfy the function signature
	_, _, err = ua.UsersFromReplaces(&apiClient, nil)
	if err == nil {
		t.Errorf("UsersFromReplaces() expected error to not be nil")
	}
	if want != err.Error() {
		t.Errorf("UsersFromReplace() expected error = %s, got = %v", want, err)
	}

	_, _, err = ua.UsersFromAddRemove(nil, nil, &apiClient, nil)
	if err == nil {
		t.Errorf("UsersFromReplaces() expected error to not be nil")
	}
	if want != err.Error() {
		t.Errorf("UsersFromReplace() expected error = %s, got = %v", want, err)
	}
}

func Test_UsersFromAddRemove(t *testing.T) {
	testCases := []struct {
		name           string
		users          []*gitlab.User          // Mock *gitlab.User received from api.UsersByNames
		merge          []*gitlab.BasicUser     // Mock `.Assignee field` from a merge request
		issue          []*gitlab.IssueAssignee // Mock `.Assignee field` from an issue
		expectedIDs    []int64
		expectedAction []string
		ua             UserAssignments
		wantErr        string
	}{
		{
			name: "add foo (issue and merge request)",
			users: []*gitlab.User{
				{
					ID:       1,
					Username: "foo",
				},
			},
			expectedIDs:    []int64{1},
			expectedAction: []string{`assigned "@foo"`},
			ua:             UserAssignments{ToAdd: []string{"foo"}},
		},
		{
			name: "add foo, bar and baz (issue and merge request)",
			users: []*gitlab.User{
				{
					ID:       1,
					Username: "foo",
				},
				{
					ID:       235,
					Username: "bar",
				},
				{
					ID:       1500,
					Username: "baz",
				},
			},
			expectedIDs:    []int64{1, 235, 1500},
			expectedAction: []string{`assigned "@foo @bar @baz"`},
			ua:             UserAssignments{ToAdd: []string{"foo", "bar", "baz"}},
		},
		{
			name:  "remove foo (issue)",
			users: []*gitlab.User{},
			issue: []*gitlab.IssueAssignee{
				{
					ID:       1,
					Username: "foo",
				},
			},
			expectedIDs:    []int64{0},
			expectedAction: []string{`unassigned "@foo"`},
			ua:             UserAssignments{ToRemove: []string{"foo"}},
		},
		{
			name:  "remove foo and baz out of foo, bar and baz (issue)",
			users: []*gitlab.User{},
			issue: []*gitlab.IssueAssignee{
				{
					ID:       1,
					Username: "foo",
				},
				{
					ID:       2,
					Username: "bar",
				},
				{
					ID:       3,
					Username: "baz",
				},
			},
			expectedIDs:    []int64{2},
			expectedAction: []string{`unassigned "@foo @baz"`},
			ua:             UserAssignments{ToRemove: []string{"foo", "baz"}},
		},
		{
			name: "remove foo out of foo and baz and add bar (issue)",
			users: []*gitlab.User{
				{
					ID:       100,
					Username: "bar",
				},
			},
			issue: []*gitlab.IssueAssignee{
				{
					ID:       1,
					Username: "foo",
				},
				{
					ID:       500,
					Username: "baz",
				},
			},
			expectedIDs: []int64{500, 100},
			expectedAction: []string{
				`unassigned "@foo"`,
				`assigned "@bar"`,
			},
			ua: UserAssignments{
				ToAdd:    []string{"bar"},
				ToRemove: []string{"foo"},
			},
		},
		{
			name:  "remove foo (merge request)",
			users: []*gitlab.User{},
			merge: []*gitlab.BasicUser{
				{
					ID:       1,
					Username: "foo",
				},
			},
			expectedIDs:    []int64{0},
			expectedAction: []string{`unassigned "@foo"`},
			ua:             UserAssignments{ToRemove: []string{"foo"}},
		},
		{
			name:  "remove foo and baz out of foo, bar and baz (merge request)",
			users: []*gitlab.User{},
			merge: []*gitlab.BasicUser{
				{
					ID:       1,
					Username: "foo",
				},
				{
					ID:       2,
					Username: "bar",
				},
				{
					ID:       3,
					Username: "baz",
				},
			},
			expectedIDs:    []int64{2},
			expectedAction: []string{`unassigned "@foo @baz"`},
			ua:             UserAssignments{ToRemove: []string{"foo", "baz"}},
		},
		{
			name: "remove foo out of foo and baz and add bar (merge request)",
			users: []*gitlab.User{
				{
					ID:       100,
					Username: "bar",
				},
			},
			merge: []*gitlab.BasicUser{
				{
					ID:       1,
					Username: "foo",
				},
				{
					ID:       500,
					Username: "baz",
				},
			},
			expectedIDs: []int64{500, 100},
			expectedAction: []string{
				`unassigned "@foo"`,
				`assigned "@bar"`,
			},
			ua: UserAssignments{
				ToAdd:    []string{"bar"},
				ToRemove: []string{"foo"},
			},
		},
		{
			name: "try to pass both issue and merge request users",
			issue: []*gitlab.IssueAssignee{
				{
					ID:       1,
					Username: "foo",
				},
			},
			merge: []*gitlab.BasicUser{
				{
					ID:       5,
					Username: "bar",
				},
			},
			wantErr: "issueAssignees and mergeRequestAssignees can't both be set",
		},
	}
	for _, tC := range testCases {
		t.Run(tC.name, func(t *testing.T) {
			api.UsersByNames = func(_ *gitlab.Client, _ []string) ([]*gitlab.User, error) {
				return tC.users, nil
			}
			var gotAction []string
			gotIDs, gotAction, err := tC.ua.UsersFromAddRemove(tC.issue, tC.merge, &gitlab.Client{}, gotAction)
			if err != nil {
				if tC.wantErr != "" && tC.wantErr != err.Error() {
					t.Errorf("UsersFromAddRemove() expected error = %s, got = %s", tC.wantErr, err)
				} else if tC.wantErr == "" {
					t.Errorf("UsersFromAddRemove() unexpected error = %s", err)
				}
			}

			assert.ElementsMatch(t, *gotIDs, tC.expectedIDs)
			assert.ElementsMatch(t, gotAction, tC.expectedAction)
		})
	}
}

func Test_ParseMilestoneTitleIsID(t *testing.T) {
	title := "1"
	expectedMilestoneID := int64(1)

	// Override function to return an error, it should never reach this
	projectMilestoneByTitle = func(client *gitlab.Client, projectID any, name string) (*gitlab.Milestone, error) {
		return nil, fmt.Errorf("We shouldn't have reached here")
	}

	got, err := ParseMilestone(&gitlab.Client{}, glrepo.New("foo", "bar", glinstance.DefaultHostname), title)
	if err != nil {
		t.Errorf("ParseMilestone() unexpected error = %s", err)
	}
	if got != expectedMilestoneID {
		t.Errorf("ParseMilestone() got = %d, expected = %d", got, expectedMilestoneID)
	}
}

func Test_ParseMilestoneAPIFail(t *testing.T) {
	title := "AsLongAsItDoesn'tConvertToInt"
	want := "API call failed in api.MilestoneByTitle()."

	// Override function to return an error simulating an API call failure
	projectMilestoneByTitle = func(client *gitlab.Client, projectID any, name string) (*gitlab.Milestone, error) {
		return nil, fmt.Errorf("API call failed in api.MilestoneByTitle().")
	}

	_, err := ParseMilestone(&gitlab.Client{}, glrepo.New("foo", "bar", glinstance.DefaultHostname), title)
	if err == nil {
		t.Errorf("ParseMilestone() expected error")
	}
	if want != err.Error() {
		t.Errorf("ParseMilestone() expected error = %s, got error = %s", want, err)
	}
}

func Test_ParseMilestoneTitleToID(t *testing.T) {
	milestoneTitle := "kind: testing"
	expectedID := int64(3)

	// Override function so it returns the correct milestone
	projectMilestoneByTitle = func(_ *gitlab.Client, _ any, _ string) (*gitlab.Milestone, error) {
		return &gitlab.Milestone{
				Title: "kind: testing",
				ID:    3,
			},
			nil
	}

	got, err := ParseMilestone(&gitlab.Client{}, glrepo.New("foo", "bar", glinstance.DefaultHostname), milestoneTitle)
	if err != nil {
		t.Errorf("ParseMilestone() unexpected error = %s", err)
	}
	if got != expectedID {
		t.Errorf("ParseMilestone() expected = %d, got = %d", expectedID, got)
	}
}

func Test_PickMetadata(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name       string
		values     []int
		expected   []Action
		skipReason string
	}{
		{
			name:       "nothing picked",
			skipReason: "empty multi-select not yet supported",
		},
		{
			name:     "labels",
			values:   []int{0}, // Select first option: "labels"
			expected: []Action{AddLabelAction},
		},
		{
			name:     "assignees",
			values:   []int{1}, // Select second option: "assignees"
			expected: []Action{AddAssigneeAction},
		},
		{
			name:     "milestone",
			values:   []int{2}, // Select third option: "milestones"
			expected: []Action{AddMilestoneAction},
		},
		{
			name:     "labels and assignees",
			values:   []int{0, 1},
			expected: []Action{AddLabelAction, AddAssigneeAction},
		},
		{
			name:     "labels and milestone",
			values:   []int{0, 2},
			expected: []Action{AddLabelAction, AddMilestoneAction},
		},
		{
			name:     "assignees and milestone",
			values:   []int{1, 2},
			expected: []Action{AddAssigneeAction, AddMilestoneAction},
		},
		{
			name:     "labels, assignees and milestone",
			values:   []int{0, 1, 2},
			expected: []Action{AddLabelAction, AddAssigneeAction, AddMilestoneAction},
		},
	}
	for _, tC := range testCases {
		t.Run(tC.name, func(t *testing.T) {
			t.Parallel()

			if tC.skipReason != "" {
				t.Skip(tC.skipReason)
			}

			c := ugh.New(t)
			c.Expect(ugh.MultiSelect("Which metadata types to add?")).
				Do(ugh.ToggleIndices(tC.values...))

			ios, cancel := testIOStreamsWithConsole(t, c)
			defer cancel()

			got, err := PickMetadata(t.Context(), ios)
			if err != nil {
				t.Errorf("PickMetadata() unexpected error = %s", err)
			}
			assert.ElementsMatch(t, got, tC.expected)
		})
	}

	t.Run("Prompt fails", func(t *testing.T) {
		// For testing prompt failure, we can use a responder that doesn't provide a response
		// This will cause a timeout/error
		c := ugh.New(t, ugh.WithSize(testConsoleWidth, testConsoleHeight), ugh.WithTimeout(100*time.Millisecond))
		// No expects - will timeout

		ios, cancel := testIOStreamsWithConsole(t, c)
		defer cancel()

		// Use a short context timeout to make the test fail quickly
		ctx, ctxCancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
		defer ctxCancel()

		got, err := PickMetadata(ctx, ios)
		assert.Nil(t, got)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "could not prompt")
	})
}

func Test_UsersPrompt(t *testing.T) {
	// mock glrepo.Remote object
	repo := glrepo.New("foo", "bar", glinstance.DefaultHostname)
	remote := &git.Remote{
		Name:     "test",
		Resolved: "base",
	}
	repoRemote := &glrepo.Remote{
		Remote: remote,
		Repo:   repo,
	}

	testCases := []struct {
		name               string
		choiceIndices      []int
		mock               []*gitlab.ProjectMember
		output             []string
		minimumAccessLevel int
		expectedStdErr     string
		expectedError      string
		skipReason         string
	}{
		{
			name:       "nothing",
			skipReason: "empty multi-select not yet supported",
		},
		{
			name:               "reporter",
			choiceIndices:      []int{0},
			output:             []string{"foo"},
			minimumAccessLevel: 20,
			mock: []*gitlab.ProjectMember{
				{
					Username:    "foo",
					AccessLevel: gitlab.AccessLevelValue(20),
				},
			},
		},
		{
			name:               "reporter-developer",
			choiceIndices:      []int{0, 1},
			output:             []string{"foo", "bar"},
			minimumAccessLevel: 20,
			mock: []*gitlab.ProjectMember{
				{
					Username:    "foo",
					AccessLevel: gitlab.AccessLevelValue(20),
				},
				{
					Username:    "bar",
					AccessLevel: gitlab.AccessLevelValue(30),
				},
			},
		},
		{
			name:               "reporter-developer-maintainer",
			choiceIndices:      []int{0, 1, 2},
			output:             []string{"foo", "bar", "baz"},
			minimumAccessLevel: 20,
			mock: []*gitlab.ProjectMember{
				{
					Username:    "foo",
					AccessLevel: gitlab.AccessLevelValue(20),
				},
				{
					Username:    "bar",
					AccessLevel: gitlab.AccessLevelValue(30),
				},
				{
					Username:    "baz",
					AccessLevel: gitlab.AccessLevelValue(40),
				},
			},
		},
		{
			name:               "no-members",
			minimumAccessLevel: 10,
			mock:               []*gitlab.ProjectMember{},
			expectedStdErr:     "Couldn't fetch any members with minimum permission level 10.\n",
		},
		{
			name:               "no-valid-members",
			minimumAccessLevel: 50,
			mock: []*gitlab.ProjectMember{
				{
					Username:    "foo",
					AccessLevel: gitlab.AccessLevelValue(40),
				},
			},
			expectedStdErr: "Couldn't fetch any members with minimum permission level 50.\n",
		},
	}
	for _, tC := range testCases {
		t.Run(tC.name, func(t *testing.T) {
			if tC.skipReason != "" {
				t.Skip(tC.skipReason)
			}

			listProjectMembers = func(ctx context.Context, client *gitlab.Client, projectID any, opts *gitlab.ListProjectMembersOptions) ([]*gitlab.ProjectMember, error) {
				return tC.mock, nil
			}

			var got []string
			var io *iostreams.IOStreams
			var cancel context.CancelFunc

			// Cases with no members don't need responder (return early)
			if tC.name == "no-members" || tC.name == "no-valid-members" {
				stderr := &bytes.Buffer{}
				io = iostreams.New(iostreams.WithStderr(stderr, false))

				err := UsersPrompt(t.Context(), &got, &gitlab.Client{}, repoRemote, io, tC.minimumAccessLevel, "some users")
				if tC.expectedError != "" {
					require.EqualError(t, err, tC.expectedError)
				} else {
					require.NoError(t, err)
				}
				if tC.expectedStdErr != "" {
					outErr := stripansi.Strip(stderr.String())
					assert.Equal(t, tC.expectedStdErr, outErr)
				}
				assert.ElementsMatch(t, got, tC.output)
				return
			}

			c := ugh.New(t)
			c.Expect(ugh.MultiSelect("Select some users")).
				Do(ugh.ToggleIndices(tC.choiceIndices...))
			io, cancel = testIOStreamsWithConsole(t, c)
			defer cancel()

			ctx, ctxCancel := context.WithTimeout(t.Context(), 2*time.Second)
			defer ctxCancel()

			err := UsersPrompt(ctx, &got, &gitlab.Client{}, repoRemote, io, tC.minimumAccessLevel, "some users")
			if tC.expectedError != "" {
				require.EqualError(t, err, tC.expectedError)
			} else {
				require.NoError(t, err)
			}
			if tC.expectedStdErr != "" {
				stderrBuf, ok := io.StdErr.(*bytes.Buffer)
				require.True(t, ok, "test setup wires StdErr to a *bytes.Buffer")
				outErr := stripansi.Strip(stderrBuf.String())
				assert.Equal(t, tC.expectedStdErr, outErr)
			}
			assert.ElementsMatch(t, got, tC.output)
		})
	}

	t.Run("Prompt fails", func(t *testing.T) {
		t.Skip("prompt failure simulation not yet supported")
	})

	t.Run("API Failed", func(t *testing.T) {
		var got []string

		listProjectMembers = func(ctx context.Context, client *gitlab.Client, projectID any, opts *gitlab.ListProjectMembersOptions) ([]*gitlab.ProjectMember, error) {
			return nil, errors.New("meant to fail")
		}

		err := UsersPrompt(t.Context(), &got, &gitlab.Client{}, repoRemote, nil, 20, "assignees")
		assert.Empty(t, got)
		assert.EqualError(t, err, "meant to fail")
	})

	t.Run("respect-flags", func(t *testing.T) {
		got := []string{"foo"}

		listProjectMembers = func(_ context.Context, _ *gitlab.Client, _ any, _ *gitlab.ListProjectMembersOptions) ([]*gitlab.ProjectMember, error) {
			return []*gitlab.ProjectMember{
				{
					Username:    "foo",
					AccessLevel: gitlab.AccessLevelValue(20),
				},
				{
					Username:    "bar",
					AccessLevel: gitlab.AccessLevelValue(30),
				},
			}, nil
		}

		c := ugh.New(t)
		c.Expect(ugh.MultiSelect("Select assignees")).
			Do(ugh.ToggleIndices(1))
		io, cancel := testIOStreamsWithConsole(t, c)
		defer cancel()

		ctx, ctxCancel := context.WithTimeout(t.Context(), 2*time.Second)
		defer ctxCancel()

		err := UsersPrompt(ctx, &got, &gitlab.Client{}, repoRemote, io, 20, "assignees")
		require.NoError(t, err)
		assert.ElementsMatch(t, []string{"foo", "bar"}, got)
	})
}

func Test_MilestonesPrompt(t *testing.T) {
	mockMilestones := []*api.Milestone{
		{
			Title: "New Release",
			ID:    5,
		},
		{
			Title: "Really big feature",
			ID:    240,
		},
		{
			Title: "Get rid of low quality Code",
			ID:    650,
		},
	}

	// Override API.ListMilestones so it doesn't make any network calls
	api.ListAllMilestones = func(_ *gitlab.Client, _ any, _ *api.ListMilestonesOptions) ([]*api.Milestone, error) {
		return mockMilestones, nil
	}

	// mock glrepo.Remote object
	repo := glrepo.New("foo", "bar", glinstance.DefaultHostname)
	remote := &git.Remote{
		Name:     "test",
		Resolved: "base",
	}
	repoRemote := &glrepo.Remote{
		Remote: remote,
		Repo:   repo,
	}

	testCases := []struct {
		name       string
		inputIdx   int   // Selected milestone
		expectedID int64 // expected global ID from the milestone
	}{
		{
			name:       "match",
			inputIdx:   0,
			expectedID: 5,
		},
	}
	for _, tC := range testCases {
		t.Run(tC.name, func(t *testing.T) {
			c := ugh.New(t)
			c.Expect(ugh.Select("Select milestone")).
				Do(ugh.SelectIndex(tC.inputIdx))
			ios, cleanup := testIOStreamsWithConsole(t, c)
			t.Cleanup(cleanup)

			var got int64
			err := MilestonesPrompt(t.Context(), &got, &gitlab.Client{}, repoRemote, ios)
			if err != nil {
				t.Errorf("MilestonesPrompt() unexpected error = %s", err)
			}
			if got != 0 && got != tC.expectedID {
				t.Errorf("MilestonesPrompt() expected = %d, got = %d", got, tC.expectedID)
			}
		})
	}
}

func Test_MilestonesPromptNoPrompts(t *testing.T) {
	// Override api.ListMilestones so it returns an empty slice, we are testing if MilestonesPrompt()
	// will print the correct message to `stderr` when it tries to get the list of Milestones in a
	// project but the project has no milestones
	api.ListAllMilestones = func(_ *gitlab.Client, _ any, _ *api.ListMilestonesOptions) ([]*api.Milestone, error) {
		return []*api.Milestone{}, nil
	}

	// mock glrepo.Remote object
	repo := glrepo.New("foo", "bar", glinstance.DefaultHostname)
	remote := &git.Remote{
		Name:     "test",
		Resolved: "base",
	}
	repoRemote := &glrepo.Remote{
		Remote: remote,
		Repo:   repo,
	}

	var got int64
	stderr := &bytes.Buffer{}
	io := iostreams.New(iostreams.WithStderr(stderr, false))

	err := MilestonesPrompt(t.Context(), &got, &gitlab.Client{}, repoRemote, io)
	if err != nil {
		t.Errorf("MilestonesPrompt() unexpected error = %s", err)
	}
	assert.Equal(t, "No active milestones exist for this project.\n", stderr.String())
}

func TestMilestonesPromptFailures(t *testing.T) {
	// Override api.ListMilestones so it returns an error, we are testing to see if error
	// handling from the usage of api.ListMilestones is correct
	api.ListAllMilestones = func(_ *gitlab.Client, _ any, _ *api.ListMilestonesOptions) ([]*api.Milestone, error) {
		return nil, errors.New("api.ListMilestones() failed")
	}

	// mock glrepo.Remote object
	repo := glrepo.New("foo", "bar", glinstance.DefaultHostname)
	remote := &git.Remote{
		Name:     "test",
		Resolved: "base",
	}
	repoRemote := &glrepo.Remote{
		Remote: remote,
		Repo:   repo,
	}

	var got int64
	io := iostreams.New()

	err := MilestonesPrompt(t.Context(), &got, &gitlab.Client{}, repoRemote, io)
	if err == nil {
		t.Error("MilestonesPrompt() expected error")
	}
	assert.Equal(t, "api.ListMilestones() failed", err.Error())
}

func Test_IDsFromUsers(t *testing.T) {
	testCases := []struct {
		name  string
		users []*gitlab.User // Mock of the gitlab.User object
		IDs   []int64        // IDs we expect from the users
	}{
		{
			name: "no users",
		},
		{
			name: "one user",
			users: []*gitlab.User{
				{
					ID: 1,
				},
			},
			IDs: []int64{1},
		},
		{
			name: "multiple users",
			users: []*gitlab.User{
				{
					ID: 3,
				},
				{
					ID: 6,
				},
				{
					ID: 2,
				},
				{
					ID: 51,
				},
				{
					ID: 32,
				},
				{
					ID: 87,
				},
				{
					ID: 210,
				},
				{
					ID: 6493,
				},
				{
					ID: 50132,
				},
			},
			IDs: []int64{
				50132,
				6493,
				210,
				87,
				32,
				51,
				2,
				3,
				6,
			},
		},
	}
	for _, tC := range testCases {
		t.Run(tC.name, func(t *testing.T) {
			got := IDsFromUsers(tC.users)
			assert.ElementsMatch(t, *got, tC.IDs)
		})
	}
}

func Test_LabelsPromptAPIFail(t *testing.T) {
	// mock glrepo.Remote object
	repo := glrepo.New("foo", "bar", glinstance.DefaultHostname)
	remote := &git.Remote{
		Name:     "test",
		Resolved: "base",
	}
	repoRemote := &glrepo.Remote{
		Remote: remote,
		Repo:   repo,
	}

	listLabels = func(_ *gitlab.Client, _ any, _ *gitlab.ListLabelsOptions) ([]*gitlab.Label, error) {
		return nil, errors.New("API call failed")
	}

	var got []string
	ios := testIOStreams()
	err := LabelsPrompt(t.Context(), ios, &got, &gitlab.Client{}, repoRemote)
	assert.Nil(t, got)
	assert.EqualError(t, err, "API call failed")
}

func Test_LabelsPromptPromptsFail(t *testing.T) {
	t.Run("MultiSelect", func(t *testing.T) {
		t.Skip("prompt failure simulation not yet supported")
	})

	t.Run("AskQuestionWithInput", func(t *testing.T) {
		t.Skip("prompt failure simulation not yet supported")
	})
}

func Test_LabelsPromptMultiSelect(t *testing.T) {
	// mock glrepo.Remote object
	repo := glrepo.New("foo", "bar", glinstance.DefaultHostname)
	remote := &git.Remote{
		Name:     "test",
		Resolved: "base",
	}
	repoRemote := &glrepo.Remote{
		Remote: remote,
		Repo:   repo,
	}

	listLabels = func(_ *gitlab.Client, _ any, _ *gitlab.ListLabelsOptions) ([]*gitlab.Label, error) {
		return []*gitlab.Label{
			{
				Name: "foo",
			},
			{
				Name: "bar",
			},
			{
				Name: "baz",
			},
			{
				Name: "qux",
			},
			{
				Name: "quux",
			},
			{
				Name: "quz",
			},
		}, nil
	}

	testCases := []struct {
		name          string
		choiceIndices []int
		labels        []string // Can be set to have initial labels
		expected      []string // expected labels
		skipReason    string
	}{
		{
			name:          "simple",
			choiceIndices: []int{0, 1}, // Select "foo" and "bar"
			expected:      []string{"foo", "bar"},
		},
		{
			name:          "respect-defined-labels",
			choiceIndices: []int{0}, // Select "foo"
			labels:        []string{"bar"},
			expected:      []string{"foo", "bar"},
		},
		{
			name:       "nothing",
			skipReason: "empty multi-select not yet supported",
		},
		{
			name:       "nothing-but-respect-already-defined",
			labels:     []string{"qux"},
			expected:   []string{"qux"},
			skipReason: "empty multi-select not yet supported",
		},
	}
	for _, tC := range testCases {
		t.Run(tC.name, func(t *testing.T) {
			if tC.skipReason != "" {
				t.Skip(tC.skipReason)
			}

			c := ugh.New(t)
			c.Expect(ugh.MultiSelect("Select labels")).
				Do(ugh.ToggleIndices(tC.choiceIndices...))
			ios, cancel := testIOStreamsWithConsole(t, c)
			defer cancel()

			ctx, ctxCancel := context.WithTimeout(t.Context(), 2*time.Second)
			defer ctxCancel()

			err := LabelsPrompt(ctx, ios, &tC.labels, &gitlab.Client{}, repoRemote)
			require.NoError(t, err)
			assert.ElementsMatch(t, tC.labels, tC.expected)
		})
	}
}

func Test_LabelsPromptAskQuestionWithInput(t *testing.T) {
	// mock glrepo.Remote object
	repo := glrepo.New("foo", "bar", glinstance.DefaultHostname)
	remote := &git.Remote{
		Name:     "test",
		Resolved: "base",
	}
	repoRemote := &glrepo.Remote{
		Remote: remote,
		Repo:   repo,
	}

	listLabels = func(_ *gitlab.Client, _ any, _ *gitlab.ListLabelsOptions) ([]*gitlab.Label, error) {
		return []*gitlab.Label{}, nil
	}

	testCases := []struct {
		name     string
		input    string
		labels   []string // Can be set to have initial labels
		expected []string // expected labels
	}{
		{
			name:     "simple",
			input:    "foo,bar",
			expected: []string{"foo", "bar"},
		},
		{
			name:     "respect-defined-labels",
			input:    "foo",
			labels:   []string{"bar"},
			expected: []string{"foo", "bar"},
		},
		{
			name: "nothing",
		},
		{
			name:     "nothing-but-respect-already-defined",
			labels:   []string{"qux"},
			expected: []string{"qux"},
		},
	}
	for _, tC := range testCases {
		t.Run(tC.name, func(t *testing.T) {
			c := ugh.New(t)
			c.Expect(ugh.Input("Label(s) (comma-separated)")).
				Do(ugh.Type(tC.input))
			ios, cancel := testIOStreamsWithConsole(t, c)
			defer cancel()

			ctx, ctxCancel := context.WithTimeout(t.Context(), 2*time.Second)
			defer ctxCancel()

			err := LabelsPrompt(ctx, ios, &tC.labels, &gitlab.Client{}, repoRemote)
			require.NoError(t, err)
			assert.ElementsMatch(t, tC.labels, tC.expected)
		})
	}
}

func Test_ConfirmSubmission(t *testing.T) {
	const (
		submitLabelIdx      = 0
		previewLabelIdx     = 1
		addMetadataLabelIdx = 2
		cancelLabelIdx      = 3
	)

	t.Run("success", func(t *testing.T) {
		testCases := []struct {
			name             string
			optionIdx        int
			allowAddMetadata bool
			output           Action
		}{
			{
				name:      "submit",
				optionIdx: submitLabelIdx,
				output:    SubmitAction,
			},
			{
				name:      "preview",
				optionIdx: previewLabelIdx,
				output:    PreviewAction,
			},
			{
				name:             "Add Metadata",
				optionIdx:        addMetadataLabelIdx,
				allowAddMetadata: true,
				output:           AddMetadataAction,
			},
			{
				name:      "cancel",
				optionIdx: cancelLabelIdx - 1, // no metadata
				output:    CancelAction,
			},
		}
		for _, tC := range testCases {
			t.Run(tC.name, func(t *testing.T) {
				c := ugh.New(t)
				c.Expect(ugh.Select("What's next?")).
					Do(ugh.SelectIndex(tC.optionIdx))
				ios, cleanup := testIOStreamsWithConsole(t, c)
				t.Cleanup(cleanup)

				got, err := ConfirmSubmission(t.Context(), ios, tC.allowAddMetadata)
				require.NoError(t, err)
				assert.Equal(t, tC.output, got)
			})
		}
	})
}

func TestListGitLabTemplates(t *testing.T) {
	tests := []struct {
		name          string
		give          string
		wantTemplates []string
		wantErr       bool
	}{
		{
			name:          "Get all the issues templates",
			give:          "issue_templates",
			wantTemplates: []string{"Bug", "Feature Request"},
		},
		{
			name:          "Get all the merge request templates",
			give:          "merge_request_templates",
			wantTemplates: []string{"Default"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			git.ToplevelDir = func() (string, error) { return "../../test/testdata", nil }
			gotTemplates, gotErr := ListGitLabTemplates(test.give)
			require.Equal(t, test.wantErr, (gotErr != nil))
			assert.Equal(t, test.wantTemplates, gotTemplates, "Templates got didn't match")
		})
	}
}
