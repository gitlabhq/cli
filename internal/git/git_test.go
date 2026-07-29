//go:build !integration

package git

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gomock "go.uber.org/mock/gomock"

	git_testing "gitlab.com/gitlab-org/cli/internal/git/testing"
	"gitlab.com/gitlab-org/cli/internal/run"
	"gitlab.com/gitlab-org/cli/test"
)

func getEnv(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return fallback
}

func TestRemoteBranchExists(t *testing.T) {
	tests := []struct {
		name     string
		branch   string
		mockOut  string
		mockErr  bool
		expected bool
	}{
		{
			name:     "branch exists",
			branch:   "main",
			mockErr:  false,
			expected: true,
		},
		{
			name:     "branch does not exist",
			branch:   "non-existent-branch",
			mockErr:  true,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			mockCmd := git_testing.NewMockGitRunner(ctrl)

			if tt.mockErr {
				mockCmd.EXPECT().Git("ls-remote",
					"--exit-code",
					"--heads",
					DefaultRemote,
					tt.branch).Return("", errors.New("can't find branch"))
			} else {
				mockCmd.EXPECT().Git("ls-remote",
					"--exit-code",
					"--heads",
					DefaultRemote,
					tt.branch)
			}

			result := RemoteBranchExists(tt.branch, mockCmd)

			assert.Equal(t, tt.expected, result)
		})
	}
}

