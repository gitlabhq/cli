//go:build !integration

package api

import (
	"bytes"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
	"gitlab.com/gitlab-org/cli/test"
)

func Test_groupGraphQLVariables(t *testing.T) {
	tests := []struct {
		name string
		args map[string]any
		want map[string]any
	}{
		{
			name: "empty",
			args: map[string]any{},
			want: map[string]any{},
		},
		{
			name: "query only",
			args: map[string]any{
				"query": "QUERY",
			},
			want: map[string]any{
				"query": "QUERY",
			},
		},
		{
			name: "variables only",
			args: map[string]any{
				"name": "gitlab-bot",
			},
			want: map[string]any{
				"variables": map[string]any{
					"name": "gitlab-bot",
				},
			},
		},
		{
			name: "query + variables",
			args: map[string]any{
				"query": "QUERY",
				"name":  "gitlab-bot",
				"power": 9001,
			},
			want: map[string]any{
				"query": "QUERY",
				"variables": map[string]any{
					"name":  "gitlab-bot",
					"power": 9001,
				},
			},
		},
		{
			name: "query + operationName + variables",
			args: map[string]any{
				"query":         "query Q1{} query Q2{}",
				"operationName": "Q1",
				"power":         9001,
			},
			want: map[string]any{
				"query":         "query Q1{} query Q2{}",
				"operationName": "Q1",
				"variables": map[string]any{
					"power": 9001,
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := groupGraphQLVariables(tt.args)
			assert.Equal(t, tt.want, got)
		})
	}
}

type roundTripFunc func(r *http.Request) (*http.Response, error)

func (s roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return s(r)
}

func Test_parseStringArrayField_emptyArray(t *testing.T) {
	t.Parallel()

	assert.Empty(t, parseStringArrayField("[]"))
	assert.Empty(t, parseStringArrayField("[  ]"))
}

func Test_httpRequest(t *testing.T) {
	test.ClearEnvironmentVariables(t)

	client := &http.Client{}
	client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		t.Log("Tsti")
		return &http.Response{
			StatusCode: http.StatusOK,
			Request:    req,
		}, nil
	})

	type args struct {
		host    string
		method  string
		p       string
		params  any
		headers []string
	}
	type expects struct {
		method  string
		u       string
		body    string
		headers string
	}
	tests := []struct {
		name    string
		args    args
		want    expects
		wantErr bool
	}{
		{
			name: "simple GET",
			args: args{
				host:    "gitlab.com",
				method:  http.MethodGet,
				p:       "projects/gitlab-com%2Fwww-gitlab-com",
				params:  nil,
				headers: []string{},
			},
			wantErr: false,
			want: expects{
				method:  http.MethodGet,
				u:       "https://gitlab.com/api/v4/projects/gitlab-com%2Fwww-gitlab-com",
				body:    "",
				headers: "Private-Token: OTOKEN\r\nUser-Agent: glab test client\r\n",
			},
		},
		{
			name: "GET with leading slash",
			args: args{
				host:    "gitlab.com",
				method:  http.MethodGet,
				p:       "/projects/gitlab-com%2Fwww-gitlab-com",
				params:  nil,
				headers: []string{},
			},
			wantErr: false,
			want: expects{
				method:  http.MethodGet,
				u:       "https://gitlab.com/api/v4/projects/gitlab-com%2Fwww-gitlab-com",
				body:    "",
				headers: "Private-Token: OTOKEN\r\nUser-Agent: glab test client\r\n",
			},
		},
		{
			name: "GET with params",
			args: args{
				host:   "gitlab.com",
				method: http.MethodGet,
				p:      "projects/gitlab-com%2Fwww-gitlab-com",
				params: map[string]any{
					"a": "b",
				},
				headers: []string{},
			},
			wantErr: false,
			want: expects{
				method:  http.MethodGet,
				u:       "https://gitlab.com/api/v4/projects/gitlab-com%2Fwww-gitlab-com?a=b",
				body:    "",
				headers: "Private-Token: OTOKEN\r\nUser-Agent: glab test client\r\n",
			},
		},
		{
			name: "POST GraphQL",
			args: args{
				host:   "gitlab.com",
				method: http.MethodPost,
				p:      "graphql",
				params: map[string]any{
					"a": []byte("b"),
				},
				headers: []string{},
			},
			wantErr: false,
			want: expects{
				method:  http.MethodPost,
				u:       "https://gitlab.com/api/graphql",
				body:    `{"variables":{"a":"b"}}`,
				headers: "Content-Type: application/json; charset=utf-8\r\nPrivate-Token: OTOKEN\r\nUser-Agent: glab test client\r\n",
			},
		},
		{
			name: "POST with body and type",
			args: args{
				host:   "gitlab.com",
				method: http.MethodPost,
				p:      "projects",
				params: bytes.NewBufferString("CUSTOM"),
				headers: []string{
					"content-type: text/plain",
					"accept: application/json",
				},
			},
			wantErr: false,
			want: expects{
				method:  http.MethodPost,
				u:       "https://gitlab.com/api/v4/projects",
				body:    `CUSTOM`,
				headers: "Accept: application/json\r\nContent-Type: text/plain\r\nPrivate-Token: OTOKEN\r\nUser-Agent: glab test client\r\n",
			},
		},
		{
			name: "POST with string array field and type",
			args: args{
				host:   "gitlab.com",
				method: http.MethodPost,
				p:      "projects",
				params: map[string]any{"scopes": "[api, read_api]"},
				headers: []string{
					"content-type: application/json",
					"accept: application/json",
				},
			},
			wantErr: false,
			want: expects{
				method:  http.MethodPost,
				u:       "https://gitlab.com/api/v4/projects",
				body:    `{"scopes":["api","read_api"]}`,
				headers: "Accept: application/json\r\nContent-Type: application/json\r\nPrivate-Token: OTOKEN\r\nUser-Agent: glab test client\r\n",
			},
		},
	}
	for _, tt := range tests {
		var options []api.ClientOption
		httpClient := cmdtest.NewTestApiClient(t, client, "OTOKEN", tt.args.host, options...)
		t.Run(tt.name, func(t *testing.T) {
			got, err := httpRequest(t.Context(), httpClient, tt.args.method, tt.args.p, tt.args.params, tt.args.headers)
			if (err != nil) != tt.wantErr {
				t.Errorf("httpRequest() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !assert.NotNil(t, got) {
				return
			}
			defer got.Body.Close()
			req := got.Request
			if req.Method != tt.want.method {
				t.Errorf("Request.Method = %q, want %q", req.Method, tt.want.method)
			}
			if req.URL.String() != tt.want.u {
				t.Errorf("Request.URL = %q, want %q", req.URL.String(), tt.want.u)
			}
			if tt.want.body != "" {
				bb, err := io.ReadAll(req.Body)
				if err != nil {
					t.Errorf("Request.Body ReadAll error = %v", err)
					return
				}
				if string(bb) != tt.want.body {
					t.Errorf("Request.Body = %q, want %q", string(bb), tt.want.body)
				}
			}

			h := bytes.Buffer{}
			err = req.Header.WriteSubset(&h, map[string]bool{})
			if err != nil {
				t.Errorf("Request.Header WriteSubset error = %v", err)
				return
			}
			if h.String() != tt.want.headers {
				t.Errorf("Request.Header = %q, want %q", h.String(), tt.want.headers)
			}
		})
	}
}

