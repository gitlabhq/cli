package sync

import (
	"bytes"
	"encoding/json"
	"math"
	"time"
	"unicode/utf8"

	"gitlab.com/gitlab-org/cli/internal/dbg"
)

// Event names and the content and token keys follow the audit events GitLab
// Duo Agent Platform records (duo_workflow_service/audit_events/event_types.py
// in gitlab-org/modelops/applied-ml/code-suggestions/ai-assist), so the audit
// event views show glab's events the same way. DAP records no tool outcome, so
// outcome, agent_initiated and duration_s follow GitLab's MCP server
// (ee/app/services/mcp/audit/capture_event_service.rb).
const (
	eventToolInvoked      = "ai_tool_invoked"
	eventToolResponse     = "ai_tool_response_received"
	eventToolFailed       = "ai_tool_execution_failed"
	eventUserInput        = "ai_user_input_received"
	eventResponseReceived = "ai_llm_response_received"

	// GitLab rejects an event whose encoded details exceed 10 KB, which makes
	// it reject the whole batch. Large values are capped below that, leaving
	// room for the other details, and the details as a whole are checked too.
	// Sizes are measured on the JSON encoding, because escaping lengthens
	// quotes, backslashes, newlines, and the <, > and & that GitLab escapes.
	maxValueBytes   = 8 * 1024
	maxDetailsBytes = 10*1024 - 512
	// errorMessageLimit matches the MCP server's limit.
	errorMessageLimit = 255
	// maxEventAge stays inside the 90 days GitLab accepts, because one older
	// event makes GitLab reject the whole batch.
	maxEventAge = 89 * 24 * time.Hour
)

var argumentsOmitted = map[string]string{"omitted": "arguments exceeded size limit"}

// auditEvents returns the audit events for data. Each event's ID is derived
// from its source, so posting the same data again is deduplicated by GitLab.
func auditEvents(agentType string, data *sessionData, now time.Time) []auditEventRequest {
	var events []auditEventRequest
	add := func(name, id string, at time.Time, details map[string]any) {
		if now.Sub(at) > maxEventAge {
			dbg.Debugf("dropping %s event %s from %s, older than GitLab accepts", name, id, at)
			return
		}
		details["agent_name"] = agentType
		if encodedLen(details) > maxDetailsBytes {
			if _, ok := details["tool_args"]; ok {
				details["tool_args"] = argumentsOmitted
			}
			if _, ok := details["content"]; ok {
				details["content"], details["content_truncated"] = "", true
			}
		}
		events = append(events, auditEventRequest{
			EventName:    name,
			CloudEventID: deterministicUUID(id),
			OccurredAt:   at.UTC().Format(time.RFC3339),
			Details:      details,
		})
	}

	for _, p := range data.Prompts {
		text, truncated := truncateEncoded(p.Text, maxValueBytes)
		details := map[string]any{
			"input_type":     "message",
			"content":        text,
			"content_length": utf8.RuneCountInString(p.Text),
		}
		if truncated {
			details["content_truncated"] = true
		}
		add(eventUserInput, "prompt:"+p.ID, p.Timestamp, details)
	}
	for _, call := range data.ToolCalls {
		details := map[string]any{
			"tool_name":       call.Name,
			"agent_initiated": true,
			"outcome":         "pending",
			"tool_call_id":    call.ID,
		}
		if call.Namespace != "" {
			details["tool_namespace"] = call.Namespace
		}
		if arguments := cappedArguments(call.Input); arguments != nil {
			details["tool_args"] = arguments
		}
		// The ID is the tool call ID alone, as before results were recorded,
		// so calls synced by an earlier glab version are not duplicated.
		add(eventToolInvoked, call.ID, call.Timestamp, details)
	}
	for _, r := range data.ToolResults {
		name, outcome := eventToolResponse, "success"
		details := map[string]any{"agent_initiated": true, "tool_call_id": r.CallID}
		if r.IsError {
			name, outcome = eventToolFailed, "error"
			details["error_message"], _ = truncateRunes(r.Error, errorMessageLimit)
		}
		if r.Outcome != "" {
			outcome = r.Outcome
		}
		details["outcome"] = outcome
		if r.Name != "" {
			details["tool_name"] = r.Name
		}
		if r.Namespace != "" {
			details["tool_namespace"] = r.Namespace
		}
		if r.Duration > 0 {
			details["duration_s"] = r.Duration.Seconds()
		}
		add(name, r.CallID+":result", r.Timestamp, details)
	}
	for _, r := range data.Responses {
		details := map[string]any{"prompt_token_count": r.Usage.InputTokens, "completion_token_count": r.Usage.OutputTokens}
		if r.Model != "" {
			details["model_name"] = r.Model
		}
		if r.Usage.CacheReadInputTokens > 0 {
			details["cache_read_input_tokens"] = r.Usage.CacheReadInputTokens
		}
		if r.Usage.CacheCreationInputTokens > 0 {
			details["cache_creation_input_tokens"] = r.Usage.CacheCreationInputTokens
		}
		add(eventResponseReceived, "response:"+r.ID, r.Timestamp, details)
	}
	return events
}

