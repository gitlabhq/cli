package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/http/httputil"
	"net/url"
	"slices"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
)

var sensitiveHeaders = []string{
	gitlab.AccessTokenHeaderName,
	gitlab.JobTokenHeaderName,
	"Authorization",
	"Proxy-Authorization",
	"Cf-Access-Client-Secret",
}

type debugTransport struct {
	rt http.RoundTripper
	w  io.Writer
}

func (d *debugTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	reqDump, err := dumpRequestRedacted(req)
	if err != nil {
		return nil, err
	}
	fmt.Fprintf(d.w, "REQUEST:\n%s\n\n", reqDump)

	// Do request
	resp, err := d.rt.RoundTrip(req)
	if err != nil {
		return nil, err
	}

	respDump, err := dumpResponseRedacted(resp)
	if err != nil {
		return nil, err
	}
	fmt.Fprintf(d.w, "RESPONSE:\n%s\n\n", respDump)

	return resp, nil
}

// sensitiveBodyFields are the fields that carry a credential, in a JSON body
// at any depth, a form-encoded body or a query string. In responses: the
// token returned by an agent session token mint, `glab artifact-registry
// get-token`, `glab cluster agent get-token`, or `glab token create`; the
// tokens of an OAuth token response and the device code of an OAuth device
// authorization; the runner registration token in the project and group
// responses a maintainer gets; and the trigger tokens in a project's trigger
// list or a GraphQL response. In requests: the pipeline trigger token that
// `glab ci run-trig` sends, and the private_token and job_token parameters
// GitLab accepts in place of a token header.
var sensitiveBodyFields = []string{"token", "access_token", "refresh_token", "id_token", "runners_token", "device_code", "private_token", "job_token"}

// sensitiveFormFields add the OAuth fields that glab's own login and token
// refresh send to /oauth/token as a form. They are not matched in JSON, where
// a field named code usually holds code.
var sensitiveFormFields = slices.Concat(sensitiveBodyFields, []string{"code", "code_verifier", "client_secret"})

// dumpRequestRedacted dumps req with sensitive headers, query parameters and
// body fields redacted, and leaves req for the server unchanged. A dump that
// fails is noted in the output, so it never stops the request.
func dumpRequestRedacted(req *http.Request) ([]byte, error) {
	r1, r2, err := drainBody(req.Body)
	if err != nil {
		return nil, err
	}
	req.Body = r2

	cloned := req.Clone(context.Background())
	cloned.Body = r1
	for _, h := range sensitiveHeaders {
		if cloned.Header.Get(h) != "" {
			cloned.Header.Set(h, "[REDACTED]")
		}
	}
	cloned.URL.RawQuery = string(redactFormFields([]byte(cloned.URL.RawQuery)))

	var body []byte
	if r1 != http.NoBody {
		if body, err = io.ReadAll(r1); err != nil {
			return nil, err
		}
		cloned.Body = io.NopCloser(bytes.NewReader(body))
	}
	dump, err := dumpRedacted(httputil.DumpRequestOut, cloned, body, redactBody(cloned.Header, body))
	if err != nil {
		dump = fmt.Appendf(nil, "(request not dumped: %v)\n", err)
	}
	return dump, nil
}

// dumpResponseRedacted dumps resp with sensitive body fields redacted, and
// leaves resp's body for the caller unchanged.
func dumpResponseRedacted(resp *http.Response) ([]byte, error) {
	if resp.Body == nil || resp.Body == http.NoBody {
		return httputil.DumpResponse(resp, true)
	}
	body, err := io.ReadAll(resp.Body)
	err = errors.Join(err, resp.Body.Close())
	if err != nil {
		return nil, err
	}
	resp.Body = io.NopCloser(bytes.NewReader(body))
	return dumpRedacted(httputil.DumpResponse, resp, body, redactBody(resp.Header, body))
}

// dumpRedacted dumps msg exactly as httputil does when redacting left its
// body unchanged. Otherwise it dumps msg's headers as sent, so Content-Length
// stays the wire value, followed by the redacted body.
func dumpRedacted[T any](dump func(T, bool) ([]byte, error), msg T, body, redacted []byte) ([]byte, error) {
	if bytes.Equal(body, redacted) {
		return dump(msg, true)
	}
	head, err := dump(msg, false)
	if err != nil {
		return nil, err
	}
	return append(head, redacted...), nil
}

