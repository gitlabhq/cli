package proxy

import (
	"net/http"

	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/policy"
)

// Match is the result of running a Matcher against a request. When Matched
// is false the request is metadata/index/sidecar traffic that passes
// straight through. When Matched is true the request is an in-scope
// artifact download or upload.
//
// Pass gates the policy check and is deliberately fail-closed: only a
// matched request whose Coordinate the matcher fully determined sets
// Pass true and is forwarded to the policy checker. A matched request that
// leaves Pass false — because the matcher recognized an in-scope upload but
// could not determine the coordinate (for example an upload body larger than
// the inspection limit), or simply forgot to set the field — is rejected
// outright by the proxy rather than letting an un-inspectable payload
// through. Reason carries the operator message for that synthesized block.
type Match struct {
	Matched    bool
	Coordinate policy.Coordinate
	Operation  policy.Operation
	Pass       bool
	Reason     string
}

// unparseableMatch is the fail-closed result for a request a matcher has
// structurally recognized as in-scope for its ecosystem (right endpoint,
// right filename shape) but whose coordinate it cannot extract — a malformed
// upload frame, an unparseable filename, corrupt metadata. It leaves Pass
// false so the proxy blocks rather than forwarding an un-inspectable in-scope
// request, matching the over-limit upload behavior. It carries only the
// ecosystem, since the name/version are exactly what could not be parsed.
func unparseableMatch(ecosystem string, op policy.Operation, reason string) Match {
	return Match{
		Matched:    true,
		Operation:  op,
		Coordinate: policy.Coordinate{Ecosystem: ecosystem},
		Reason:     reason,
	}
}

// Matcher inspects an intercepted request and reports whether it carries an
// exact package coordinate (an artifact download or an upload). Each
// ecosystem implements one, keyed off the public-upstream URL/body shape.
// Implementations that read an upload body MUST restore req.Body so the
// upstream RoundTrip still sees the full payload.
type Matcher interface {
	Match(req *http.Request) Match
}
