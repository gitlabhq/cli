package sync

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"time"

	"gitlab.com/gitlab-org/cli/internal/commands/govern/internal/fallbacksync"
	"gitlab.com/gitlab-org/cli/internal/commands/govern/internal/gaig"
	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/dbg"
)

// discoveredSession is a session found by scanning an agent's transcripts
// rather than recorded by a hook.
type discoveredSession struct {
	id      string
	locator string
	// remote is the Git remote URL of the repository the session ran in, or
	// empty when it is unknown.
	remote string
	// skip explains why the session must not be synced, for example because
	// its repository cannot be identified with certainty.
	skip string
}

// discoverSessions records metadata for sessions of the enabled agents that
// no hook or earlier run has recorded, and returns their IDs. A session is
// skipped when its repository is not on a GitLab host glab is logged in to.
func discoverSessions(ctx context.Context, agents []agent, known map[string]struct{}, cfg config.Config, recordErr func(error), recordSkip func(string)) []string {
	enabled, err := fallbacksync.DiscoveredAgents()
	if err != nil {
		recordErr(err)
		return nil
	}
	if len(enabled) == 0 {
		return nil
	}
	hosts, err := cfg.Hosts()
	if err != nil {
		recordErr(fmt.Errorf("could not read configured GitLab hosts: %w", err))
		return nil
	}
	isKnown := func(id string) bool {
		_, ok := known[id]
		return ok
	}

	var found []string
	for _, a := range agents {
		if !slices.Contains(enabled, a.name()) {
			continue
		}
		sessions, err := a.discover(ctx, isKnown)
		if err != nil {
			recordErr(fmt.Errorf("%s discovery: %w", a.name(), err))
			continue
		}
		for _, s := range sessions {
			if s.skip != "" {
				recordSkip(s.skip)
				continue
			}
			host, path, err := gaig.ProjectForRemote(s.remote, hosts, cfg)
			if err != nil {
				dbg.Debugf("not syncing %s session %s: %v", a.name(), s.id, err)
				// Discovery finds such a session on every run, so it is only
				// reported while active, like one a hook recorded.
				if info, statErr := os.Stat(s.locator); statErr == nil && time.Since(info.ModTime()) <= idleCompleteAfter {
					recordSkip(withoutRemoteReason(a.name()))
				}
				continue
			}
			meta := sessionMeta{
				AgentType:         a.name(),
				PathWithNamespace: path,
				Host:              host,
				Transcript:        s.locator,
			}
			err = withSessionMetaLock(func() error {
				// A hook may have recorded the session since discovery listed it.
				if _, err := readSessionMeta(s.id); !errors.Is(err, errNoSessionMeta) {
					return err
				}
				return writeSessionMeta(s.id, meta)
			})
			if err != nil {
				recordErr(fmt.Errorf("session %s: %w", s.id, err))
				continue
			}
			found = append(found, s.id)
		}
	}
	return found
}
