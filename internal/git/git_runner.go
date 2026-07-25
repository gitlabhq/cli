package git

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"

	"gitlab.com/gitlab-org/cli/internal/run"
)

type GitRunner interface {
	Git(args ...string) (string, error)
	// GitWithIO runs git with the provided stdout and stderr writers attached
	// directly to the subprocess, allowing live output (progress bars, errors)
	// to reach the user during long-running operations like fetch and checkout.
	GitWithIO(stdout, stderr io.Writer, args ...string) error
}

// Executor is a subset of cmdutils.Executor defined here to avoid a circular
// import: the cmdutils package already imports the git package.
type Executor interface {
	ExecWithIO(ctx context.Context, name string, args []string, env []string, stdin io.Reader, stdout, stderr io.Writer) error
}

type StandardGitCommand struct {
	executor Executor
}

func NewGitCommand(executor Executor) StandardGitCommand {
	return StandardGitCommand{executor: executor}
}

func (gitc StandardGitCommand) Git(args ...string) (string, error) {
	env := append(os.Environ(), "LC_ALL=C")

	if gitc.executor == nil {
		cmd := GitCommand(args...)
		cmd.Env = env
		output, err := run.PrepareCmd(cmd).Output()
		if err != nil {
			return "", err
		}
		return string(output), nil
	}

	var stdout, stderr bytes.Buffer
	err := gitc.executor.ExecWithIO(context.Background(), "git", args, env, nil, &stdout, &stderr)
	if err != nil {
		errMsg := strings.TrimSpace(stderr.String())
		if errMsg != "" {
			return "", fmt.Errorf("%s: %w", errMsg, err)
		}
		return "", err
	}
	return stdout.String(), nil
}

func (gitc StandardGitCommand) GitWithIO(stdout, stderr io.Writer, args ...string) error {
	env := append(os.Environ(), "LC_ALL=C")

	if gitc.executor == nil {
		var stderrBuf bytes.Buffer
		cmd := GitCommand(args...)
		cmd.Env = env
		cmd.Stdout = stdout
		cmd.Stderr = io.MultiWriter(stderr, &stderrBuf)
		if err := cmd.Run(); err != nil {
			if msg := strings.TrimSpace(stderrBuf.String()); msg != "" {
				return fmt.Errorf("%s: %w", msg, err)
			}
			return err
		}
		return nil
	}

	return gitc.executor.ExecWithIO(context.Background(), "git", args, env, nil, stdout, stderr)
}

const (
	DefaultBranchName = "main"
)

// headBranchRegex is used to parse the default branch from git remote output
var headBranchRegex = regexp.MustCompile(`HEAD branch:\s+(.+)`)

type (
	Git interface {
		CheckoutBranch(branch string) error
		CheckoutNewBranch(branch string) error
		CurrentBranch() (string, error)
		DeleteLocalBranch(branch string) error
		DefaultBranch(remote string) (string, error)
		RemoteBranchExists(remote string, branch string) (bool, error)
		UncommittedChangeCount() (int, error)
		UserName() (string, error)
		LatestCommit(ref string) (*Commit, error)
		Commits(baseRef, headRef string) ([]*Commit, error)
		CommitBody(sha string) (string, error)
		Push(remote string, ref string) error
		HasLocalBranch(branch string) (bool, error)
	}

	StandardGitRunner struct {
		gitBinary string
	}
)

var _ Git = (*StandardGitRunner)(nil)

func NewStandardGitRunner(gitBinary string) *StandardGitRunner {
	if gitBinary == "" {
		gitBinary = "git" // default to using "git" from PATH
	}
	return &StandardGitRunner{gitBinary: gitBinary}
}

// CurrentBranch reads the checked-out branch for the git repository
func (g *StandardGitRunner) CurrentBranch() (string, error) {
	stdout, stderr, err := g.runGitCommand("symbolic-ref", "--quiet", "--short", "HEAD")
	if err == nil {
		before, _ := bytes.CutSuffix(stdout, []byte("\n"))
		return string(before), nil
	}
	var cmdErr *run.CmdError
	if errors.As(err, &cmdErr) {
		if cmdErr.Stderr.Len() == 0 {
			return "", ErrNotOnAnyBranch // Detached HEAD error
		}
	}
	return "", fmt.Errorf("unknown error getting current branch: %w - %s", err, stderr)
}

// DefaultBranch gets the default branch for a remote
func (g *StandardGitRunner) DefaultBranch(remote string) (string, error) {
	stdout, stderr, err := g.runGitCommand("remote", "show", remote)
	if err != nil {
		return DefaultBranchName, fmt.Errorf("could not get default branch for remote %s: %w - %s", remote, err, stderr)
	}

	defaultBranch, err := parseDefaultBranch(stdout)
	if err != nil {
		return DefaultBranchName, fmt.Errorf("could not parse default branch for remote %s: %w", remote, err)
	}

	return defaultBranch, nil
}

// DeleteLocalBranch deletes a local git branch
func (g *StandardGitRunner) DeleteLocalBranch(branch string) error {
	_, stderr, err := g.runGitCommand("branch", "-D", branch)
	if err != nil {
		return fmt.Errorf("could not delete local branch %s: %w - %s", branch, err, stderr)
	}
	return nil
}

// CheckoutBranch switches to an existing branch
func (g *StandardGitRunner) CheckoutBranch(branch string) error {
	_, stderr, err := g.runGitCommand("checkout", branch)
	if err != nil {
		return fmt.Errorf("could not checkout branch %s: %w - %s", branch, err, stderr)
	}
	return nil
}

// RemoteBranchExists checks if a remote branch exists
func (g *StandardGitRunner) RemoteBranchExists(remote string, branch string) (bool, error) {
	_, _, err := g.runGitCommand("ls-remote", "--exit-code", "--heads", remote, branch)
	return err == nil, nil // this is either true or false, really
}