func Test_httpRequest_invalidURLReturnsError(t *testing.T) {
	test.ClearEnvironmentVariables(t)

	client := &http.Client{}
	client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("HTTP request made with invalid URL")
	})

	httpClient := cmdtest.NewTestApiClient(t, client, "OTOKEN", "gitlab.com")
	_, err := httpRequest(t.Context(), httpClient, http.MethodGet, "http://host:abc", nil, nil) //nolint:bodyclose // transport returns nil response on invalid-URL error path
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid request URL")
}

func Test_addQuery(t *testing.T) {
	type args struct {
		path   string
		params map[string]any
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{
			name: "string",
			args: args{
				path:   "",
				params: map[string]any{"a": "hello"},
			},
			want: "?a=hello",
		},
		{
			name: "append",
			args: args{
				path:   "path",
				params: map[string]any{"a": "b"},
			},
			want: "path?a=b",
		},
		{
			name: "append query",
			args: args{
				path:   "path?foo=bar",
				params: map[string]any{"a": "b"},
			},
			want: "path?foo=bar&a=b",
		},
		{
			name: "[]byte",
			args: args{
				path:   "",
				params: map[string]any{"a": []byte("hello")},
			},
			want: "?a=hello",
		},
		{
			name: "int",
			args: args{
				path:   "",
				params: map[string]any{"a": 123},
			},
			want: "?a=123",
		},
		{
			name: "nil",
			args: args{
				path:   "",
				params: map[string]any{"a": nil},
			},
			want: "?a=",
		},
		{
			name: "bool",
			args: args{
				path:   "",
				params: map[string]any{"a": true, "b": false},
			},
			want: "?a=true&b=false",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got, err := parseQuery(tt.args.path, tt.args.params); got != tt.want {
				if err != nil {
					t.Error(err.Error())
				}
				t.Errorf("parseQuery() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_buildMultipartBody(t *testing.T) {
	t.Parallel()

	t.Run("text fields only", func(t *testing.T) {
		t.Parallel()
		body, contentType := buildMultipartBody(
			[]string{"branch=main", "message=hello world"},
			io.NopCloser(bytes.NewReader(nil)),
		)

		mediaType, params, err := mime.ParseMediaType(contentType)
		require.NoError(t, err)
		assert.Equal(t, "multipart/form-data", mediaType)

		mr := multipart.NewReader(body, params["boundary"])

		part, err := mr.NextPart()
		require.NoError(t, err)
		assert.Equal(t, "branch", part.FormName())
		val, _ := io.ReadAll(part)
		assert.Equal(t, "main", string(val))

		part, err = mr.NextPart()
		require.NoError(t, err)
		assert.Equal(t, "message", part.FormName())
		val, _ = io.ReadAll(part)
		assert.Equal(t, "hello world", string(val))
	})

	t.Run("file field via @filepath", func(t *testing.T) {
		t.Parallel()
		tmp := t.TempDir()
		f := filepath.Join(tmp, "upload.txt")
		require.NoError(t, os.WriteFile(f, []byte("file content"), 0o600))

		body, contentType := buildMultipartBody(
			[]string{"file=@" + f, "branch=main"},
			io.NopCloser(bytes.NewReader(nil)),
		)

		mediaType, params, err := mime.ParseMediaType(contentType)
		require.NoError(t, err)
		assert.Equal(t, "multipart/form-data", mediaType)

		mr := multipart.NewReader(body, params["boundary"])

		// first part: the file
		part, err := mr.NextPart()
		require.NoError(t, err)
		assert.Equal(t, "file", part.FormName())
		assert.Equal(t, "upload.txt", part.FileName())
		val, _ := io.ReadAll(part)
		assert.Equal(t, "file content", string(val))

		// second part: text field
		part, err = mr.NextPart()
		require.NoError(t, err)
		assert.Equal(t, "branch", part.FormName())
		val, _ = io.ReadAll(part)
		assert.Equal(t, "main", string(val))
	})

	t.Run("file field via stdin (@-)", func(t *testing.T) {
		t.Parallel()
		stdin := io.NopCloser(bytes.NewBufferString("stdin content"))
		body, contentType := buildMultipartBody(
			[]string{"file=@-"},
			stdin,
		)

		_, params, err := mime.ParseMediaType(contentType)
		require.NoError(t, err)

		mr := multipart.NewReader(body, params["boundary"])
		part, err := mr.NextPart()
		require.NoError(t, err)
		assert.Equal(t, "file", part.FormName())
		val, _ := io.ReadAll(part)
		assert.Equal(t, "stdin content", string(val))
	})

	t.Run("missing file returns error", func(t *testing.T) {
		t.Parallel()
		// Errors propagate through the pipe reader, not at construction time.
		body, _ := buildMultipartBody(
			[]string{"file=@/nonexistent/path/file.bin"},
			io.NopCloser(bytes.NewReader(nil)),
		)
		_, readErr := io.ReadAll(body)
		require.Error(t, readErr)
	})

	t.Run("malformed field returns error", func(t *testing.T) {
		t.Parallel()
		// Errors propagate through the pipe reader, not at construction time.
		body, _ := buildMultipartBody(
			[]string{"no-equals-sign"},
			io.NopCloser(bytes.NewReader(nil)),
		)
		_, readErr := io.ReadAll(body)
		require.Error(t, readErr)
	})
}
