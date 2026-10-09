package sync

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/google/uuid"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/commands/govern/internal/fallbacksync"
	"gitlab.com/gitlab-org/cli/internal/commands/govern/internal/gaig"
	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
	"gitlab.com/gitlab-org/cli/internal/text"
)

type options struct {
	io        *iostreams.IOStreams
	apiClient func(repoHost string) (*api.Client, error)
	baseRepo  func() (glrepo.Interface, error)
	executor  cmdutils.Executor
	config    func() config.Config
	silent    bool
	complete  bool
	agentType string
	all       bool
}

const (
	// idleCompleteAfter is how long a session must be inactive before
	// `--all` marks it completed on behalf of a SessionEnd hook that never ran.
	idleCompleteAfter = 24 * time.Hour
	// forgetUnfoundAfter is how long --all keeps reporting a session the
	// agent says it does not have, and keeps a completed session it cannot
	// check, before forgetting it.
	forgetUnfoundAfter = 30 * 24 * time.Hour
	maxStatusErrors    = 20
)

func NewCmd(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		io:        f.IO(),
		apiClient: f.ApiClient,
		baseRepo:  f.BaseRepo,
		executor:  f.Executor(),
		config:    f.Config,
	}

	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Sync agent session data to GitLab. (EXPERIMENTAL)",
		Long: heredoc.Docf(`
			Read new entries from the local agent transcript since the last sync and POST them to GitLab as audit events.

			The audit events contain the prompts you type, each tool call with its arguments (such as commands, file paths, and edits, replaced with a marker when over 8 KB), each tool call's outcome, duration, and error message, and the model and token usage of each response. Tool output and the text of responses are not sent. A session that has never synced and has had no activity in the last 89 days is not uploaded, because GitLab does not accept events that old.

			Some agents record less:

			- OpenCode: prompts and responses are not sent, and a tool call's outcome is sent only if the call had finished when it was first synced.
			- Codex: a tool call's outcome is "completed", because Codex does not record whether it succeeded.
			- Cursor: only prompts and tool calls are sent, timed by the transcript's last modification, because Cursor records no results, token usage, or timestamps. A Cursor session is not synced if the files its tool calls read or write are outside the workspace glab finds, or if several directories match the workspace's name and those files don't show which, so that it is not uploaded to the wrong project.

			If a GitLab instance does not accept an agent's sessions yet, they are not uploaded, then or after the instance is upgraded. Sessions from after the upgrade are.

			Called by the Stop hook after every agent turn. Also used by the SessionEnd hook (with %[1]s--complete%[1]s) to mark the session as complete. The hooks also record the session's project, host, and transcript location so that %[1]s--all%[1]s can sync it later.

			Project is resolved from the Git remote of the current directory, or overridden with %[1]s-R/--repo%[1]s.

			With %[1]s--all%[1]s, syncs every session the hooks have recorded, each to the project and host it was recorded with. Sessions inactive for 24 hours are marked completed. Claude Code sessions are skipped while the glab Stop hook is missing from %[1]s~/.claude/settings.json%[1]s, so removing the hooks pauses them. The fallback periodic sync installed by %[1]sglab govern setup%[1]s runs this, and %[1]sglab govern doctor%[1]s shows the result of its last run.

			Supports Claude Code and OpenCode sessions recorded by hooks. OpenCode sessions are read with %[1]sopencode export%[1]s. With %[1]s--all%[1]s, it also finds and syncs the sessions of agents enabled with %[1]sglab govern setup --agents%[1]s (Codex and Cursor) by scanning their transcripts, for repositories on GitLab hosts glab is logged in to.
		`, "`") + text.ExperimentalString,
		Example: heredoc.Doc(`
			# Sync the current agent session to GitLab
			$ glab govern audit sync

			# Sync and mark the session as completed
			$ glab govern audit sync --complete

			# Sync against a specific project
			$ glab govern audit sync -R my-group/my-project

			# Sync every recorded session that has unsynced activity
			$ glab govern audit sync --all
		`),
		Args:              cobra.NoArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
		Annotations: map[string]string{
			mcpannotations.Destructive: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSync(cmd.Context(), opts)
		},
	}

	fl := cmd.Flags()
	fl.BoolVar(&opts.silent, "silent", false, "Suppress all output. Used when invoked from hooks.")
	fl.BoolVar(&opts.complete, "complete", false, "Mark the session as completed. Used by the SessionEnd hook.")
	fl.BoolVar(&opts.all, "all", false, "Sync all sessions recorded by the hooks. Used by the fallback periodic sync.")

	cmdutils.EnableRepoOverride(cmd, f)
	cmd.MarkFlagsMutuallyExclusive("all", "repo")
	cmd.MarkFlagsMutuallyExclusive("all", "complete")

	return cmd
}