func Test_isFilesystemPath(t *testing.T) {
	type args struct {
		p string
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{
			name: "Filesystem",
			args: args{"./.git"},
			want: true,
		},
		{
			name: "Filesystem",
			args: args{".git"},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("%s - %s", tt.name, tt.args.p), func(t *testing.T) {
			if got := isFilesystemPath(tt.args.p); got != tt.want {
				t.Errorf("isFilesystemPath() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_UncommittedChangeCount(t *testing.T) {
	type c struct {
		Label    string
		Expected int
		Output   string
	}
	cases := []c{
		{Label: "no changes", Expected: 0, Output: ""},
		{Label: "one change", Expected: 1, Output: " M poem.txt"},
		{Label: "untracked file", Expected: 2, Output: " M poem.txt\n?? new.txt"},
	}

	teardown := run.SetPrepareCmd(func(*exec.Cmd) run.Runnable {
		return &test.OutputStub{}
	})
	defer teardown()

	for _, v := range cases {
		_ = run.SetPrepareCmd(func(*exec.Cmd) run.Runnable {
			return &test.OutputStub{Out: []byte(v.Output)}
		})
		ucc, _ := UncommittedChangeCount()

		if ucc != v.Expected {
			t.Errorf("got unexpected ucc value: %d for case %s", ucc, v.Label)
		}
	}
}

func Test_CurrentBranch(t *testing.T) {
	cs, teardown := test.InitCmdStubber()
	defer teardown()

	expected := "branch-name"

	cs.Stub(expected)

	result, err := CurrentBranch()
	if err != nil {
		t.Errorf("got unexpected error: %v", err)
	}
	if len(cs.Calls) != 1 {
		t.Errorf("expected 1 Git call, saw %d", len(cs.Calls))
	}
	if result != expected {
		t.Errorf("unexpected branch name: %s instead of %s", result, expected)
	}
}

func Test_CurrentBranch_detached_head(t *testing.T) {
	cs, teardown := test.InitCmdStubber()
	defer teardown()

	cs.StubError("")

	_, err := CurrentBranch()
	if err == nil {
		t.Errorf("expected an error")
	}
	if !errors.Is(err, ErrNotOnAnyBranch) {
		t.Errorf("got unexpected error: %s instead of %s.", err, ErrNotOnAnyBranch)
	}
	if len(cs.Calls) != 1 {
		t.Errorf("expected 1 Git call, saw %d.", len(cs.Calls))
	}
}

func Test_CurrentBranch_unexpected_error(t *testing.T) {
	cs, teardown := test.InitCmdStubber()
	defer teardown()

	cs.StubError("lol")

	expectedError := "lol\nstub: lol"

	_, err := CurrentBranch()
	if err == nil {
		t.Errorf("expected an error")
	}
	if err.Error() != expectedError {
		t.Errorf("got unexpected error: %s instead of %s.", err.Error(), expectedError)
	}
	if len(cs.Calls) != 1 {
		t.Errorf("expected 1 Git call, saw %d.", len(cs.Calls))
	}
}

func TestReadBranchConfig(t *testing.T) {
	cs, teardown := test.InitCmdStubber()
	defer teardown()

	cs.Stub(`branch.branch-name.remote origin
branch.branch.remote git@gitlab.com:glab-test/test.git
branch.branch.merge refs/heads/branch-name`)

	u, err := ParseURL("git@gitlab.com:glab-test/test.git")
	require.NoError(t, err)
	wantCfg := BranchConfig{
		"origin",
		u,
		"refs/heads/branch-name",
	}

	t.Run("", func(t *testing.T) {
		if gotCfg := ReadBranchConfig("branch-name"); !reflect.DeepEqual(gotCfg, wantCfg) {
			t.Errorf("ReadBranchConfig() = %v, want %v", gotCfg, wantCfg)
		}
	})
}

func Test_parseRemotes(t *testing.T) {
	remoteList := []string{
		"mona\tgit@gitlab.com:monalisa/myfork.git (fetch)",
		"origin\thttps://gitlab.com/monalisa/octo-cat.git (fetch)",
		"origin\thttps://gitlab.com/monalisa/octo-cat-push.git (push)",
		"upstream\thttps://example.com/nowhere.git (fetch)",
		"upstream\thttps://gitlab.com/hubot/tools (push)",
		"zardoz\thttps://example.com/zed.git (push)",
	}
	r := parseRemotes(remoteList)
	eq(t, len(r), 4)

	eq(t, r[0].Name, "mona")
	eq(t, r[0].FetchURL.String(), "ssh://git@gitlab.com/monalisa/myfork.git")
	if r[0].PushURL != nil {
		t.Errorf("expected no PushURL, got %q", r[0].PushURL)
	}
	eq(t, r[1].Name, "origin")
	eq(t, r[1].FetchURL.Path, "/monalisa/octo-cat.git")
	eq(t, r[1].PushURL.Path, "/monalisa/octo-cat-push.git")

	eq(t, r[2].Name, "upstream")
	eq(t, r[2].FetchURL.Host, "example.com")
	eq(t, r[2].PushURL.Host, "gitlab.com")

	eq(t, r[3].Name, "zardoz")
}

func TestGetDefaultBranch(t *testing.T) {
	tests := []struct {
		name    string
		args    string
		want    string
		wantErr bool
	}{
		{
			name: "No Params",
			want: "master",
		},
		{
			name: "Different Remote",
			want: "master",
			args: "profclems/test",
		},
		{
			name:    "Invalid repo",
			want:    "main",
			args:    "testssz",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := GetDefaultBranch(tt.args)
			if (err != nil) != tt.wantErr {
				t.Logf("GetDefaultBranch() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("GetDefaultBranch() got = %v, want %v", got, tt.want)
			}
		})
	}

	t.Run("get branch from HEAD", func(t *testing.T) {
		cs, teardown := test.InitCmdStubber()
		defer teardown()

		cs.Stub(`* remote origin
Fetch URL: https://gitlab.com/gitlab-community/cli.git
Push  URL: https://gitlab.com/gitlab-community/cli.git
HEAD branch: main`)

		got, err := GetDefaultBranch("origin")
		require.NoError(t, err)
		assert.Equal(t, "main", got)
	})
}

func TestGetRemoteURL(t *testing.T) {
	t.Run("is valid", func(t *testing.T) {
		InitGitRepo(t)

		_, err := GitCommand("config", "remote.origin.url", getEnv("CI_PROJECT_PATH", "gitlab-org/cli")).Output()
		require.NoError(t, err)

		got, err := GetRemoteURL("origin")

		require.Contains(t, got, getEnv("CI_PROJECT_PATH", "gitlab-org/cli"))
		require.NoError(t, err)
	})

	t.Run("is not valid", func(t *testing.T) {
		InitGitRepo(t)

		got, err := GetRemoteURL("lkajwflkwejlakjdsal")

		require.Contains(t, got, "")
		if err == nil {
			t.Errorf("GetRemoteURL() error = %v, wantErr %v", err, true)
		}
	})
}

func TestGitCommonDir(t *testing.T) {
	t.Run("normal repo", func(t *testing.T) {
		repoDir := InitGitRepo(t)

		got, err := GitCommonDir()
		require.NoError(t, err)
		// git rev-parse --git-common-dir may return a relative path (".git")
		// when cwd is the repo root, so normalize before comparing.
		absGot, err := filepath.Abs(got)
		require.NoError(t, err)
		require.Equal(t, filepath.Join(repoDir, ".git"), absGot)
	})

	t.Run("worktree", func(t *testing.T) {
		repoDir := InitGitRepoWithCommit(t)
		worktreeDir := t.TempDir()

		addCmd := GitCommand("worktree", "add", worktreeDir, "-b", "worktree-branch")
		addCmd.Dir = repoDir
		_, err := run.PrepareCmd(addCmd).Output()
		require.NoError(t, err)

		t.Chdir(worktreeDir)

		got, err := GitCommonDir()
		require.NoError(t, err)
		// git resolves symlinks in returned paths (macOS: /var → /private/var),
		// so resolve both sides before comparing.
		wantResolved, err := filepath.EvalSymlinks(filepath.Join(repoDir, ".git"))
		require.NoError(t, err)
		gotResolved, err := filepath.EvalSymlinks(got)
		require.NoError(t, err)
		require.Equal(t, wantResolved, gotResolved)
	})
}

func TestDescribeByTags(t *testing.T) {
	cases := map[string]struct {
		expected   string
		output     string
		errorValue error
	}{
		"commit is tag": {
			expected:   "1.0.0",
			output:     "1.0.0",
			errorValue: nil,
		},
		"commit is not tag": {
			expected:   "1.0.0-1-g4aa1b8",
			output:     "1.0.0-1-g4aa1b8",
			errorValue: nil,
		},
	}

	for name, v := range cases {
		t.Run(name, func(t *testing.T) {
			InitGitRepoWithCommit(t)

			_, err := exec.Command("git", "tag", v.output).Output()
			require.NoError(t, err)

			version, err := DescribeByTags()
			require.Equal(t, v.errorValue, errors.Unwrap(err))
			require.Equal(t, v.expected, version, "unexpected version value for case %s", name)
		})
	}
}

func TestOutputLines(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		input  string
		expect []string
	}{
		{
			name:   "empty output",
			input:  "",
			expect: []string{""},
		},
		{
			name:   "single line without final newline",
			input:  "alpha",
			expect: []string{"alpha"},
		},
		{
			name:   "LF without final newline",
			input:  "alpha\nbeta",
			expect: []string{"alpha", "beta"},
		},
		{
			name:   "LF with final newline",
			input:  "alpha\nbeta\n",
			expect: []string{"alpha", "beta"},
		},
		{
			name:   "LF with empty interior line",
			input:  "alpha\n\nbeta\n",
			expect: []string{"alpha", "", "beta"},
		},
		{
			name:   "CRLF without final newline",
			input:  "alpha\r\nbeta",
			expect: []string{"alpha", "beta"},
		},
		{
			name:   "CRLF with final newline",
			input:  "alpha\r\nbeta\r\n",
			expect: []string{"alpha", "beta"},
		},
		{
			name:   "CRLF with empty interior line",
			input:  "alpha\r\n\r\nbeta\r\n",
			expect: []string{"alpha", "", "beta"},
		},
		{
			name:   "isolated carriage return",
			input:  "alpha\rbeta",
			expect: []string{"alpha\rbeta"},
		},
		{
			name:   "terminal carriage return",
			input:  "alpha\r",
			expect: []string{"alpha\r"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.expect, outputLines([]byte(tt.input)))
		})
	}
}

func Test_assertValidConfig(t *testing.T) {
	t.Run("config key is valid with three levels", func(t *testing.T) {
		err := assertValidConfigKey("remote.this.testsuite")
		require.NoError(t, err)
	})
	t.Run("config key is valid with two levels", func(t *testing.T) {
		err := assertValidConfigKey("this.testsuite")
		require.NoError(t, err)
	})

	t.Run("panic modes", func(t *testing.T) {
		err := assertValidConfigKey("this")
		require.Error(t, err)
		require.Errorf(t, err, "incorrect Git configuration key.")
	})
}

func Test_configValueExists(t *testing.T) {
	tests := []struct {
		name   string
		value  string
		key    string
		check  string
		exists bool
	}{
		{
			name:   "config value exists",
			value:  "rocks",
			check:  "rocks",
			exists: true,
		},
		{
			name:   "config value doesn't exist",
			value:  "stinks",
			check:  "rocks",
			exists: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			InitGitRepo(t)
			err := SetRemoteConfig("this", "testsuite", tt.value)
			require.NoError(t, err)

			result, err := configValueExists("remote.this.testsuite", tt.check)

			require.NoError(t, err)
			require.Equal(t, tt.exists, result)
		})
	}
}

func TestSetConfig(t *testing.T) {
	tests := []struct {
		name        string
		value       string
		valueExists bool
		oldValue    string
		expected    string
	}{
		{
			name:        "config value already exists",
			value:       "hello",
			valueExists: true,
			oldValue:    "goodbye",
			expected:    "goodbye\nhello\n",
		},
		{
			name:        "config value doesn't exist",
			value:       "hey",
			valueExists: false,
			expected:    "hey\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			InitGitRepo(t)

			if tt.valueExists {
				_, err := exec.Command("git", "config", "cool.testcase", tt.oldValue).Output()
				require.NoError(t, err)
			}

			err := SetConfig("cool.testcase", tt.value)
			require.NoError(t, err)

			output, err := exec.Command("git", "config", "--get-all", "cool.testcase").Output()

			require.Equal(t, tt.expected, string(output))
			require.NoError(t, err)
		})
	}
}

func TestListTags(t *testing.T) {
	cases := map[string]struct {
		expected  []string
		output    string
		wantErr   bool
		errString string
	}{
		"no tags": {
			expected: nil,
			output:   "",
			wantErr:  false,
		},
		"invalid repository": {
			expected:  nil,
			output:    "",
			wantErr:   true,
			errString: "fatal: not a git repository (or any of the parent directories): .git\ngit: exit status 128",
		},
		"no tags w/ extra newline": {
			expected: []string(nil),
			output:   "\n",
			wantErr:  false,
		},
		"single semver tag": {
			expected: []string{"1.0.0"},
			output:   "1.0.0",
			wantErr:  false,
		},
		"multiple semver tags": {
			expected: []string{"1.0.0", "2.0.0", "3.0.0"},
			output:   "1.0.0\n2.0.0\n3.0.0",
			wantErr:  false,
		},
		"multiple semver tags with extra newlines": {
			expected: []string{"1.0.0", "2.0.0", "3.0.0"},
			output:   "1.0.0\n2.0.0\n3.0.0\n\n",
			wantErr:  false,
		},
		"single non-semver tag": {
			expected: []string{"a"},
			output:   "a",
			wantErr:  false,
		},
		"multiple non-semver tag": {
			expected: []string{"a", "b"},
			output:   "a\nb",
			wantErr:  false,
		},
	}

	unsetGitHookEnv(t)
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_DISCOVERY_ACROSS_FILESYSTEM", "true")

	for name, tt := range cases {
		t.Run(name, func(t *testing.T) {
			if tt.wantErr {
				tempDir := t.TempDir()
				// move to a directory without a .git subdirectory
				t.Chdir(tempDir)

				tags, err := ListTags()
				require.Equal(t, tt.errString, errors.Unwrap(err).Error())
				require.Equal(t, tt.expected, tags)
			} else {
				InitGitRepoWithCommit(t)

				for tag := range tt.expected {
					_, err := exec.Command("git", "tag", tt.expected[tag]).Output()
					require.NoError(t, err)
				}

				tags, err := ListTags()
				require.Equal(t, tt.expected, tags)
				require.NoError(t, err)
			}
		})
	}
}

func TestGitUserName(t *testing.T) {
	testCases := []struct {
		desc     string
		setName  string
		expected string
	}{
		{
			desc:     "with a set name",
			setName:  "Bob",
			expected: "Bob\n",
		},
		// NOTE: it's not possible to do any kind of committing without setting
		// a username for git, so it's unlikely this would not set
	}
	for _, tC := range testCases {
		t.Run(tC.desc, func(t *testing.T) {
			InitGitRepo(t)

			_ = SetLocalConfig("user.name", tC.setName)

			output, err := GitUserName()
			require.NoError(t, err)

			require.Equal(t, tC.expected, string(output))
		})
	}
}

func TestSetRemoteResolution(t *testing.T) {
	tests := []struct {
		name          string
		resolution    string
		expectedKey   string
		expectedValue string
	}{
		{
			name:          "base resolution uses glab-resolved-base key",
			resolution:    "base",
			expectedKey:   "remote.origin.glab-resolved-base",
			expectedValue: "base",
		},
		{
			name:          "base: resolution uses glab-resolved-base key",
			resolution:    "base: gitlab.com/gitlab-org/cli",
			expectedKey:   "remote.origin.glab-resolved-base",
			expectedValue: "base: gitlab.com/gitlab-org/cli",
		},
		{
			name:          "head resolution uses glab-resolved-head key",
			resolution:    "head",
			expectedKey:   "remote.origin.glab-resolved-head",
			expectedValue: "head",
		},
		{
			name:          "head: resolution uses glab-resolved-head key",
			resolution:    "head: gitlab.com/user/fork",
			expectedKey:   "remote.origin.glab-resolved-head",
			expectedValue: "head: gitlab.com/user/fork",
		},
		{
			name:          "other resolution uses legacy glab-resolved key",
			resolution:    "something-else",
			expectedKey:   "remote.origin.glab-resolved",
			expectedValue: "something-else",
		},
		{
			name:          "basement does not match base prefix",
			resolution:    "basement",
			expectedKey:   "remote.origin.glab-resolved",
			expectedValue: "basement",
		},
		{
			name:          "baseball does not match base prefix",
			resolution:    "baseball",
			expectedKey:   "remote.origin.glab-resolved",
			expectedValue: "baseball",
		},
		{
			name:          "header does not match head prefix",
			resolution:    "header",
			expectedKey:   "remote.origin.glab-resolved",
			expectedValue: "header",
		},
		{
			name:          "heading does not match head prefix",
			resolution:    "heading",
			expectedKey:   "remote.origin.glab-resolved",
			expectedValue: "heading",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			InitGitRepo(t)

			// Call SetRemoteResolution
			err := SetRemoteResolution("origin", tc.resolution)
			require.NoError(t, err)

			// Verify the correct config key was set
			cmd := exec.Command("git", "config", tc.expectedKey)
			output, err := run.PrepareCmd(cmd).Output()
			require.NoError(t, err)

			assert.Equal(t, tc.expectedValue, string(output[:len(output)-1])) // trim trailing newline
		})
	}
}

func Test_cloneTargetDir(t *testing.T) {
	tests := []struct {
		name     string
		cloneURL string
		want     string
	}{
		{"https with .git", "https://gitlab.com/group/source.git", "source"},
		{"https without .git", "https://gitlab.com/group/source", "source"},
		{"trailing slash", "https://gitlab.com/group/source/", "source"},
		{"dot git with trailing slash", "https://gitlab.com/group/source.git/", "source"},
		{"scp style", "git@gitlab.com:group/source.git", "source"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, cloneTargetDir(tt.cloneURL))
		})
	}
}
