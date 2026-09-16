package sync

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/google/uuid"
	"github.com/spf13/cobra"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/commands/govern/internal/gaig"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/text"
)

type options struct {
	io        *iostreams.IOStreams
	apiClient func(repoHost string) (*api.Client, error)
	baseRepo  func() (glrepo.Interface, error)
	silent    bool
	complete  bool
	project   string
	hostname  string
	agentType string
}

func NewCmd(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		io:        f.IO(),
		apiClient: f.ApiClient,
		baseRepo:  f.BaseRepo,
	}

	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Sync agent session data to GitLab. (EXPERIMENTAL)",
		Long: heredoc.Doc(`
			Read new entries from the local agent transcript since the last sync
			and POST them to GitLab as audit events.

			Called by the Stop hook after every agent turn. Also used by the
			SessionEnd hook (with --complete) to mark the session as complete.

			Project is resolved from:

			1. --project flag (requires --hostname)
			2. Git remote of the current directory
		`) + text.ExperimentalString,
		Example: heredoc.Doc(`
		    	# Sync the current agent session to GitLab
		    	$ glab govern audit sync

		    	# Sync and mark the session as completed
		    	$ glab govern audit sync --complete

		    	# Sync against a specific project
		    	$ glab govern audit sync --project my-group/my-project --hostname gitlab.com
		`),
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.agentType = detectAgentType()
			return runSync(cmd.Context(), opts)
		},
	}

	cmd.Flags().BoolVar(&opts.silent, "silent", false, "Suppress all output. Used when invoked from hooks.")
	cmd.Flags().BoolVar(&opts.complete, "complete", false, "Mark the session as completed. Used by the SessionEnd hook.")
	cmd.Flags().StringVarP(&opts.project, "project", "p", "", "Project ID or path to sync against.")
	cmd.Flags().StringVarP(&opts.hostname, "hostname", "H", "", "GitLab hostname (required with --project).")
	cmd.MarkFlagsRequiredTogether("project", "hostname")

	return cmd
}

func detectAgentType() string {
	if os.Getenv("CLAUDE_CODE_SESSION_ID") != "" {
		return "claude-code"
	}
	if os.Getenv("OPENCODE_SESSION_ID") != "" {
		return "opencode"
	}
	return ""
}

func runSync(ctx context.Context, opts *options) error {
	// Resolve project and client
	var project *gitlab.Project
	var client *api.Client
	var err error

	if opts.project != "" && opts.hostname != "" {
		client, err = opts.apiClient(opts.hostname)
		if err != nil {
			return fmt.Errorf("could not create API client: %w", err)
		}
		project, err = api.GetProject(client.Lab(), opts.project)
		if err != nil {
			return fmt.Errorf("could not fetch project %s: %w", opts.project, err)
		}
	} else {
		project, client, err = gaig.ResolveProject(opts.baseRepo, opts.apiClient)
		if err != nil {
			if !opts.silent {
				opts.io.LogErrorf("error: could not resolve project: %v\n", err)
			}
			return err
		}
	}

	if !opts.silent {
		opts.io.LogInfof("Project: %s (ID: %d)\n", project.PathWithNamespace, project.ID)
	}

	// Register or load cached agent identity
	var identityID int
	if opts.agentType != "" {
		identity, err := gaig.RegisterIdentity(client, project.ID, opts.agentType)
		if err != nil && !opts.silent {
			opts.io.LogErrorf("warning: could not cache agent identity: %v\n", err)
		}
		if identity != nil {
			identityID = identity.ID
			if !opts.silent {
				opts.io.LogInfof("Agent identity: %d (type: %s)\n", identity.ID, identity.AgentType)
			}
		}
	}

	// Single-session mode: get session ID from environment
	sessionID := os.Getenv("CLAUDE_CODE_SESSION_ID")
	if sessionID == "" {
		sessionID = os.Getenv("OPENCODE_SESSION_ID")
	}
	if sessionID == "" {
		if !opts.silent {
			opts.io.LogInfo("No active agent session detected.")
		}
		return nil
	}

	cwd, err := currentCWD()
	if err != nil {
		return fmt.Errorf("could not get current directory: %w", err)
	}

	transcriptFile, err := transcriptPath(sessionID, cwd)
	if err != nil {
		return fmt.Errorf("could not resolve transcript path: %w", err)
	}

	return syncSession(ctx, client, project, identityID, sessionID, transcriptFile, opts)
}