// CheckoutNewBranch creates and checks out a new branch
func (g *StandardGitRunner) CheckoutNewBranch(branch string) error {
	_, stderr, err := g.runGitCommand("checkout", "-b", branch)
	if err != nil {
		return fmt.Errorf("could not create new branch: %w - %s", err, stderr)
	}
	return nil
}

// UncommittedChangeCount returns the number of uncommitted changes
func (g *StandardGitRunner) UncommittedChangeCount() (int, error) {
	stdout, stderr, err := g.runGitCommand("status", "--porcelain")
	if err != nil {
		return 0, fmt.Errorf("could not get status: %w - %s", err, stderr)
	}

	lines := strings.Split(string(stdout), "\n")
	count := 0
	for _, l := range lines {
		if l != "" {
			count++
		}
	}
	return count, nil
}

// UserName gets the git user name
func (g *StandardGitRunner) UserName() (string, error) {
	stdout, stderr, err := g.runGitCommand("config", "user.name")
	if err != nil {
		return "", fmt.Errorf("could not get user name: %w - %s", err, stderr)
	}
	return string(stdout), nil
}

// LatestCommit gets the latest commit for a ref
func (g *StandardGitRunner) LatestCommit(ref string) (*Commit, error) {
	stdout, stderr, err := g.runGitCommand("show", "-s", "--format=%h %s", ref)
	if err != nil {
		return &Commit{}, fmt.Errorf("could not get latest commit: %w - %s", err, stderr)
	}

	output := strings.TrimSpace(string(stdout))
	sha, title, ok := strings.Cut(output, " ")
	if !ok || sha == "" || title == "" {
		return &Commit{}, fmt.Errorf("could not parse commit for %s: unexpected format %q", ref, string(stdout))
	}

	return &Commit{
		Sha:   sha,
		Title: title,
	}, nil
}

// Commits gets commits between two refs
func (g *StandardGitRunner) Commits(baseRef, headRef string) ([]*Commit, error) {
	stdout, stderr, err := g.runGitCommand(
		"-c", "log.ShowSignature=false",
		"log", "--pretty=format:%H,%s",
		"--cherry", fmt.Sprintf("%s...%s", baseRef, headRef))
	if err != nil {
		return nil, fmt.Errorf("could not get commits: %w - %s", err, stderr)
	}

	var commits []*Commit
	for _, line := range outputLines(stdout) {
		split := strings.SplitN(line, ",", 2)
		if len(split) != 2 {
			continue
		}
		commits = append(commits, &Commit{
			Sha:   split[0],
			Title: split[1],
		})
	}

	if len(commits) == 0 {
		return commits, fmt.Errorf("could not find any commits between %s and %s", baseRef, headRef)
	}

	return commits, nil
}

// CommitBody gets the body of a commit
func (g *StandardGitRunner) CommitBody(sha string) (string, error) {
	stdout, stderr, err := g.runGitCommand("-c", "log.ShowSignature=false", "show", "-s", "--pretty=format:%b", sha)
	if err != nil {
		return "", fmt.Errorf("could not get commit body: %w - %s", err, stderr)
	}
	return string(stdout), nil
}

// Push publishes a git ref to a remote
func (g *StandardGitRunner) Push(remote string, ref string) error {
	_, stderr, err := g.runGitCommand("push", remote, ref)
	if err != nil {
		return fmt.Errorf("could not push: %w - %s", err, stderr)
	}
	return nil
}

// HasLocalBranch checks if a local branch exists
func (g *StandardGitRunner) HasLocalBranch(branch string) (bool, error) {
	_, stderr, err := g.runGitCommand("rev-parse", "--verify", "refs/heads/"+branch)
	if err == nil {
		return true, nil
	}

	// git rev-parse exits with code 128 when the ref doesn't exist
	var cmdErr *run.CmdError
	if errors.As(err, &cmdErr) {
		// if stderr contains "fatal: Needed a single revision", it means the branch doesn't exist
		// this is expected behavior, not an error
		if strings.Contains(stderr, "fatal: Needed a single revision") ||
			strings.Contains(stderr, "fatal: ambiguous argument") {
			return false, nil
		}
	}

	// otherwise, this is an actual error
	return false, fmt.Errorf("failed to check if branch %s exists: %w - %s", branch, err, stderr)
}

// runGitCommand executes a git command with proper environment setup and returns stdout/stderr
func (g *StandardGitRunner) runGitCommand(args ...string) ([]byte, string, error) {
	cmd := exec.Command(g.gitBinary, args...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	// ensure output from git is in English for string matching
	cmd.Env = os.Environ()
	cmd.Env = append(cmd.Env, "LC_ALL=C")

	err := cmd.Run()
	return stdout.Bytes(), stderr.String(), err
}

// parseDefaultBranch parses the default branch from git remote output
func parseDefaultBranch(output []byte) (string, error) {
	scanner := bufio.NewScanner(bytes.NewReader(output))

	// try to find "HEAD branch:" line
	for scanner.Scan() {
		line := scanner.Text()
		if matches := headBranchRegex.FindStringSubmatch(line); len(matches) > 1 {
			return strings.TrimSpace(matches[1]), nil
		}
	}

	// if scanner encountered an error
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("error scanning output: %w", err)
	}

	// reset scanner to look for branch marked with (HEAD)
	scanner = bufio.NewScanner(bytes.NewReader(output))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.Contains(line, "(HEAD)") {
			parts := strings.Fields(line)
			if len(parts) > 0 {
				return parts[0], nil
			}
		}
	}

	// couldn't find HEAD branch(?)
	return "", errors.New("could not determine default branch from remote output")
}