// cappedArguments returns a tool call's arguments with their secrets
// redacted, or a marker when they are too large or not valid JSON. The size is
// checked before redaction. Arguments that redaction lengthens past GitLab's
// limit are omitted by the check on the whole event. DAP's tool_args is an
// object, so other values are wrapped in one.
func cappedArguments(input json.RawMessage) any {
	if len(input) == 0 || string(input) == "null" {
		return nil
	}
	if !json.Valid(input) || encodedLen(input) > maxValueBytes {
		return argumentsOmitted
	}
	redacted := redactJSON(input)
	if trimmed := bytes.TrimSpace(redacted); trimmed[0] != '{' {
		return map[string]json.RawMessage{"input": trimmed}
	}
	return redacted
}

// encodedLen returns the length of v encoded as JSON with <, > and & escaped,
// as GitLab measures it, or a length over every limit if v cannot be encoded.
func encodedLen(v any) int {
	encoded, err := json.Marshal(v) //nolint:forbidigo // measuring, not output
	if err != nil {
		return math.MaxInt
	}
	return len(encoded)
}

// auditEventsRequestElements is the number of JSON elements an
// auditEventsRequest has before its events: the request object and its two
// members.
const auditEventsRequestElements = 5

// auditEventElements returns the number of JSON elements event adds to a
// request, or a number over every limit if it cannot be encoded.
func auditEventElements(event auditEventRequest) int {
	encoded, err := json.Marshal(event) //nolint:forbidigo // measuring, not output
	if err != nil {
		return math.MaxInt / 2
	}
	var v any
	if err := json.Unmarshal(encoded, &v); err != nil {
		return math.MaxInt / 2
	}
	return 1 + jsonElements(v)
}

// jsonElements counts the elements in v the way GitLab's JSON validation
// does: two for each object member and one for each array item, plus the
// elements in each value.
func jsonElements(v any) int {
	n := 0
	switch v := v.(type) {
	case map[string]any:
		for _, child := range v {
			n += 2 + jsonElements(child)
		}
	case []any:
		for _, child := range v {
			n += 1 + jsonElements(child)
		}
	}
	return n
}

// truncateEncoded shortens s, without splitting a character, until its JSON
// encoding is at most limit bytes, and reports whether it did.
func truncateEncoded(s string, limit int) (string, bool) {
	if encodedLen(s) <= limit {
		return s, false
	}
	runeStart := func(cut int) int {
		for cut > 0 && cut < len(s) && !utf8.RuneStart(s[cut]) {
			cut--
		}
		return cut
	}
	// The encoded length only grows as the cut moves right, so the longest
	// prefix that fits is found by binary search.
	lo, hi := 0, len(s)
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if encodedLen(s[:runeStart(mid)]) <= limit {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	return s[:runeStart(lo)], true
}

// truncateRunes shortens s to at most limit characters, and reports whether
// it did.
func truncateRunes(s string, limit int) (string, bool) {
	if utf8.RuneCountInString(s) <= limit {
		return s, false
	}
	return string([]rune(s)[:limit]), true
}
