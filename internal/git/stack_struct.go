package git

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"iter"
	"os"
	"path/filepath"
	"strings"

	"gitlab.com/gitlab-org/cli/internal/config"
)

type StackRef struct {
	Prev        string `json:"prev"`
	Branch      string `json:"branch"`
	SHA         string `json:"sha"`
	Next        string `json:"next"`
	MR          string `json:"mr"`
	Description string `json:"description"`
}

// Stack represents a stacked diff data structure.
// Refs are structured as a doubly linked list where
// the links are identified with the StackRef.Prev
// and StackRef.Next fields.
// The StackRef.SHA is the id that the former two
// fields can point to.
// All stacks must be created with GatherStackRefs
// which validates the stack for consistency.
type Stack struct {
	Title string
	Refs  map[string]StackRef
}

func (s Stack) Empty() bool { return len(s.Refs) == 0 }

func (s *Stack) RemoveRef(ref StackRef, gr GitRunner) error {
	if ref.IsFirst() && ref.IsLast() {
		// this is the only ref, so just remove it
		err := DeleteStackRefFile(s.Title, ref)
		delete(s.Refs, ref.SHA)
		if err != nil {
			return fmt.Errorf("could not delete reference file: %w", err)
		}

		return nil
	}

	err := s.adjustAdjacentRefs(ref)
	if err != nil {
		return fmt.Errorf("error adjusting next reference: %w", err)
	}

	err = DeleteStackRefFile(s.Title, ref)
	if err != nil {
		return fmt.Errorf("could not delete reference file: %w", err)
	}

	err = s.RemoveBranch(ref, gr)
	if err != nil {
		return fmt.Errorf("could not remove branch: %w", err)
	}

	delete(s.Refs, ref.SHA)

	return nil
}

func (s *Stack) RemoveBranch(ref StackRef, gr GitRunner) error {
	var branch string
	var err error

	if ref.IsFirst() {
		branch, err = s.BaseBranch(gr)
		if err != nil {
			return err
		}

	} else {
		branch = s.Refs[ref.Prev].Branch
	}

	err = CheckoutBranch(branch, gr)
	if err != nil {
		return err
	}

	err = DeleteLocalBranch(ref.Branch, gr)
	if err != nil {
		return err
	}

	return nil
}

func (s *Stack) adjustAdjacentRefs(ref StackRef) error {
	refs := s.Refs

	if ref.Prev != "" {
		prev := refs[ref.Prev]
		delete(refs, ref.Prev)

		prev.Next = ref.Next
		refs[ref.Prev] = prev

		err := UpdateStackRefFile(s.Title, prev)
		if err != nil {
			return fmt.Errorf("could not update reference file: %w", err)
		}
	}

	if ref.Next != "" {
		next := refs[ref.Next]
		delete(refs, ref.Next)

		next.Prev = ref.Prev
		refs[ref.Next] = next

		err := UpdateStackRefFile(s.Title, next)
		if err != nil {
			return fmt.Errorf("could not update reference file: %w", err)
		}
	}

	return nil
}

func (s *Stack) IndexAt(ref StackRef) int {
	for i, r := range s.Iter2() {
		if r == ref {
			return i
		}
	}

	return -1
}

func (s *Stack) Last() StackRef {
	if s.Empty() {
		return StackRef{}
	}

	for _, ref := range s.Refs {
		if ref.IsLast() {
			return ref
		}
	}

	// All Stacks should be created with GatherStackRefs which validates the Stack consistency.
	panic(errors.New("can't find the last ref in the chain; data might be corrupted"))
}

func (s *Stack) First() StackRef {
	if s.Empty() {
		return StackRef{}
	}

	for _, ref := range s.Refs {
		if ref.IsFirst() {
			return ref
		}
	}

	// All Stacks should be created with GatherStackRefs which validates the Stack consistency.
	panic(errors.New("can't find the first ref in the chain; data might be corrupted"))
}

// Iter returns an iterator to range from the first to the last ref in the stack.
func (s *Stack) Iter() iter.Seq[StackRef] {
	return func(yield func(StackRef) bool) {
		ref := s.First()
		for !ref.Empty() {
			if !yield(ref) {
				return
			}

			ref = s.Refs[ref.Next]
		}
	}
}

func (s *Stack) Branches() []string {
	branches := []string{}
	for ref := range s.Iter() {
		branches = append(branches, ref.Branch)
	}

	return branches
}

// Iter2 returns an iterator like Iter, but includes an index
func (s *Stack) Iter2() iter.Seq2[int, StackRef] {
	return func(yield func(int, StackRef) bool) {
		ref := s.First()
		i := 0

		for !ref.Empty() {
			if !yield(i, ref) {
				return
			}

			i++
			ref = s.Refs[ref.Next]
		}
	}
}

