package sync

import (
	"cmp"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"os"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/google/uuid"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/commands/govern/internal/fallbacksync"
	"gitlab.com/gitlab-org/cli/internal/commands/govern/internal/gaig"
	"gitlab.com/gitlab-org/cli/internal/commands/govern/internal/redact"
	"gitlab.com/gitlab-org/cli/internal/commands/govern/internal/syncstate"
	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/dbg"
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
	// hook is the agent whose hook passes the session on stdin.
	hook      string
	buildInfo api.BuildInfo
	// rateLimitWaited is how long this run has waited out GitLab's rate limit.
	rateLimitWaited time.Duration
	// waitFor, when set, replaces waiting for a duration, for tests.
	waitFor func(ctx context.Context, d time.Duration) error
	// startDetached, when set, replaces starting glab in the background, for tests.
	startDetached func(dir string, env []string, args ...string) error
}

// wait waits for d, or until ctx is done.
func (o *options) wait(ctx context.Context, d time.Duration) error {
	if o.waitFor != nil {
		return o.waitFor(ctx, d)
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
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
		buildInfo: f.BuildInfo(),
	}

	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Sync agent session data to GitLab. (EXPERIMENTAL)",
		Long: heredoc.Docf(`
			Read new entries from the local agent transcript since the last sync and POST them to GitLab as audit events.

			The audit events contain the prompts you type, each tool call with its arguments (such as commands, file paths, and edits, replaced with a marker when over 8 KB), each tool call's outcome, duration, and error message, and the model and token usage of each response. Tool output and the text of responses are not sent.

			Before anything is sent, glab replaces common secret formats with a marker such as %[1]s[REDACTED:gitlab_token]%[1]s: GitLab, GitHub, Slack, npm and PyPI tokens, AWS, Google, Stripe, OpenAI and Anthropic API keys, JWTs, private keys, credentials in Authorization and token headers or under credential-like keys, and passwords in URLs. This is best effort: other secrets in prompts, commands, or file contents are sent as they are.

			When glab creates a session on GitLab, it also sends the glab version and the agent's version. A session that has never synced and has had no activity in the last 89 days is not uploaded, because GitLab does not accept events that old. Activity from before an agent was set up with %[1]sglab minder setup%[1]s is not uploaded either, unless setup was run with %[1]s--include-history%[1]s.

			Some agents record less:

			- OpenCode: prompts and responses are not sent, and a tool call's outcome is sent only if the call had finished when it was first synced.
			- Codex: a tool call's outcome is "completed", because Codex does not record whether it succeeded.
			- Cursor: only prompts and tool calls are sent, timed by the transcript's last modification, because Cursor records no results, token usage, timestamps, or version. For a Cursor session that started before Cursor was set up, only what was added after setup is uploaded. A Cursor session is not synced if the files its tool calls read or write are outside the workspace glab finds, or if several directories match the workspace's name and those files don't show which, so that it is not uploaded to the wrong project.

			If a GitLab instance does not accept an agent's sessions yet, they are not uploaded, then or after the instance is upgraded. Sessions from after the upgrade are.

			Called by the Claude Code Stop hook after every agent turn. Also used by its SessionEnd hook (with %[1]s--complete%[1]s) to mark the session as complete. The hooks also record the session's project, host, folder, and transcript location so that %[1]s--all%[1]s can sync it later.

			With %[1]s--hook codex%[1]s or %[1]s--hook cursor%[1]s, reads the session from the JSON that a Codex Stop hook, or a Cursor stop or sessionEnd hook, gets on stdin, and syncs it with a glab started in the background, so the agent does not wait for the upload. It prints nothing, because the agent reads a hook's output as a reason to continue. The project is resolved from the session's directory, or for Cursor from the workspace folder that the transcript belongs to.

			In a Cursor workspace with several folders, glab uses the folder the workspace is named after, unless the session's tool calls point elsewhere, or otherwise the one folder the tool calls used. Until a turn shows which, the session is not synced. Cursor's sessionEnd hook does not mark a session as complete. A Cursor session is completed once it has been inactive for 24 hours.

			A session that works in several GitLab projects becomes one GitLab session in each of them, and each holds only the activity from its project. glab sends each prompt, tool call, and response to the project of the folder the agent was working in when it recorded it:

			- Claude Code records the folder of every entry, and follows a %[1]scd%[1]s. Codex records it for every turn and shell call. OpenCode and Cursor record one folder for a session, so their sessions go to one project.
			- The folder is where the agent's shell was, not always the project it acted on. A command such as %[1]scd ../other && git push%[1]s, %[1]sgit -C%[1]s, an absolute path, or an API call counts towards the project of the folder the shell was in.
			- A folder the hooks ran in, and its subfolders in the same Git repository, go to the project the hooks resolved for it. Another folder goes to the project of its %[1]sorigin%[1]s remote. Activity that records no folder goes to the project of the folder the hooks last ran in, or of %[1]s-R/--repo%[1]s.
			- Activity goes only to projects on the same GitLab host and in the same top-level group as the first project the session was recorded with, which does not change. Activity in other projects is not uploaded, so a session does not upload to another organization's project because the agent worked in a clone of it, even if its hooks ran there.
			- Activity in a folder without a GitLab project, such as your home folder, is not uploaded, then or later.
			- If a project does not accept the session, for example because AI governance is off there, while another project does, the activity in that project is not uploaded, then or later. If no project accepts the session, it is retried, so it uploads once governance is turned on.

			With %[1]s--all%[1]s, syncs every session the hooks have recorded, each to the projects of the folders it worked in. Sessions inactive for 24 hours are marked completed. Sessions from agents that use hooks are skipped while the glab hooks are missing, so removing the hooks pauses them, including an agent session that was already open and still runs them. The fallback periodic sync installed by %[1]sglab minder setup%[1]s runs this, and %[1]sglab minder doctor%[1]s shows the result of its last run.

			Supports Claude Code, OpenCode, Codex, and Cursor sessions. OpenCode sessions are read with %[1]sopencode session export%[1]s, or %[1]sopencode export%[1]s before OpenCode 2. With %[1]s--all%[1]s, it also finds and syncs the sessions of Codex and Cursor, when they are enabled with %[1]sglab minder setup --agents%[1]s, by scanning their transcripts, for repositories on GitLab hosts glab is logged in to.
		`, "`") + text.ExperimentalString,
		Example: heredoc.Doc(`
			# Sync the current agent session to GitLab
			$ glab minder audit sync

			# Sync and mark the session as completed
			$ glab minder audit sync --complete

			# Sync against a specific project
			$ glab minder audit sync -R my-group/my-project

			# Sync every recorded session that has unsynced activity
			$ glab minder audit sync --all
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
	fl.Var(cmdutils.NewEnumValue(hookAgents, "", &opts.hook), "hook", fmt.Sprintf("Read the session from the stdin of this agent's hook, and sync it in the background: %s. Used by the Codex and Cursor hooks.", strings.Join(hookAgents, ", ")))

	cmdutils.EnableRepoOverride(cmd, f)
	cmd.MarkFlagsMutuallyExclusive("all", "repo")
	cmd.MarkFlagsMutuallyExclusive("all", "complete")
	for _, other := range []string{"all", "complete", "repo"} {
		cmd.MarkFlagsMutuallyExclusive("hook", other)
	}

	return cmd
}

func runSync(ctx context.Context, opts *options) error {
	if opts.hook != "" {
		return runHook(opts)
	}
	if opts.all {
		return syncAllSessions(ctx, opts)
	}

	ag, sessionID := detectAgent(supportedAgents(opts.executor))

	var src transcript
	if ag != nil {
		// A session that started before the hooks were removed still runs
		// them, so the pause that --all and --hook honour applies here too.
		reason, err := ag.pausedReason()
		if err != nil {
			return fmt.Errorf("not syncing the %s session: could not check whether syncing is paused: %w", ag.name(), err)
		}
		if reason != "" {
			if !opts.silent {
				opts.io.LogInfof("Not syncing the %s session: %s.\n", ag.name(), reason)
			}
			return nil
		}
		opts.agentType = ag.name()
		var locator string
		src, locator, err = ag.current(sessionID)
		if err != nil {
			return err
		}
		// Written before ResolveProject so a failed project lookup still
		// leaves enough context for --all to retry this session later, or to
		// report it when the folder has no GitLab project.
		err = recordSessionMeta(ctx, opts, ag, sessionID, locator)
		if errors.Is(err, errEarlierActivityNotSkipped) {
			return err
		}
		if err != nil && !opts.silent {
			opts.io.LogErrorf("warning: could not record session metadata: %v\n", err)
		}
	}

	var repo glrepo.Interface
	project, client, err := gaig.ResolveProject(func() (glrepo.Interface, error) {
		var err error
		repo, err = opts.baseRepo()
		return repo, err
	}, opts.apiClient)
	if err != nil {
		if !opts.silent {
			opts.io.LogErrorf("error: could not resolve project: %v\n", err)
		}
		return err
	}

	if !opts.silent {
		opts.io.LogInfof("Project: %s (ID: %d)\n", project.PathWithNamespace, project.ID)
	}

	if sessionID == "" {
		if !opts.silent {
			opts.io.LogInfo("No active agent session detected.")
		}
		return nil
	}

	s := newSyncer(opts)
	resolved := projectRef{Host: repo.RepoHost(), PathWithNamespace: repo.FullName()}
	s.targets[resolved.key()] = targetLookup{target: target{client: client, project: project}}
	return syncSession(ctx, s, sessionID, src, resolved)
}

// withoutRemoteReason is the not-synced reason for an agent's sessions that ran
// in a folder without a GitLab project.
func withoutRemoteReason(agentType string) string {
	return agentType + " sessions in a folder where glab could not identify a GitLab project"
}

var errEarlierActivityNotSkipped = errors.New("could not skip activity from before the session had a GitLab project")

// recordSessionMeta records the session for --all. A session recorded without a
// project skips its earlier activity before it is recorded with one, so a
// failed skip leaves it without a project for the next hook call to retry.
func recordSessionMeta(ctx context.Context, opts *options, ag agent, sessionID, locator string) error {
	if opts.baseRepo == nil {
		return nil
	}
	meta, readErr := readSessionMeta(sessionID)
	recordedWithoutProject := readErr == nil && meta.PathWithNamespace == ""
	repo, err := opts.baseRepo()
	if err != nil {
		// ResolveProject reports err. Recording the session lets --all report
		// it as not synced, and every hook call rewrites the record, which
		// --all reads as the session's last activity.
		return withSessionMetaLock(func() error {
			latest, err := readSessionMeta(sessionID)
			switch {
			case errors.Is(err, errNoSessionMeta):
			case err != nil:
				return err
			case latest.PathWithNamespace != "":
				return nil
			}
			return writeSessionMeta(sessionID, sessionMeta{AgentType: opts.agentType, Transcript: locator})
		})
	}
	if recordedWithoutProject {
		// The recorded transcript, not the hook's: the hook's path follows its
		// working directory, which changes if the agent changed directory.
		src, _, err := ag.recorded(sessionID, cmp.Or(meta.Transcript, locator))
		if err == nil {
			err = skipToEnd(ctx, sessionID, src)
		}
		if err != nil {
			return fmt.Errorf("%w: %w", errEarlierActivityNotSkipped, err)
		}
	}
	cwd, cwdErr := currentCWD()
	if cwdErr != nil {
		dbg.Debugf("not recording the folder of session %s: %v", sessionID, cwdErr)
	}
	return withSessionMetaLock(func() error {
		recorded := sessionMeta{
			AgentType:         opts.agentType,
			PathWithNamespace: repo.FullName(),
			Host:              repo.RepoHost(),
			Transcript:        locator,
		}
		recorded.FirstProject = recorded.project()
		latest, err := readSessionMeta(sessionID)
		switch {
		case err == nil:
			recorded.Parts, recorded.Folders, recorded.PendingCalls = latest.Parts, latest.Folders, latest.PendingCalls
			if anchor := latest.anchor(); anchor.PathWithNamespace != "" {
				recorded.FirstProject = anchor
			}
		case !errors.Is(err, errNoSessionMeta):
			// Nothing in a record that cannot be read can be kept.
			dbg.Debugf("replacing the unreadable record of session %s: %v", sessionID, err)
		}
		if cwdErr == nil {
			recorded.recordFolder(cwd, recorded.project())
		}
		return writeSessionMeta(sessionID, recorded)
	})
}

// skipToEnd moves the session's sync position past what the transcript holds
// now. The user agreed to upload sessions in a GitLab repository, so a session
// that only now has a GitLab remote uploads its activity from this point, not
// what it did before in another folder or repository.
func skipToEnd(ctx context.Context, sessionID string, src transcript) error {
	if src == nil {
		return nil
	}
	position, err := syncstate.ReadPosition(sessionID)
	if err != nil {
		return err
	}
	_, end, err := src.read(ctx, position)
	if err != nil {
		return err
	}
	return syncstate.StartAt(sessionID, end)
}

// target is the project a session is synced to.
type target struct {
	client     *api.Client
	project    *gitlab.Project
	identityID int
}

// syncer uploads sessions, and looks up each project, agent identity and
// folder once per run.
type syncer struct {
	opts       *options
	targets    map[string]targetLookup
	identities map[string]identityLookup
	folders    *folderProjects
}

func newSyncer(opts *options) *syncer {
	return &syncer{
		opts:       opts,
		targets:    map[string]targetLookup{},
		identities: map[string]identityLookup{},
		folders:    newFolderProjects(opts.executor, opts.config),
	}
}

// syncSession syncs a single session transcript to GitLab. Entries that
// record no folder go to project.
func syncSession(ctx context.Context, s *syncer, sessionID string, src transcript, project projectRef) error {
	opts := s.opts
	since, err := uploadsFrom(opts.agentType)
	if err != nil {
		return err
	}
	meta, err := readSessionMeta(sessionID)
	switch {
	case errors.Is(err, errNoSessionMeta):
		meta = &sessionMeta{}
	case err != nil:
		// The record holds the session's GitLab sessions and their keys, so
		// syncing without it would create them again.
		return fmt.Errorf("could not read session metadata: %w", err)
	}
	p, err := readPending(ctx, sessionID, src, since)
	if errors.Is(err, errTranscriptNotFound) {
		if !opts.silent {
			opts.io.LogInfof("No transcript found at %s\n", src)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("could not read the session: %w", err)
	}

	if p.data.empty() && (!opts.complete || !p.uploadedTo(meta, sessionID)) {
		if !opts.silent {
			opts.io.LogInfo("No new entries to sync.")
		}
		return nil
	}

	if opts.agentType == "" {
		if !opts.silent {
			opts.io.LogInfo("No agent type detected -- skipping session sync.")
		}
		return nil
	}

	res := s.pushSession(ctx, sessionID, opts.agentType, src, p.data, p.next, opts.complete, project, meta)
	if opts.silent {
		return nil
	}
	if res.dropped > 0 {
		opts.io.LogInfo("Not syncing the activity in folders without a GitLab project in the session's top-level groups.")
	}
	for _, part := range res.parts {
		switch part.outcome {
		case partNotEnabled:
			opts.io.LogInfof("%s: %s\n", part.project, governanceNotEnabled)
		case partProjectNotFound:
			opts.io.LogErrorf("warning: project %s was not found or is not accessible\n", part.project)
		case partRefused, partFailed, partUnsupported:
			opts.io.LogErrorf("warning: %v\n", part.err)
		}
	}
	return nil
}

// partOutcome is how syncing one part of a session ended.
type partOutcome int

const (
	partSynced partOutcome = iota
	// partNotEnabled: AI agent governance is off for the project.
	partNotEnabled
	// partRefused: the project refused to create the session, for example
	// because the user's role there is too low.
	partRefused
	// partUnsupported: the GitLab instance does not accept the agent type.
	partUnsupported
	// partProjectNotFound: the project lookup answered 404.
	partProjectNotFound
	// partFailed: the part is retried.
	partFailed
)

type partResult struct {
	project        projectRef
	outcome        partOutcome
	err            error
	eventsPosted   int
	eventsTooLarge int
	glSessionID    int
	// recorded is the part to keep in the session record, once its GitLab
	// session exists or its instance does not accept the agent type.
	recorded *sessionPart
	// reportOnce, when set, names a failure that is shared by every session in
	// the project, such as a failed lookup, so --all reports it once.
	reportOnce string
}

type pushResult struct {
	parts []partResult
	// dropped counts the entries from folders without a GitLab project.
	dropped int
	// completed reports that every part was completed, or refused by its
	// project.
	completed bool
}

func (r pushResult) eventsPosted() int {
	n := 0
	for _, p := range r.parts {
		n += p.eventsPosted
	}
	return n
}

func (r pushResult) eventsTooLarge() int {
	n := 0
	for _, p := range r.parts {
		n += p.eventsTooLarge
	}
	return n
}

// unsupportedOnly reports whether every part was refused for an agent type the
// GitLab instance does not accept.
func (r pushResult) unsupportedOnly() bool {
	return len(r.parts) > 0 && !slices.ContainsFunc(r.parts, func(p partResult) bool { return p.outcome != partUnsupported })
}

// refuseForbidden treats a 403 for a project as the project refusing its
// part, once another part on the same host was posted. GitLab also answers
// 403 for problems with the user's account, such as terms not accepted, but a
// part posted with the same account shows that the account is fine, and that
// this project does not let the user sync there: their role is too low, or
// it has no license for AI governance.
func (r *pushResult) refuseForbidden() {
	for i, p := range r.parts {
		if p.outcome != partFailed || !gitlab.HasStatusCode(p.err, http.StatusForbidden) {
			continue
		}
		if slices.ContainsFunc(r.parts, func(other partResult) bool {
			return other.outcome == partSynced && strings.EqualFold(other.project.Host, p.project.Host)
		}) {
			r.parts[i].outcome = partRefused
		}
	}
}

// settled reports whether the cursor can move past the events read for the
// parts, project being the session's own project.
//
// A project that refuses its part holds the cursor only while no other part
// is posted. A session that works in one project with governance off then
// uploads its activity once governance is turned on, as before the split, but
// a project that refuses does not make the others post the same events again
// on every run.
//
// A 404 for the session's own project holds the cursor, because a missing
// token causes it too. Another project is one the agent named in a folder's
// remote, which may not exist, so its events are dropped rather than uploaded
// once a project of that name is created.
func (r pushResult) settled(project projectRef) bool {
	posted := slices.ContainsFunc(r.parts, func(p partResult) bool { return p.outcome == partSynced })
	for _, p := range r.parts {
		switch p.outcome {
		case partNotEnabled, partRefused:
			if !posted {
				return false
			}
		case partProjectNotFound:
			if p.project.key() == project.key() {
				return false
			}
		case partFailed:
			return false
		}
	}
	return true
}

// pushSession uploads each project's part of data to its GitLab session,
// advances the cursor to newCursor once every part's events are posted or
// refused, and marks the session completed if requested. Entries that record
// no folder go to project.
func (s *syncer) pushSession(ctx context.Context, sessionID, agentType string, src transcript, data *sessionData, newCursor int64, complete bool, project projectRef, meta *sessionMeta) pushResult {
	redactSessionData(data)
	router := sessionRouter{
		known:   meta.Folders,
		session: project,
		lookup: func(dir string) projectRef {
			if dir == unknownFolder {
				return projectRef{}
			}
			return s.folders.lookup(ctx, dir)
		},
		topLevel: func(dir string) string { return s.folders.topLevel(ctx, dir) },
	}
	// Every entry stays in the top-level group of the session's first project,
	// so that an agent that changes to a clone of another organization's
	// project, where its hooks then run too, does not upload the session there.
	anchor := meta.anchor()
	if anchor.PathWithNamespace == "" {
		anchor = project
	}
	projectOf := func(dir string) projectRef {
		p := router.project(dir)
		if p.PathWithNamespace != "" && p.group() != anchor.group() {
			dbg.Debugf("not syncing the activity in %q: %s is outside the top-level group of %s", dir, p, anchor)
			return projectRef{}
		}
		return p
	}
	split := splitByProject(data, projectOf, meta.PendingCalls)
	parts := split.parts
	if complete {
		for _, recorded := range meta.Parts {
			if !slices.ContainsFunc(parts, func(p projectData) bool { return p.project.key() == recorded.key() }) {
				parts = append(parts, projectData{project: recorded.projectRef, data: &sessionData{}})
			}
		}
	}
	res := pushResult{dropped: split.dropped}
	for _, part := range parts {
		if recorded, ok := meta.part(part.project); ok && recorded.Unsupported {
			continue
		}
		res.parts = append(res.parts, s.pushPart(ctx, sessionID, agentType, part, meta))
	}
	res.refuseForbidden()

	settled := res.settled(project)
	err := updateSessionMeta(sessionID, func(m *sessionMeta) {
		for _, part := range res.parts {
			if part.recorded != nil {
				m.recordPart(*part.recorded)
			}
		}
		if !settled {
			return
		}
		// Another run may have recorded calls since this one read the record.
		pending := maps.Clone(m.PendingCalls)
		if pending == nil {
			pending = map[string]projectRef{}
		}
		for id := range split.answered {
			delete(pending, id)
		}
		maps.Copy(pending, split.pending)
		m.PendingCalls = pending
	})
	if err != nil && !s.opts.silent {
		s.opts.io.LogErrorf("warning: could not record the session's GitLab sessions: %v\n", err)
	}
	if !settled {
		return res
	}
	if err := syncstate.WritePosition(sessionID, newCursor); err != nil && !s.opts.silent {
		s.opts.io.LogErrorf("warning: could not update the sync position: %v\n", err)
	}
	if complete {
		s.completeParts(ctx, &res, src)
		if res.completed {
			if err := markSessionCompleted(sessionID, time.Now()); err != nil && !s.opts.silent {
				s.opts.io.LogErrorf("warning: could not record session completion: %v\n", err)
			}
		}
	}
	return res
}

// completeParts marks the GitLab session of every synced part completed. The
// parts include every project the session synced to, not only those in the
// last read.
func (s *syncer) completeParts(ctx context.Context, res *pushResult, src transcript) {
	sum, err := src.checksum(ctx)
	if err != nil && !s.opts.silent {
		s.opts.io.LogErrorf("warning: could not compute transcript checksum: %v\n", err)
	}
	res.completed = true
	for i := range res.parts {
		part := &res.parts[i]
		if part.outcome != partSynced {
			continue
		}
		t, err := s.target(part.project)
		if err == nil {
			err = completeSession(ctx, t.client, t.project.ID, part.glSessionID, sum)
		}
		if err != nil {
			part.outcome, part.err = partFailure(part.project, fmt.Errorf("could not complete session: %w", err), false)
			res.completed = res.completed && part.outcome == partNotEnabled
			continue
		}
		if !s.opts.silent {
			s.opts.io.LogInfof("Session in %s marked as completed.\n", part.project)
		}
	}
}

// pushPart uploads one project's part of a session.
func (s *syncer) pushPart(ctx context.Context, sessionID, agentType string, part projectData, meta *sessionMeta) partResult {
	res := partResult{project: part.project}
	t, err := s.target(part.project)
	switch {
	case errors.Is(err, gitlab.ErrNotFound):
		res.outcome = partProjectNotFound
		return res
	case err != nil:
		res.outcome, res.err, res.reportOnce = partFailed, err, "project "+part.project.key()
		return res
	}
	t.identityID, err = s.identity(ctx, t, part.project, agentType)
	if err != nil {
		res.outcome, res.err = partFailure(part.project, err, false)
		res.reportOnce = "identity " + part.project.key() + " " + agentType
		if res.outcome == partUnsupported {
			res.recorded = &sessionPart{projectRef: part.project, Unsupported: true}
		}
		return res
	}

	key := fmt.Sprintf("%s-%d", sessionID, t.project.ID)
	if recorded, ok := meta.part(part.project); ok {
		key = recorded.Key
	}
	req := newSessionRequest(agentType, syncTypeFor(s.opts), s.opts.buildInfo.Version, t.identityID, key, sessionID, part.data)
	glSessionID, err := ensureSession(ctx, t.client, t.project.ID, req)
	if err == nil && glSessionID <= 0 {
		err = errors.New("could not create session: GitLab returned no session ID")
	}
	if err != nil {
		res.outcome, res.err = partFailure(part.project, err, true)
		if res.outcome == partUnsupported {
			res.recorded = &sessionPart{projectRef: part.project, Unsupported: true}
		}
		return res
	}
	res.recorded = &sessionPart{projectRef: part.project, Key: key}
	res.glSessionID = glSessionID
	if !s.opts.silent {
		s.opts.io.LogInfof("Session: %d (%s)\n", glSessionID, part.project)
	}

	events := auditEvents(agentType, part.data, time.Now())
	if len(events) == 0 {
		return res
	}
	poster := auditEventPoster{client: t.client, projectID: t.project.ID, sessionID: glSessionID, opts: s.opts}
	tooLarge, err := poster.post(ctx, events)
	res.eventsTooLarge = tooLarge
	if err != nil {
		res.outcome, res.err = partFailure(part.project, fmt.Errorf("could not post audit events: %w", err), false)
		return res
	}
	res.eventsPosted = len(events) - tooLarge
	if !s.opts.silent {
		s.opts.io.LogInfof("Posted %d audit events\n", res.eventsPosted)
		if tooLarge > 0 {
			s.opts.io.LogErrorf("warning: skipped %d audit events too large for GitLab to accept\n", tooLarge)
		}
	}
	return res
}

// target returns the GitLab project p, looking it up once per run.
func (s *syncer) target(p projectRef) (target, error) {
	lookup, ok := s.targets[p.key()]
	if !ok {
		lookup = resolveTarget(p, s.opts)
		s.targets[p.key()] = lookup
	}
	return lookup.target, lookup.err
}

// identity returns the ID of the agent identity in project p, registering it
// once per run.
func (s *syncer) identity(ctx context.Context, t target, p projectRef, agentType string) (int, error) {
	key := p.key() + " " + agentType
	lookup, ok := s.identities[key]
	if !ok {
		registered, err := gaig.RegisterIdentity(ctx, t.client, t.project.ID, agentType)
		switch {
		case registered != nil && err != nil:
			// The identity was registered, but could not be cached.
			dbg.Debugf("agent identity %d in %s: %v", registered.ID, p, err)
			lookup.id = registered.ID
		case err != nil:
			lookup.err = err
		default:
			lookup.id = registered.ID
		}
		s.identities[key] = lookup
	}
	return lookup.id, lookup.err
}

// partFailure returns how a part ends when a governance endpoint of project p
// answers err, and the error to report. The endpoints answer 404 while AI
// agent governance is off for the project. A 403 also comes from the user's
// account or the group's rules, such as terms not accepted, SAML or an IP
// allowlist, so it is retried, except when creating the session after the
// agent identity was registered, where it means that the project does not let
// the user create sessions.
func partFailure(p projectRef, err error, creatingSession bool) (partOutcome, error) {
	switch {
	case unsupportedAgentType(err):
		return partUnsupported, err
	case gitlab.HasStatusCode(err, http.StatusNotFound):
		return partNotEnabled, err
	case creatingSession && gitlab.HasStatusCode(err, http.StatusForbidden):
		return partRefused, fmt.Errorf("project %s refused the session: %w", p, err)
	default:
		return partFailed, fmt.Errorf("%w (project %s)", err, p)
	}
}

// redactSessionData replaces the secrets in the prompts and error messages
// that glab sends from data. The goal is redacted when it is read, because it
// is shortened then, and tool arguments when their size is checked, because a
// marker can be longer than the secret it replaces.
func redactSessionData(data *sessionData) {
	for i := range data.Prompts {
		data.Prompts[i].Text = redact.Secrets(data.Prompts[i].Text)
	}
	for i := range data.ToolResults {
		data.ToolResults[i].Error = redact.Secrets(data.ToolResults[i].Error)
	}
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
	setups, err := fallbacksync.ReadAgentSetups()
	if err != nil {
		err = fmt.Errorf("could not tell which activity to upload: %w", err)
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

	s := newSyncer(opts)
	reported := map[string]struct{}{}
	recordOnce := func(key, sessionID string, err error) {
		if _, ok := reported[key]; ok {
			return
		}
		reported[key] = struct{}{}
		recordErr(sessionID, err)
	}
	// A GitLab version that does not accept an agent type rejects all its
	// sessions. They are not retried, so they are not uploaded in a burst
	// after an upgrade, long after the user agreed to syncing: the session's
	// part on that instance is recorded as unsupported, and a session with no
	// other part is marked completed locally.
	recordUnsupported := func(agentType, host string) {
		entry := agentType + " on " + host
		if !slices.Contains(status.AgentTypeNotSupported, entry) {
			status.AgentTypeNotSupported = append(status.AgentTypeNotSupported, entry)
		}
	}
	// The governance endpoints answer 404 when the feature is off for a
	// project that was found, which is expected rather than an error.
	recordNotEnabled := func(projectKey string) {
		if !slices.Contains(status.GovernanceNotEnabled, projectKey) {
			status.GovernanceNotEnabled = append(status.GovernanceNotEnabled, projectKey)
		}
	}
	// A project lookup answers 404 for a deleted project, one the user cannot
	// see, and any private project when no token is found, which the job can
	// hit when it runs without the user's shell environment. The session is
	// kept for a later run rather than dropped.
	recordProjectNotFound := func(projectKey string) {
		if !slices.Contains(status.ProjectNotFound, projectKey) {
			status.ProjectNotFound = append(status.ProjectNotFound, projectKey)
		}
	}
	// A host is only reported when something there failed, because a host
	// without a token still syncs if every lookup and upload succeeds.
	tokenChecked := map[string]struct{}{}
	failedWithoutToken := func(host string) {
		if _, ok := tokenChecked[host]; ok {
			return
		}
		tokenChecked[host] = struct{}{}
		// A token that cannot be read, such as from a locked keyring, fails
		// the client and is recorded as an error instead.
		cfg := opts.config()
		if token, _, err := cfg.GetWithSource(host, "token", true); err != nil || token != "" {
			return
		}
		if jobToken, err := cfg.Get(host, "job_token"); err == nil && jobToken == "" {
			status.NoToken = append(status.NoToken, host)
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
			err := updateSessionMeta(sessionID, func(m *sessionMeta) { m.Transcript = locator })
			if err != nil {
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
		if meta.PathWithNamespace == "" {
			recordSkip(withoutRemoteReason(meta.AgentType))
			last, err := sessionMetaModTime(sessionID)
			if err != nil {
				recordErr(sessionID, err)
			} else if now.Sub(last) > idleCompleteAfter {
				// Completed rather than forgotten: if the session resumes in a
				// GitLab project, the record is what makes it skip its earlier
				// activity. canForget deletes it once the agent cannot resume it.
				if err := markSessionCompleted(sessionID, now); err != nil {
					recordErr(sessionID, err)
				}
			}
			continue
		}
		p, err := readPending(ctx, sessionID, src, setups.UploadsFrom(meta.AgentType))
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
		if !p.uploadedTo(meta, sessionID) && now.Sub(last) > gaig.MaxEventAge {
			// GitLab accepts no events this old, so a session with nothing
			// uploaded would arrive empty. It is marked completed locally so it
			// is not checked again. A session that has synced still goes
			// through idle completion, so it is completed on GitLab.
			if err := markSessionCompleted(sessionID, now); err != nil {
				recordErr(sessionID, err)
			}
			continue
		}
		idle := now.Sub(last) > idleCompleteAfter
		if p.data.empty() && (!idle || !p.uploadedTo(meta, sessionID)) {
			continue
		}

		res := s.pushSession(ctx, sessionID, meta.AgentType, src, p.data, p.next, idle, meta.project(), meta)
		status.EventsPosted += res.eventsPosted()
		status.EventsTooLarge += res.eventsTooLarge()
		if res.eventsPosted() > 0 {
			status.SessionsSynced++
		}
		if res.completed {
			status.SessionsCompleted++
		}
		if res.unsupportedOnly() {
			if err := markSessionCompleted(sessionID, now); err != nil {
				recordErr(sessionID, err)
			}
		}
		if res.dropped > 0 {
			recordSkip(meta.AgentType + " activity in folders without a GitLab project in the session's top-level groups")
		}
		// Lookups are cached per project for the run, and a failure shared by
		// the project's sessions is recorded once, so one unreachable project
		// cannot fill the error list with a copy for each of its sessions.
		for _, part := range res.parts {
			projectKey := part.project.String()
			switch part.outcome {
			case partNotEnabled:
				recordNotEnabled(projectKey)
			case partProjectNotFound:
				failedWithoutToken(part.project.Host)
				recordProjectNotFound(projectKey)
			case partUnsupported:
				recordUnsupported(meta.AgentType, part.project.Host)
			case partRefused:
				if part.reportOnce != "" {
					recordOnce(part.reportOnce, sessionID, part.err)
				} else {
					recordErr(sessionID, part.err)
				}
			case partFailed:
				failedWithoutToken(part.project.Host)
				if part.reportOnce != "" {
					recordOnce(part.reportOnce, sessionID, part.err)
				} else {
					recordErr(sessionID, part.err)
				}
			}
		}
	}
	return nil
}

type targetLookup struct {
	target target
	err    error
}

type identityLookup struct {
	id  int
	err error
}

// unsupportedAgentType reports whether GitLab rejected a request because it
// does not accept the session's agent type, as GitLab versions from before
// an agent was added do.
func unsupportedAgentType(err error) bool {
	var resp *gitlab.ErrorResponse
	return errors.As(err, &resp) && resp.StatusCode == http.StatusBadRequest && strings.Contains(resp.Message, "agent_type")
}

func resolveTarget(p projectRef, opts *options) targetLookup {
	client, err := opts.apiClient(p.Host)
	if err != nil {
		return targetLookup{err: fmt.Errorf("could not create client for %s: %w", p.Host, err)}
	}
	project, err := api.GetProject(client.Lab(), p.PathWithNamespace)
	if err != nil {
		return targetLookup{err: fmt.Errorf("could not resolve project %s on %s: %w", p.PathWithNamespace, p.Host, err)}
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
	// ParentIdempotencyKey is the agent session's ID, which the GitLab
	// sessions of its parts share.
	ParentIdempotencyKey string `json:"parent_idempotency_key,omitempty"`
	StartedAt            string `json:"started_at,omitempty"`
	GlabVersion          string `json:"glab_version,omitempty"`
	AgentVersion         string `json:"agent_version,omitempty"`
}

// sessionResponse is the response from POST /ai_agent/sessions.
type sessionResponse struct {
	ID int `json:"id"`
}

const governanceNotEnabled = "AI Governance is not turned on for this project. " +
	"Ask a group Owner or administrator to turn it on. It is not available for personal projects on GitLab.com."

// syncTypeFor reports which path is syncing the session. GitLab keeps the value
// sent when it creates the session. The hooks are the only callers that pass
// --silent without --all.
func syncTypeFor(opts *options) string {
	switch {
	case opts.all:
		return "fallback"
	case opts.silent:
		return "hook"
	default:
		return "manual"
	}
}

// newSessionRequest returns the request that creates the GitLab session for
// data, a part of agent session sessionID, under the idempotency key given.
func newSessionRequest(agentType, syncType, glabVersion string, identityID int, key, sessionID string, data *sessionData) sessionRequest {
	if agentType == "" {
		agentType = "unknown"
	}
	req := sessionRequest{
		AgentType:            agentType,
		AgentIdentityID:      identityID,
		SyncType:             syncType,
		Goal:                 data.Goal,
		IdempotencyKey:       key,
		ParentIdempotencyKey: sessionID,
		GlabVersion:          sessionVersion(strings.TrimPrefix(glabVersion, "v")),
		AgentVersion:         sessionVersion(data.AgentVersion),
	}
	if !data.StartedAt.IsZero() && time.Since(data.StartedAt) < 30*24*time.Hour {
		req.StartedAt = data.StartedAt.UTC().Format(time.RFC3339)
	}
	return req
}

// ensureSession creates a session on GitLab or returns the existing one via idempotency key.
func ensureSession(ctx context.Context, client *api.Client, projectID int64, reqBody sessionRequest) (int, error) {
	req, err := client.Lab().NewRequest(
		http.MethodPost,
		fmt.Sprintf("projects/%d/ai_agent/sessions", projectID),
		reqBody,
		[]gitlab.RequestOptionFunc{gitlab.WithContext(ctx), api.WithSafeRetries()},
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

// maxVersionLength is the longest version GitLab accepts.
const maxVersionLength = 255

// sessionVersion returns v as GitLab accepts it, or "" to leave it out. GitLab
// rejects the whole session over a version it does not accept, and the
// versions are only informational.
func sessionVersion(v string) string {
	v = strings.TrimSpace(v)
	if utf8.RuneCountInString(v) > maxVersionLength {
		return ""
	}
	return v
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

const (
	// maxEventsPerRequest is the most events GitLab accepts in one request.
	maxEventsPerRequest = 500
	// maxElementsPerRequest keeps a request under the 5,000 JSON elements that
	// older GitLab versions accept for audit events, with some margin.
	maxElementsPerRequest = 4500
	// maxBytesPerRequest keeps a request under the 5 MB body that GitLab
	// accepts for audit events, with some margin.
	maxBytesPerRequest = 4 * 1024 * 1024
)

// jsonLimitErrors are the errors GitLab's JSON validation returns for a
// request over one of its limits.
var jsonLimitErrors = []string{
	"Too many total parameters",
	"JSON body too large",
	"Array parameter too large",
	"Hash parameter too large",
	"Parameters nested too deeply",
}

// auditEventPoster posts a session's audit events to GitLab.
type auditEventPoster struct {
	client    *api.Client
	projectID int64
	sessionID int
	opts      *options
}

// post posts events in batches GitLab accepts, and returns how many events
// were too large for GitLab to accept. If a batch fails, earlier batches stay
// posted; GitLab deduplicates them by cloud_event_id when they are retried.
func (p auditEventPoster) post(ctx context.Context, events []auditEventRequest) (int, error) {
	tooLarge := 0
	for _, batch := range auditEventBatches(events) {
		n, err := p.postSplitting(ctx, batch)
		tooLarge += n
		if err != nil {
			return tooLarge, err
		}
	}
	return tooLarge, nil
}

// auditEventBatches splits events into batches of at most maxEventsPerRequest
// events, maxElementsPerRequest JSON elements and maxBytesPerRequest bytes.
func auditEventBatches(events []auditEventRequest) [][]auditEventRequest {
	var batches [][]auditEventRequest
	var batch []auditEventRequest
	elements, size := auditEventsRequestElements, auditEventsRequestBytes
	for _, event := range events {
		n := auditEventElements(event)
		// Counts a comma for every event, one more than the request has.
		b := encodedLen(event) + 1
		full := elements+n > maxElementsPerRequest || size+b > maxBytesPerRequest
		if len(batch) == maxEventsPerRequest || (len(batch) > 0 && full) {
			batches = append(batches, batch)
			batch, elements, size = nil, auditEventsRequestElements, auditEventsRequestBytes
		}
		batch = append(batch, event)
		elements += n
		size += b
	}
	if len(batch) > 0 {
		batches = append(batches, batch)
	}
	return batches
}

// postSplitting posts events, and posts each half separately if GitLab finds
// the request over one of its JSON limits. An event over a limit on its own
// can never be posted, so it is skipped and counted rather than blocking the
// rest of the session.
//
// A hook returns the error instead when the fallback sync is installed. It
// leaves the cursor where it is, so the fallback sync splits the batch and
// records any skipped events in the status file that glab minder doctor reads,
// without the hook using up the rate limit.
func (p auditEventPoster) postSplitting(ctx context.Context, events []auditEventRequest) (int, error) {
	err := p.postBatch(ctx, events)
	if !requestTooLarge(err) || (syncTypeFor(p.opts) == "hook" && fallbackSyncInstalled()) {
		return 0, err
	}
	if len(events) == 1 {
		dbg.Debugf("skipping audit event %s that is too large for GitLab: %v", events[0].CloudEventID, err)
		return 1, nil
	}
	half := len(events) / 2
	tooLarge, err := p.postSplitting(ctx, events[:half])
	if err != nil {
		return tooLarge, err
	}
	n, err := p.postSplitting(ctx, events[half:])
	return tooLarge + n, err
}

// fallbackSyncInstalled reports whether the fallback sync job is installed for
// this user. A job whose files exist but cannot be read counts as installed.
func fallbackSyncInstalled() bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	_, err = fallbacksync.InstalledBinary(runtime.GOOS, home)
	return !errors.Is(err, fallbacksync.ErrNotInstalled) && !errors.Is(err, fallbacksync.ErrUnsupportedOS)
}

func requestTooLarge(err error) bool {
	var resp *gitlab.ErrorResponse
	if !errors.As(err, &resp) || resp.StatusCode != http.StatusBadRequest {
		return false
	}
	return slices.ContainsFunc(jsonLimitErrors, func(msg string) bool {
		return strings.Contains(resp.Message, msg)
	})
}

const (
	// maxRateLimitWaits is how many times one request waits out GitLab's rate
	// limit before the sync gives up until its next run.
	maxRateLimitWaits = 5
	// maxRateLimitWait caps a single wait. GitLab asks for 60 seconds.
	maxRateLimitWait = 90 * time.Second
	// maxRateLimitWaitPerRun keeps one large session from holding up a run,
	// and the other sessions in it, for long.
	maxRateLimitWaitPerRun = 20 * time.Minute
)

// postBatch posts events in one request. The API client gives up on GitLab's
// rate limit after a few seconds because it ignores Retry-After, so a sync
// other than a hook, which must finish quickly, waits as GitLab asks.
func (p auditEventPoster) postBatch(ctx context.Context, events []auditEventRequest) error {
	for waits := 0; ; waits++ {
		err := p.postBatchOnce(ctx, events)
		wait, limited := rateLimitWait(err)
		if !limited || waits == maxRateLimitWaits || syncTypeFor(p.opts) == "hook" ||
			p.opts.rateLimitWaited+wait > maxRateLimitWaitPerRun {
			return err
		}
		p.opts.rateLimitWaited += wait
		if !p.opts.silent {
			p.opts.io.LogInfof("GitLab rate limit reached, retrying in %s\n", wait)
		}
		if err := p.opts.wait(ctx, wait); err != nil {
			return err
		}
	}
}

// rateLimitWait reports whether err is GitLab's rate limit, and how long it
// asks the client to wait.
func rateLimitWait(err error) (time.Duration, bool) {
	var resp *gitlab.ErrorResponse
	if !errors.As(err, &resp) || resp.StatusCode != http.StatusTooManyRequests {
		return 0, false
	}
	wait := time.Minute
	if resp.Response != nil {
		if seconds, err := strconv.Atoi(resp.Response.Header.Get("Retry-After")); err == nil && seconds >= 0 {
			wait = min(time.Duration(seconds)*time.Second, maxRateLimitWait)
		}
	}
	return wait, true
}

func (p auditEventPoster) postBatchOnce(ctx context.Context, events []auditEventRequest) error {
	reqBody := auditEventsRequest{
		SessionID: p.sessionID,
		Events:    events,
	}

	req, err := p.client.Lab().NewRequest(
		http.MethodPost,
		fmt.Sprintf("projects/%d/ai_agent/audit_events", p.projectID),
		reqBody,
		[]gitlab.RequestOptionFunc{gitlab.WithContext(ctx), api.WithSafeRetries()},
	)
	if err != nil {
		return fmt.Errorf("could not create audit events request: %w", err)
	}

	_, err = p.client.Lab().Do(req, nil)
	return err
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
		[]gitlab.RequestOptionFunc{gitlab.WithContext(ctx), api.WithSafeRetries()},
	)
	if err != nil {
		return fmt.Errorf("could not create complete request: %w", err)
	}

	if _, err := client.Lab().Do(req, nil); err != nil {
		return fmt.Errorf("could not complete session: %w", err)
	}

	return nil
}

// deterministicUUID derives a name-based UUIDv8 from SHA-256, the RFC 9562
// replacement for SHA-1 UUIDv5, which FIPS 140-3 mode rejects.
func deterministicUUID(toolCallID string) string {
	return uuid.NewHash(sha256.New(), uuid.NameSpaceDNS, []byte("gaig.gitlab.com/"+toolCallID), 8).String()
}
