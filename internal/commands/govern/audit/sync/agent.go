package sync

import (
	"time"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
)

// agent is an AI coding agent whose sessions can be synced. Supporting a new
// agent means implementing agent and adding it to supportedAgents.
type agent interface {
	// name is the agent type reported to GitLab and recorded in session metadata.
	name() string
	// sessionID returns the ID of the session the hook is running in, or an
	// empty string when the hook is not running in this agent.
	sessionID() string
	// current opens the transcript of the session the hook is running in. It
	// also returns a locator that recorded can later use to reopen the
	// transcript without the agent's environment.
	current(sessionID string) (transcript, string, error)
	// recorded reopens a transcript from its recorded locator, and returns
	// the locator to record from now on if the transcript has moved.
	recorded(sessionID, locator string) (transcript, string, error)
	// canForget reports whether the local state of a session completed at
	// completedAt can be removed, because the agent can no longer resume it.
	// It must be cheap, because --all calls it for every completed session.
	canForget(src transcript, completedAt time.Time) (bool, error)
	// pausedReason explains why the user has paused syncing for this agent,
	// or returns an empty string when syncing is not paused.
	pausedReason() (string, error)
}

func supportedAgents(executor cmdutils.Executor) []agent {
	return []agent{claudeCode{}, openCode{executor: executor}}
}

// detectAgent returns the agent the hook is running in and its session ID.
func detectAgent(agents []agent) (agent, string) {
	for _, a := range agents {
		if id := a.sessionID(); id != "" {
			return a, id
		}
	}
	return nil, ""
}

func agentByName(agents []agent, name string) agent {
	for _, a := range agents {
		if a.name() == name {
			return a
		}
	}
	return nil
}
