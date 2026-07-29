package git

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path"
	"regexp"
	"slices"
	"strings"

	"gitlab.com/gitlab-org/cli/internal/run"
)

const DefaultRemote = "origin"

func GetRemoteURL(remoteAlias string) (string, error) {
	return Config("remote." + remoteAlias + ".url")
}

// GetDefaultBranch finds and returns the remote's default branch
func GetDefaultBranch(remote string) (string, error) {
	getDefBranch := exec.Command("git", "remote", "show", remote)

	// Ensure output from git is in English
	getDefBranch.Env = os.Environ()
	getDefBranch.Env = append(getDefBranch.Env, "LC_ALL=C")

	output, err := run.PrepareCmd(getDefBranch).Output()
	if err != nil {
		return DefaultBranchName, err
	}

	return ParseDefaultBranch(output)
}

func RemoteBranchExists(branch string, gr GitRunner) bool {
	_, err := gr.Git("ls-remote", "--exit-code", "--heads", DefaultRemote, branch)
	return err == nil
}

func ParseDefaultBranch(output []byte) (string, error) {
	var headBranch string

	for o := range strings.SplitSeq(string(output), "\n") {
		o = strings.TrimSpace(o)
		r, err := regexp.Compile(`(HEAD branch:)\s+`)
		if err != nil {
			return DefaultBranchName, err
		}
		if r.MatchString(o) {
			headBranch = strings.TrimPrefix(o, "HEAD branch: ")
			break
		}
	}
	return headBranch, nil
}

// ErrNotOnAnyBranch indicates that the user is in detached HEAD state
var ErrNotOnAnyBranch = errors.New("you're not on any Git branch (a 'detached HEAD' state)")

// Ref represents a git commit reference
type Ref struct {
	Hash string
	Name string
}

// CurrentBranch reads the checked-out branch for the git repository
func CurrentBranch() (string, error) {
	refCmd := GitCommand("symbolic-ref", "--quiet", "--short", "HEAD")

	output, err := run.PrepareCmd(refCmd).Output()
	if err == nil {
		// Found the branch name
		return firstLine(output), nil
	}

	var cmdErr *run.CmdError
	if errors.As(err, &cmdErr) {
		if cmdErr.Stderr.Len() == 0 {
			// Detached head
			return "", ErrNotOnAnyBranch
		}
	}

	// Unknown error
	return "", err
}

func listRemotes() ([]string, error) {
	remoteCmd := exec.Command("git", "remote", "-v")
	output, err := run.PrepareCmd(remoteCmd).Output()
	return outputLines(output), err
}

func Config(name string) (string, error) {
	configCmd := exec.Command("git", "config", name)
	output, err := run.PrepareCmd(configCmd).Output()
	if err != nil {
		return "", fmt.Errorf("unknown config key: %s", name)
	}

	return firstLine(output), nil
}

var GitCommand = func(args ...string) *exec.Cmd {
	return exec.Command("git", args...)
}

func UncommittedChangeCount() (int, error) {
	statusCmd := GitCommand("status", "--porcelain")
	output, err := run.PrepareCmd(statusCmd).Output()
	if err != nil {
		return 0, err
	}
	lines := strings.Split(string(output), "\n")

	count := 0

	for _, l := range lines {
		if l != "" {
			count++
		}
	}

	return count, nil
}

func GitUserName() ([]byte, error) {
	nameGrab := GitCommand("config", "user.name")
	output, err := run.PrepareCmd(nameGrab).Output()
	if err != nil {
		return []byte{}, err
	}

	return output, nil
}

type Commit struct {
	Sha   string
	Title string
}

func Commits(baseRef, headRef string) ([]*Commit, error) {
	logCmd := GitCommand(
		"-c", "log.ShowSignature=false",
		"log", "--pretty=format:%H,%s",
		"--cherry", fmt.Sprintf("%s...%s", baseRef, headRef))
	output, err := run.PrepareCmd(logCmd).Output()
	if err != nil {
		return []*Commit{}, err
	}

	var commits []*Commit
	sha := 0
	title := 1
	for _, line := range outputLines(output) {
		split := strings.SplitN(line, ",", 2)
		if len(split) != 2 {
			continue
		}
		commits = append(commits, &Commit{
			Sha:   split[sha],
			Title: split[title],
		})
	}

	if len(commits) == 0 {
		return commits, fmt.Errorf("could not find any commits between %s and %s", baseRef, headRef)
	}

	return commits, nil
}

func CommitBody(sha string) (string, error) {
	showCmd := GitCommand("-c", "log.ShowSignature=false", "show", "-s", "--pretty=format:%b", sha)
	output, err := run.PrepareCmd(showCmd).Output()
	if err != nil {
		return "", err
	}
	return string(output), nil
}