func (s *Stack) BaseBranch(gr GitRunner) (string, error) {
	root, err := StackRootDir(s.Title)
	if err != nil {
		return "", fmt.Errorf("could not determine stack root: %w", err)
	}

	filename := filepath.Join(root, BaseBranchFile)

	// we do have a base branch in the metadata
	fileInfo, err := os.Stat(filename)
	if err == nil && !fileInfo.IsDir() {
		trimmed, err := config.TrimmedFileContents(filename)
		if err != nil {
			return "", fmt.Errorf("could not read base branch file: %w", err)
		}

		return trimmed, nil
	}

	// if there's an error reading the file, show that.
	// it's ok if doesn't exist yet, however.
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("could not access base branch file: %w", err)
	}

	// no metadata file - lets try to get it from git
	defBranchOutput, err := gr.Git("remote", "show", DefaultRemote)
	if err != nil {
		return "", fmt.Errorf("could not get remote data: %w", err)
	}

	branch, err := ParseDefaultBranch([]byte(defBranchOutput))
	if err != nil {
		return "", fmt.Errorf("could not parse default branch from remote data: %w", err)
	}

	return branch, nil
}

func AddStackBaseBranch(title string, branch string) error {
	root, err := StackRootDir(title)
	if err != nil {
		return fmt.Errorf("could not determine stack root: %w", err)
	}

	filename := filepath.Join(root, BaseBranchFile)

	// os.WriteFile creates the file itself; the previous os.Create left an open
	// file handle that was never closed, which on Windows blocked the file from
	// being removed afterwards.
	if err := os.WriteFile(filename, []byte(branch), 0o644); err != nil {
		return fmt.Errorf("error adding branch metadata file %v: %w", filename, err)
	}

	return nil
}

func GatherStackRefs(title string) (Stack, error) {
	stack := Stack{Title: title}
	stack.Refs = make(map[string]StackRef)

	root, err := StackRootDir(title)
	if err != nil {
		return stack, err
	}

	err = filepath.WalkDir(root, func(dir string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() {
			return nil
		}
		// read files in the stacked ref directory
		// TODO: this may be quicker if we introduce a package
		// https://github.com/bmatcuk/doublestar
		if filepath.Ext(d.Name()) == ".json" {
			data, err := os.ReadFile(dir)
			if err != nil {
				return err
			}

			// marshal them into our StackRef type
			stackRef := StackRef{}
			err = json.Unmarshal(data, &stackRef)
			if err != nil {
				return err
			}

			stack.Refs[stackRef.SHA] = stackRef
		}

		return nil
	})
	if err != nil {
		if os.IsNotExist(err) { // there might not be any refs yet, this is ok.
			return stack, nil
		} else {
			return stack, err
		}
	}

	err = validateStackRefs(stack)
	if err != nil {
		return Stack{}, err
	}

	return stack, nil
}

func validateStackRefs(s Stack) error {
	endRefs := 0
	startRefs := 0

	if s.Empty() {
		// empty stacks are okay.
		return nil
	}

	for _, ref := range s.Refs {
		if ref.IsFirst() {
			startRefs++
		}

		if ref.IsLast() {
			endRefs++
		}

		if endRefs > 1 || startRefs > 1 {
			return errors.New("more than one end or start ref detected; data might be corrupted")
		}
	}

	if startRefs != 1 {
		return errors.New("expected exactly one start ref; data might be corrupted")
	}
	if endRefs != 1 {
		return errors.New("expected exactly one end ref; data might be corrupted")
	}
	return nil
}

func CurrentStackRefFromCurrentBranch(title string) (StackRef, error) {
	stack, err := GatherStackRefs(title)
	if err != nil {
		return StackRef{}, err
	}

	branch, err := CurrentBranch()
	if err != nil {
		return StackRef{}, err
	}

	return stack.RefFromBranch(branch)
}

func (s Stack) RefFromBranch(branch string) (StackRef, error) {
	for ref := range s.Iter() {
		if ref.Branch == branch {
			return ref, nil
		}
	}

	return StackRef{}, errors.New("Could not find stack ref for branch: " + branch)
}

// Empty returns true if the stack ref does not have an associated SHA (commit).
// This indicates that the StackRef is invalid.
func (r StackRef) Empty() bool { return r.SHA == "" }

// IsFirst returns true if the stack ref is the first of the stack.
// A stack ref is considered the first if it does not reference any previous ref.
func (r StackRef) IsFirst() bool { return r.Prev == "" }

// IsLast returns true if the stack ref is the last of the stack.
// A stack ref is considered the last if it does not reference any next ref.
func (r StackRef) IsLast() bool { return r.Next == "" }

// Subject returns the stack ref description suitable as commit Subject
// and for other in space limited places.
// It only takes the first line of the description into account
// and truncates it to 72 characters.
func (r StackRef) Subject() string {
	ls := strings.SplitN(r.Description, "\n", 2)
	if len(ls[0]) <= 72 {
		return ls[0]
	}

	return ls[0][:69] + "..."
}

func (r StackRef) Body() string {
	_, body, _ := strings.Cut(r.Description, "\n")
	return strings.TrimSpace(body)
}
