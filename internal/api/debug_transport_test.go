//go:build !integration

package api

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
)

func TestDebugTransport_RedactsTokensInResponseBodies(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		body        string
		wantDump    []string
		hidden      string
	}{
		{
			name:     "a top-level token is redacted",
			body:     `{"token":"secret-session-token","token_type":"oauth","session_id":77}`,
			wantDump: []string{`"token":"[REDACTED]"`, `"token_type":"oauth"`, `"session_id":77`},
			hidden:   "secret-session-token",
		},
		{
			name:     "OAuth access and refresh tokens are redacted",
			body:     `{"access_token":"secret-access","token_type":"Bearer","expires_in":7200,"refresh_token":"secret-refresh","scope":"api","created_at":1}`,
			wantDump: []string{`"access_token":"[REDACTED]"`, `"refresh_token":"[REDACTED]"`, `"token_type":"Bearer"`, `"expires_in":7200`, `"scope":"api"`},
			hidden:   "secret-",
		},
		{
			name:     "an OpenID Connect ID token is redacted",
			body:     `{"access_token":"secret-access","id_token":"secret-id","token_type":"Bearer"}`,
			wantDump: []string{`"id_token":"[REDACTED]"`, `"token_type":"Bearer"`},
			hidden:   "secret-",
		},
		{
			name:     "an OAuth device authorization's device code is redacted",
			body:     `{"device_code":"secret-device","user_code":"ABCD-1234","verification_uri":"https://gitlab.example.com/oauth/device","expires_in":300,"interval":5}`,
			wantDump: []string{`"device_code":"[REDACTED]"`, `"user_code":"ABCD-1234"`},
			hidden:   "secret-device",
		},
		{
			name:     "a project's runner registration token is redacted",
			body:     `{"id":1,"path_with_namespace":"group/proj","runners_token":"secret-runner"}`,
			wantDump: []string{`"runners_token":"[REDACTED]"`, `"path_with_namespace":"group/proj"`},
			hidden:   "secret-runner",
		},
		{
			name:     "a body without a token is unchanged",
			body:     `{"id":1,"name":"proj"}`,
			wantDump: []string{`{"id":1,"name":"proj"}`},
		},
		{
			name:     "a token nested under GraphQL data is redacted",
			body:     `{"data":{"project":{"pipelineTriggers":{"nodes":[{"id":"gid://gitlab/Ci::Trigger/1","token":"secret-graphql"}]}}}}`,
			wantDump: []string{`{"data":{"project":{"pipelineTriggers":{"nodes":[{"id":"gid://gitlab/Ci::Trigger/1","token":"[REDACTED]"}]}}}}`},
			hidden:   "secret-graphql",
		},
		{
			name:     "tokens in a top-level array are redacted",
			body:     `[{"id":1,"token":"secret-one","description":"deploy"},{"id":2,"token":"secret-two"}]`,
			wantDump: []string{`[{"id":1,"token":"[REDACTED]","description":"deploy"},{"id":2,"token":"[REDACTED]"}]`},
			hidden:   "secret-",
		},
		{
			name:     "the rest of the body is dumped as sent",
			body:     "{\n  \"zeta\": 9007199254740993,\n  \"ratio\": 1.50,\n  \"token\" :  \"secret-spaced\",\n  \"html\": \"\\u003cb\\u003e <i> &\"\n}\n",
			wantDump: []string{"{\n  \"zeta\": 9007199254740993,\n  \"ratio\": 1.50,\n  \"token\" :  \"[REDACTED]\",\n  \"html\": \"\\u003cb\\u003e <i> &\"\n}\n"},
			hidden:   "secret-spaced",
		},
		{
			name:     "a number too large for float64 does not stop redaction",
			body:     `{"size":1e400,"token":"secret-after-number"}`,
			wantDump: []string{`{"size":1e400,"token":"[REDACTED]"}`},
			hidden:   "secret-after-number",
		},
		{
			name:     "a token field that is not a string is searched, not replaced",
			body:     `{"token":{"id":"gid://gitlab/Clusters::AgentToken/1","token":"secret-inner"},"other":{"token":null}}`,
			wantDump: []string{`{"token":{"id":"gid://gitlab/Clusters::AgentToken/1","token":"[REDACTED]"},"other":{"token":null}}`},
			hidden:   "secret-inner",
		},
		{
			name:     "a token that is a number, a boolean or a list is redacted",
			body:     `{"token":123456789,"tokens":[1],"access_token":["secret-a", "secret-b",["secret-c"]],"refresh_token":true}`,
			wantDump: []string{`{"token":"[REDACTED]","tokens":[1],"access_token":["[REDACTED]", "[REDACTED]",["[REDACTED]"]],"refresh_token":"[REDACTED]"}`},
			hidden:   "secret-",
		},
		{
			name:        "a form-encoded response is redacted like a form-encoded request",
			contentType: "application/x-www-form-urlencoded",
			body:        "access_token=secret-form&token_type=bearer",
			wantDump:    []string{"access_token=[REDACTED]&token_type=bearer"},
			hidden:      "secret-form",
		},
		{
			name:     "every duplicated key and concatenated value is redacted",
			body:     "{\"token\":\"secret-a\",\"token\":\"secret-b\"}\n{\"token\":\"secret-c\"}\n",
			wantDump: []string{"{\"token\":\"[REDACTED]\",\"token\":\"[REDACTED]\"}\n{\"token\":\"[REDACTED]\"}\n"},
			hidden:   "secret-",
		},
		{
			name:     "JSON followed by other text is redacted",
			body:     `{"token":"secret-before-text"}<html>`,
			wantDump: []string{`{"token":"[REDACTED]"}<html>`},
			hidden:   "secret-before-text",
		},
		{
			name:     "a body that is not JSON is unchanged",
			body:     `token=not-json`,
			wantDump: []string{`token=not-json`},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				contentType := tc.contentType
				if contentType == "" {
					contentType = "application/json"
				}
				w.Header().Set("Content-Type", contentType)
				_, _ = io.WriteString(w, tc.body)
			}))
			t.Cleanup(server.Close)

			var output bytes.Buffer
			client := &http.Client{Transport: &debugTransport{rt: http.DefaultTransport, w: &output}}
			resp, err := client.Get(server.URL)
			require.NoError(t, err)
			body, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			require.NoError(t, resp.Body.Close())

			assert.Equal(t, tc.body, string(body), "the caller reads the body unchanged")
			assert.Contains(t, output.String(), fmt.Sprintf("Content-Length: %d\r\n", len(tc.body)), "the dump shows the length that is sent")
			for _, want := range tc.wantDump {
				assert.Contains(t, output.String(), want)
			}
			if tc.hidden != "" {
				assert.NotContains(t, output.String(), tc.hidden)
			}
		})
	}
}