// syncSession syncs a single session transcript to GitLab.
func syncSession(ctx context.Context, client *api.Client, project *gitlab.Project, identityID int, sessionID, transcriptFile string, opts *options) error {
	if _, err := os.Stat(transcriptFile); err != nil {
		if !opts.silent {
			opts.io.LogInfof("No transcript found at %s\n", transcriptFile)
		}
		return nil
	}

	offset, err := readCursor(sessionID)
	if err != nil && !opts.silent {
		opts.io.LogErrorf("warning: could not read cursor: %v\n", err)
	}

	data, newOffset, err := parseTranscript(transcriptFile, offset)
	if err != nil {
		return fmt.Errorf("could not parse transcript: %w", err)
	}

	if data == nil || (len(data.ToolCalls) == 0 && !opts.complete) {
		if !opts.silent {
			opts.io.LogInfo("No new entries to sync.")
		}
		return nil
	}

	if client == nil || project == nil {
		return fmt.Errorf("client and project are required")
	}

	// Agent type is required for session creation
	if opts.agentType == "" {
		if !opts.silent {
			opts.io.LogInfo("No agent type detected -- skipping session sync.")
		}
		return nil
	}

	// Create or find the session on GitLab
	glSessionID, err := ensureSession(ctx, client, project.ID, identityID, sessionID, opts, data)
	if err != nil {
		if !opts.silent {
			opts.io.LogErrorf("warning: could not create session: %v\n", err)
		}
		return nil
	}
	if !opts.silent {
		opts.io.LogInfof("Session: %d\n", glSessionID)
	}

	// Post audit events -- only advance cursor on success
	posted := false
	if len(data.ToolCalls) > 0 && glSessionID > 0 {
		if err := postAuditEvents(ctx, client, project.ID, glSessionID, data.ToolCalls); err != nil {
			if !opts.silent {
				opts.io.LogErrorf("warning: could not post audit events: %v\n", err)
			}
			return nil // do not advance cursor on failure
		}
		posted = true
		if !opts.silent {
			opts.io.LogInfof("Posted %d audit events\n", len(data.ToolCalls))
		}
	}

	// Complete the session if requested
	if opts.complete && glSessionID > 0 {
		sha256, sha256Err := sha256File(transcriptFile)
		if sha256Err != nil && !opts.silent {
			opts.io.LogErrorf("warning: could not compute transcript checksum: %v\n", sha256Err)
		}
		if err := completeSession(ctx, client, project.ID, glSessionID, sha256); err != nil {
			if !opts.silent {
				opts.io.LogErrorf("warning: could not complete session: %v\n", err)
			}
		} else if !opts.silent {
			opts.io.LogInfo("Session marked as completed.")
		}
	}

	// Only advance cursor if we successfully posted events (or had nothing to post)
	if posted || len(data.ToolCalls) == 0 {
		if err := writeCursor(sessionID, newOffset); err != nil && !opts.silent {
			opts.io.LogErrorf("warning: could not update cursor: %v\n", err)
		}
	}

	return nil
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
func ensureSession(ctx context.Context, client *api.Client, projectID int64, identityID int, sessionID string, opts *options, data *sessionData) (int, error) {
	agentType := opts.agentType
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
func postAuditEvents(ctx context.Context, client *api.Client, projectID int64, sessionID int, calls []toolCall) error {
	events := make([]auditEventRequest, 0, len(calls))
	for _, call := range calls {
		events = append(events, auditEventRequest{
			EventName:    "ai_tool_invoked",
			CloudEventID: deterministicUUID(call.ID),
			OccurredAt:   call.Timestamp.UTC().Format(time.RFC3339),
			Details: map[string]any{
				"tool_name": call.Name,
			},
		})
	}

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
