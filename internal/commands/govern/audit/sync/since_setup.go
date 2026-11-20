package sync

import (
	"context"
	"fmt"
	"slices"
	"time"

	"gitlab.com/gitlab-org/cli/internal/commands/govern/internal/fallbacksync"
	"gitlab.com/gitlab-org/cli/internal/commands/govern/internal/syncstate"
)

// uploadsFrom returns the time from which agentType's activity is uploaded,
// or the zero time when there is no such limit.
func uploadsFrom(agentType string) (time.Time, error) {
	setups, err := fallbacksync.ReadAgentSetups()
	if err != nil {
		return time.Time{}, fmt.Errorf("could not tell which %s activity to upload: %w", agentType, err)
	}
	return setups.UploadsFrom(agentType), nil
}

// pending is what a sync reads from a session's transcript.
type pending struct {
	data *sessionData
	// next is the position to save once data is uploaded.
	next int64
	// uploaded reports whether glab has uploaded any of the session. A
	// session without uploads is not completed, which would create it on
	// GitLab empty.
	uploaded bool
}

// readPending reads what src recorded after the session's sync position,
// leaving out activity from before since, the time the agent was set up.
func readPending(ctx context.Context, sessionID string, src transcript, since time.Time) (pending, error) {
	position, err := syncstate.ReadPosition(sessionID)
	if err != nil {
		return pending{}, err
	}
	position, err = skipBeforeSetup(ctx, sessionID, src, position, since)
	if err != nil {
		return pending{}, err
	}
	data, next, err := src.read(ctx, position)
	if err != nil {
		return pending{}, err
	}
	data.dropBefore(since)
	uploaded, err := syncstate.HasUploads(sessionID, position)
	if err != nil {
		return pending{}, err
	}
	if !uploaded {
		data.dropUnmatchedResults()
		// The readers take the goal only from the start of a transcript, so a
		// session whose syncing starts later, such as one resumed from before
		// setup, takes it from its first prompt synced.
		if data.Goal == "" && len(data.Prompts) > 0 {
			data.Goal = goalFromText(data.Prompts[0].Text)
		}
	}
	return pending{data: data, next: next, uploaded: uploaded}, nil
}

// skipBeforeSetup moves the sync position of a session glab has not read yet,
// and whose transcript was last active before since, the time the agent was
// set up, to the end of the transcript. It returns the position to read from.
// Reading such a transcript again on every run would find nothing to upload,
// and a session the user resumes later uploads only its new turns.
func skipBeforeSetup(ctx context.Context, sessionID string, src transcript, position int64, since time.Time) (int64, error) {
	if position != 0 || since.IsZero() {
		return position, nil
	}
	last, err := src.lastActivity(ctx)
	if err != nil || !last.Before(since) {
		return position, err
	}
	if err := skipToEnd(ctx, sessionID, src); err != nil {
		return position, err
	}
	return syncstate.ReadPosition(sessionID)
}

// dropBefore leaves out what the agent recorded before since, unless since is
// zero, including the results of calls made before since. A goal or start
// time from before since is left out too.
func (d *sessionData) dropBefore(since time.Time) {
	if since.IsZero() {
		return
	}
	prompts := len(d.Prompts)
	d.Prompts = slices.DeleteFunc(d.Prompts, func(p prompt) bool { return p.Timestamp.Before(since) })
	droppedCalls := map[string]struct{}{}
	d.ToolCalls = slices.DeleteFunc(d.ToolCalls, func(c toolCall) bool {
		if c.Timestamp.Before(since) {
			droppedCalls[c.ID] = struct{}{}
			return true
		}
		return false
	})
	d.ToolResults = slices.DeleteFunc(d.ToolResults, func(r toolResult) bool {
		_, dropped := droppedCalls[r.CallID]
		return dropped || r.Timestamp.Before(since)
	})
	d.Responses = slices.DeleteFunc(d.Responses, func(r llmResponse) bool { return r.Timestamp.Before(since) })

	startedBefore := !d.StartedAt.IsZero() && d.StartedAt.Before(since)
	if startedBefore || len(d.Prompts) < prompts {
		d.Goal = ""
	}
	if startedBefore {
		d.StartedAt = time.Time{}
	}
}

// dropUnmatchedResults leaves out the results of calls that data does not
// hold. A session with nothing uploaded has synced none of its calls, so
// such a call was skipped, for example because it started before setup.
func (d *sessionData) dropUnmatchedResults() {
	calls := make(map[string]struct{}, len(d.ToolCalls))
	for _, c := range d.ToolCalls {
		calls[c.ID] = struct{}{}
	}
	d.ToolResults = slices.DeleteFunc(d.ToolResults, func(r toolResult) bool {
		_, ok := calls[r.CallID]
		return !ok
	})
}
