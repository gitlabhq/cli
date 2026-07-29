//go:build !integration

package infer

import (
	"fmt"
	"testing"
	"time"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/commands/stack/stackutils"
	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/git"
	git_testing "gitlab.com/gitlab-org/cli/internal/git/testing"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestGenerateStackSha(t *testing.T) {
	t.Setenv("NO_COLOR", "true")

	t.Run("produces deterministic output for same inputs", func(t *testing.T) {
		ts := fixedTime()
		sha1, err := stackutils.GenerateStackSha("msg", "title", "author", ts)
		require.NoError(t, err)

		sha2, err := stackutils.GenerateStackSha("msg", "title", "author", ts)
		require.NoError(t, err)

		assert.Equal(t, sha1, sha2)
	})

	t.Run("produces different output for different inputs", func(t *testing.T) {
		ts := fixedTime()
		sha1, err := stackutils.GenerateStackSha("msg1", "title", "author", ts)
		require.NoError(t, err)

		sha2, err := stackutils.GenerateStackSha("msg2", "title", "author", ts)
		require.NoError(t, err)

		assert.NotEqual(t, sha1, sha2)
	})

	t.Run("returns 8 hex characters", func(t *testing.T) {
		sha, err := stackutils.GenerateStackSha("msg", "title", "author", fixedTime())
		require.NoError(t, err)
		assert.Len(t, sha, 8)
	})
}

func TestCreateShaBranch(t *testing.T) {
	t.Setenv("NO_COLOR", "true")
	t.Setenv("BRANCH_PREFIX", "")

	t.Run("uses configured branch prefix", func(t *testing.T) {
		git.InitGitRepo(t)

		factory := createFactoryWithConfig("myprefix")

		branch, err := stackutils.CreateShaBranch(factory, "abcd1234", "my-stack")
		require.NoError(t, err)
		assert.Equal(t, "myprefix-my-stack-abcd1234", branch)
	})
}

func TestCreateBranches(t *testing.T) {
	t.Setenv("NO_COLOR", "true")
	t.Setenv("USER", "testuser")

	t.Run("creates branches and ref files for each commit", func(t *testing.T) {
		dir := git.InitGitRepoWithCommit(t)

		stackTitle := "test-stack"
		err := git.SetLocalConfig("glab.currentstack", stackTitle)
		require.NoError(t, err)

		_, err = git.AddStackRefDir(stackTitle)
		require.NoError(t, err)

		err = git.AddStackBaseBranch(stackTitle, "main")
		require.NoError(t, err)

		ctrl := gomock.NewController(t)
		mockGR := git_testing.NewMockGitRunner(ctrl)

		mockGR.EXPECT().
			Git("symbolic-ref", "--quiet", "--short", "HEAD").
			Return("feature-branch", nil)

		mockGR.EXPECT().
			Git("log", "-1", "--format=%s", "abc123").
			Return("First commit", nil)
		mockGR.EXPECT().
			Git("checkout", "-b", gomock.Any(), "main").
			Return("", nil)
		mockGR.EXPECT().
			Git("cherry-pick", "abc123").
			Return("", nil)

		mockGR.EXPECT().
			Git("log", "-1", "--format=%s", "def456").
			Return("Second commit", nil)
		mockGR.EXPECT().
			Git("checkout", "-b", gomock.Any(), gomock.Any()).
			Return("", nil)
		mockGR.EXPECT().
			Git("cherry-pick", "def456").
			Return("", nil)

		mockGR.EXPECT().
			Git("checkout", "feature-branch").
			Return("", nil)

		factory := createFactoryWithConfig("test")

		emptyStack := git.Stack{Title: stackTitle, Refs: make(map[string]git.StackRef)}
		commits := []string{"abc123", "def456"}

		err = createBranches(factory, mockGR, commits, stackTitle, emptyStack)
		require.NoError(t, err)

		stack, err := git.GatherStackRefs(stackTitle)
		require.NoError(t, err)
		assert.Len(t, stack.Refs, 2)

		first := stack.First()
		assert.Equal(t, "First commit", first.Description)
		assert.True(t, first.IsFirst())
		assert.False(t, first.IsLast())

		last := stack.Last()
		assert.Equal(t, "Second commit", last.Description)
		assert.False(t, last.IsFirst())
		assert.True(t, last.IsLast())

		assert.Equal(t, first.Next, last.SHA)
		assert.Equal(t, last.Prev, first.SHA)

		_ = dir
	})

	t.Run("appends to existing stack", func(t *testing.T) {
		dir := git.InitGitRepoWithCommit(t)

		stackTitle := "test-stack"
		err := git.SetLocalConfig("glab.currentstack", stackTitle)
		require.NoError(t, err)

		_, err = git.AddStackRefDir(stackTitle)
		require.NoError(t, err)

		err = git.AddStackBaseBranch(stackTitle, "main")
		require.NoError(t, err)

		existingRef := git.StackRef{
			SHA:         "existing01",
			Branch:      "test-test-stack-existing01",
			Description: "Existing layer",
		}
		err = git.AddStackRefFile(stackTitle, existingRef)
		require.NoError(t, err)

		ctrl := gomock.NewController(t)
		mockGR := git_testing.NewMockGitRunner(ctrl)

		mockGR.EXPECT().
			Git("symbolic-ref", "--quiet", "--short", "HEAD").
			Return("feature-branch", nil)

		mockGR.EXPECT().
			Git("log", "-1", "--format=%s", "newcommit").
			Return("New commit", nil)
		mockGR.EXPECT().
			Git("checkout", "-b", gomock.Any(), "test-test-stack-existing01").
			Return("", nil)
		mockGR.EXPECT().
			Git("cherry-pick", "newcommit").
			Return("", nil)

		mockGR.EXPECT().
			Git("checkout", "feature-branch").
			Return("", nil)

		factory := createFactoryWithConfig("test")

		existingStack, err := git.GatherStackRefs(stackTitle)
		require.NoError(t, err)
		require.Len(t, existingStack.Refs, 1)

		err = createBranches(factory, mockGR, []string{"newcommit"}, stackTitle, existingStack)
		require.NoError(t, err)

		stack, err := git.GatherStackRefs(stackTitle)
		require.NoError(t, err)
		assert.Len(t, stack.Refs, 2)

		first := stack.First()
		assert.Equal(t, "Existing layer", first.Description)

		last := stack.Last()
		assert.Equal(t, "New commit", last.Description)
		assert.Equal(t, first.SHA, last.Prev)

		_ = dir
	})

	t.Run("rolls back on cherry-pick conflict", func(t *testing.T) {
		dir := git.InitGitRepoWithCommit(t)

		stackTitle := "test-stack"
		err := git.SetLocalConfig("glab.currentstack", stackTitle)
		require.NoError(t, err)

		_, err = git.AddStackRefDir(stackTitle)
		require.NoError(t, err)

		err = git.AddStackBaseBranch(stackTitle, "main")
		require.NoError(t, err)

		ctrl := gomock.NewController(t)
		mockGR := git_testing.NewMockGitRunner(ctrl)

		mockGR.EXPECT().
			Git("symbolic-ref", "--quiet", "--short", "HEAD").
			Return("feature-branch", nil)

		mockGR.EXPECT().
			Git("log", "-1", "--format=%s", "abc123").
			Return("First commit", nil)
		mockGR.EXPECT().
			Git("checkout", "-b", gomock.Any(), "main").
			Return("", nil)
		mockGR.EXPECT().
			Git("cherry-pick", "abc123").
			Return("", nil)

		mockGR.EXPECT().
			Git("log", "-1", "--format=%s", "def456").
			Return("Conflicting commit", nil)
		mockGR.EXPECT().
			Git("checkout", "-b", gomock.Any(), gomock.Any()).
			Return("", nil)
		mockGR.EXPECT().
			Git("cherry-pick", "def456").
			Return("", fmt.Errorf("conflict"))

		mockGR.EXPECT().
			Git("cherry-pick", "--abort").
			Return("", nil)

		mockGR.EXPECT().
			Git("checkout", "feature-branch").
			Return("", nil).Times(2)

		mockGR.EXPECT().
			Git("branch", "-D", gomock.Any()).
			Return("", nil).Times(2)

		factory := createFactoryWithConfig("test")

		emptyStack := git.Stack{Title: stackTitle, Refs: make(map[string]git.StackRef)}
		err = createBranches(factory, mockGR, []string{"abc123", "def456"}, stackTitle, emptyStack)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "conflict cherry-picking commit 2/2")

		stack, err := git.GatherStackRefs(stackTitle)
		require.NoError(t, err)
		assert.Empty(t, stack.Refs)

		_ = dir
	})

	t.Run("rolls back modified existing ref on cherry-pick conflict", func(t *testing.T) {
		dir := git.InitGitRepoWithCommit(t)

		stackTitle := "test-stack"
		err := git.SetLocalConfig("glab.currentstack", stackTitle)
		require.NoError(t, err)

		_, err = git.AddStackRefDir(stackTitle)
		require.NoError(t, err)

		err = git.AddStackBaseBranch(stackTitle, "main")
		require.NoError(t, err)

		// Create an existing stack layer (simulates a previous "infer" or "save")
		existingRef := git.StackRef{
			SHA:         "existing01",
			Branch:      "test-test-stack-existing01",
			Description: "Existing layer",
		}
		err = git.AddStackRefFile(stackTitle, existingRef)
		require.NoError(t, err)

		ctrl := gomock.NewController(t)
		mockGR := git_testing.NewMockGitRunner(ctrl)

		mockGR.EXPECT().
			Git("symbolic-ref", "--quiet", "--short", "HEAD").
			Return("feature-branch", nil)

		// First commit succeeds
		mockGR.EXPECT().
			Git("log", "-1", "--format=%s", "goodcommit").
			Return("Good commit", nil)
		mockGR.EXPECT().
			Git("checkout", "-b", gomock.Any(), "test-test-stack-existing01").
			Return("", nil)
		mockGR.EXPECT().
			Git("cherry-pick", "goodcommit").
			Return("", nil)

		// Second commit conflicts
		mockGR.EXPECT().
			Git("log", "-1", "--format=%s", "badcommit").
			Return("Bad commit", nil)
		mockGR.EXPECT().
			Git("checkout", "-b", gomock.Any(), gomock.Any()).
			Return("", nil)
		mockGR.EXPECT().
			Git("cherry-pick", "badcommit").
			Return("", fmt.Errorf("conflict"))

		mockGR.EXPECT().
			Git("cherry-pick", "--abort").
			Return("", nil)

		// Rollback: restore branch, delete created branches
		mockGR.EXPECT().
			Git("checkout", "feature-branch").
			Return("", nil).Times(2)
		mockGR.EXPECT().
			Git("branch", "-D", gomock.Any()).
			Return("", nil).Times(2)

		factory := createFactoryWithConfig("test")

		existingStack, err := git.GatherStackRefs(stackTitle)
		require.NoError(t, err)
		require.Len(t, existingStack.Refs, 1)

		err = createBranches(factory, mockGR, []string{"goodcommit", "badcommit"}, stackTitle, existingStack)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "conflict cherry-picking commit 2/2")

		// Verify the existing ref's Next pointer was reverted (no dangling pointer)
		stack, err := git.GatherStackRefs(stackTitle)
		require.NoError(t, err)
		assert.Len(t, stack.Refs, 1, "only the original ref should remain")

		restoredRef := stack.First()
		assert.Equal(t, "Existing layer", restoredRef.Description)
		assert.Empty(t, restoredRef.Next, "existing ref's Next should be reverted to empty")

		_ = dir
	})
}

