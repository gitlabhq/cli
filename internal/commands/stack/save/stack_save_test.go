//go:build !integration

package save

import (
	"bytes"
	"net/http"
	"os"
	"path"
	"strings"
	"testing"
	"time"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/commands/stack/stackutils"
	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/git"
	git_testing "gitlab.com/gitlab-org/cli/internal/git/testing"
	"gitlab.com/gitlab-org/cli/internal/run"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

// setupTestFactory is used by prompt_test.go to test internal functions.
func setupTestFactory(t *testing.T, rt http.RoundTripper, isTTY bool) (*bytes.Buffer, *bytes.Buffer, cmdutils.Factory) {
	t.Helper()

	ios, _, stdout, stderr := cmdtest.TestIOStreams(cmdtest.WithTestIOStreamsAsTTY(isTTY))

	factory := cmdtest.NewTestFactory(ios,
		cmdtest.WithGitLabClient(cmdtest.NewTestApiClient(t, &http.Client{Transport: rt}, "", "gitlab.com").Lab()),
	)

	return stdout, stderr, factory
}

func TestSaveNewStack(t *testing.T) {
	// NOTE: we need to force disable colors, otherwise we'd need ANSI sequences in our test output assertions.
	t.Setenv("NO_COLOR", "true")

	tests := []struct {
		desc          string
		args          []string
		files         []string
		trackedFiles  []string
		message       string
		expected      string
		wantErr       bool
		noTTY         bool
		editorMessage string
	}{
		{
			desc:     "adding regular files",
			args:     []string{"testfile", "randomfile"},
			files:    []string{"testfile", "randomfile"},
			message:  "this is a commit message",
			expected: "• cool-test-feature: Saved with message: \"this is a commit message\".\n",
		},

		{
			desc:     "adding files with a dot argument",
			args:     []string{"."},
			files:    []string{"testfile", "randomfile"},
			message:  "this is a commit message",
			expected: "• cool-test-feature: Saved with message: \"this is a commit message\".\n",
		},

		{
			desc:     "adding files with no argument and no staged changes",
			args:     []string{""},
			files:    []string{"testfile", "randomfile"},
			message:  "this is a commit message",
			expected: "no staged changes",
			wantErr:  true,
		},

		{
			desc:          "omitting a message",
			args:          []string{"."},
			files:         []string{"testfile"},
			editorMessage: "oh ok fine how about blah blah",
			expected:      "• cool-test-feature: Saved with message: \"oh ok fine how about blah blah\".\n",
		},

		{
			desc:     "with no changed files",
			args:     []string{"."},
			files:    []string{},
			expected: "could not save: \"no changes to save.\"",
			wantErr:  true,
		},

		{
			desc:     "Test with no message and noTTY",
			args:     []string{"."},
			files:    []string{"testfile"},
			expected: "glab stack save without `-m` and without a TTY should throw an error.",
			wantErr:  true,
			noTTY:    true,
		},

		{
			desc:         "adding tracked files with -a flag",
			args:         []string{"-a"},
			trackedFiles: []string{"trackedfile"},
			message:      "this is a commit message",
			expected:     "• cool-test-feature: Saved with message: \"this is a commit message\".\n",
		},

		{
			desc:     "adding with -a flag but only untracked files returns error",
			args:     []string{"-a"},
			files:    []string{"untrackedfile"},
			message:  "this is a commit message",
			wantErr:  true,
			expected: "glab stack save -a with no tracked changes should return an error",
		},

		{
			desc:     "adding regular files with --no-verify",
			args:     []string{"testfile", "--no-verify"},
			files:    []string{"testfile"},
			message:  "commit with no verify",
			expected: "• cool-test-feature: Saved with message: \"commit with no verify\".\n",
		},
	}

	for _, tc := range tests {
		isTTY := !tc.noTTY
		t.Run(tc.desc, func(t *testing.T) {
			if tc.message != "" && isTTY {
				tc.args = append(tc.args, "-m")
				tc.args = append(tc.args, "\""+tc.message+"\"")
			}

			dir := git.InitGitRepoWithCommit(t)
			err := git.SetLocalConfig("glab.currentstack", "cool-test-feature")
			require.NoError(t, err)

			createTemporaryFiles(t, dir, tc.files)

			if len(tc.trackedFiles) > 0 {
				createTemporaryFiles(t, dir, tc.trackedFiles)

				gitCmd := git.GitCommand("add", ".")
				_, err = run.PrepareCmd(gitCmd).Output()
				require.NoError(t, err)

				gitCmd = git.GitCommand("commit", "-m", "initial tracked files")
				_, err = run.PrepareCmd(gitCmd).Output()
				require.NoError(t, err)

				for _, file := range tc.trackedFiles {
					err = os.WriteFile(path.Join(dir, file), []byte("modified content"), 0o644)
					require.NoError(t, err)
				}
			}

			getText := getMockEditor(tc.editorMessage, &[]string{})
			args := strings.Join(tc.args, " ")

			ctrl := gomock.NewController(t)
			mockCmd := git_testing.NewMockGitRunner(ctrl)

			exec := cmdtest.SetupCmdForTest(t, func(f cmdutils.Factory) *cobra.Command {
				return NewCmdSaveStack(f, mockCmd, getText)
			}, isTTY,
				cmdtest.WithGitLabClient(cmdtest.NewTestApiClient(t, nil, "", "gitlab.com").Lab()),
			)

			output, err := exec(args)

			if tc.wantErr {
				require.Errorf(t, err, tc.expected)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.expected, output.String())
			}
		})
	}
}