func runSync(ctx context.Context, opts *options) error {
	if opts.all {
		return syncAllSessions(ctx, opts)
	}

	ag, sessionID := detectAgent(supportedAgents(opts.executor))

	var src transcript
	if ag != nil {
		opts.agentType = ag.name()
		var locator string
		var err error
		src, locator, err = ag.current(sessionID)
		if err != nil {
			return err
		}
		// Written before ResolveProject so a failed project lookup still
		// leaves enough context for --all to retry this session later.
		if err := recordSessionMeta(opts, sessionID, locator); err != nil && !opts.silent {
			opts.io.LogErrorf("warning: could not record session metadata: %v\n", err)
		}
	}

	project, client, err := gaig.ResolveProject(opts.baseRepo, opts.apiClient)
	if err != nil {
		if !opts.silent {
			opts.io.LogErrorf("error: could not resolve project: %v\n", err)
		}
		return err
	}

	if !opts.silent {
		opts.io.LogInfof("Project: %s (ID: %d)\n", project.PathWithNamespace, project.ID)
	}

	t := target{client: client, project: project}
	if opts.agentType != "" {
		identity, err := gaig.RegisterIdentity(client, project.ID, opts.agentType)
		if err != nil && !opts.silent {
			opts.io.LogErrorf("warning: %v\n", err)
		}
		if identity != nil {
			t.identityID = identity.ID
			if !opts.silent {
				opts.io.LogInfof("Agent identity: %d\n", identity.ID)
			}
		}
	}

	if sessionID == "" {
		if !opts.silent {
			opts.io.LogInfo("No active agent session detected.")
		}
		return nil
	}

	return syncSession(ctx, t, sessionID, src, opts)
}

func recordSessionMeta(opts *options, sessionID, locator string) error {
	if opts.baseRepo == nil {
		return nil
	}
	repo, err := opts.baseRepo()
	if err != nil {
		return err
	}
	return writeSessionMeta(sessionID, sessionMeta{
		AgentType:         opts.agentType,
		PathWithNamespace: repo.FullName(),
		Host:              repo.RepoHost(),
		Transcript:        locator,
	})
}

// target is the project a session is synced to.
type target struct {
	client     *api.Client
	project    *gitlab.Project
	identityID int
}

