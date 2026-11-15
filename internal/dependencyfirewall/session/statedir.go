package session

import (
	"os"
)

// StateDirEnv names the directory holding the proxy's runtime state (address,
// pid, CA). `job-proxy start` exports it so `job-proxy stop` finds the same
// daemon, and a caller can set it to pin a known location.
const StateDirEnv = "GLAB_DF_JOB_PROXY_STATE_DIR"

// NewStateDir returns the runtime-state directory for a new proxy: the one
// named by StateDirEnv, or else a fresh directory only the current user can
// read. A shared fixed path would let concurrent jobs on one host clobber each
// other's CA and pid, and let another user plant files the job then trusts.
func NewStateDir() (string, error) {
	if dir := os.Getenv(StateDirEnv); dir != "" {
		return dir, os.MkdirAll(dir, 0o700)
	}
	return os.MkdirTemp("", "glab-df-job-proxy-")
}

// LogBaseDir returns the directory the job-level proxy writes its CI log
// under: CI_PROJECT_DIR in a CI/CD job, otherwise the working directory.
func LogBaseDir() (string, error) {
	if dir := os.Getenv("CI_PROJECT_DIR"); dir != "" {
		return dir, nil
	}
	return os.Getwd()
}