func TestParseBaseBranch(t *testing.T) {
	t.Setenv("NO_COLOR", "true")

	t.Run("resolves branch name from revision range", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockGR := git_testing.NewMockGitRunner(ctrl)

		mockGR.EXPECT().
			Git("rev-parse", "--abbrev-ref", "main").
			Return("main", nil)

		branch, err := parseBaseBranch(mockGR, []string{"main..HEAD"})
		require.NoError(t, err)
		assert.Equal(t, "main", branch)
	})

	t.Run("rejects relative revision like HEAD~3", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockGR := git_testing.NewMockGitRunner(ctrl)

		mockGR.EXPECT().
			Git("rev-parse", "--abbrev-ref", "HEAD~3").
			Return("HEAD~3", nil)

		_, err := parseBaseBranch(mockGR, []string{"HEAD~3..HEAD"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "relative revision")
		assert.Contains(t, err.Error(), "HEAD~3")
	})

	t.Run("rejects HEAD^ syntax", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockGR := git_testing.NewMockGitRunner(ctrl)

		mockGR.EXPECT().
			Git("rev-parse", "--abbrev-ref", "HEAD^").
			Return("HEAD^", nil)

		_, err := parseBaseBranch(mockGR, []string{"HEAD^..HEAD"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "relative revision")
	})

	t.Run("returns empty string when no range provided", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockGR := git_testing.NewMockGitRunner(ctrl)

		branch, err := parseBaseBranch(mockGR, []string{"abc123"})
		require.NoError(t, err)
		assert.Empty(t, branch)
	})

	t.Run("returns error when rev-parse fails", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockGR := git_testing.NewMockGitRunner(ctrl)

		mockGR.EXPECT().
			Git("rev-parse", "--abbrev-ref", "nonexistent").
			Return("", fmt.Errorf("unknown revision"))

		_, err := parseBaseBranch(mockGR, []string{"nonexistent..HEAD"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "could not resolve")
	})
}

func TestRunNoTTY(t *testing.T) {
	t.Setenv("NO_COLOR", "true")

	t.Run("errors when no stack and no TTY", func(t *testing.T) {
		git.InitGitRepoWithCommit(t)

		ctrl := gomock.NewController(t)
		mockGR := git_testing.NewMockGitRunner(ctrl)

		mockGR.EXPECT().
			Git("rev-parse", "--abbrev-ref", "main").
			Return("main", nil)

		exec := cmdtest.SetupCmdForTest(t, func(f cmdutils.Factory) *cobra.Command {
			return NewCmdInferStack(f, mockGR)
		}, false,
			cmdtest.WithGitLabClient(cmdtest.NewTestApiClient(t, nil, "", "gitlab.com").Lab()),
		)

		_, err := exec("main..HEAD")
		require.Error(t, err)
	})
}

func createFactoryWithConfig(value string) cmdutils.Factory {
	strconfig := heredoc.Doc(`
				branch_prefix: ` + value + `
			`)

	cfg := config.NewFromString(strconfig)
	ios, _, _, _ := cmdtest.TestIOStreams()

	return cmdtest.NewTestFactory(ios, cmdtest.WithConfig(cfg))
}

func fixedTime() time.Time {
	return time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
}