// Push publishes a git ref to a remote
func Push(remote string, ref string, cmdOut, cmdErr io.Writer) error {
	pushCmd := GitCommand("push", remote, ref)
	pushCmd.Stdout = cmdOut
	pushCmd.Stderr = cmdErr
	return run.PrepareCmd(pushCmd).Run()
}

// SetUpstream sets the upstream (tracking) of a branch
func SetUpstream(remote string, branch string, cmdOut, cmdErr io.Writer) error {
	setCmd := GitCommand("branch", "--set-upstream-to", fmt.Sprintf("%s/%s", remote, branch))
	setCmd.Stdout = cmdOut
	setCmd.Stderr = cmdErr
	return run.PrepareCmd(setCmd).Run()
}

type BranchConfig struct {
	RemoteName string
	RemoteURL  *url.URL
	MergeRef   string
}

// ReadBranchConfig parses the `branch.BRANCH.(remote|merge)` part of git config
func ReadBranchConfig(branch string) BranchConfig {
	prefix := regexp.QuoteMeta(fmt.Sprintf("branch.%s.", branch))
	configCmd := GitCommand("config", "--get-regexp", fmt.Sprintf("^%s(remote|merge)$", prefix))
	output, err := run.PrepareCmd(configCmd).Output()
	if err != nil {
		return BranchConfig{}
	}
	cfg := BranchConfig{}
	for _, line := range outputLines(output) {
		parts := strings.SplitN(line, " ", 2)
		if len(parts) < 2 {
			continue
		}
		keys := strings.Split(parts[0], ".")
		switch keys[len(keys)-1] {
		case "remote":
			if strings.Contains(parts[1], ":") {
				u, err := ParseURL(parts[1])
				if err != nil {
					continue
				}
				cfg.RemoteURL = u
			} else if !isFilesystemPath(parts[1]) {
				cfg.RemoteName = parts[1]
			}
		case "merge":
			cfg.MergeRef = parts[1]
		}
	}
	return cfg
}

func DeleteLocalBranch(branch string, gr GitRunner) error {
	_, err := gr.Git("branch", "-D", branch)
	if err != nil {
		return fmt.Errorf("could not delete local branch: %w", err)
	}

	return nil
}

// ListLocalBranches returns the names of every local branch in the repository.
func ListLocalBranches(gr GitRunner) ([]string, error) {
	out, err := gr.Git("for-each-ref", "--format=%(refname:short)", "refs/heads/")
	if err != nil {
		return nil, fmt.Errorf("could not list local branches: %w", err)
	}

	var branches []string
	for line := range strings.SplitSeq(out, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			branches = append(branches, line)
		}
	}
	return branches, nil
}

// ListMergedBranches returns the names of local branches reachable from target
// (i.e. what `git branch --merged target` would print). This only catches
// fast-forward merges — squash and rebase merges look like distinct commits to
// git, so they are not reported as merged.
func ListMergedBranches(target string, gr GitRunner) ([]string, error) {
	out, err := gr.Git("branch", "--merged", target, "--format=%(refname:short)")
	if err != nil {
		return nil, fmt.Errorf("could not list merged branches: %w", err)
	}

	var branches []string
	for line := range strings.SplitSeq(out, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			branches = append(branches, line)
		}
	}
	return branches, nil
}

func CheckoutBranch(branch string, gr GitRunner) error {
	_, err := gr.Git("checkout", branch)
	if err != nil {
		return fmt.Errorf("could not checkout branch: %w", err)
	}

	return nil
}

func CheckoutNewBranch(branch string) error {
	configCmd := GitCommand("checkout", "-b", branch)
	err := run.PrepareCmd(configCmd).Run()
	return err
}

func RunClone(cloneURL string, target string, args []string) (string, error) {
	cloneArgs := append(args, cloneURL)

	// If the args contain an explicit target, pass it to clone
	//    otherwise, parse the URL to determine where git cloned it to so we can return it
	if target != "" {
		cloneArgs = append(cloneArgs, target)
	} else {
		target = cloneTargetDir(cloneURL)
	}

	cloneArgs = append([]string{"clone"}, cloneArgs...)

	cloneCmd := GitCommand(cloneArgs...)
	cloneCmd.Stdin = os.Stdin
	cloneCmd.Stdout = os.Stdout
	cloneCmd.Stderr = os.Stderr

	err := run.PrepareCmd(cloneCmd).Run()
	return target, err
}

// cloneTargetDir returns the directory git clones cloneURL into when no
// explicit target is given. Trailing slashes are trimmed before the ".git"
// suffix is stripped: a URL ending in ".git/" does not match the ".git" suffix,
// so it would otherwise resolve to "repo.git" while git clones into "repo".
func cloneTargetDir(cloneURL string) string {
	return path.Base(StripDotGit(cloneURL))
}