// redactBody redacts the sensitive fields of a form-encoded body, or of a JSON
// body at any depth. Any other body is returned unchanged.
func redactBody(header http.Header, body []byte) []byte {
	if mediaType, _, _ := mime.ParseMediaType(header.Get("Content-Type")); mediaType == "application/x-www-form-urlencoded" {
		return redactFormFields(body)
	}
	return redactBodyFields(body)
}

// redactBodyFields replaces the values of sensitive fields at any depth of a
// JSON body, in place, so every other byte is dumped as sent. An object under
// a sensitive field is searched rather than replaced, and null is kept. A body
// that is not JSON, and anything after the part that parses, is kept.
func redactBodyFields(body []byte) []byte {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber() // so a number such as 1e400 does not stop the walk
	type container struct {
		delim json.Delim
		// sensitive is set on an array that is the value of a sensitive field,
		// so every value in it is replaced.
		sensitive bool
	}
	var stack []container
	var out []byte
	var done int64
	key, wantKey := "", false
	for {
		start := dec.InputOffset()
		tok, err := dec.Token()
		if err != nil {
			break
		}
		inObject := len(stack) > 0 && stack[len(stack)-1].delim == '{'
		sensitive := len(stack) > 0 && (inObject && slices.Contains(sensitiveBodyFields, key) || !inObject && stack[len(stack)-1].sensitive)
		switch tok := tok.(type) {
		case json.Delim:
			if tok == '{' || tok == '[' {
				stack = append(stack, container{tok, sensitive})
			} else {
				stack = stack[:len(stack)-1]
			}
			wantKey = len(stack) > 0 && stack[len(stack)-1].delim == '{'
			continue
		case string:
			if wantKey {
				key, wantKey = tok, false
				continue
			}
		case nil:
			sensitive = false
		}
		wantKey = inObject
		if sensitive {
			// Only whitespace and the ':' or ',' before it precede the value.
			end := dec.InputOffset()
			start = end - int64(len(bytes.TrimLeft(body[start:end], " \t\r\n:,")))
			out = append(append(out, body[done:start]...), `"[REDACTED]"`...)
			done = end
		}
	}
	if out == nil {
		return body
	}
	return append(out, body[done:]...)
}

// redactFormFields replaces the sensitive values of a form-encoded body or
// query string in place, so the dump keeps the field order and encoding that
// was sent.
func redactFormFields(body []byte) []byte {
	pairs := bytes.Split(body, []byte("&"))
	redacted := false
	for i, pair := range pairs {
		key, _, _ := bytes.Cut(pair, []byte("="))
		name, err := url.QueryUnescape(string(key))
		if err != nil {
			name = string(key)
		}
		if slices.Contains(sensitiveFormFields, name) {
			pairs[i] = []byte(string(key) + "=[REDACTED]")
			redacted = true
		}
	}
	if !redacted {
		return body
	}
	return bytes.Join(pairs, []byte("&"))
}

// The code below is copied from net/http/httputil/dump.go
// Copyright 2009 The Go Authors. All rights reserved.

// drainBody reads all of b to memory and then returns two equivalent
// ReadClosers yielding the same bytes.
//
// It returns an error if the initial slurp of all bytes fails. It does not attempt
// to make the returned ReadClosers have identical error-matching behavior.
func drainBody(b io.ReadCloser) (r1, r2 io.ReadCloser, err error) { //nolint:nonamedreturns
	if b == nil || b == http.NoBody {
		// No copying needed. Preserve the magic sentinel meaning of NoBody.
		return http.NoBody, http.NoBody, nil
	}
	var buf bytes.Buffer
	if _, err = buf.ReadFrom(b); err != nil {
		return nil, b, err
	}
	if err := b.Close(); err != nil {
		return nil, b, err
	}
	return io.NopCloser(&buf), io.NopCloser(bytes.NewReader(buf.Bytes())), nil
}