func TestSaveStack_WarningWhenNotOnLastEntry(t *testing.T) {
	t.Setenv("NO_COLOR", "true")

	firstRef := git.StackRef{
		SHA:         "first123",
		Branch:      "user-test-stack-first123",
		Description: "first entry",
		Next:        "second456",
	}
	secondRef := git.StackRef{
		SHA:         "second456",
		Branch:      "user-test-stack-second456",
		Description: "second entry",
		Prev:        "first123",
	}

	tests := []struct {
		desc          string
		checkoutRef   git.StackRef
		expectWarning bool
	}{
		{
			desc:          "warning when on first entry of stack",
			checkoutRef:   firstRef,
			expectWarning: true,
		},
		{
			desc:          "no warning when on last entry of stack",
			checkoutRef:   secondRef,
			expectWarning: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.desc, func(t *testing.T) {
			dir := git.InitGitRepoWithCommit(t)
			stackTitle := "test-stack"
			err := git.SetLocalConfig("glab.currentstack", stackTitle)
			require.NoError(t, err)

			err = git.AddStackRefFile(stackTitle, firstRef)
			require.NoError(t, err)
			err = git.AddStackRefFile(stackTitle, secondRef)
			require.NoError(t, err)

			err = git.CheckoutNewBranch(tc.checkoutRef.Branch)
			require.NoError(t, err)

			createTemporaryFiles(t, dir, []string{"newfile"})

			ctrl := gomock.NewController(t)
			mockCmd := git_testing.NewMockGitRunner(ctrl)

			exec := cmdtest.SetupCmdForTest(t, func(f cmdutils.Factory) *cobra.Command {
				return NewCmdSaveStack(f, mockCmd, getMockEditor("", &[]string{}))
			}, true,
				cmdtest.WithGitLabClient(cmdtest.NewTestApiClient(t, nil, "", "gitlab.com").Lab()),
			)

			output, err := exec(". -m \"new changes\"")
			require.NoError(t, err)

			if tc.expectWarning {
				require.Contains(t, output.Stderr(), "warning: you are not on the last entry of the stack")
				require.Contains(t, output.Stderr(), "glab stack amend")
			} else {
				require.NotContains(t, output.Stderr(), "warning: you are not on the last entry of the stack")
			}
		})
	}
}

func Test_addFiles(t *testing.T) {
	tests := []struct {
		desc           string
		args           []string
		expected       []string
		untrackedFiles []string
		stageAll       bool
		wantErr        bool
		errContains    string
		statusPrefix   string
	}{
		{
			desc:         "adding regular files",
			args:         []string{"file1", "file2"},
			expected:     []string{"file1", "file2"},
			statusPrefix: "A  ",
		},
		{
			desc:         "adding files with a dot argument",
			args:         []string{"."},
			expected:     []string{"file1", "file2"},
			statusPrefix: "A  ",
		},
		{
			desc:         "with -a flag stages tracked files only",
			args:         []string{},
			expected:     []string{"file1"},
			stageAll:     true,
			statusPrefix: "M  ",
		},
		{
			desc:        "with no args and no staged changes returns error",
			args:        []string{},
			expected:    []string{},
			wantErr:     true,
			errContains: "no staged changes. Stage files manually",
		},
		{
			desc:        "with -a flag but no tracked changes returns error",
			args:        []string{""},
			expected:    []string{},
			wantErr:     true,
			stageAll:    true,
			errContains: "no staged changes after 'git add --update'",
		},
		{
			desc:           "with -a flag but only untracked files returns error",
			args:           []string{"-a"},
			expected:       []string{},
			untrackedFiles: []string{"untracked1", "untracked2"},
			wantErr:        true,
			stageAll:       true,
			errContains:    "no staged changes after 'git add --update'",
		},
	}

	for _, tc := range tests {
		t.Run(tc.desc, func(t *testing.T) {
			dir := git.InitGitRepoWithCommit(t)
			err := git.SetLocalConfig("glab.currentstack", "cool-test-feature")
			require.NoError(t, err)

			createTemporaryFiles(t, dir, tc.expected)
			createTemporaryFiles(t, dir, tc.untrackedFiles)

			// For the tracked files test, we need to add the expected files to git first
			if tc.stageAll && len(tc.expected) > 0 {
				gitCmd := git.GitCommand("add", tc.expected[0])
				_, err := run.PrepareCmd(gitCmd).Output()
				require.NoError(t, err)

				gitCmd = git.GitCommand("commit", "-m", "initial")
				_, err = run.PrepareCmd(gitCmd).Output()
				require.NoError(t, err)

				// Now modify the file so we have a tracked file change
				err = os.WriteFile(path.Join(dir, tc.expected[0]), []byte("modified"), 0o644)
				require.NoError(t, err)
			}

			err = addFiles(tc.args, tc.stageAll)

			if tc.wantErr {
				require.Error(t, err)
				if tc.errContains != "" {
					require.ErrorContains(t, err, tc.errContains)
				}
				return
			}

			require.NoError(t, err)

			gitCmd := git.GitCommand("status", "--short", "-u")
			output, err := run.PrepareCmd(gitCmd).Output()
			require.NoError(t, err)

			normalizedFiles := []string{}
			for _, file := range tc.expected {
				file = tc.statusPrefix + file

				normalizedFiles = append(normalizedFiles, file)
			}

			formattedOutput := strings.Replace(string(output), "\n", "", -1)
			require.Equal(t, formattedOutput, strings.Join(normalizedFiles, ""))
		})
	}
}