// StripDotGit normalizes a Git URL path by removing trailing slashes and a
// ".git" suffix.
func StripDotGit(value string) string {
	return strings.TrimSuffix(strings.TrimRight(value, "/"), ".git")
}

func AddUpstreamRemote(upstreamURL, cloneDir string) error {
	cloneCmd := GitCommand("-C", cloneDir, "remote", "add", "-f", "upstream", upstreamURL)
	cloneCmd.Stdout = os.Stdout
	cloneCmd.Stderr = os.Stderr
	return run.PrepareCmd(cloneCmd).Run()
}

func isFilesystemPath(p string) bool {
	return p == "." || strings.HasPrefix(p, "./") || strings.HasPrefix(p, "/")
}

// ToplevelDir returns the top-level directory path of the current repository
var ToplevelDir = func() (string, error) {
	showCmd := exec.Command("git", "rev-parse", "--show-toplevel")
	output, err := run.PrepareCmd(showCmd).Output()
	return firstLine(output), err
}

// GitDir returns the path to the .git directory (or the worktree-specific git dir).
// In a normal repo this returns "<toplevel>/.git".
// In a worktree this returns something like "<main-repo>/.git/worktrees/<name>".
var GitDir = func() (string, error) {
	cmd := exec.Command("git", "rev-parse", "--git-dir")
	output, err := run.PrepareCmd(cmd).Output()
	if err != nil {
		return "", err
	}
	return firstLine(output), nil
}

// GitCommonDir returns the path to the shared git directory for the repository.
// In a normal repo this returns "<toplevel>/.git".
// In a worktree this returns "<main-repo>/.git" (the common dir shared by all worktrees).
func GitCommonDir() (string, error) {
	cmd := exec.Command("git", "rev-parse", "--git-common-dir")
	output, err := run.PrepareCmd(cmd).Output()
	if err != nil {
		return "", err
	}
	return firstLine(output), nil
}

func outputLines(output []byte) []string {
	lines := strings.ReplaceAll(string(output), "\r\n", "\n")
	lines = strings.TrimSuffix(lines, "\n")
	return strings.Split(lines, "\n")
}

func firstLine(output []byte) string {
	if i := bytes.IndexAny(output, "\n"); i >= 0 {
		return string(output)[0:i]
	}
	return string(output)
}

var remoteRE = regexp.MustCompile(`(.+)\s+(.+)\s+\((push|fetch)\)`)

// RemoteSet is a slice of git remotes
type RemoteSet []*Remote

func NewRemote(name string, u string) *Remote {
	pu, _ := url.Parse(u)
	return &Remote{
		Name:     name,
		FetchURL: pu,
		PushURL:  pu,
	}
}

// Remote is a parsed git remote
type Remote struct {
	Name         string
	Resolved     string // Deprecated: Use ResolvedBase or ResolvedHead
	ResolvedBase string
	ResolvedHead string
	FetchURL     *url.URL
	PushURL      *url.URL
}

func (r *Remote) String() string {
	return r.Name
}

// Remotes gets the git remotes set for the current repo
func Remotes() (RemoteSet, error) {
	list, err := listRemotes()
	if err != nil {
		return nil, err
	}
	remotes := parseRemotes(list)

	// this is affected by SetRemoteResolution
	// Read from both old and new config keys for backward compatibility
	remoteCmd := exec.Command("git", "config", "--get-regexp", `^remote\..*\.glab-resolved(-base|-head)?$`)
	output, _ := run.PrepareCmd(remoteCmd).Output()
	for _, l := range outputLines(output) {
		parts := strings.SplitN(l, " ", 2)
		if len(parts) < 2 {
			continue
		}
		rp := strings.SplitN(parts[0], ".", 4)
		if len(rp) < 3 {
			continue
		}
		name := rp[1]
		configKey := rp[2]
		resolution := parts[1]

		for _, r := range remotes {
			if r.Name == name {
				// Store in appropriate field based on config key
				switch configKey {
				case "glab-resolved-base":
					r.ResolvedBase = resolution
				case "glab-resolved-head":
					r.ResolvedHead = resolution
				case "glab-resolved":
					// Legacy config key - populate Resolved for backward compatibility
					r.Resolved = resolution
				}
				break
			}
		}
	}

	return remotes, nil
}

func parseRemotes(gitRemotes []string) RemoteSet {
	remotes := RemoteSet{}
	for _, r := range gitRemotes {
		match := remoteRE.FindStringSubmatch(r)
		if match == nil {
			continue
		}
		name := strings.TrimSpace(match[1])
		urlStr := strings.TrimSpace(match[2])
		urlType := strings.TrimSpace(match[3])

		var rem *Remote
		if len(remotes) > 0 {
			rem = remotes[len(remotes)-1]
			if name != rem.Name {
				rem = nil
			}
		}
		if rem == nil {
			rem = &Remote{Name: name}
			remotes = append(remotes, rem)
		}

		u, err := ParseURL(urlStr)
		if err != nil {
			continue
		}

		switch urlType {
		case "fetch":
			rem.FetchURL = u
		case "push":
			rem.PushURL = u
		}
	}
	return remotes
}