// syncSession syncs a single session transcript to GitLab.
func syncSession(ctx context.Context, t target, sessionID string, src transcript, opts *options) error {
	offset, err := readCursor(sessionID)
	if err != nil && !opts.silent {
		opts.io.LogErrorf("warning: could not read cursor: %v\n", err)
	}

	data, newOffset, err := src.read(ctx, offset)
	if errors.Is(err, errTranscriptNotFound) {
		if !opts.silent {
			opts.io.LogInfof("No transcript found at %s\n", src)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("could not parse transcript: %w", err)
	}

	if data.empty() && !opts.complete {
		if !opts.silent {
			opts.io.LogInfo("No new entries to sync.")
		}
		return nil
	}

	if t.client == nil || t.project == nil {
		return fmt.Errorf("client and project are required")
	}

	if opts.agentType == "" {
		if !opts.silent {
			opts.io.LogInfo("No agent type detected -- skipping session sync.")
		}
		return nil
	}

	if _, err := pushSession(ctx, t, sessionID, opts.agentType, src, data, newOffset, opts.complete, opts); err != nil && !opts.silent {
		opts.io.LogErrorf("warning: %v\n", err)
	}
	return nil
}

type pushResult struct {
	eventsPosted int
	completed    bool
}

// pushSession uploads data to GitLab, advances the cursor to newCursor once
// the events are posted, and marks the session completed if requested.
func pushSession(ctx context.Context, t target, sessionID, agentType string, src transcript, data *sessionData, newCursor int64, complete bool, opts *options) (pushResult, error) {
	var res pushResult

	glSessionID, err := ensureSession(ctx, t.client, t.project.ID, t.identityID, sessionID, agentType, data)
	if err != nil {
		return res, err
	}
	if glSessionID <= 0 {
		return res, errors.New("could not create session: GitLab returned no session ID")
	}
	if !opts.silent {
		opts.io.LogInfof("Session: %d\n", glSessionID)
	}

	if events := auditEvents(agentType, data, time.Now()); len(events) > 0 {
		if err := postAuditEvents(ctx, t.client, t.project.ID, glSessionID, events); err != nil {
			return res, fmt.Errorf("could not post audit events: %w", err)
		}
		res.eventsPosted = len(events)
		if !opts.silent {
			opts.io.LogInfof("Posted %d audit events\n", len(events))
		}
	}

	if err := writeCursor(sessionID, newCursor); err != nil && !opts.silent {
		opts.io.LogErrorf("warning: could not update cursor: %v\n", err)
	}

	if !complete {
		return res, nil
	}

	sum, err := src.checksum(ctx)
	if err != nil && !opts.silent {
		opts.io.LogErrorf("warning: could not compute transcript checksum: %v\n", err)
	}
	if err := completeSession(ctx, t.client, t.project.ID, glSessionID, sum); err != nil {
		return res, fmt.Errorf("could not complete session: %w", err)
	}
	res.completed = true
	if !opts.silent {
		opts.io.LogInfo("Session marked as completed.")
	}
	if err := markSessionCompleted(sessionID, time.Now()); err != nil && !opts.silent {
		opts.io.LogErrorf("warning: could not record session completion: %v\n", err)
	}
	return res, nil
}

func syncAllSessions(ctx context.Context, opts *options) error {
	now := time.Now()
	status := fallbacksync.Status{StartedAt: now}
	defer func() {
		status.FinishedAt = time.Now()
		if err := fallbacksync.WriteStatus(status); err != nil && !opts.silent {
			opts.io.LogErrorf("warning: could not record sync status: %v\n", err)
		}
	}()
	recordRunErr := func(err error) {
		if !opts.silent {
			opts.io.LogErrorf("warning: %v\n", err)
		}
		if len(status.Errors) < maxStatusErrors {
			status.Errors = append(status.Errors, err.Error())
		}
	}
	recordErr := func(sessionID string, err error) {
		recordRunErr(fmt.Errorf("session %s: %w", sessionID, err))
	}

	sessionIDs, err := listSessionMetas()
	if err != nil {
		status.Errors = append(status.Errors, err.Error())
		return err
	}

	agents := supportedAgents(opts.executor)
	known := make(map[string]struct{}, len(sessionIDs))
	for _, id := range sessionIDs {
		known[id] = struct{}{}
	}
	recordSkip := func(reason string) {
		if !slices.Contains(status.Skipped, reason) {
			status.Skipped = append(status.Skipped, reason)
		}
		if !opts.silent {
			opts.io.LogInfof("Not syncing: %s.\n", reason)
		}
	}
	sessionIDs = append(sessionIDs, discoverSessions(ctx, agents, known, opts.config(), recordRunErr, recordSkip)...)
	// paused caches each agent's pause check for the run. A failed check
	// pauses the agent, because uploading is not safe when it is unclear
	// whether the user turned syncing off.
	paused := map[string]bool{}
	isPaused := func(a agent) bool {
		if p, ok := paused[a.name()]; ok {
			return p
		}
		reason, err := a.pausedReason()
		if err != nil {
			reason = fmt.Sprintf("could not check whether syncing is paused: %v", err)
		}
		paused[a.name()] = reason != ""
		if reason != "" {
			status.Paused = append(status.Paused, a.name()+": "+reason)
			if !opts.silent {
				opts.io.LogInfof("Skipping %s sessions: %s.\n", a.name(), reason)
			}
		}
		return reason != ""
	}

	targets := map[string]targetLookup{}
	identities := map[string]identityLookup{}
	reported := map[string]struct{}{}
	recordOnce := func(key, sessionID string, err error) {
		if _, ok := reported[key]; ok {
			return
		}
		reported[key] = struct{}{}
		recordErr(sessionID, err)
	}
	// A GitLab version that does not accept an agent type rejects all its
	// sessions. They are marked completed locally rather than retried, so
	// they are not uploaded in a burst after an upgrade, long after the user
	// agreed to syncing.
	recordUnsupported := func(agentType, host, sessionID string) {
		entry := agentType + " on " + host
		if !slices.Contains(status.AgentTypeNotSupported, entry) {
			status.AgentTypeNotSupported = append(status.AgentTypeNotSupported, entry)
		}
		if err := markSessionCompleted(sessionID, now); err != nil {
			recordErr(sessionID, err)
		}
	}
	// The governance endpoints answer 404 when the feature is off for a
	// project that was found, which is expected rather than an error.
	recordNotEnabled := func(projectKey string) {
		if !slices.Contains(status.GovernanceNotEnabled, projectKey) {
			status.GovernanceNotEnabled = append(status.GovernanceNotEnabled, projectKey)
		}
	}
	for _, sessionID := range sessionIDs {
		meta, err := readSessionMeta(sessionID)
		if err != nil {
			recordErr(sessionID, err)
			continue
		}

		ag := agentByName(agents, meta.AgentType)
		if ag == nil {
			recordErr(sessionID, fmt.Errorf("unsupported agent %q", meta.AgentType))
			continue
		}

		src, locator, err := ag.recorded(sessionID, meta.Transcript)
		if err != nil {
			recordErr(sessionID, err)
			continue
		}
		if locator != meta.Transcript {
			meta.Transcript = locator
			if err := writeSessionMeta(sessionID, *meta); err != nil {
				recordErr(sessionID, err)
			}
		}

		if meta.CompletedAt != nil {
			// Local state is kept while the agent can still resume the session.
			forget, err := ag.canForget(src, *meta.CompletedAt)
			if err != nil {
				recordErr(sessionID, err)
			} else if forget {
				if err := forgetSession(sessionID); err != nil {
					recordErr(sessionID, err)
				}
			}
			continue
		}

		if isPaused(ag) {
			continue
		}
		offset, err := readCursor(sessionID)
		if err != nil {
			recordErr(sessionID, err)
			continue
		}
		data, newOffset, err := src.read(ctx, offset)
		if errors.Is(err, errTranscriptNotFound) {
			// The agent has deleted the transcript, so there is nothing left
			// to sync. Forgetting the session stops later runs searching for it.
			if err := forgetSession(sessionID); err != nil {
				recordErr(sessionID, err)
			}
			continue
		}
		if errors.Is(err, errAgentSessionNotFound) {
			if recorded, statErr := sessionMetaModTime(sessionID); statErr == nil && now.Sub(recorded) > forgetUnfoundAfter {
				if err := forgetSession(sessionID); err != nil {
					recordErr(sessionID, err)
				}
				continue
			}
		}
		if err != nil {
			recordErr(sessionID, err)
			continue
		}

		last, err := src.lastActivity(ctx)
		if err != nil {
			recordErr(sessionID, err)
			continue
		}
		if offset == 0 && now.Sub(last) > maxEventAge {
			// GitLab accepts no events this old, so a session that has never
			// synced would arrive empty. It is marked completed locally so it
			// is not checked again. A session that has synced still goes
			// through idle completion, so it is completed on GitLab.
			if err := markSessionCompleted(sessionID, now); err != nil {
				recordErr(sessionID, err)
			}
			continue
		}
		idle := now.Sub(last) > idleCompleteAfter
		if data.empty() && !idle {
			continue
		}

		// Lookups are cached per project for the run, and a failure is
		// recorded once, so one unreachable project cannot fill the error
		// list with a copy for each of its sessions.
		projectKey := meta.Host + "/" + meta.PathWithNamespace
		t, err := lookupTarget(targets, meta, opts)
		if err != nil {
			recordOnce("project "+projectKey, sessionID, err)
			continue
		}
		identityKey := projectKey + " " + meta.AgentType
		identity, ok := identities[identityKey]
		if !ok {
			registered, err := gaig.RegisterIdentity(t.client, t.project.ID, meta.AgentType)
			switch {
			case errors.Is(err, gitlab.ErrNotFound):
				recordNotEnabled(projectKey)
			case unsupportedAgentType(err):
				identity.unsupported = true
			case err != nil:
				recordOnce("identity "+identityKey, sessionID, err)
			}
			if registered != nil {
				identity = identityLookup{id: registered.ID, ok: true}
			}
			identities[identityKey] = identity
		}
		if identity.unsupported {
			recordUnsupported(meta.AgentType, meta.Host, sessionID)
			continue
		}
		if !identity.ok {
			// GitLab requires an agent identity to create a session.
			continue
		}
		t.identityID = identity.id

		res, err := pushSession(ctx, t, sessionID, meta.AgentType, src, data, newOffset, idle, opts)
		status.EventsPosted += res.eventsPosted
		if res.eventsPosted > 0 {
			status.SessionsSynced++
		}
		if res.completed {
			status.SessionsCompleted++
		}
		switch {
		case errors.Is(err, gitlab.ErrNotFound) && res.eventsPosted == 0 && !res.completed:
			recordNotEnabled(projectKey)
		case unsupportedAgentType(err):
			recordUnsupported(meta.AgentType, meta.Host, sessionID)
		case err != nil:
			recordErr(sessionID, err)
		}
	}
	return nil
}

type targetLookup struct {
	target target
	err    error
}

type identityLookup struct {
	id int
	ok bool
	// unsupported reports that GitLab does not accept the agent type.
	unsupported bool
}

// unsupportedAgentType reports whether GitLab rejected a request because it
// does not accept the session's agent type, as GitLab versions from before
// an agent was added do.
func unsupportedAgentType(err error) bool {
	var resp *gitlab.ErrorResponse
	return errors.As(err, &resp) && resp.StatusCode == http.StatusBadRequest && strings.Contains(resp.Message, "agent_type")
}

// lookupTarget resolves the project a session was recorded with, caching the
// result per host and project so each is looked up once per run.
func lookupTarget(cache map[string]targetLookup, meta *sessionMeta, opts *options) (target, error) {
	if meta.Host == "" || meta.PathWithNamespace == "" {
		return target{}, errors.New("session metadata has no project")
	}

	key := meta.Host + "/" + meta.PathWithNamespace
	lookup, ok := cache[key]
	if !ok {
		lookup = resolveTarget(meta, opts)
		cache[key] = lookup
	}
	if lookup.err != nil {
		return target{}, lookup.err
	}

	return lookup.target, nil
}

func resolveTarget(meta *sessionMeta, opts *options) targetLookup {
	client, err := opts.apiClient(meta.Host)
	if err != nil {
		return targetLookup{err: fmt.Errorf("could not create client for %s: %w", meta.Host, err)}
	}
	project, err := api.GetProject(client.Lab(), meta.PathWithNamespace)
	if err != nil {
		return targetLookup{err: fmt.Errorf("could not resolve project %s on %s: %w", meta.PathWithNamespace, meta.Host, err)}
	}
	return targetLookup{target: target{client: client, project: project}}
}

// sessionRequest is the body for POST /ai_agent/sessions.
type sessionRequest struct {
	AgentType       string `json:"agent_type"`
	AgentIdentityID int    `json:"agent_identity_id,omitempty"`
	SyncType        string `json:"sync_type"`
	Goal            string `json:"goal,omitempty"`
	IdempotencyKey  string `json:"idempotency_key"`
	StartedAt       string `json:"started_at,omitempty"`
}

// sessionResponse is the response from POST /ai_agent/sessions.
type sessionResponse struct {
	ID int `json:"id"`
}

// ensureSession creates a session on GitLab or returns the existing one via idempotency key.
func ensureSession(ctx context.Context, client *api.Client, projectID int64, identityID int, sessionID, agentType string, data *sessionData) (int, error) {
	if agentType == "" {
		agentType = "unknown"
	}
	reqBody := sessionRequest{
		AgentType:      agentType,
		SyncType:       "hook",
		IdempotencyKey: sessionID,
	}

	if identityID > 0 {
		reqBody.AgentIdentityID = identityID
	}

	if data.Goal != "" {
		reqBody.Goal = data.Goal
	}

	if !data.StartedAt.IsZero() && time.Since(data.StartedAt) < 30*24*time.Hour {
		reqBody.StartedAt = data.StartedAt.UTC().Format(time.RFC3339)
	}

	req, err := client.Lab().NewRequest(
		http.MethodPost,
		fmt.Sprintf("projects/%d/ai_agent/sessions", projectID),
		reqBody,
		[]gitlab.RequestOptionFunc{gitlab.WithContext(ctx)},
	)
	if err != nil {
		return 0, fmt.Errorf("could not create session request: %w", err)
	}

	var resp sessionResponse
	if _, err := client.Lab().Do(req, &resp); err != nil {
		return 0, fmt.Errorf("could not create session: %w", err)
	}

	return resp.ID, nil
}

// auditEventRequest is a single audit event.
type auditEventRequest struct {
	EventName    string         `json:"event_name"`
	CloudEventID string         `json:"cloud_event_id"`
	OccurredAt   string         `json:"occurred_at"`
	Details      map[string]any `json:"details"`
}

// auditEventsRequest is the body for POST /ai_agent/audit_events.
type auditEventsRequest struct {
	SessionID int                 `json:"session_id"`
	Events    []auditEventRequest `json:"events"`
}

// postAuditEvents posts tool call audit events to GitLab.
// maxEventsPerRequest is the most events GitLab accepts in one request.
const maxEventsPerRequest = 500

// postAuditEvents posts audit events to GitLab in batches of at
// most maxEventsPerRequest. If a batch fails, earlier batches stay posted;
// GitLab deduplicates them by cloud_event_id when they are retried.
func postAuditEvents(ctx context.Context, client *api.Client, projectID int64, sessionID int, events []auditEventRequest) error {
	for batch := range slices.Chunk(events, maxEventsPerRequest) {
		if err := postAuditEventBatch(ctx, client, projectID, sessionID, batch); err != nil {
			return err
		}
	}
	return nil
}

func postAuditEventBatch(ctx context.Context, client *api.Client, projectID int64, sessionID int, events []auditEventRequest) error {
	reqBody := auditEventsRequest{
		SessionID: sessionID,
		Events:    events,
	}

	req, err := client.Lab().NewRequest(
		http.MethodPost,
		fmt.Sprintf("projects/%d/ai_agent/audit_events", projectID),
		reqBody,
		[]gitlab.RequestOptionFunc{gitlab.WithContext(ctx)},
	)
	if err != nil {
		return fmt.Errorf("could not create audit events request: %w", err)
	}

	if _, err := client.Lab().Do(req, nil); err != nil {
		return fmt.Errorf("could not post audit events: %w", err)
	}

	return nil
}

// completeRequest is the body for PATCH /ai_agent/sessions/:id.
type completeRequest struct {
	Status      string `json:"status"`
	Jsonlsha256 string `json:"jsonl_sha256,omitempty"`
}

// completeSession marks a session as completed on GitLab.
func completeSession(ctx context.Context, client *api.Client, projectID int64, sessionID int, sha256 string) error {
	reqBody := completeRequest{
		Status:      "completed",
		Jsonlsha256: sha256,
	}

	req, err := client.Lab().NewRequest(
		http.MethodPatch,
		fmt.Sprintf("projects/%d/ai_agent/sessions/%d", projectID, sessionID),
		reqBody,
		[]gitlab.RequestOptionFunc{gitlab.WithContext(ctx)},
	)
	if err != nil {
		return fmt.Errorf("could not create complete request: %w", err)
	}

	if _, err := client.Lab().Do(req, nil); err != nil {
		return fmt.Errorf("could not complete session: %w", err)
	}

	return nil
}

func deterministicUUID(toolCallID string) string {
	return uuid.NewSHA1(uuid.NameSpaceDNS, []byte("gaig.gitlab.com/"+toolCallID)).String()
}
