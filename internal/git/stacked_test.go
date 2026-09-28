//go:build !integration

package git

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/cli/internal/config"
)

func TestSetLocalConfig(t *testing.T) {
	tests := []struct {
		name           string
		value          string
		existingConfig bool
	}{
		{
			name:           "config already exists",
			value:          "exciting new value",
			existingConfig: true,
		},
		{
			name:           "config doesn't exist",
			value:          "default value",
			existingConfig: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tempDir := InitGitRepo(t)
			defer os.RemoveAll(tempDir)

			if tt.existingConfig {
				_ = GitCommand("config", "--local", "this.glabstacks", "prev-value")
			}

			err := SetLocalConfig("this.glabstacks", tt.value)
			require.NoError(t, err)

			config, err := GetAllConfig("this.glabstacks")
			require.NoError(t, err)

			// GetAllConfig() appends a new line. Let's get rid of that.
			compareString := strings.TrimSuffix(string(config), "\n")

			if compareString != tt.value {
				t.Errorf("config value = %v, want %v", compareString, tt.value)
			}
		})
	}
}

func Test_AddStackRefDir(t *testing.T) {
	tests := []struct {
		name     string
		branch   string
		worktree bool
	}{
		{
			name:   "normal filename",
			branch: "thing",
		},
		{
			name:   "advanced filename",
			branch: "something-with-dashes",
		},
		{
			name:     "normal filename in worktree",
			branch:   "thing",
			worktree: true,
		},
		{
			name:     "advanced filename in worktree",
			branch:   "something-with-dashes",
			worktree: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			InitGitRepoOrWorktree(t, tt.worktree)

			_, err := AddStackRefDir(tt.branch)
			require.NoError(t, err)

			stackLoc, locErr := StackLocation()
			require.NoError(t, locErr)

			_, err = os.Stat(filepath.Join(stackLoc, tt.branch))
			require.NoError(t, err)

			if tt.worktree {
				// Ensure nothing was written to the per-worktree git dir.
				gitDir, err := GitDir()
				require.NoError(t, err)
				require.NoDirExists(t, filepath.Join(gitDir, "stacked"))
			}
		})
	}
}

func Test_StackRootDir(t *testing.T) {
	tests := []struct {
		name     string
		title    string
		worktree bool
	}{
		{
			name:  "valid title",
			title: "test-stack",
		},
		{
			name:  "empty title",
			title: "",
		},
		{
			name:     "valid title in worktree",
			title:    "test-stack",
			worktree: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			InitGitRepoOrWorktree(t, tt.worktree)

			got, err := StackRootDir(tt.title)
			require.NoError(t, err)

			// Verify the path contains the expected components
			require.Contains(t, got, "stacked", "StackRootDir() should contain stacked dir name")
			require.Contains(t, got, tt.title, "StackRootDir() should contain title")

			if tt.worktree {
				// Verify it resolves to the common git dir, not the per-worktree one.
				commonDir, err := GitCommonDir()
				require.NoError(t, err)
				require.True(t, strings.HasPrefix(got, commonDir), "StackRootDir() should be under common git dir")
			}
		})
	}
}

func Test_AddStackRefFile(t *testing.T) {
	type args struct {
		title    string
		stackRef StackRef
	}
	tests := []struct {
		name     string
		args     args
		worktree bool
		wantErr  bool
	}{
		{
			name: "no message",
			args: args{
				title: "sweet-title-123",
				stackRef: StackRef{
					Prev:   "hello",
					Branch: "gmh-feature-3ab3da",
					Next:   "goodbye",
					SHA:    "1a2b3c4d",
					MR:     "https://gitlab.com/",
				},
			},
			wantErr: true,
		},
		{
			name:     "no message in worktree",
			worktree: true,
			args: args{
				title: "sweet-title-123",
				stackRef: StackRef{
					Prev:   "hello",
					Branch: "gmh-feature-3ab3da",
					Next:   "goodbye",
					SHA:    "1a2b3c4d",
					MR:     "https://gitlab.com/",
				},
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			InitGitRepoOrWorktree(t, tt.worktree)

			err := AddStackRefFile(tt.args.title, tt.args.stackRef)
			require.NoError(t, err)

			stackLoc, locErr := StackLocation()
			require.NoError(t, locErr)
			file := filepath.Join(stackLoc, tt.args.title, tt.args.stackRef.SHA+".json")
			require.True(t, config.CheckFileExists(file))

			stackRef := StackRef{}
			readData, err := os.ReadFile(file)
			require.NoError(t, err)

			err = json.Unmarshal(readData, &stackRef)
			require.NoError(t, err)

			require.Equal(t, stackRef, tt.args.stackRef)

			if tt.worktree {
				gitDir, err := GitDir()
				require.NoError(t, err)
				require.NoDirExists(t, filepath.Join(gitDir, "stacked"))
			}
		})
	}
}

func Test_DeleteStackRefFile(t *testing.T) {
	// TODO: write test
}

