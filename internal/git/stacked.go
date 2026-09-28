package git

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/run"
)

// StackLocation returns the path to the stacked metadata directory.
// It uses git rev-parse --git-common-dir so stacks are shared across all worktrees.
func StackLocation() (string, error) {
	commonDir, err := GitCommonDir()
	if err != nil {
		return "", fmt.Errorf("finding git common directory: %w", err)
	}
	return filepath.Join(commonDir, "stacked"), nil
}

const (
	BaseBranchFile   = "BASE_BRANCH"
	ReorderStateFile = "REORDER_STATE"
)

// ReorderState tracks the progress of an in-flight stack reorder so it can
// be resumed with --continue or rolled back with --abort.
type ReorderState struct {
	NewOrder       []string            `json:"new_order"`
	NextIndex      int                 `json:"next_index"`
	OldTips        map[string]string   `json:"old_tips"`
	OldParent      map[string]string   `json:"old_parent"`
	BaseBranch     string              `json:"base_branch"`
	OriginalBranch string              `json:"original_branch"`
	OldRefs        map[string]StackRef `json:"old_refs"`
	NewRefs        map[string]StackRef `json:"new_refs"`
}

func SetLocalConfig(key, value string) error {
	found, err := configValueExists(key, value)
	if err != nil {
		return fmt.Errorf("Git config value exists: %w", err)
	}

	if found {
		return nil
	}

	addCmd := GitCommand("config", "--local", key, value)
	_, err = run.PrepareCmd(addCmd).Output()
	if err != nil {
		return fmt.Errorf("setting local Git config: %w", err)
	}
	return nil
}

// UnsetLocalConfig removes a key from the repository-local Git config.
// Git exits with a non-zero status when the key is not set, so check that it
// has a value before calling this.
func UnsetLocalConfig(key string) error {
	unsetCmd := GitCommand("config", "--local", "--unset", key)
	_, err := run.PrepareCmd(unsetCmd).Output()
	if err != nil {
		return fmt.Errorf("unsetting local Git config: %w", err)
	}
	return nil
}

func GetCurrentStackTitle() (string, error) {
	return Config("glab.currentstack")
}

func AddStackRefDir(dir string) (string, error) {
	stackLoc, err := StackLocation()
	if err != nil {
		return "", fmt.Errorf("finding stack location: %w", err)
	}

	createdDir := filepath.Join(stackLoc, dir)

	err = os.MkdirAll(createdDir, 0o755)
	if err != nil {
		return "", fmt.Errorf("creating stacked diff directory: %w", err)
	}

	return createdDir, nil
}

// RemoveStackRefDir deletes a stack's metadata directory and everything in it,
// including the BASE_BRANCH file that remains after the last ref file is gone.
func RemoveStackRefDir(title string) error {
	// filepath.Base returns "." and ".." unchanged, so they are rejected explicitly.
	if title == "" || title == "." || title == ".." || title != filepath.Base(title) {
		return fmt.Errorf("invalid stack name: %q", title)
	}

	stackDir, err := StackRootDir(title)
	if err != nil {
		return fmt.Errorf("finding stack location: %w", err)
	}

	if err := os.RemoveAll(stackDir); err != nil {
		return fmt.Errorf("removing stacked diff directory: %w", err)
	}

	return nil
}

func StackRootDir(title string) (string, error) {
	stackLoc, err := StackLocation()
	if err != nil {
		return "", err
	}

	return filepath.Join(stackLoc, title), nil
}

func AddStackRefFile(title string, stackRef StackRef) error {
	refDir, err := StackRootDir(title)
	if err != nil {
		return fmt.Errorf("error determining Git root: %w", err)
	}

	initialJsonData, err := json.Marshal(stackRef) //nolint:forbidigo // stack reference is written to disk, not stdout
	if err != nil {
		return fmt.Errorf("error marshaling data: %w", err)
	}

	if _, err = os.Stat(refDir); os.IsNotExist(err) {
		err = os.MkdirAll(refDir, 0o700) // create directory if it doesn't exist
		if err != nil {
			return fmt.Errorf("error creating directory: %w", err)
		}
	}

	fullPath := filepath.Join(refDir, stackRef.SHA+".json")

	err = os.WriteFile(fullPath, initialJsonData, 0o644)
	if err != nil {
		return fmt.Errorf("error running writing file: %w", err)
	}

	return nil
}

func DeleteStackRefFile(title string, stackRef StackRef) error {
	refDir, err := StackRootDir(title)
	if err != nil {
		return fmt.Errorf("error determining Git root: %w", err)
	}

	fullPath := filepath.Join(refDir, stackRef.SHA+".json")

	err = os.Remove(fullPath)
	if err != nil {
		return fmt.Errorf("error removing stack file: %w", err)
	}

	return nil
}

func UpdateStackRefFile(title string, s StackRef) error {
	refDir, err := StackRootDir(title)
	if err != nil {
		return fmt.Errorf("error determining Git root: %w", err)
	}

	fullPath := filepath.Join(refDir, s.SHA+".json")

	initialJsonData, err := json.Marshal(s) //nolint:forbidigo // stack ref file is written to disk, not stdout
	if err != nil {
		return fmt.Errorf("error marshaling data: %w", err)
	}

	err = os.WriteFile(fullPath, initialJsonData, 0o644)
	if err != nil {
		return fmt.Errorf("error writing file: %w", err)
	}

	return nil
}

// WriteReorderState persists the reorder state for the given stack to the
// REORDER_STATE file.
func WriteReorderState(title string, state ReorderState) error {
	root, err := StackRootDir(title)
	if err != nil {
		return fmt.Errorf("could not determine stack root: %w", err)
	}
	data, err := json.MarshalIndent(state, "", "  ") //nolint:forbidigo // reorder state is written to disk, not stdout
	if err != nil {
		return fmt.Errorf("could not marshal reorder state: %w", err)
	}
	return config.WriteFile(filepath.Join(root, ReorderStateFile), data, 0o644)
}

// ReadReorderState loads the reorder state for the given stack. It returns an
// error satisfying errors.Is(err, os.ErrNotExist) when no reorder is in progress.
func ReadReorderState(title string) (ReorderState, error) {
	root, err := StackRootDir(title)
	if err != nil {
		return ReorderState{}, err
	}
	data, err := os.ReadFile(filepath.Join(root, ReorderStateFile))
	if err != nil {
		return ReorderState{}, err
	}
	var state ReorderState
	if err := json.Unmarshal(data, &state); err != nil {
		return ReorderState{}, fmt.Errorf("could not parse reorder state: %w", err)
	}
	return state, nil
}

// DeleteReorderState removes the REORDER_STATE file for the given stack.
func DeleteReorderState(title string) error {
	root, err := StackRootDir(title)
	if err != nil {
		return err
	}
	return os.Remove(filepath.Join(root, ReorderStateFile))
}

// ReorderInProgress returns true if a REORDER_STATE file exists for the
// given stack title. Other stack commands should call this to block
// operations while a reorder is in progress.
func ReorderInProgress(title string) (bool, error) {
	root, err := StackRootDir(title)
	if err != nil {
		return false, err
	}
	_, err = os.Stat(filepath.Join(root, ReorderStateFile))
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, err
}

func GetStacks() ([]Stack, error) {
	stackLocationDir, err := StackLocation()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(stackLocationDir)
	if err != nil {
		return nil, err
	}
	var stacks []Stack
	for _, v := range entries {
		if !v.IsDir() {
			continue
		}
		stacks = append(stacks, Stack{Title: v.Name()})
	}
	return stacks, nil
}