func TestDebugTransport_RedactsCredentialsInRequestBodies(t *testing.T) {
	const form = "application/x-www-form-urlencoded"
	tests := []struct {
		name        string
		contentType string
		body        string
		wantDump    []string
		hidden      string
	}{
		{
			name:        "an OAuth authorization code exchange",
			contentType: form,
			body:        "client_id=abc&code=secret-code&code_verifier=secret-verifier&grant_type=authorization_code&redirect_uri=http%3A%2F%2Flocalhost%3A7171%2Fauth%2Fredirect",
			wantDump:    []string{"client_id=abc&code=[REDACTED]&code_verifier=[REDACTED]&grant_type=authorization_code&redirect_uri=http%3A%2F%2Flocalhost%3A7171%2Fauth%2Fredirect"},
			hidden:      "secret-",
		},
		{
			name:        "an OAuth device access token request",
			contentType: form,
			body:        "client_id=abc&device_code=secret-device&grant_type=urn%3Aietf%3Aparams%3Aoauth%3Agrant-type%3Adevice_code",
			wantDump:    []string{"client_id=abc&device_code=[REDACTED]&grant_type=urn%3Aietf%3Aparams%3Aoauth%3Agrant-type%3Adevice_code"},
			hidden:      "secret-device",
		},
		{
			name:        "a form content type with parameters",
			contentType: form + "; charset=utf-8",
			body:        "grant_type=refresh_token&refresh_token=secret-refresh",
			wantDump:    []string{"grant_type=refresh_token&refresh_token=[REDACTED]"},
			hidden:      "secret-refresh",
		},
		{
			name:        "a pipeline trigger token in a JSON body",
			contentType: "application/json",
			body:        `{"ref":"main","token":"secret-trigger"}`,
			wantDump:    []string{`"token":"[REDACTED]"`, `"ref":"main"`},
			hidden:      "secret-trigger",
		},
		{
			name:        "a form without credentials is unchanged",
			contentType: form,
			body:        "grant_type=client_credentials&scope=api",
			wantDump:    []string{"grant_type=client_credentials&scope=api"},
		},
		{
			name:        "a code field outside a form is unchanged",
			contentType: "application/json",
			body:        `{"code":"puts 1"}`,
			wantDump:    []string{`{"code":"puts 1"}`},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var received string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				received = string(body)
				w.WriteHeader(http.StatusNoContent)
			}))
			t.Cleanup(server.Close)

			var output bytes.Buffer
			client := &http.Client{Transport: &debugTransport{rt: http.DefaultTransport, w: &output}}
			resp, err := client.Post(server.URL, tc.contentType, strings.NewReader(tc.body))
			require.NoError(t, err)
			require.NoError(t, resp.Body.Close())

			assert.Equal(t, tc.body, received, "the server receives the body unchanged")
			assert.Contains(t, output.String(), fmt.Sprintf("Content-Length: %d\r\n", len(tc.body)), "the dump shows the length that is sent")
			for _, want := range tc.wantDump {
				assert.Contains(t, output.String(), want)
			}
			if tc.hidden != "" {
				assert.NotContains(t, output.String(), tc.hidden)
			}
		})
	}
}

