package sync

import (
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"gitlab.com/gitlab-org/cli/internal/dbg"
)

// The hook passes the session to the glab it starts through these variables,
// the way the OpenCode plugin passes OPENCODE_SESSION_ID.
const (
	hookAgentEnv      = "GLAB_MINDER_HOOK_AGENT"
	hookSessionEnv    = "GLAB_MINDER_HOOK_SESSION_ID"
	hookTranscriptEnv = "GLAB_MINDER_HOOK_TRANSCRIPT"
)

// maxHookInputBytes bounds what a hook reads from stdin. Codex also sends the
// last response, so the input can be large.
const maxHookInputBytes = 16 << 20

// hookAgents are the agents whose hooks pass the session on stdin.
var hookAgents = []string{"codex"}

// hookInput is the JSON a Codex Stop hook gets on stdin.
type hookInput struct {
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path"`
	CWD            string `json:"cwd"`
}

// hookTarget returns the session ID, transcript and directory of the session
// that the hook input in names.
func hookTarget(in hookInput) (string, string, string) {
	dir := in.CWD
	if dir == "" {
		// The Codex hook runs in the session's directory.
		dir = "."
	}
	return in.SessionID, in.TranscriptPath, dir
}

// hookSession returns the session and transcript that a hook for agent passed
// to this glab, or empty strings when it was not started by one.
func hookSession(agent string) (string, string) {
	if os.Getenv(hookAgentEnv) != agent {
		return "", ""
	}
	return os.Getenv(hookSessionEnv), os.Getenv(hookTranscriptEnv)
}

// runHook reads the session from the hook's stdin, and syncs it in a glab
// started in the background, so the agent doesn't wait for the upload and an
// agent that exits doesn't stop it. A shell & can't do this, because a command
// it starts gets an empty stdin. runHook prints nothing and never fails,
// because the agent reads a Stop hook's output as a reason to keep going.
func runHook(opts *options) error {
	var in hookInput
	if err := json.NewDecoder(io.LimitReader(opts.io.In, maxHookInputBytes)).Decode(&in); err != nil {
		dbg.Debugf("could not read the %s hook input: %v", opts.hook, err)
		return nil
	}
	// --all skips the sessions of an agent that is not enabled, so its hook
	// does not upload them either.
	if ag := agentByName(supportedAgents(opts.executor), opts.hook); ag != nil {
		reason, err := ag.pausedReason()
		if err != nil || reason != "" {
			if err != nil {
				reason = err.Error()
			}
			dbg.Debugf("not syncing the %s session: %s", opts.hook, reason)
			return nil
		}
	}
	sessionID, transcriptPath, dir := hookTarget(in)
	if sessionID == "" || transcriptPath == "" {
		dbg.Debugf("the %s hook input has no session or transcript", opts.hook)
		return nil
	}
	// The glab started below runs in another directory, and records the path
	// for --all to reopen.
	transcriptPath, err := absolutePath(transcriptPath)
	if err != nil {
		dbg.Debugf("could not resolve the %s transcript path: %v", opts.hook, err)
		return nil
	}

	env := slices.DeleteFunc(os.Environ(), func(kv string) bool {
		// A Claude Code or OpenCode session that started the agent must not
		// be synced in its place.
		return strings.HasPrefix(kv, "CLAUDE_CODE_SESSION_ID=") || strings.HasPrefix(kv, "OPENCODE_SESSION_ID=")
	})
	env = append(env,
		hookAgentEnv+"="+opts.hook,
		hookSessionEnv+"="+sessionID,
		hookTranscriptEnv+"="+transcriptPath,
	)
	start := opts.startDetached
	if start == nil {
		start = startDetached
	}
	if err := start(dir, env, "minder", "audit", "sync", "--silent"); err != nil {
		dbg.Debugf("could not start glab for the %s hook: %v", opts.hook, err)
	}
	return nil
}

// absolutePath returns path with a leading ~ expanded, relative to the current
// directory.
func absolutePath(path string) (string, error) {
	if rest, ok := strings.CutPrefix(path, "~"+string(filepath.Separator)); ok || path == "~" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		path = filepath.Join(home, rest)
	}
	return filepath.Abs(path)
}

// startDetached starts this glab with args in dir, in its own session with
// no standard streams, and doesn't wait for it.
func startDetached(dir string, env []string, args ...string) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(self, args...)
	cmd.Dir = dir
	cmd.Env = env
	cmd.SysProcAttr = detachedProcAttr()
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