func Test_UnsetLocalConfig(t *testing.T) {
	InitGitRepo(t)

	require.NoError(t, SetLocalConfig("this.glabstacks", "a value"))

	require.NoError(t, UnsetLocalConfig("this.glabstacks"))

	_, err := Config("this.glabstacks")
	require.Error(t, err, "the key should be gone")
}

func Test_RemoveStackRefDir(t *testing.T) {
	for _, worktree := range []bool{false, true} {
		suffix := ""
		if worktree {
			suffix = " in worktree"
		}

		t.Run("removes the stack and its files"+suffix, func(t *testing.T) {
			InitGitRepoOrWorktree(t, worktree)

			require.NoError(t, AddStackRefFile("doomed-stack", StackRef{SHA: "1a2b3c4d", Branch: "a-branch"}))
			require.NoError(t, AddStackRefFile("keeper-stack", StackRef{SHA: "5e6f7a8b", Branch: "another-branch"}))

			require.NoError(t, RemoveStackRefDir("doomed-stack"))

			stackLoc, err := StackLocation()
			require.NoError(t, err)
			require.NoDirExists(t, filepath.Join(stackLoc, "doomed-stack"))
			require.DirExists(t, filepath.Join(stackLoc, "keeper-stack"))
		})
	}

	t.Run("removes a stack left with only a BASE_BRANCH file", func(t *testing.T) {
		InitGitRepo(t)

		stackDir, err := AddStackRefDir("merged-stack")
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(stackDir, BaseBranchFile), []byte("main"), 0o644))

		require.NoError(t, RemoveStackRefDir("merged-stack"))
		require.NoDirExists(t, stackDir)
	})

	t.Run("refuses a name that escapes the stacked directory", func(t *testing.T) {
		InitGitRepo(t)

		require.NoError(t, AddStackRefFile("real-stack", StackRef{SHA: "1a2b3c4d", Branch: "a-branch"}))

		for _, name := range []string{"", ".", "..", "../..", "nested/stack"} {
			require.Error(t, RemoveStackRefDir(name), "name %q should be rejected", name)
		}

		gitDir, err := GitDir()
		require.NoError(t, err)
		require.DirExists(t, gitDir)

		stackLoc, err := StackLocation()
		require.NoError(t, err)
		require.DirExists(t, filepath.Join(stackLoc, "real-stack"))
	})
}

func Test_UpdateStackRefFile(t *testing.T) {
	type args struct {
		title    string
		stackRef StackRef
	}
	tests := []struct {
		name     string
		args     args
		worktree bool
		wantErr  bool
	}{
		{
			name: "no message",
			args: args{
				title: "sweet-title-123",
				stackRef: StackRef{
					Prev:   "hello",
					Branch: "gmh-feature-3ab3da",
					Next:   "goodbye",
					SHA:    "1a2b3c4d",
					MR:     "https://gitlab.com/",
				},
			},
			wantErr: true,
		},
		{
			name:     "no message in worktree",
			worktree: true,
			args: args{
				title: "sweet-title-123",
				stackRef: StackRef{
					Prev:   "hello",
					Branch: "gmh-feature-3ab3da",
					Next:   "goodbye",
					SHA:    "1a2b3c4d",
					MR:     "https://gitlab.com/",
				},
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			InitGitRepoOrWorktree(t, tt.worktree)

			// add the initial data
			initial := StackRef{Prev: "123", Branch: "gmh"}
			err := AddStackRefFile(tt.args.title, initial)
			require.NoError(t, err)

			err = UpdateStackRefFile(tt.args.title, tt.args.stackRef)

			require.NoError(t, err)

			stackLoc, locErr := StackLocation()
			require.NoError(t, locErr)
			file := filepath.Join(stackLoc, tt.args.title, tt.args.stackRef.SHA+".json")
			require.True(t, config.CheckFileExists(file))

			stackRef := StackRef{}
			readData, err := os.ReadFile(file)
			require.NoError(t, err)

			err = json.Unmarshal(readData, &stackRef)
			require.NoError(t, err)

			require.Equal(t, stackRef, tt.args.stackRef)

			if tt.worktree {
				gitDir, err := GitDir()
				require.NoError(t, err)
				require.NoDirExists(t, filepath.Join(gitDir, "stacked"))
			}
		})
	}
}

func Test_GetStacks(t *testing.T) {
	stacks := []Stack{
		{
			Title: "stack-0",
			Refs: map[string]StackRef{
				"0": {
					Description: "stack-0 initial commit",
				},
			},
		},
		{
			Title: "stack-1",
			Refs: map[string]StackRef{
				"0": {
					Description: "stack-1 initial commit",
				},
			},
		},
	}

	for _, worktree := range []bool{false, true} {
		suffix := ""
		if worktree {
			suffix = " in worktree"
		}

		t.Run("two stacks"+suffix, func(t *testing.T) {
			InitGitRepoOrWorktree(t, worktree)
			var want []Stack
			for _, v := range stacks {
				for _, ref := range v.Refs {
					err := AddStackRefFile(v.Title, ref)
					require.NoError(t, err)
				}
				want = append(want, Stack{Title: v.Title})
			}
			got, err := GetStacks()
			require.NoError(t, err)
			require.Equal(t, want, got)

			if worktree {
				gitDir, err := GitDir()
				require.NoError(t, err)
				require.NoDirExists(t, filepath.Join(gitDir, "stacked"))
			}
		})
		t.Run("no stacks"+suffix, func(t *testing.T) {
			InitGitRepoOrWorktree(t, worktree)
			got, err := GetStacks()
			var want []Stack = nil
			require.Error(t, err)
			require.Equal(t, want, got)
		})
	}
}