func TestDebugTransport_RedactsOAuthTokenRefresh(t *testing.T) {
	var received string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		received = string(body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"secret-new-access","token_type":"Bearer","expires_in":7200,"refresh_token":"secret-new-refresh"}`)
	}))
	t.Cleanup(server.Close)

	var output bytes.Buffer
	client := &http.Client{Transport: &debugTransport{rt: http.DefaultTransport, w: &output}}
	ctx := context.WithValue(t.Context(), oauth2.HTTPClient, client)
	config := &oauth2.Config{ClientID: "glab", Endpoint: oauth2.Endpoint{TokenURL: server.URL + "/oauth/token"}}
	token, err := config.TokenSource(ctx, &oauth2.Token{RefreshToken: "secret-old-refresh"}).Token()
	require.NoError(t, err)

	assert.Equal(t, "secret-new-access", token.AccessToken)
	assert.Contains(t, received, "refresh_token=secret-old-refresh", "the server receives the refresh token")
	assert.Contains(t, output.String(), "POST /oauth/token")
	assert.Contains(t, output.String(), "refresh_token=[REDACTED]")
	assert.Contains(t, output.String(), fmt.Sprintf("Content-Length: %d\r\n", len(received)))
	assert.NotContains(t, output.String(), "secret-")
}

func TestDebugTransport_SendsTheRequestWhenItCannotBeDumped(t *testing.T) {
	var output bytes.Buffer
	sent := false
	transport := &debugTransport{
		rt: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			sent = true
			return &http.Response{StatusCode: http.StatusNoContent, Body: http.NoBody, Request: req}, nil
		}),
		w: &output,
	}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "unsupported://gitlab.example.com/api/v4/user", http.NoBody)
	require.NoError(t, err)

	resp, err := transport.RoundTrip(req)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())

	assert.True(t, sent)
	assert.Contains(t, output.String(), "request not dumped")
}

func TestDebugTransport_RedactsCredentialsInTheQuery(t *testing.T) {
	var received string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received = r.URL.RawQuery
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)

	var output bytes.Buffer
	client := &http.Client{Transport: &debugTransport{rt: http.DefaultTransport, w: &output}}
	resp, err := client.Get(server.URL + "/api/v4/projects?per_page=2&private_token=secret-pat&job%5Ftoken=secret-job")
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())

	assert.Equal(t, "per_page=2&private_token=secret-pat&job%5Ftoken=secret-job", received, "the server receives the query unchanged")
	assert.Contains(t, output.String(), "GET /api/v4/projects?per_page=2&private_token=[REDACTED]&job%5Ftoken=[REDACTED] HTTP/1.1\r\n")
	assert.NotContains(t, output.String(), "secret-")
}

func TestDebugTransport_DumpsMessagesWithoutCredentialsAsHttputilDoes(t *testing.T) {
	const body = `{"id":1,"name":"proj"}`
	newResponse := func(req *http.Request) *http.Response {
		return &http.Response{
			Status: "200 OK", StatusCode: http.StatusOK, Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1,
			Header:           http.Header{"Content-Type": {"application/json"}},
			TransferEncoding: []string{"chunked"},
			ContentLength:    -1,
			Body:             io.NopCloser(strings.NewReader(body)),
			Request:          req,
		}
	}
	newRequest := func() *http.Request {
		// A reader of unknown length, so the request is sent chunked.
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "https://gitlab.example.com/api/v4/projects", io.MultiReader(strings.NewReader(body)))
		require.NoError(t, err)
		return req
	}
	wantRequest, err := httputil.DumpRequestOut(newRequest(), true)
	require.NoError(t, err)
	expected := newResponse(nil)
	wantResponse, err := httputil.DumpResponse(expected, true)
	require.NoError(t, err)
	require.NoError(t, expected.Body.Close())

	var output bytes.Buffer
	transport := &debugTransport{
		rt: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return newResponse(req), nil
		}),
		w: &output,
	}
	resp, err := transport.RoundTrip(newRequest())
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())

	assert.Equal(t, fmt.Sprintf("REQUEST:\n%s\n\nRESPONSE:\n%s\n\n", wantRequest, wantResponse), output.String())
}

type failingBody struct{ readErr, closeErr error }

func (b failingBody) Read([]byte) (int, error) { return 0, b.readErr }
func (b failingBody) Close() error             { return b.closeErr }

func TestDebugTransport_ReturnsBothTheReadAndTheCloseError(t *testing.T) {
	readErr := errors.New("read failed")
	closeErr := errors.New("close failed")
	transport := &debugTransport{
		rt: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Body: failingBody{readErr, closeErr}, Request: req}, nil
		}),
		w: io.Discard,
	}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://gitlab.example.com/api/v4/user", http.NoBody)
	require.NoError(t, err)

	resp, err := transport.RoundTrip(req) //nolint:bodyclose // no response is returned on error
	assert.Nil(t, resp)
	require.ErrorIs(t, err, readErr)
	require.ErrorIs(t, err, closeErr)
}