// AddRemote adds a new git remote and auto-fetches objects from it
func AddRemote(name, u string) (*Remote, error) {
	addCmd := exec.Command("git", "remote", "add", "-f", name, u)
	addCmd.Stderr = os.Stderr
	err := run.PrepareCmd(addCmd).Run()
	if err != nil {
		return nil, err
	}

	var urlParsed *url.URL
	if strings.HasPrefix(u, "https") {
		urlParsed, err = url.Parse(u)
		if err != nil {
			return nil, err
		}

	} else {
		urlParsed, err = ParseURL(u)
		if err != nil {
			return nil, err
		}

	}

	return &Remote{
		Name:     name,
		FetchURL: urlParsed,
		PushURL:  urlParsed,
	}, nil
}

var SetRemoteResolution = func(name, resolution string) error {
	// Use separate config keys for base and head resolutions to avoid conflicts
	// when both are stored for the same remote
	configKey := "glab-resolved"
	if resolution == "base" || strings.HasPrefix(resolution, "base:") {
		configKey = "glab-resolved-base"
	} else if resolution == "head" || strings.HasPrefix(resolution, "head:") {
		configKey = "glab-resolved-head"
	}
	return SetRemoteConfig(name, configKey, resolution)
}

func SetRemoteConfig(remote, key, value string) error {
	return SetConfig(fmt.Sprintf("remote.%s.%s", remote, key), value)
}

func SetConfig(key, value string) error {
	found, err := configValueExists(key, value)
	if err != nil {
		return err
	}
	if found {
		return nil
	}
	addCmd := GitCommand("config", "--add", key, value)
	_, err = run.PrepareCmd(addCmd).Output()
	if err != nil {
		return fmt.Errorf("setting git config: %w", err)
	}
	return nil
}

func configValueExists(key, value string) (bool, error) {
	output, err := GetAllConfig(key)
	if err == nil {
		return outputContainsLine(output, value), nil
	}
	return false, err
}

// GetConfig returns the local config value associated with the provided key.
// If there are multiple values associated with the key, they are all returned.
func GetAllConfig(key string) ([]byte, error) {
	err := assertValidConfigKey(key)
	if err != nil {
		return nil, err
	}

	gitCmd := GitCommand("config", "--get-all", key)
	output, err := run.PrepareCmd(gitCmd).Output()
	if err == nil {
		return output, nil
	}

	// git-config will exit with 1 in almost all cases, but only when it prints
	// out things it is an actual error that is worth mentioning.
	// Therefore ignore errors that don't output to stderr.
	var cmdErr *run.CmdError
	if errors.As(err, &cmdErr) && cmdErr.Stderr.Len() == 0 {
		return nil, nil
	}
	return nil, fmt.Errorf("getting Git configuration value cmd: %s: %w", gitCmd.String(), err)
}

func assertValidConfigKey(key string) error {
	s := strings.Split(key, ".")
	if len(s) < 2 {
		return fmt.Errorf("incorrect Git configuration key")
	}
	return nil
}

// outputContainsLine searches through each line in the command output
// and returns true if one matches the needle a.k.a. the search string.
func outputContainsLine(output []byte, needle string) bool {
	return slices.Contains(outputLines(output), needle)
}

func RunCmd(args []string) error {
	gitCmd := GitCommand(args...)
	gitCmd.Stdout = os.Stdout
	gitCmd.Stderr = os.Stderr

	return run.PrepareCmd(gitCmd).Run()
}

// DescribeByTags gives a description of the current object.
// Non-annotated tags are considered.
// Reference: https://git-scm.com/docs/git-describe
func DescribeByTags() (string, error) {
	gitCmd := GitCommand("describe", "--tags")

	output, err := run.PrepareCmd(gitCmd).Output()
	if err != nil {
		return "", fmt.Errorf("running cmd: %s out: %s: %w", gitCmd.String(), output, err)
	}

	return firstLine(output), nil
}

// ListTags gives a slice of tags from the current repository.
func ListTags() ([]string, error) {
	gitCmd := GitCommand("tag", "-l")

	output, err := run.PrepareCmd(gitCmd).Output()
	if err != nil {
		return nil, fmt.Errorf("running cmd: %s out: %s: %w", gitCmd.String(), output, err)
	}

	tagsStr := string(output)
	if tagsStr == "" {
		return nil, nil
	}

	return strings.Fields(tagsStr), nil
}