func Test_WriteAndReadReorderState(t *testing.T) {
	InitGitRepo(t)

	state := ReorderState{
		NewOrder:       []string{"branchC", "branchA", "branchB"},
		NextIndex:      1,
		OldTips:        map[string]string{"branchA": "aaa", "branchB": "bbb", "branchC": "ccc", "main": "000"},
		OldParent:      map[string]string{"branchA": "main", "branchB": "branchA", "branchC": "branchB"},
		BaseBranch:     "main",
		OriginalBranch: "branchA",
		OldRefs: map[string]StackRef{
			"ref-a": {SHA: "ref-a", Prev: "", Next: "ref-b", Branch: "branchA"},
			"ref-b": {SHA: "ref-b", Prev: "ref-a", Next: "ref-c", Branch: "branchB"},
			"ref-c": {SHA: "ref-c", Prev: "ref-b", Next: "", Branch: "branchC"},
		},
		NewRefs: map[string]StackRef{
			"ref-c": {SHA: "ref-c", Prev: "", Next: "ref-a", Branch: "branchC"},
			"ref-a": {SHA: "ref-a", Prev: "ref-c", Next: "ref-b", Branch: "branchA"},
			"ref-b": {SHA: "ref-b", Prev: "ref-a", Next: "", Branch: "branchB"},
		},
	}

	// Create the stack directory first
	err := AddStackRefFile("test-stack", StackRef{SHA: "dummy"})
	require.NoError(t, err)

	err = WriteReorderState("test-stack", state)
	require.NoError(t, err)

	got, err := ReadReorderState("test-stack")
	require.NoError(t, err)
	require.Equal(t, state, got)
}

func Test_DeleteReorderState(t *testing.T) {
	InitGitRepo(t)

	// Create directory and write state
	err := AddStackRefFile("test-stack", StackRef{SHA: "dummy"})
	require.NoError(t, err)

	state := ReorderState{NewOrder: []string{"a"}}
	err = WriteReorderState("test-stack", state)
	require.NoError(t, err)

	// Verify it exists
	inProgress, err := ReorderInProgress("test-stack")
	require.NoError(t, err)
	require.True(t, inProgress)

	err = DeleteReorderState("test-stack")
	require.NoError(t, err)

	// Verify it's gone
	inProgress, err = ReorderInProgress("test-stack")
	require.NoError(t, err)
	require.False(t, inProgress)
}

func Test_ReorderInProgress(t *testing.T) {
	InitGitRepo(t)

	// Create the stack directory
	err := AddStackRefFile("test-stack", StackRef{SHA: "dummy"})
	require.NoError(t, err)

	// No state file yet
	inProgress, err := ReorderInProgress("test-stack")
	require.NoError(t, err)
	require.False(t, inProgress)

	// Write state
	state := ReorderState{NewOrder: []string{"a"}}
	err = WriteReorderState("test-stack", state)
	require.NoError(t, err)

	// Now it should be in progress
	inProgress, err = ReorderInProgress("test-stack")
	require.NoError(t, err)
	require.True(t, inProgress)
}

func Test_ReadReorderState_NotExist(t *testing.T) {
	InitGitRepo(t)

	// Create the stack directory but no state file
	err := AddStackRefFile("test-stack", StackRef{SHA: "dummy"})
	require.NoError(t, err)

	_, err = ReadReorderState("test-stack")
	require.ErrorIs(t, err, os.ErrNotExist)
}

func Test_StackLocation_SharedAcrossWorktrees(t *testing.T) {
	repo := NewTestRepo(t)
	worktreeDir1 := repo.addWorktree(t)
	worktreeDir2 := repo.addWorktree(t)

	// Create a stack from worktree 1.
	t.Chdir(worktreeDir1)
	ref1 := StackRef{SHA: "abc123", Branch: "wt1-branch"}
	err := AddStackRefFile("shared-stack", ref1)
	require.NoError(t, err)

	// Verify it is visible from worktree 2.
	t.Chdir(worktreeDir2)
	stacks, err := GetStacks()
	require.NoError(t, err)
	require.Len(t, stacks, 1)
	require.Equal(t, "shared-stack", stacks[0].Title)

	// Verify the file is in the common dir, not in either worktree git dir.
	commonDir, err := GitCommonDir()
	require.NoError(t, err)
	require.FileExists(t, filepath.Join(commonDir, "stacked", "shared-stack", "abc123.json"))

	gitDir1, err := GitDir()
	require.NoError(t, err)
	require.NoDirExists(t, filepath.Join(gitDir1, "stacked"))
}
