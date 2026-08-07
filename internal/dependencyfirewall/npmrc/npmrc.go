package npmrc

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/config"
	"gitlab.com/gitlab-org/cli/internal/fsx"
)

const backupName = ".gitlab.npmrc.backup"

type Handle struct {
	baseDir     string
	hadOriginal bool
}

func npmrcPath(baseDir string) string  { return filepath.Join(baseDir, ".npmrc") }
func backupPath(baseDir string) string { return filepath.Join(baseDir, backupName) }

// The .npmrc and its backup can embed an inline `_authToken`, so both are
// written 0o600 (owner read/write only) to keep the token off shared hosts
// and multi-tenant CI runners. This matches npm, which chmods its own
// token-bearing user config to 0o600.
func Apply(baseDir string, s config.Settings) (*Handle, error) {
	h := &Handle{baseDir: baseDir}

	switch _, err := os.Stat(backupPath(baseDir)); {
	case err == nil:
		return nil, fmt.Errorf("a previous .npmrc backup already exists at %s; restore or remove it before running again", backupPath(baseDir))
	case os.IsNotExist(err):
	default:
		return nil, err
	}

	original, err := os.ReadFile(npmrcPath(baseDir))
	switch {
	case err == nil:
		h.hadOriginal = true
		if err := fsx.WriteOwnerOnly(backupPath(baseDir), original); err != nil {
			return nil, err
		}
	case os.IsNotExist(err):
		h.hadOriginal = false
	default:
		return nil, err
	}

	body := rewrite(string(original), s)
	if err := fsx.WriteOwnerOnly(npmrcPath(baseDir), []byte(body)); err != nil {
		return h, err
	}
	return h, nil
}

func rewrite(original string, s config.Settings) string {
	var b strings.Builder
	for line := range strings.SplitSeq(original, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "registry=") {
			continue
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	b.WriteString(fmt.Sprintf("registry=%s\n", s.RegistryURL))
	if s.AuthToken != "" {
		b.WriteString(fmt.Sprintf("//%s:_authToken=%s\n", s.AuthHost, s.AuthToken))
	}
	return b.String()
}

func (h *Handle) Restore() error {
	if h == nil {
		return nil
	}
	if h.hadOriginal {
		original, err := os.ReadFile(backupPath(h.baseDir))
		if err != nil {
			return err
		}
		if err := fsx.WriteOwnerOnly(npmrcPath(h.baseDir), original); err != nil {
			return err
		}
		return os.Remove(backupPath(h.baseDir))
	}
	if err := os.Remove(npmrcPath(h.baseDir)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