func Test_checkForChanges(t *testing.T) {
	tests := []struct {
		desc     string
		args     []string
		expected bool
	}{
		{
			desc:     "check for changes with modified files",
			args:     []string{"file1", "file2"},
			expected: true,
		},
		{
			desc:     "check for changes without anything",
			args:     []string{},
			expected: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.desc, func(t *testing.T) {
			dir := git.InitGitRepoWithCommit(t)
			err := git.SetLocalConfig("glab.currentstack", "cool-test-feature")
			require.NoError(t, err)

			createTemporaryFiles(t, dir, tc.args)

			err = checkForChanges()
			if tc.expected {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}

func Test_commitFiles(t *testing.T) {
	tests := []struct {
		name     string
		want     string
		message  string
		noVerify bool
		wantErr  bool
	}{
		{
			name:    "a regular commit message",
			message: "i am a test message",
			want:    "i am a test message\n 2 files changed, 0 insertions(+), 0 deletions(-)\n create mode 100644 test\n create mode 100644 yo\n",
		},
		{
			name:     "a regular commit message, no verify",
			message:  "i am a test message",
			noVerify: true,
			want:     "i am a test message\n 2 files changed, 0 insertions(+), 0 deletions(-)\n create mode 100644 test\n create mode 100644 yo\n",
		},
		{
			name:    "no message",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := git.InitGitRepoWithCommit(t)

			createTemporaryFiles(t, dir, []string{"yo", "test"})
			err := addFiles([]string{"."}, false)
			require.NoError(t, err)

			got, err := commitFiles(tt.message, tt.noVerify)

			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Contains(t, got, tt.want)
			}
		})
	}
}

func Test_generateStackSha(t *testing.T) {
	type args struct {
		message   string
		title     string
		author    string
		timestamp time.Time
	}
	tests := []struct {
		name    string
		args    args
		want    string
		wantErr bool
	}{
		{
			name: "basic test",
			args: args{message: "hello", title: "supercool stack title", author: "norm maclean", timestamp: time.Date(1998, time.July, 6, 1, 3, 3, 7, time.UTC)},
			want: "e062296a",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			git.InitGitRepo(t)

			got, err := stackutils.GenerateStackSha(tt.args.message, tt.args.title, tt.args.author, tt.args.timestamp)

			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.want, got)
			}
		})
	}
}

func Test_createShaBranch(t *testing.T) {
	type args struct {
		sha   string
		title string
	}
	tests := []struct {
		name    string
		args    args
		prefix  string
		want    string
		wantErr bool
	}{
		{
			name:   "standard test case",
			args:   args{sha: "237ec83c", title: "cool-change"},
			prefix: "asdf",
			want:   "asdf-cool-change-237ec83c",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			git.InitGitRepo(t)

			factory := createFactoryWithConfig("branch_prefix", tt.prefix)

			got, err := stackutils.CreateShaBranch(factory, tt.args.sha, tt.args.title)
			require.NoError(t, err)

			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.want, got)
			}
		})
	}
}

func createTemporaryFiles(t *testing.T, dir string, files []string) {
	t.Helper()

	for _, file := range files {
		file = path.Join(dir, file)
		_, err := os.Create(file)

		require.NoError(t, err)
	}
}

func createFactoryWithConfig(key string, value string) cmdutils.Factory {
	strconfig := heredoc.Doc(`
				` + key + `: ` + value + `
			`)

	cfg := config.NewFromString(strconfig)

	ios, _, _, _ := cmdtest.TestIOStreams()

	return cmdtest.NewTestFactory(ios, cmdtest.WithConfig(cfg))
}
