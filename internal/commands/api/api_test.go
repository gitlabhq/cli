//go:build !integration

package api

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/google/shlex"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/git"
	"gitlab.com/gitlab-org/cli/internal/glinstance"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

// rawFields and magicFields build the ordered field list the flags produce;
// slices.Concat of the two spells out an order they were interleaved in.
func rawFields(specs ...string) []fieldFlag { return fieldFlags(specs, true) }

func magicFields(specs ...string) []fieldFlag { return fieldFlags(specs, false) }

func fieldFlags(specs []string, raw bool) []fieldFlag {
	flags := make([]fieldFlag, len(specs))
	for i, spec := range specs {
		flags[i] = fieldFlag{spec: spec, raw: raw}
	}
	return flags
}

// flagLine spells a field list the way it was typed, so a table whose cases are
// its flags can name its subtests with them.
func flagLine(fields []fieldFlag) string {
	parts := make([]string, len(fields))
	for i, f := range fields {
		flag := "-F"
		if f.raw {
			flag = "-f"
		}
		parts[i] = flag + " '" + f.spec + "'"
	}
	return strings.Join(parts, " ")
}

// sent is what one api run put on the wire.
type sent struct {
	req  *http.Request
	body []byte
}

// parseFieldsAt runs a field list through the seam apiRun reaches parseFields
// through: the method and --input together decide whether the fields become a
// JSON body, which is what makes a name holding a bracket an error. An empty
// method leaves --method unset, as apiRun sees it with no -X. The returned flag
// is that decision, so a case can assert which path its fields took.
func parseFieldsAt(t *testing.T, method, input string, fields []fieldFlag) (map[string]any, bool, error) {
	t.Helper()

	ios, stdin, _, _ := cmdtest.TestIOStreams()
	_, _ = stdin.WriteString("RAW BODY")

	opts := options{io: ios, requestMethod: http.MethodGet, requestInputFile: input, fields: fields}
	if method != "" {
		opts.requestMethod, opts.requestMethodPassed = method, true
	}
	jsonBody := opts.fieldsBecomeJSONBody(opts.methodForRequest())
	params, _, err := parseFields(&opts, jsonBody)
	return params, jsonBody, err
}

// runAPIArgv runs the command from an argv string, so cobra's flag parsing is
// part of what the case exercises: the order two field flags interleave in, and
// which of them overrides the other, are decided nowhere else. Stdin holds
// "RAW BODY" so a case can pass --input -. tr answers the request.
func runAPIArgv(t *testing.T, cli string, tr roundTripFunc) error {
	t.Helper()

	ios, stdin, _, _ := cmdtest.TestIOStreams()
	_, _ = stdin.WriteString("RAW BODY")
	a := cmdtest.NewTestApiClient(t, &http.Client{Transport: tr}, "OTOKEN", "gitlab.com")
	cmd := NewCmdApi(cmdtest.NewTestFactory(ios, cmdtest.WithApiClient(a)), nil)

	argv, err := shlex.Split(cli)
	require.NoError(t, err)
	cmd.SetArgs(argv)
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	_, err = cmd.ExecuteC()
	return err
}

// runAPIArgvRecording records the request and its body, and answers 204.
func runAPIArgvRecording(t *testing.T, cli string) (sent, error) {
	t.Helper()

	var got sent
	err := runAPIArgv(t, cli, func(req *http.Request) (*http.Response, error) {
		if req.Body != nil {
			var readErr error
			if got.body, readErr = io.ReadAll(req.Body); readErr != nil {
				return nil, readErr
			}
		}
		got.req = req
		return &http.Response{StatusCode: http.StatusNoContent, Request: req}, nil
	})
	return got, err
}

// runAPIArgvGuarded runs against a transport that fails the test if any request
// is issued at all. That is the whole assertion for a case proving the error
// fires before anything leaves; a canned response would hide it.
func runAPIArgvGuarded(t *testing.T, cli string) error {
	t.Helper()

	return runAPIArgv(t, cli, func(req *http.Request) (*http.Response, error) {
		t.Error("no request should be made")
		return nil, fmt.Errorf("not supposed to be called")
	})
}

func Test_fieldFlagValue_Set(t *testing.T) {
	newPair := func(seed []fieldFlag) (*[]fieldFlag, *fieldFlagValue, *fieldFlagValue) {
		fields := seed
		list := &fieldFlagList{fields: &fields}
		return &fields, &fieldFlagValue{list: list}, &fieldFlagValue{list: list, raw: true}
	}

	t.Run("the first value replaces a default and later values append", func(t *testing.T) {
		fields, magic, raw := newPair(rawFields("default=1"))
		require.NoError(t, magic.Set("a=1"))
		require.NoError(t, raw.Set("b=2"))
		require.NoError(t, magic.Set("c=3"))
		assert.Equal(t, slices.Concat(magicFields("a=1"), rawFields("b=2"), magicFields("c=3")), *fields)
	})

	t.Run("either flag may be the one that replaces the default", func(t *testing.T) {
		fields, magic, raw := newPair(magicFields("default=1"))
		require.NoError(t, raw.Set("a=1"))
		require.NoError(t, magic.Set("b=2"))
		assert.Equal(t, slices.Concat(rawFields("a=1"), magicFields("b=2")), *fields)
	})

	t.Run("String reports only its own flag's values", func(t *testing.T) {
		_, magic, raw := newPair(nil)
		assert.Empty(t, magic.String())
		assert.Empty(t, raw.String())
		require.NoError(t, magic.Set("a=1"))
		require.NoError(t, raw.Set("b=2"))
		require.NoError(t, magic.Set("c=3"))
		assert.Equal(t, "[a=1,c=3]", magic.String())
		assert.Equal(t, "[b=2]", raw.String())
		assert.Equal(t, "stringArray", magic.Type())
	})
}

func Test_NewCmdApi(t *testing.T) {
	ios, _, _, _ := cmdtest.TestIOStreams()
	f := cmdtest.NewTestFactory(ios)

	tests := []struct {
		name     string
		cli      string
		wants    options
		wantsErr bool
	}{
		{
			name: "no flags",
			cli:  "graphql",
			wants: options{
				hostname:            "",
				requestMethod:       http.MethodGet,
				requestMethodPassed: false,
				requestPath:         "graphql",
				requestInputFile:    "",
				fields:              nil,
				requestHeaders:      []string(nil),
				showResponseHeaders: false,
				paginate:            false,
				silent:              false,
			},
			wantsErr: false,
		},
		{
			name: "override method",
			cli:  "projects/octocat%2FSpoon-Knife -XDELETE",
			wants: options{
				hostname:            "",
				requestMethod:       http.MethodDelete,
				requestMethodPassed: true,
				requestPath:         "projects/octocat%2FSpoon-Knife",
				requestInputFile:    "",
				fields:              nil,
				requestHeaders:      []string(nil),
				showResponseHeaders: false,
				paginate:            false,
				silent:              false,
			},
			wantsErr: false,
		},
		{
			// Array element order depends on this reaching the parser intact.
			name: "with fields interleaved between the two flags",
			cli:  "graphql -F a=1 -f b=2 -F c=3",
			wants: options{
				hostname:            "",
				requestMethod:       http.MethodGet,
				requestMethodPassed: false,
				requestPath:         "graphql",
				requestInputFile:    "",
				fields:              slices.Concat(magicFields("a=1"), rawFields("b=2"), magicFields("c=3")),
				requestHeaders:      []string(nil),
				showResponseHeaders: false,
				paginate:            false,
				silent:              false,
			},
			wantsErr: false,
		},
		{
			name: "with fields",
			cli:  "graphql -f query=QUERY -F body=@file.txt",
			wants: options{
				hostname:            "",
				requestMethod:       http.MethodGet,
				requestMethodPassed: false,
				requestPath:         "graphql",
				requestInputFile:    "",
				fields:              slices.Concat(rawFields("query=QUERY"), magicFields("body=@file.txt")),
				requestHeaders:      []string(nil),
				showResponseHeaders: false,
				paginate:            false,
				silent:              false,
			},
			wantsErr: false,
		},
		{
			name: "with headers",
			cli:  "user -H 'accept: text/plain' -i",
			wants: options{
				hostname:            "",
				requestMethod:       http.MethodGet,
				requestMethodPassed: false,
				requestPath:         "user",
				requestInputFile:    "",
				fields:              nil,
				requestHeaders:      []string{"accept: text/plain"},
				showResponseHeaders: true,
				paginate:            false,
				silent:              false,
			},
			wantsErr: false,
		},
		{
			name: "with pagination",
			cli:  "projects/OWNER%2FREPO/issues --paginate",
			wants: options{
				hostname:            "",
				requestMethod:       http.MethodGet,
				requestMethodPassed: false,
				requestPath:         "projects/OWNER%2FREPO/issues",
				requestInputFile:    "",
				fields:              nil,
				requestHeaders:      []string(nil),
				showResponseHeaders: false,
				paginate:            true,
				silent:              false,
			},
			wantsErr: false,
		},
		{
			// validate reads the --method default, so fields with no -X reach
			// run() and are only then inferred to POST.
			name: "pagination with fields",
			cli:  "projects --paginate -f a=b",
			wants: options{
				hostname:            "",
				requestMethod:       http.MethodGet,
				requestMethodPassed: false,
				requestPath:         "projects",
				requestInputFile:    "",
				fields:              rawFields("a=b"),
				requestHeaders:      []string(nil),
				showResponseHeaders: false,
				paginate:            true,
				silent:              false,
			},
			wantsErr: false,
		},
		{
			name: "with silenced output",
			cli:  "projects/OWNER%2FREPO/issues --silent",
			wants: options{
				hostname:            "",
				requestMethod:       http.MethodGet,
				requestMethodPassed: false,
				requestPath:         "projects/OWNER%2FREPO/issues",
				requestInputFile:    "",
				fields:              nil,
				requestHeaders:      []string(nil),
				showResponseHeaders: false,
				paginate:            false,
				silent:              true,
			},
			wantsErr: false,
		},
		{
			name:     "POST pagination",
			cli:      "-XPOST projects/OWNER%2FREPO/issues --paginate",
			wantsErr: true,
		},
		{
			name: "GraphQL pagination",
			cli:  "-XPOST graphql --paginate",
			wants: options{
				hostname:            "",
				requestMethod:       http.MethodPost,
				requestMethodPassed: true,
				requestPath:         "graphql",
				requestInputFile:    "",
				fields:              nil,
				requestHeaders:      []string(nil),
				showResponseHeaders: false,
				paginate:            true,
				silent:              false,
			},
			wantsErr: false,
		},
		{
			name:     "input pagination",
			cli:      "--input projects/OWNER%2FREPO/issues --paginate",
			wantsErr: true,
		},
		{
			name: "with request body from file",
			cli:  "user --input myfile",
			wants: options{
				hostname:            "",
				requestMethod:       http.MethodGet,
				requestMethodPassed: false,
				requestPath:         "user",
				requestInputFile:    "myfile",
				fields:              nil,
				requestHeaders:      []string(nil),
				showResponseHeaders: false,
				paginate:            false,
				silent:              false,
			},
			wantsErr: false,
		},
		{
			name:     "no arguments",
			cli:      "",
			wantsErr: true,
		},
		{
			name: "with hostname",
			cli:  "graphql --hostname tom.petty",
			wants: options{
				hostname:            "tom.petty",
				requestMethod:       http.MethodGet,
				requestMethodPassed: false,
				requestPath:         "graphql",
				requestInputFile:    "",
				fields:              nil,
				requestHeaders:      []string(nil),
				showResponseHeaders: false,
				paginate:            false,
				silent:              false,
			},
			wantsErr: false,
		},
		{
			name:     "invalid output format",
			cli:      "projects --output xml",
			wantsErr: true,
		},
		{
			name: "valid ndjson output format",
			cli:  "projects --output ndjson",
			wants: options{
				hostname:            "",
				requestMethod:       http.MethodGet,
				requestMethodPassed: false,
				requestPath:         "projects",
				requestInputFile:    "",
				fields:              nil,
				requestHeaders:      []string(nil),
				showResponseHeaders: false,
				paginate:            false,
				silent:              false,
				outputFormat:        "ndjson",
			},
			wantsErr: false,
		},
		{
			name: "with form fields",
			cli:  `projects/:fullpath/wikis/attachments --form "file=@image.png" --form "branch=main"`,
			wants: options{
				requestMethod:       http.MethodGet,
				requestMethodPassed: false,
				requestPath:         "projects/:fullpath/wikis/attachments",
				fields:              nil,
				formFields:          []string{"file=@image.png", "branch=main"},
				requestHeaders:      []string(nil),
			},
			wantsErr: false,
		},
		{
			name:     "form and field are mutually exclusive",
			cli:      `projects --form "file=@foo.png" --field "name=test"`,
			wantsErr: true,
		},
		{
			name:     "form and raw-field are mutually exclusive",
			cli:      `projects --form "file=@foo.png" --raw-field "name=test"`,
			wantsErr: true,
		},
		{
			name:     "form and input are mutually exclusive",
			cli:      `projects --form "file=@foo.png" --input body.json`,
			wantsErr: true,
		},
		{
			name:     "form stdin used twice is invalid",
			cli:      `projects --form "file=@-" --form "other=@-"`,
			wantsErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := NewCmdApi(f, func(o *options) error {
				assert.Equal(t, tt.wants.hostname, o.hostname)
				assert.Equal(t, tt.wants.requestMethod, o.requestMethod)
				assert.Equal(t, tt.wants.requestMethodPassed, o.requestMethodPassed)
				assert.Equal(t, tt.wants.requestPath, o.requestPath)
				assert.Equal(t, tt.wants.requestInputFile, o.requestInputFile)
				assert.Equal(t, tt.wants.fields, o.fields)
				assert.Equal(t, tt.wants.formFields, o.formFields)
				assert.Equal(t, tt.wants.requestHeaders, o.requestHeaders)
				assert.Equal(t, tt.wants.showResponseHeaders, o.showResponseHeaders)
				return nil
			})

			argv, err := shlex.Split(tt.cli)
			require.NoError(t, err)
			cmd.SetArgs(argv)
			cmd.SetIn(&bytes.Buffer{})
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetErr(&bytes.Buffer{})
			_, err = cmd.ExecuteC()
			if tt.wantsErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
		})
	}
}

func Test_apiRun(t *testing.T) {
	tests := []struct {
		name         string
		options      options
		httpResponse *http.Response
		err          error
		stdout       string
		stderr       string
	}{
		{
			name: "success",
			httpResponse: &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(bytes.NewBufferString(`bam!`)),
			},
			err:    nil,
			stdout: `bam!`,
			stderr: ``,
		},
		{
			name: "show response headers",
			options: options{
				showResponseHeaders: true,
			},
			httpResponse: &http.Response{
				Proto:      "HTTP/1.1",
				Status:     "200 Okey-dokey",
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(bytes.NewBufferString(`body`)),
				Header:     http.Header{"Content-Type": []string{"text/plain"}},
			},
			err:    nil,
			stdout: "HTTP/1.1 200 Okey-dokey\nContent-Type: text/plain\r\n\r\nbody",
			stderr: ``,
		},
		{
			name: "success 204",
			httpResponse: &http.Response{
				StatusCode: http.StatusNoContent,
				Body:       nil,
			},
			err:    nil,
			stdout: ``,
			stderr: ``,
		},
		{
			name: "REST error",
			httpResponse: &http.Response{
				StatusCode: http.StatusBadRequest,
				Body:       io.NopCloser(bytes.NewBufferString(`{"message": "THIS IS FINE"}`)),
				Header:     http.Header{"Content-Type": []string{"application/json; charset=utf-8"}},
			},
			err:    cmdutils.SilentError,
			stdout: `{"message": "THIS IS FINE"}`,
			stderr: "glab: THIS IS FINE (HTTP 400)\n",
		},
		{
			name: "REST string errors",
			httpResponse: &http.Response{
				StatusCode: http.StatusBadRequest,
				Body:       io.NopCloser(bytes.NewBufferString(`{"errors": ["ALSO", "FINE"]}`)),
				Header:     http.Header{"Content-Type": []string{"application/json; charset=utf-8"}},
			},
			err:    cmdutils.SilentError,
			stdout: `{"errors": ["ALSO", "FINE"]}`,
			stderr: "glab: ALSO\nFINE\n",
		},
		{
			name: "REST string errors and we can't unmarshal",
			httpResponse: &http.Response{
				StatusCode: http.StatusBadRequest,
				Body:       io.NopCloser(bytes.NewBufferString(`{"message": {"password": ["is too short (minimum is 8 characters)"] } }`)),
				Header:     http.Header{"Content-Type": []string{"application/json; charset=utf-8"}},
			},
			err:    cmdutils.SilentError,
			stdout: `{"message": {"password": ["is too short (minimum is 8 characters)"] } }`,
			stderr: "glab: map[message:map[password:[is too short (minimum is 8 characters)]]]\n",
		},
		{
			name: "GraphQL error",
			options: options{
				requestPath: "graphql",
			},
			httpResponse: &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(bytes.NewBufferString(`{"errors": [{"message":"AGAIN"}, {"message":"FINE"}]}`)),
				Header:     http.Header{"Content-Type": []string{"application/json; charset=utf-8"}},
			},
			err:    cmdutils.SilentError,
			stdout: `{"errors": [{"message":"AGAIN"}, {"message":"FINE"}]}`,
			stderr: "glab: AGAIN\nFINE\n",
		},
		{
			name: "failure",
			httpResponse: &http.Response{
				StatusCode: http.StatusBadGateway,
				Body:       io.NopCloser(bytes.NewBufferString(`gateway timeout`)),
			},
			err:    cmdutils.SilentError,
			stdout: `gateway timeout`,
			stderr: "glab: HTTP 502\n",
		},
		{
			name: "silent",
			options: options{
				silent: true,
			},
			httpResponse: &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(bytes.NewBufferString(`body`)),
			},
			err:    nil,
			stdout: ``,
			stderr: ``,
		},
		{
			name: "show response headers even when silent",
			options: options{
				showResponseHeaders: true,
				silent:              true,
			},
			httpResponse: &http.Response{
				Proto:      "HTTP/1.1",
				Status:     "200 Okey-dokey",
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(bytes.NewBufferString(`body`)),
				Header:     http.Header{"Content-Type": []string{"text/plain"}},
			},
			err:    nil,
			stdout: "HTTP/1.1 200 Okey-dokey\nContent-Type: text/plain\r\n\r\n",
			stderr: ``,
		},
		{
			name: "REST empty array errors",
			httpResponse: &http.Response{
				StatusCode: http.StatusBadRequest,
				Body:       io.NopCloser(bytes.NewBufferString(`{"errors": []}`)),
				Header:     http.Header{"Content-Type": []string{"application/json; charset=utf-8"}},
			},
			err:    cmdutils.SilentError,
			stdout: `{"errors": []}`,
			stderr: "glab: HTTP 400\n",
		},
		{
			name: "REST nested array errors",
			httpResponse: &http.Response{
				StatusCode: http.StatusBadRequest,
				Body:       io.NopCloser(bytes.NewBufferString(`{"errors": [["nested", "error"]]}`)),
				Header:     http.Header{"Content-Type": []string{"application/json; charset=utf-8"}},
			},
			err:    cmdutils.SilentError,
			stdout: `{"errors": [["nested", "error"]]}`,
			stderr: "glab: HTTP 400\n",
		},
		{
			name: "GraphQL error with empty errors array",
			options: options{
				requestPath: "graphql",
			},
			httpResponse: &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(bytes.NewBufferString(`{"errors": []}`)),
				Header:     http.Header{"Content-Type": []string{"application/json; charset=utf-8"}},
			},
			err:    nil,
			stdout: `{"errors": []}`,
			stderr: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ios, _, stdout, stderr := cmdtest.TestIOStreams()

			tt.options.io = ios
			tt.options.baseRepo = func() (glrepo.Interface, error) {
				return nil, fmt.Errorf("not supposed to be called")
			}
			tt.options.apiClient = func(repoHost string) (*api.Client, error) {
				var tr roundTripFunc = func(req *http.Request) (*http.Response, error) {
					resp := tt.httpResponse
					resp.Request = req
					return resp, nil
				}
				return cmdtest.NewTestApiClient(t, &http.Client{Transport: tr}, "OTOKEN", "gitlab.com"), nil
			}

			err := tt.options.run(t.Context())
			if !errors.Is(err, tt.err) {
				t.Errorf("expected error %v, got %v", tt.err, err)
			}

			if stdout.String() != tt.stdout {
				t.Errorf("expected output %q, got %q", tt.stdout, stdout.String())
			}
			if stderr.String() != tt.stderr {
				t.Errorf("expected error output %q, got %q", tt.stderr, stderr.String())
			}
		})
	}
}

func Test_apiRun_paginationREST(t *testing.T) {
	ios, _, stdout, stderr := cmdtest.TestIOStreams()

	requestCount := 0
	responses := []*http.Response{
		{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewBufferString(`{"page":1}`)),
			Header: http.Header{
				"Link": []string{`<https://gitlab.com/api/v4/projects/1227/issues?page=2>; rel="next", <https://gitlab.com/api/v4/projects/1227/issues?page=3>; rel="last"`},
			},
		},
		{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewBufferString(`{"page":2}`)),
			Header: http.Header{
				"Link": []string{`<https://gitlab.com/api/v4/projects/1227/issues?page=3>; rel="next", <https://gitlab.com/api/v4/projects/1227/issues?page=3>; rel="last"`},
			},
		},
		{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewBufferString(`{"page":3}`)),
			Header:     http.Header{},
		},
	}

	var tr roundTripFunc = func(req *http.Request) (*http.Response, error) {
		resp := responses[requestCount]
		resp.Request = req
		requestCount++
		return resp, nil
	}
	a := cmdtest.NewTestApiClient(t, &http.Client{Transport: tr}, "OTOKEN", "gitlab.com")
	options := options{
		io: ios,
		baseRepo: func() (glrepo.Interface, error) {
			return nil, fmt.Errorf("not supposed to be called")
		},
		apiClient: func(repoHost string) (*api.Client, error) {
			return a, nil
		},

		requestPath: "issues",
		paginate:    true,
	}

	err := options.run(t.Context())
	require.NoError(t, err)

	assert.Equal(t, `{"page":1}{"page":2}{"page":3}`, stdout.String(), "stdout")
	assert.Empty(t, stderr.String(), "stderr")

	assert.Equal(t, "https://gitlab.com/api/v4/issues?per_page=100", responses[0].Request.URL.String())
	assert.Equal(t, "https://gitlab.com/api/v4/projects/1227/issues?page=2", responses[1].Request.URL.String())
	assert.Equal(t, "https://gitlab.com/api/v4/projects/1227/issues?page=3", responses[2].Request.URL.String())
}

// Test_apiRun_paginationREST_followsLinkURLVerbatim pins the Link header
// contract for --paginate: every page after the first is requested at exactly
// the URL the server advertised as rel="next". Re-applying the caller's fields
// duplicates array values and can override the page the server asked for. A
// method that carries its fields in the body instead of the query keeps sending
// them, so the same loop must not drop that body.
func Test_apiRun_paginationREST_followsLinkURLVerbatim(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		fields []fieldFlag
		// requestMethodPassed false leaves the --method default in place, which
		// validate accepts and method inference then turns into a POST.
		requestMethodPassed bool
		outputFormat        string
		// wantFirstQuery: each caller parameter exactly once, plus per_page.
		wantFirstQuery url.Values
		// nextPageURLs: rel="next" targets requests two and three must match.
		nextPageURLs [2]string
		// wantMethod, wantBody and wantContentType hold for all three requests,
		// not only the first: whichever of the query or the body carries the
		// caller's fields must carry them on every page.
		wantMethod      string
		wantBody        string
		wantContentType string
		// wantStdout: every page's payload, in the order the pages arrived.
		wantStdout string
	}{
		{
			name:                "array field",
			fields:              magicFields("ids=[1,2]"),
			requestMethodPassed: true,
			outputFormat:        "json",
			wantFirstQuery:      url.Values{"ids[]": {"1", "2"}, "per_page": {"100"}},
			nextPageURLs: [2]string{
				"https://gitlab.com/api/v4/issues?ids%5B%5D=1&ids%5B%5D=2&page=2&per_page=100",
				"https://gitlab.com/api/v4/issues?ids%5B%5D=1&ids%5B%5D=2&page=3&per_page=100",
			},
			wantMethod: http.MethodGet,
			wantStdout: `[{"id":1}][{"id":2}][{"id":3}]`,
		},
		{
			name:                "array field with ndjson output",
			fields:              magicFields("ids=[1,2]"),
			requestMethodPassed: true,
			outputFormat:        "ndjson",
			wantFirstQuery:      url.Values{"ids[]": {"1", "2"}, "per_page": {"100"}},
			nextPageURLs: [2]string{
				"https://gitlab.com/api/v4/issues?ids%5B%5D=1&ids%5B%5D=2&page=2&per_page=100",
				"https://gitlab.com/api/v4/issues?ids%5B%5D=1&ids%5B%5D=2&page=3&per_page=100",
			},
			wantMethod: http.MethodGet,
			wantStdout: "{\"id\":1}\n{\"id\":2}\n{\"id\":3}\n",
		},
		{
			name:                "scalar field",
			fields:              rawFields("foo=bar"),
			requestMethodPassed: true,
			outputFormat:        "json",
			wantFirstQuery:      url.Values{"foo": {"bar"}, "per_page": {"100"}},
			nextPageURLs: [2]string{
				"https://gitlab.com/api/v4/issues?foo=bar&page=2&per_page=100",
				"https://gitlab.com/api/v4/issues?foo=bar&page=3&per_page=100",
			},
			wantMethod: http.MethodGet,
			wantStdout: `[{"id":1}][{"id":2}][{"id":3}]`,
		},
		{
			name:                "caller-supplied page",
			fields:              rawFields("page=1"),
			requestMethodPassed: true,
			outputFormat:        "json",
			wantFirstQuery:      url.Values{"page": {"1"}, "per_page": {"100"}},
			nextPageURLs: [2]string{
				"https://gitlab.com/api/v4/issues?page=2&per_page=100",
				"https://gitlab.com/api/v4/issues?page=3&per_page=100",
			},
			wantMethod: http.MethodGet,
			wantStdout: `[{"id":1}][{"id":2}][{"id":3}]`,
		},
		{
			// glab api --paginate -f a=b issues, an inferred POST: the field is
			// in the body and never in the query, so every page has to re-send
			// it. per_page is still appended to the path.
			name:                "field with no method flag",
			fields:              rawFields("a=b"),
			requestMethodPassed: false,
			outputFormat:        "json",
			wantFirstQuery:      url.Values{"per_page": {"100"}},
			nextPageURLs: [2]string{
				"https://gitlab.com/api/v4/issues?page=2&per_page=100",
				"https://gitlab.com/api/v4/issues?page=3&per_page=100",
			},
			wantMethod:      http.MethodPost,
			wantBody:        `{"a":"b"}`,
			wantContentType: "application/json; charset=utf-8",
			wantStdout:      `[{"id":1}][{"id":2}][{"id":3}]`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ios, _, stdout, stderr := cmdtest.TestIOStreams()

			responses := make([]*http.Response, len(tt.nextPageURLs)+1)
			for i := range responses {
				header := http.Header{"Content-Type": []string{`application/json`}}
				if i < len(tt.nextPageURLs) {
					header.Set("Link", fmt.Sprintf(`<%s>; rel="next"`, tt.nextPageURLs[i]))
				}
				responses[i] = &http.Response{
					StatusCode: http.StatusOK,
					// A distinct payload per page, so stdout pins order.
					Body:   io.NopCloser(bytes.NewBufferString(fmt.Sprintf(`[{"id":%d}]`, i+1))),
					Header: header,
				}
			}

			requestCount := 0
			var gotMethods, gotBodies, gotContentTypes []string
			var tr roundTripFunc = func(req *http.Request) (*http.Response, error) {
				// An unadvertised page errors, so a regression fails instead of hanging.
				if requestCount >= len(responses) {
					return nil, fmt.Errorf("unexpected request %d: %s", requestCount+1, req.URL)
				}
				// The transport is the last reader of the body, so draining it
				// here records what the server would have received.
				body := ""
				if req.Body != nil {
					b, err := io.ReadAll(req.Body)
					if err != nil {
						return nil, err
					}
					body = string(b)
				}
				gotMethods = append(gotMethods, req.Method)
				gotBodies = append(gotBodies, body)
				gotContentTypes = append(gotContentTypes, req.Header.Get("Content-Type"))
				resp := responses[requestCount]
				resp.Request = req
				requestCount++
				return resp, nil
			}
			a := cmdtest.NewTestApiClient(t, &http.Client{Transport: tr}, "OTOKEN", "gitlab.com")
			options := options{
				io: ios,
				baseRepo: func() (glrepo.Interface, error) {
					return nil, fmt.Errorf("not supposed to be called")
				},
				apiClient: func(repoHost string) (*api.Client, error) {
					return a, nil
				},

				requestPath: "issues",
				// "GET" is the --method default, held whether or not -X was passed.
				requestMethod:       http.MethodGet,
				requestMethodPassed: tt.requestMethodPassed,
				fields:              tt.fields,
				paginate:            true,
				outputFormat:        tt.outputFormat,
			}

			err := options.run(t.Context())
			require.NoError(t, err)
			require.Equal(t, len(responses), requestCount, "number of requests")

			firstQuery, err := url.ParseQuery(responses[0].Request.URL.RawQuery)
			require.NoError(t, err)
			assert.Equal(t, tt.wantFirstQuery, firstQuery, "query of the first request")

			assert.Equal(t, tt.nextPageURLs[0], responses[1].Request.URL.String(),
				`request 2 must be the rel="next" URL of response 1, unmodified`)
			assert.Equal(t, tt.nextPageURLs[1], responses[2].Request.URL.String(),
				`request 3 must be the rel="next" URL of response 2, unmodified`)

			assert.Equal(t, tt.wantMethod, gotMethods[0], "method of request 1")
			assert.Equal(t, tt.wantMethod, gotMethods[1], "method of request 2")
			assert.Equal(t, tt.wantMethod, gotMethods[2], "method of request 3")

			assert.Equal(t, tt.wantBody, gotBodies[0], "body of request 1")
			assert.Equal(t, tt.wantBody, gotBodies[1], "body of request 2")
			assert.Equal(t, tt.wantBody, gotBodies[2], "body of request 3")

			assert.Equal(t, tt.wantContentType, gotContentTypes[0], "Content-Type of request 1")
			assert.Equal(t, tt.wantContentType, gotContentTypes[1], "Content-Type of request 2")
			assert.Equal(t, tt.wantContentType, gotContentTypes[2], "Content-Type of request 3")

			assert.Equal(t, tt.wantStdout, stdout.String(), "stdout")
			assert.Empty(t, stderr.String(), "stderr")
		})
	}
}

func Test_apiRun_paginationGraphQL(t *testing.T) {
	ios, _, stdout, stderr := cmdtest.TestIOStreams()

	requestCount := 0
	responses := []*http.Response{
		{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{`application/json`}},
			Body: io.NopCloser(bytes.NewBufferString(`{
				"data": {
					"nodes": ["page one"],
					"pageInfo": {
						"endCursor": "PAGE1_END",
						"hasNextPage": true
					}
				}
			}`)),
		},
		{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{`application/json`}},
			Body: io.NopCloser(bytes.NewBufferString(`{
				"data": {
					"nodes": ["page two"],
					"pageInfo": {
						"endCursor": "PAGE2_END",
						"hasNextPage": false
					}
				}
			}`)),
		},
	}

	var tr roundTripFunc = func(req *http.Request) (*http.Response, error) {
		resp := responses[requestCount]
		resp.Request = req
		requestCount++
		return resp, nil
	}
	a := cmdtest.NewTestApiClient(t, &http.Client{Transport: tr}, "OTOKEN", "gitlab.com")
	options := options{
		io: ios,
		baseRepo: func() (glrepo.Interface, error) {
			return nil, fmt.Errorf("not supposed to be called")
		},
		apiClient: func(repoHost string) (*api.Client, error) {
			return a, nil
		},

		requestMethod: http.MethodPost,
		requestPath:   "graphql",
		paginate:      true,
		outputFormat:  "json",
	}

	err := options.run(t.Context())
	require.NoError(t, err)

	assert.Contains(t, stdout.String(), `"page one"`)
	assert.Contains(t, stdout.String(), `"page two"`)
	assert.Empty(t, stderr.String(), "stderr")

	var requestData struct {
		Variables map[string]any
	}

	bb, err := io.ReadAll(responses[0].Request.Body)
	require.NoError(t, err)
	err = json.Unmarshal(bb, &requestData)
	require.NoError(t, err)
	_, hasCursor := requestData.Variables["endCursor"].(string)
	assert.False(t, hasCursor)

	bb, err = io.ReadAll(responses[1].Request.Body)
	require.NoError(t, err)
	err = json.Unmarshal(bb, &requestData)
	require.NoError(t, err)
	endCursor, hasCursor := requestData.Variables["endCursor"].(string)
	assert.True(t, hasCursor)
	assert.Equal(t, "PAGE1_END", endCursor)
}

// Test_apiRun_paginationGraphQL_bodyNotConsumedTwice verifies that the GraphQL
// pagination flow doesn't consume the response body twice, ensuring cursor
// extraction and output both work correctly.
func Test_apiRun_paginationGraphQL_bodyNotConsumedTwice(t *testing.T) {
	t.Parallel()

	ios, _, stdout, stderr := cmdtest.TestIOStreams()

	requestCount := 0
	responses := []*http.Response{
		{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{`application/json`}},
			Body: io.NopCloser(bytes.NewBufferString(`{
				"data": {
					"project": {
						"issues": {
							"nodes": [
								{"id": "1", "title": "First issue"}
							],
							"pageInfo": {
								"endCursor": "CURSOR_PAGE_1",
								"hasNextPage": true
							}
						}
					}
				}
			}`)),
		},
		{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{`application/json`}},
			Body: io.NopCloser(bytes.NewBufferString(`{
				"data": {
					"project": {
						"issues": {
							"nodes": [
								{"id": "2", "title": "Second issue"}
							],
							"pageInfo": {
								"endCursor": "CURSOR_PAGE_2",
								"hasNextPage": false
							}
						}
					}
				}
			}`)),
		},
	}

	var tr roundTripFunc = func(req *http.Request) (*http.Response, error) {
		resp := responses[requestCount]
		resp.Request = req
		requestCount++
		return resp, nil
	}
	a := cmdtest.NewTestApiClient(t, &http.Client{Transport: tr}, "OTOKEN", "gitlab.com")
	options := options{
		io: ios,
		baseRepo: func() (glrepo.Interface, error) {
			return nil, fmt.Errorf("not supposed to be called")
		},
		apiClient: func(repoHost string) (*api.Client, error) {
			return a, nil
		},

		requestMethod: http.MethodPost,
		requestPath:   "graphql",
		paginate:      true,
		outputFormat:  "json",
	}

	err := options.run(t.Context())
	require.NoError(t, err)

	// Verify both pages were output
	output := stdout.String()
	assert.Contains(t, output, "First issue")
	assert.Contains(t, output, "Second issue")
	assert.Empty(t, stderr.String(), "stderr should be empty")

	// Verify exactly 2 requests were made (one per page)
	assert.Equal(t, 2, requestCount, "expected 2 requests for pagination")

	// Verify the second request included the endCursor from the first page
	var requestData struct {
		Variables map[string]any
	}
	bb, err := io.ReadAll(responses[1].Request.Body)
	require.NoError(t, err)
	err = json.Unmarshal(bb, &requestData)
	require.NoError(t, err)
	endCursor, hasCursor := requestData.Variables["endCursor"].(string)
	assert.True(t, hasCursor, "second request should have endCursor variable")
	assert.Equal(t, "CURSOR_PAGE_1", endCursor, "endCursor should match first page's endCursor")
}

// Test_apiRun_paginationGraphQL_emptyResults tests pagination with empty result sets
func Test_apiRun_paginationGraphQL_emptyResults(t *testing.T) {
	t.Parallel()

	ios, _, stdout, stderr := cmdtest.TestIOStreams()

	responses := []*http.Response{
		{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{`application/json`}},
			Body: io.NopCloser(bytes.NewBufferString(`{
				"data": {
					"project": {
						"issues": {
							"nodes": [],
							"pageInfo": {
								"endCursor": "",
								"hasNextPage": false
							}
						}
					}
				}
			}`)),
		},
	}

	requestCount := 0
	var tr roundTripFunc = func(req *http.Request) (*http.Response, error) {
		resp := responses[requestCount]
		resp.Request = req
		requestCount++
		return resp, nil
	}
	a := cmdtest.NewTestApiClient(t, &http.Client{Transport: tr}, "OTOKEN", "gitlab.com")
	options := options{
		io: ios,
		baseRepo: func() (glrepo.Interface, error) {
			return nil, fmt.Errorf("not supposed to be called")
		},
		apiClient: func(repoHost string) (*api.Client, error) {
			return a, nil
		},

		requestMethod: http.MethodPost,
		requestPath:   "graphql",
		paginate:      true,
		outputFormat:  "json",
	}

	err := options.run(t.Context())
	require.NoError(t, err)

	// Verify empty nodes array is in output
	output := stdout.String()
	assert.Contains(t, output, "nodes")
	assert.Empty(t, stderr.String(), "stderr should be empty")

	// Should only make 1 request since hasNextPage is false
	assert.Equal(t, 1, requestCount, "expected only 1 request for empty results")
}

// Test_apiRun_paginationGraphQL_withNDJSON verifies that NDJSON output works with GraphQL pagination.
// With GraphQL, each full response object is output as one line per page (not array elements).
func Test_apiRun_paginationGraphQL_withNDJSON(t *testing.T) {
	t.Parallel()

	ios, _, stdout, stderr := cmdtest.TestIOStreams()

	requestCount := 0
	responses := []*http.Response{
		{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{`application/json`}},
			Body: io.NopCloser(bytes.NewBufferString(`{
				"data": {
					"nodes": [
						{"id": "1", "title": "Issue 1"}
					],
					"pageInfo": {
						"endCursor": "CURSOR_1",
						"hasNextPage": true
					}
				}
			}`)),
		},
		{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{`application/json`}},
			Body: io.NopCloser(bytes.NewBufferString(`{
				"data": {
					"nodes": [
						{"id": "2", "title": "Issue 2"}
					],
					"pageInfo": {
						"endCursor": "CURSOR_2",
						"hasNextPage": false
					}
				}
			}`)),
		},
	}

	var tr roundTripFunc = func(req *http.Request) (*http.Response, error) {
		resp := responses[requestCount]
		resp.Request = req
		requestCount++
		return resp, nil
	}
	a := cmdtest.NewTestApiClient(t, &http.Client{Transport: tr}, "OTOKEN", "gitlab.com")
	options := options{
		io: ios,
		baseRepo: func() (glrepo.Interface, error) {
			return nil, fmt.Errorf("not supposed to be called")
		},
		apiClient: func(repoHost string) (*api.Client, error) {
			return a, nil
		},

		requestMethod: http.MethodPost,
		requestPath:   "graphql",
		paginate:      true,
		outputFormat:  "ndjson",
	}

	err := options.run(t.Context())
	require.NoError(t, err)

	// NDJSON with GraphQL outputs each full response object as one line
	output := stdout.String()
	lines := strings.Split(strings.TrimSpace(output), "\n")

	// Each GraphQL response is output as a complete object per line
	assert.GreaterOrEqual(t, len(lines), 2, "should have at least 2 lines")

	// Verify both issues appear in the output
	assert.Contains(t, output, "Issue 1")
	assert.Contains(t, output, "Issue 2")
	assert.Empty(t, stderr.String(), "stderr should be empty")

	// Verify that both pages were fetched
	assert.Equal(t, 2, requestCount, "should have made 2 requests for pagination")
}

func Test_apiRun_ndjson(t *testing.T) {
	t.Parallel()

	ios, _, stdout, stderr := cmdtest.TestIOStreams()

	var tr roundTripFunc = func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{`application/json`}},
			Body:       io.NopCloser(bytes.NewBufferString(`[{"id":1,"title":"Issue 1"},{"id":2,"title":"Issue 2"}]`)),
			Request:    req,
		}, nil
	}
	a := cmdtest.NewTestApiClient(t, &http.Client{Transport: tr}, "OTOKEN", "gitlab.com")
	options := options{
		io: ios,
		baseRepo: func() (glrepo.Interface, error) {
			return nil, fmt.Errorf("not supposed to be called")
		},
		apiClient: func(repoHost string) (*api.Client, error) {
			return a, nil
		},
		requestPath:  "issues",
		outputFormat: "ndjson",
	}

	err := options.run(t.Context())
	require.NoError(t, err)

	// NDJSON should output each element on a separate line
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	assert.Len(t, lines, 2, "should have 2 lines")

	// Verify each line is valid JSON
	var obj1, obj2 map[string]any
	require.NoError(t, json.Unmarshal([]byte(lines[0]), &obj1))
	require.NoError(t, json.Unmarshal([]byte(lines[1]), &obj2))

	assert.InDelta(t, 1, obj1["id"], 0)
	assert.Equal(t, "Issue 1", obj1["title"])
	assert.InDelta(t, 2, obj2["id"], 0)
	assert.Equal(t, "Issue 2", obj2["title"])
	assert.Empty(t, stderr.String(), "stderr")
}

func Test_apiRun_ndjson_singleObject(t *testing.T) {
	t.Parallel()

	ios, _, stdout, stderr := cmdtest.TestIOStreams()

	var tr roundTripFunc = func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{`application/json`}},
			Body:       io.NopCloser(bytes.NewBufferString(`{"id":1,"title":"Single Issue"}`)),
			Request:    req,
		}, nil
	}
	a := cmdtest.NewTestApiClient(t, &http.Client{Transport: tr}, "OTOKEN", "gitlab.com")
	options := options{
		io: ios,
		baseRepo: func() (glrepo.Interface, error) {
			return nil, fmt.Errorf("not supposed to be called")
		},
		apiClient: func(repoHost string) (*api.Client, error) {
			return a, nil
		},
		requestPath:  "issues/1",
		outputFormat: "ndjson",
	}

	err := options.run(t.Context())
	require.NoError(t, err)

	// Single object should be output as one line
	output := strings.TrimSpace(stdout.String())
	assert.Len(t, strings.Split(output, "\n"), 1, "should have 1 line")

	var obj map[string]any
	require.NoError(t, json.Unmarshal([]byte(output), &obj))
	assert.InDelta(t, 1, obj["id"], 0)
	assert.Equal(t, "Single Issue", obj["title"])
	assert.Empty(t, stderr.String(), "stderr")
}

func Test_streamNDJSON_preservesLargeNumber(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	err := streamNDJSON(strings.NewReader("18446744073709551615"), &out)

	require.NoError(t, err)
	assert.Equal(t, "18446744073709551615\n", out.String())
}

func Test_parseErrorResponse_doesNotAppendPlus(t *testing.T) {
	t.Parallel()

	_, message, err := parseErrorResponse(strings.NewReader(`["request failed"]`), http.StatusBadRequest)

	require.NoError(t, err)
	assert.Equal(t, "[request failed]", message)
}

func Test_apiRun_ndjson_pagination(t *testing.T) {
	t.Parallel()

	ios, _, stdout, stderr := cmdtest.TestIOStreams()

	requestCount := 0
	responses := []*http.Response{
		{
			StatusCode: http.StatusOK,
			Header: http.Header{
				"Content-Type": []string{`application/json`},
				"Link":         []string{`<https://gitlab.com/api/v4/issues?page=2>; rel="next"`},
			},
			Body: io.NopCloser(bytes.NewBufferString(`[{"id":1,"title":"Issue 1"}]`)),
		},
		{
			StatusCode: http.StatusOK,
			Header: http.Header{
				"Content-Type": []string{`application/json`},
			},
			Body: io.NopCloser(bytes.NewBufferString(`[{"id":2,"title":"Issue 2"}]`)),
		},
	}

	var tr roundTripFunc = func(req *http.Request) (*http.Response, error) {
		resp := responses[requestCount]
		resp.Request = req
		requestCount++
		return resp, nil
	}
	a := cmdtest.NewTestApiClient(t, &http.Client{Transport: tr}, "OTOKEN", "gitlab.com")
	options := options{
		io: ios,
		baseRepo: func() (glrepo.Interface, error) {
			return nil, fmt.Errorf("not supposed to be called")
		},
		apiClient: func(repoHost string) (*api.Client, error) {
			return a, nil
		},
		requestPath:  "issues",
		paginate:     true,
		outputFormat: "ndjson",
	}

	err := options.run(t.Context())
	require.NoError(t, err)

	// Should have 2 lines total (one from each page)
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	assert.Len(t, lines, 2, "should have 2 lines from 2 pages")

	var obj1, obj2 map[string]any
	require.NoError(t, json.Unmarshal([]byte(lines[0]), &obj1))
	require.NoError(t, json.Unmarshal([]byte(lines[1]), &obj2))

	assert.InDelta(t, 1, obj1["id"], 0)
	assert.InDelta(t, 2, obj2["id"], 0)
	assert.Empty(t, stderr.String(), "stderr")
}

func Test_apiRun_inputFile(t *testing.T) {
	tests := []struct {
		name          string
		inputFile     string
		inputContents []byte

		contentLength    int64
		expectedContents []byte
	}{
		{
			name:          "stdin",
			inputFile:     "-",
			inputContents: []byte("I WORK OUT"),
			contentLength: 0,
		},
		{
			name:          "from file",
			inputFile:     "gitlab-test-file",
			inputContents: []byte("I WORK OUT"),
			contentLength: 10,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ios, stdin, _, _ := cmdtest.TestIOStreams()
			resp := &http.Response{StatusCode: http.StatusNoContent}

			inputFile := tt.inputFile
			if tt.inputFile == "-" {
				_, _ = stdin.Write(tt.inputContents)
			} else {
				f, err := os.CreateTemp(t.TempDir(), tt.inputFile)
				if err != nil {
					t.Fatal(err)
				}
				_, _ = f.Write(tt.inputContents)
				require.NoError(t, f.Close())
				inputFile = f.Name()
			}

			var bodyBytes []byte
			var tr roundTripFunc = func(req *http.Request) (*http.Response, error) {
				var err error
				if bodyBytes, err = io.ReadAll(req.Body); err != nil {
					return nil, err
				}
				resp.Request = req
				return resp, nil
			}
			a := cmdtest.NewTestApiClient(t, &http.Client{Transport: tr}, "OTOKEN", "gitlab.com")
			options := options{
				requestPath:      "hello",
				requestInputFile: inputFile,
				fields:           rawFields("a=b", "c=d"),

				io: ios,
				baseRepo: func() (glrepo.Interface, error) {
					return nil, fmt.Errorf("not supposed to be called")
				},
				apiClient: func(repoHost string) (*api.Client, error) {
					return a, nil
				},
			}

			err := options.run(t.Context())
			if err != nil {
				t.Errorf("got error %v", err)
			}

			assert.Equal(t, http.MethodPost, resp.Request.Method)
			assert.Equal(t, "/api/v4/hello?a=b&c=d", resp.Request.URL.RequestURI())
			assert.Equal(t, tt.contentLength, resp.Request.ContentLength)
			assert.Empty(t, resp.Request.Header.Get("Content-Type"))
			assert.Equal(t, tt.inputContents, bodyBytes)
		})
	}
}

// Test_options_methodForRequest pins the method the request is sent with. Only
// a --method flag, a field, a --form field, or --input moves it off the flag's
// GET default, and each of those four has to move it on its own: a request with
// no fields must stay a GET, and --form and --input each have to reach POST
// without help from a field.
func Test_options_methodForRequest(t *testing.T) {
	tests := []struct {
		name string
		opts options
		want string
	}{
		{name: "no fields and no --input stay GET", opts: options{requestMethod: http.MethodGet}, want: http.MethodGet},
		{name: "a field alone becomes POST", opts: options{requestMethod: http.MethodGet, fields: rawFields("a=1")}, want: http.MethodPost},
		{name: "a form field alone becomes POST", opts: options{requestMethod: http.MethodGet, formFields: []string{"a=1"}}, want: http.MethodPost},
		{name: "--input alone becomes POST", opts: options{requestMethod: http.MethodGet, requestInputFile: "-"}, want: http.MethodPost},
		{name: "an explicit --method wins over both", opts: options{requestMethod: http.MethodDelete, requestMethodPassed: true, fields: rawFields("a=1"), requestInputFile: "-"}, want: http.MethodDelete},
		{name: "an explicit --method GET is kept", opts: options{requestMethod: http.MethodGet, requestMethodPassed: true, fields: rawFields("a=1")}, want: http.MethodGet},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.opts.methodForRequest())
		})
	}
}

// Test_parseFields_queryString pins the query strings bracketed names produce,
// at the two seams apiRun builds one with: parseFields for the parameters and
// parseQuery for the bytes. The path is the one apiRun would have reached, so a
// case's expectation is the whole request URI.
//
// Reading brackets on GET and DELETE would alter commands that work now, so most
// of these are regression guards. Every case also asserts its fields took the
// query path rather than a body, which is what a method spelled "get" and an
// --input alongside a POST are here to prove. An absent method means -X GET.
func Test_parseFields_queryString(t *testing.T) {
	// An @file value is the one non-scalar that reaches an accumulated array.
	dir := t.TempDir()
	first, second := dir+"/first.txt", dir+"/second.txt"
	require.NoError(t, os.WriteFile(first, []byte("alpha"), 0o600))
	require.NoError(t, os.WriteFile(second, []byte("beta"), 0o600))

	tests := []struct {
		method  string
		input   string
		fields  []fieldFlag
		wantURI string
		wantErr string
	}{
		// The issue's own repro, minus the body, as a GET.
		{fields: rawFields("position[base_sha]=abc", "position[position_type]=text", "position[new_path]=file.kt"), wantURI: "/api/v4/hello?position%5Bbase_sha%5D=abc&position%5Bnew_path%5D=file.kt&position%5Bposition_type%5D=text"},
		{fields: rawFields("position[base_sha]=abc"), wantURI: "/api/v4/hello?position%5Bbase_sha%5D=abc"},
		{fields: rawFields("body=test", "position[base_sha]=abc"), wantURI: "/api/v4/hello?body=test&position%5Bbase_sha%5D=abc"},
		{fields: rawFields("a[b][c]=1"), wantURI: "/api/v4/hello?a%5Bb%5D%5Bc%5D=1"},
		// Also the single-value case the accumulating suffix leaves unchanged.
		{fields: rawFields("ids[]=1"), wantURI: "/api/v4/hello?ids%5B%5D=1"},
		// --field types the value, so this leaf is an int and renders as 42.
		{fields: magicFields("position[new_line]=42"), wantURI: "/api/v4/hello?position%5Bnew_line%5D=42"},
		{method: http.MethodDelete, fields: rawFields("position[base_sha]=abc", "position[new_path]=file.kt"), wantURI: "/api/v4/hello?position%5Bbase_sha%5D=abc&position%5Bnew_path%5D=file.kt"},
		// A case-sensitive method comparison would send these as a body instead.
		{method: "get", fields: rawFields("position[base_sha]=abc"), wantURI: "/api/v4/hello?position%5Bbase_sha%5D=abc"},
		{method: "delete", fields: rawFields("position[base_sha]=abc"), wantURI: "/api/v4/hello?position%5Bbase_sha%5D=abc"},
		// An empty array contributes no parameters, so no separator either.
		{fields: magicFields("a[b]=[]"), wantURI: "/api/v4/hello"},
		{fields: magicFields(`a[b]={"c":1}`), wantErr: `query parameter "a[b]": objects are not supported as query parameters; use --input or a POST, PUT, or PATCH request body`},

		// Repeated names ending in "[]" send every value, in the order typed.
		{fields: rawFields("ids[]=1", "ids[]=2"), wantURI: "/api/v4/hello?ids%5B%5D=1&ids%5B%5D=2"},
		{fields: magicFields("ids[]=1", "ids[]=2"), wantURI: "/api/v4/hello?ids%5B%5D=1&ids%5B%5D=2"},
		// Across the two flags as well: because the name collects, --field does
		// not override a --raw-field of the same name the way it does for other
		// names. This is the order that override would drop a value in.
		{fields: slices.Concat(magicFields("ids[]=1"), rawFields("ids[]=2")), wantURI: "/api/v4/hello?ids%5B%5D=1&ids%5B%5D=2"},
		{fields: rawFields("ids[]=3", "ids[]=1", "ids[]=2"), wantURI: "/api/v4/hello?ids%5B%5D=3&ids%5B%5D=1&ids%5B%5D=2"},
		{method: http.MethodDelete, fields: rawFields("ids[]=1", "ids[]=2"), wantURI: "/api/v4/hello?ids%5B%5D=1&ids%5B%5D=2"},
		{fields: rawFields("ids[]=1", "ids[]=1"), wantURI: "/api/v4/hello?ids%5B%5D=1&ids%5B%5D=1"},
		{fields: rawFields("ids[]="), wantURI: "/api/v4/hello?ids%5B%5D="},
		{fields: rawFields("ids[]=", "ids[]=2"), wantURI: "/api/v4/hello?ids%5B%5D=&ids%5B%5D=2"},
		// null has no query representation, so it encodes as an empty value.
		{fields: magicFields("ids[]=true", "ids[]=null", "ids[]=false"), wantURI: "/api/v4/hello?ids%5B%5D=true&ids%5B%5D=&ids%5B%5D=false"},
		// A []byte is the one value the array-element renderer has no case for.
		{fields: magicFields("ids[]=@"+first, "ids[]=@"+second), wantURI: "/api/v4/hello?ids%5B%5D=alpha&ids%5B%5D=beta"},
		// --input puts the fields on the query path whatever the method is.
		{method: http.MethodPost, input: "-", fields: rawFields("ids[]=1", "ids[]=2"), wantURI: "/api/v4/hello?ids%5B%5D=1&ids%5B%5D=2"},

		// Only a trailing "[]" accumulates; every other name holds one value.
		{fields: rawFields("position[base_sha]=first", "position[base_sha]=second"), wantURI: "/api/v4/hello?position%5Bbase_sha%5D=second"},
		{fields: rawFields("search=first", "search=second"), wantURI: "/api/v4/hello?search=second"},
		{fields: magicFields("search=first", "search=second"), wantURI: "/api/v4/hello?search=second"},
		// "a[][b]" contains "[]" without ending in it, so it holds one value like
		// any other name: the last raw value wins, and a --field still overrides.
		// Reading the suffix test as a substring would accumulate both instead.
		{fields: rawFields("a[][b]=1", "a[][b]=2"), wantURI: "/api/v4/hello?a%5B%5D%5Bb%5D=2"},
		{fields: slices.Concat(rawFields("a[][b]=1"), magicFields("a[][b]=2")), wantURI: "/api/v4/hello?a%5B%5D%5Bb%5D=2"},
		// A trailing "[]" after a segment does accumulate, which is the boundary
		// the two names above sit on the other side of.
		{fields: rawFields("a[b][]=1", "a[b][]=2"), wantURI: "/api/v4/hello?a%5Bb%5D%5B%5D=1&a%5Bb%5D%5B%5D=2"},
		// Only the body path rejects this, where it has no shape to advise on.
		{fields: rawFields("[]=1"), wantURI: "/api/v4/hello?%5B%5D=1"},
		// No bracket character, so the body error misses it too. Not decoded,
		// deliberately: decoding a key typed literally would be a guess. With no
		// trailing bracket there is no accumulation either, so last wins.
		{fields: rawFields("ids%5B%5D=1", "ids%5B%5D=2"), wantURI: "/api/v4/hello?ids%255B%255D=2"},

		// One flag, a name carrying its own "[]", and a JSON array value: the
		// name already spells the repetition the wire format has, so the values
		// go under it as given. A single occurrence never becomes a queryList,
		// so this reaches parseQuery as a bare array and is the one shape where
		// both sites could append a second pair and send ids[][]=.
		{fields: magicFields("ids[]=[1,2]"), wantURI: "/api/v4/hello?ids%5B%5D=1&ids%5B%5D=2"},
		// The same request as the bare name with the same array, which is the
		// point: the two spellings are one wire key, so they also collide below.
		{fields: magicFields("ids=[1,2]"), wantURI: "/api/v4/hello?ids%5B%5D=1&ids%5B%5D=2"},
		{fields: magicFields("a[b][]=[1,2]"), wantURI: "/api/v4/hello?a%5Bb%5D%5B%5D=1&a%5Bb%5D%5B%5D=2"},

		// A query string cannot express an array of arrays.
		{fields: magicFields("ids[]=[1]", "ids[]=[2]"), wantErr: `query parameter "ids[]": nested arrays and objects are not supported as query parameters; use --input or a POST, PUT, or PATCH request body`},
		{fields: magicFields(`ids[]={"a":1}`, "ids[]=2"), wantErr: `query parameter "ids[]": nested arrays and objects are not supported as query parameters; use --input or a POST, PUT, or PATCH request body`},
		// A second occurrence is what makes each occurrence one element, so the
		// array the single-flag row above sends as a whole list becomes a nested
		// element here and has no query representation. Adding a value to that
		// row's request is --input's job, not a flattening this cannot tell from
		// the array-of-arrays the two rows above reject.
		{fields: slices.Concat(magicFields("ids[]=[1,2]"), rawFields("ids[]=3")), wantErr: `query parameter "ids[]": nested arrays and objects are not supported as query parameters; use --input or a POST, PUT, or PATCH request body`},
		// Both emit ids[]=, and map order would interleave them unpredictably.
		// The message is the same whichever flag came first.
		{fields: slices.Concat(rawFields("ids[]=1"), magicFields("ids=[2,3]")), wantErr: `field names "ids" and "ids[]" both address query parameter "ids[]"; spell the array one way, or use --input`},
		{fields: slices.Concat(magicFields("ids=[2,3]"), rawFields("ids[]=1")), wantErr: `field names "ids" and "ids[]" both address query parameter "ids[]"; spell the array one way, or use --input`},
		// The same clash with the array one spelled with its own brackets, which
		// only shows up once the wire key stops picking up a second pair. Before
		// that it addressed ids[][] and this sent three values under two keys.
		{fields: slices.Concat(magicFields("ids[]=[1,2]"), magicFields("ids=[3]")), wantErr: `field names "ids" and "ids[]" both address query parameter "ids[]"; spell the array one way, or use --input`},
		{fields: slices.Concat(magicFields("ids[]=[1,2]"), rawFields("ids=3")), wantURI: "/api/v4/hello?ids=3&ids%5B%5D=1&ids%5B%5D=2"},
	}

	for _, tt := range tests {
		method := cmp.Or(tt.method, http.MethodGet)
		name := method
		if tt.input != "" {
			name += " --input " + tt.input
		}
		t.Run(name+" "+flagLine(tt.fields), func(t *testing.T) {
			params, jsonBody, err := parseFieldsAt(t, method, tt.input, tt.fields)
			assert.False(t, jsonBody, "these fields must take the query path")
			got := ""
			if err == nil {
				got, err = parseQuery("/api/v4/hello", params)
			}
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Equal(t, tt.wantErr, err.Error())
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantURI, got)
		})
	}
}

// Test_parseFields_bracketedFieldNameError pins the advice each shape of name
// earns where the fields become a JSON body, replacing a silent no-op. A name
// whose brackets describe a shape is advised with its own segments, so the fix
// is mechanical rather than an example to translate; a name that describes none
// is advised with the forms that work and no invented key.
//
// A case is the name and the advice; the rest of the message is invariant, and
// Test_apiRun_bracketNameWiring spells one whole message out. An absent method
// means no -X, which the fields alone turn into a POST; an absent field list
// means the name with a value of 1.
func Test_parseFields_bracketedFieldNameError(t *testing.T) {
	tests := []struct {
		key    string
		method string
		fields []fieldFlag
		advice string
	}{
		{key: "position[base_sha]", advice: `, for example -F 'position={"base_sha":"..."}'`},
		{key: "ids[]", advice: `, for example -F 'ids=[...]'`},
		// PUT and PATCH take a JSON body on the same terms POST does, and an
		// explicit POST is no different from the one the fields default to.
		{key: "position[base_sha]", method: http.MethodPut, advice: `, for example -F 'position={"base_sha":"..."}'`},
		{key: "position[base_sha]", method: http.MethodPatch, advice: `, for example -F 'position={"base_sha":"..."}'`},
		{key: "position[base_sha]", method: http.MethodPost, advice: `, for example -F 'position={"base_sha":"..."}'`},
		// --field is rejected on the same terms --raw-field is.
		{key: "position[new_line]", fields: magicFields("position[new_line]=42"), advice: `, for example -F 'position={"new_line":"..."}'`},
		// The boundary the emit-every-value fix does not reach: with no -X these
		// are a body, so they error rather than accumulating.
		{key: "ids[]", fields: rawFields("ids[]=1", "ids[]=2"), advice: `, for example -F 'ids=[...]'`},

		// Segments nest outermost first, and an empty one is an array.
		{key: "a[b][c]", advice: `, for example -F 'a={"b":{"c":"..."}}'`},
		{key: "a[b][]", advice: `, for example -F 'a={"b":[...]}'`},
		{key: "a[][b]", advice: `, for example -F 'a=[{"b":"..."}]'`},
		// A quote in a segment must not break the JSON the advice shows.
		{key: `a[b"c]`, advice: `, for example -F 'a={"b\"c":"..."}'`},

		// Not bracketed names, just names holding a bracket: no shape to read, so
		// the advice names the forms and invents neither a root nor an inner key.
		{key: "a[b", advice: " with -F"},
		{key: "a]", advice: " with -F"},
		{key: "[]", advice: " with -F"},
		{key: "]x", advice: " with -F"},
		{key: "[b]", advice: " with -F"},
		{key: "a[b]c", advice: " with -F"},
		{key: "a[[b]]", advice: " with -F"},

		// The first bracketed name is the one reported.
		{key: "first[x]", fields: rawFields("body=test", "first[x]=1", "second[y]=2"), advice: `, for example -F 'first={"x":"..."}'`},
		// The GraphQL spec spells a variable name /[_A-Za-z][_0-9A-Za-z]*/, so no
		// valid query can name one with a bracket: this removes no working use.
		// GraphQL adds no branch of its own ahead of the check.
		{key: "input[title]", fields: rawFields("query=query { currentUser { username } }", "input[title]=Hello"), advice: `, for example -F 'input={"title":"..."}'`},
	}

	for _, tt := range tests {
		t.Run(cmp.Or(tt.method, "default POST")+" "+tt.key, func(t *testing.T) {
			fields := tt.fields
			if fields == nil {
				fields = rawFields(tt.key + "=1")
			}
			_, jsonBody, err := parseFieldsAt(t, tt.method, "", fields)
			assert.True(t, jsonBody, "these fields must become a JSON body")
			require.Error(t, err)
			want := fmt.Sprintf("field name %q: %s%s, or use --input", tt.key, bracketedNameProblem, tt.advice)
			assert.Equal(t, want, err.Error())
		})
	}
}

// Test_parseFields_percentEncodedBracketsStayLiteral pins the silent no-op that
// survives: -f 'ids%5B%5D=1' holds no bracket character, so the body error
// misses it and the key reaches the body literally. Test_httpRequest pins the
// bytes it becomes, and Test_parseFields_queryString the query half.
func Test_parseFields_percentEncodedBracketsStayLiteral(t *testing.T) {
	params, jsonBody, err := parseFieldsAt(t, http.MethodPost, "", rawFields("ids%5B%5D=1"))

	assert.True(t, jsonBody)
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"ids%5B%5D": "1"}, params)
}

// Test_apiRun_bracketNameWiring covers what only a whole run reaches: cobra's
// flag parsing, which is the only thing that decides the order two field flags
// interleave in and which of them overrides the other; run()'s choice of method,
// which decides whether a bracketed name is an error at all; the method the
// request leaves with; and the two paths that never reach the field parser,
// --form and GraphQL. The bytes and the advice are pinned at the parseFields and
// parseQuery seams, and the first case here is the one deliberate place the
// whole error message is spelled out, so the shared clause cannot drift unseen.
func Test_apiRun_bracketNameWiring(t *testing.T) {
	tests := []struct {
		cli              string
		wantMethod       string
		wantURI          string
		wantBody         string
		wantBodyContains []string
		wantErr          string
	}{
		{
			// No -X, so the field makes this a POST, where the name is an error.
			cli:     `hello --silent -f 'position[base_sha]=abc'`,
			wantErr: `field name "position[base_sha]": a field name containing a bracket is not supported in a JSON request body; pass the value as JSON, for example -F 'position={"base_sha":"..."}', or use --input`,
		},
		// An explicit GET or DELETE, in either case, sends the name as a parameter
		// and reaches the wire with the method as typed.
		{cli: `hello --silent -X GET -f 'position[base_sha]=abc'`, wantMethod: http.MethodGet, wantURI: "/api/v4/hello?position%5Bbase_sha%5D=abc"},
		{cli: `hello --silent -X get -f 'position[base_sha]=abc'`, wantMethod: "get", wantURI: "/api/v4/hello?position%5Bbase_sha%5D=abc"},
		{cli: `hello --silent -X DELETE -f 'position[base_sha]=abc' -f 'position[new_path]=file.kt'`, wantMethod: http.MethodDelete, wantURI: "/api/v4/hello?position%5Bbase_sha%5D=abc&position%5Bnew_path%5D=file.kt"},
		{cli: `hello --silent -X delete -f 'position[base_sha]=abc'`, wantMethod: "delete", wantURI: "/api/v4/hello?position%5Bbase_sha%5D=abc"},
		{
			// The file supplies the body, so the fields are query parameters even
			// though the method still defaults to POST. As query parameters, "a" and
			// "a[b]" are two unrelated names.
			cli:        `hello --silent --input - -f 'position[base_sha]=abc' -f a=1 -f 'a[b]=2'`,
			wantMethod: http.MethodPost,
			wantURI:    "/api/v4/hello?a=1&a%5Bb%5D=2&position%5Bbase_sha%5D=abc",
			wantBody:   "RAW BODY",
		},
		// Read from a slice per flag, the interleave would come out 1, 3, 2.
		{cli: `hello --silent -X GET -f 'ids[]=1' -F 'ids[]=2' -f 'ids[]=3'`, wantMethod: http.MethodGet, wantURI: "/api/v4/hello?ids%5B%5D=1&ids%5B%5D=2&ids%5B%5D=3"},
		// --field overrides --raw-field of the same name, given in either order.
		{cli: `hello --silent -X GET -f search=raw -F search=magic`, wantMethod: http.MethodGet, wantURI: "/api/v4/hello?search=magic"},
		{cli: `hello --silent -X GET -F search=magic -f search=raw`, wantMethod: http.MethodGet, wantURI: "/api/v4/hello?search=magic"},
		{
			// --form never reaches the field parser, so a bracketed part name is sent
			// as typed. This is the pin behind the help text's "--form is unaffected".
			cli:              `hello --silent --form 'position[base_sha]=abc' --form branch=main`,
			wantMethod:       http.MethodPost,
			wantURI:          "/api/v4/hello",
			wantBodyContains: []string{`name="position[base_sha]"`, "abc", `name="branch"`},
		},
		{
			// GraphQL is the one path that rewrites the URL and wraps the fields:
			// everything but query and operationName is nested under "variables".
			cli:              `graphql --silent -f 'query=query { currentUser { username } }' -f fullPath=gitlab-org/cli`,
			wantMethod:       http.MethodPost,
			wantURI:          "/api/graphql",
			wantBodyContains: []string{`"query":"query { currentUser { username } }"`, `"variables":{"fullPath":"gitlab-org/cli"}`},
		},
		{
			// A bracketed variable name errors before groupGraphQLVariables would
			// nest it under "variables", so the guard proves the check runs ahead of
			// the GraphQL wrapping rather than after it. The GraphQL spec spells a
			// variable name /[_A-Za-z][_0-9A-Za-z]*/, so no valid query names one
			// with a bracket: rejecting it removes no working use.
			cli:     `graphql --silent -f 'query=query { currentUser { username } }' -f 'input[title]=Hello'`,
			wantErr: `field name "input[title]": ` + bracketedNameProblem + `, for example -F 'input={"title":"..."}', or use --input`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.cli, func(t *testing.T) {
			if tt.wantErr != "" {
				err := runAPIArgvGuarded(t, tt.cli)
				require.Error(t, err)
				assert.Equal(t, tt.wantErr, err.Error())
				return
			}
			got, err := runAPIArgvRecording(t, tt.cli)
			require.NoError(t, err)
			assert.Equal(t, tt.wantMethod, got.req.Method)
			assert.Equal(t, tt.wantURI, got.req.URL.RequestURI())
			if tt.wantBody != "" {
				assert.Equal(t, tt.wantBody, string(got.body))
			}
			for _, want := range tt.wantBodyContains {
				assert.Contains(t, string(got.body), want)
			}
		})
	}
}

func Test_parseFields(t *testing.T) {
	ios, stdin, _, _ := cmdtest.TestIOStreams()
	fmt.Fprint(stdin, "pasted contents")

	opts := options{
		io: ios,
		fields: slices.Concat(
			rawFields(
				"robot=Hubot",
				"destroyer=false",
				"helper=true",
				"location=@work",
			),
			magicFields(
				"input=@-",
				"enabled=true",
				"victories=123",
			),
		),
	}

	params, _, err := parseFields(&opts, true)
	if err != nil {
		t.Fatalf("parseFields error: %v", err)
	}

	expect := map[string]any{
		"robot":     "Hubot",
		"destroyer": "false",
		"helper":    "true",
		"location":  "@work",
		"input":     []byte("pasted contents"),
		"enabled":   true,
		"victories": 123,
	}
	assert.Equal(t, expect, params)
}

// Test_parseFields_bracketedValuesByFlag pins the boundary between the two
// flags: --field parses JSON, --raw-field sends its value literally.
//
// Before this change the array regex ran over the merged parameter map in
// httpRequest, so bracketed --raw-field values were converted into arrays too.
// That was never the documented behaviour of --raw-field, and array handling now
// belongs to --field alone.
func Test_parseFields_bracketedValuesByFlag(t *testing.T) {
	ios, _, _, _ := cmdtest.TestIOStreams()

	opts := options{
		io: ios,
		fields: slices.Concat(
			rawFields(
				"rawArray=[api,read_api]",
				"rawEmpty=[]",
			),
			magicFields(
				`parsedArray=["api","read_api"]`,
				"parsedEmpty=[]",
			),
		),
	}

	params, _, err := parseFields(&opts, true)
	require.NoError(t, err)

	assert.Equal(t, map[string]any{
		"rawArray":    "[api,read_api]",
		"rawEmpty":    "[]",
		"parsedArray": []any{"api", "read_api"},
		"parsedEmpty": []any{},
	}, params)
}

func Test_warnOnLegacyRawArrays(t *testing.T) {
	tests := []struct {
		name     string
		method   string
		fields   []fieldFlag
		wantHint bool
		// wantOrder, when set, pins the keys warned about and their order.
		// Warnings follow flag order: reading them off the parameter map would
		// leave the order to Go's map iteration, which is the same defect this
		// change rejects in expandPlaceholdersIn.
		wantOrder []string
		// wantHintText, when set, is the whole line the hint prints.
		wantHintText string
		// wantErr, when set, is the error parseFields returns instead.
		wantErr string
	}{
		{
			name:     "warns on a write method for the old shorthand shape",
			method:   http.MethodPost,
			fields:   rawFields("scopes=[api,read_api]"),
			wantHint: true,
		},
		{
			name:     "silent on GET, where values were never converted",
			method:   http.MethodGet,
			fields:   rawFields("scopes=[api,read_api]"),
			wantHint: false,
		},
		{
			name:     "silent for a value the old regex never matched",
			method:   http.MethodPost,
			fields:   rawFields("topics=[My-Topic]"),
			wantHint: false,
		},
		{
			name:     "silent for an ordinary string",
			method:   http.MethodPost,
			fields:   rawFields("robot=Hubot"),
			wantHint: false,
		},
		{
			name:      "warns in flag order, not map order",
			method:    http.MethodPost,
			fields:    rawFields("ccc=[three]", "aaa=[one]", "bbb=[two]"),
			wantHint:  true,
			wantOrder: []string{"ccc", "aaa", "bbb"},
		},
		{
			// Warning here would describe a request body that does not exist.
			name:     "silent when a --field of the same name overrides the raw value",
			method:   http.MethodPost,
			fields:   slices.Concat(rawFields("scopes=[api,read_api]"), magicFields(`scopes=["api","read_api"]`)),
			wantHint: false,
		},
		{
			// The hint's own -F 's[scopes]=[...]' is a bracketed name too.
			name:    "errors before warning for a nested name on a write method",
			method:  http.MethodPost,
			fields:  rawFields("s[scopes]=[api,read_api]"),
			wantErr: `field name "s[scopes]": ` + bracketedNameProblem + `, for example -F 's={"scopes":"..."}', or use --input`,
		},
		{
			// The case the hint used to advise on directly.
			name:    "errors before warning for an array element on a write method",
			method:  http.MethodPost,
			fields:  rawFields("s[]=[api,read_api]"),
			wantErr: `field name "s[]": ` + bracketedNameProblem + `, for example -F 's=[...]', or use --input`,
		},
		{
			name:     "silent for a nested name on GET, where names stay literal",
			method:   http.MethodGet,
			fields:   rawFields("s[scopes]=[api,read_api]"),
			wantHint: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ios, _, _, stderr := cmdtest.TestIOStreams()
			opts := options{io: ios, fields: tt.fields}

			params, rawKeys, err := parseFields(&opts, opts.fieldsBecomeJSONBody(tt.method))
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Equal(t, tt.wantErr, err.Error())
				assert.Empty(t, stderr.String())
				return
			}
			require.NoError(t, err)

			opts.warnOnLegacyRawArrays(tt.method, params, rawKeys)

			if tt.wantOrder != nil {
				var warned []string
				for line := range strings.SplitSeq(strings.TrimSpace(stderr.String()), "\n") {
					warned = append(warned, strings.SplitN(line, `"`, 3)[1])
				}
				assert.Equal(t, tt.wantOrder, warned)
			}

			if tt.wantHintText != "" {
				assert.Equal(t, tt.wantHintText, stderr.String())
			}

			if tt.wantHint {
				assert.Contains(t, stderr.String(), "sent as the literal string")
			} else {
				assert.Empty(t, stderr.String())
			}
		})
	}
}

func Test_magicFieldValue(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "gitlab-test")
	require.NoError(t, err)
	_, err = f.WriteString("file contents")
	require.NoError(t, err)
	require.NoError(t, f.Close())

	ios, _, _, _ := cmdtest.TestIOStreams()

	type args struct {
		v    string
		opts *options
	}
	tests := []struct {
		name    string
		args    args
		want    any
		wantErr bool
		// wantErrContains pins the user-visible wording where it carries
		// diagnostic value, so a future refactor cannot quietly flatten a
		// decoder message back into a generic one.
		wantErrContains string
	}{
		{
			name:    "string",
			args:    args{v: "hello"},
			want:    "hello",
			wantErr: false,
		},
		{
			name:    "bool true",
			args:    args{v: "true"},
			want:    true,
			wantErr: false,
		},
		{
			name:    "bool false",
			args:    args{v: "false"},
			want:    false,
			wantErr: false,
		},
		{
			name:    "null",
			args:    args{v: "null"},
			want:    nil,
			wantErr: false,
		},
		{
			name: "placeholder",
			args: args{
				v: ":namespace",
				opts: &options{
					io: ios,
					baseRepo: func() (glrepo.Interface, error) {
						return glrepo.New("gitlab-com", "www-gitlab-com", glinstance.DefaultHostname), nil
					},
				},
			},
			want:    "gitlab-com",
			wantErr: false,
		},
		{
			name: "branch placeholder is not URL-encoded in field values",
			args: args{
				v: ":branch",
				opts: &options{
					io: ios,
					baseRepo: func() (glrepo.Interface, error) {
						return glrepo.New("glab-cli", "test", glinstance.DefaultHostname), nil
					},
					branch: func() (string, error) {
						return "feature/foo", nil
					},
				},
			},
			want:    "feature/foo",
			wantErr: false,
		},
		{
			name: "file",
			args: args{
				v:    "@" + f.Name(),
				opts: &options{io: ios},
			},
			want:    []byte("file contents"),
			wantErr: false,
		},
		{
			name: "file error",
			args: args{
				v:    "@",
				opts: &options{io: ios},
			},
			want:    nil,
			wantErr: true,
		},
		{
			name:    "JSON array",
			args:    args{v: `["my-topic","GitLab"]`},
			want:    []any{"my-topic", "GitLab"},
			wantErr: false,
		},
		{
			name:    "JSON object preserves number as json.Number",
			args:    args{v: `{"key":"value","count":42}`},
			want:    map[string]any{"key": "value", "count": json.Number("42")},
			wantErr: false,
		},
		{
			name:    "nested JSON array",
			args:    args{v: `["a",["b","c"]]`},
			want:    []any{"a", []any{"b", "c"}},
			wantErr: false,
		},
		{
			name:            "unparseable value with bracket prefix",
			args:            args{v: `[api,read_api]`},
			want:            nil,
			wantErr:         true,
			wantErrContains: "invalid character 'a' looking for beginning of value",
		},
		{
			name:            "unparseable value with brace prefix",
			args:            args{v: `{bad}`},
			want:            nil,
			wantErr:         true,
			wantErrContains: "invalid character 'b' looking for beginning of object key string",
		},
		{
			name:    "large integer keeps full precision",
			args:    args{v: `[9007199254740993]`},
			want:    []any{json.Number("9007199254740993")},
			wantErr: false,
		},
		{
			name:            "trailing data after JSON is rejected",
			args:            args{v: `[1,2]oops`},
			want:            nil,
			wantErr:         true,
			wantErrContains: "unexpected trailing data: invalid character 'o' looking for beginning of value",
		},
		{
			// dec.Token() returns a legal token and a nil error here, so the
			// trailing-data branch has to supply its own reason rather than
			// wrapping nil.
			name:            "a second JSON value is rejected",
			args:            args{v: `[1,2][3]`},
			want:            nil,
			wantErr:         true,
			wantErrContains: "unexpected trailing data: more than one JSON value",
		},
		// Empty-array handling carried over from !3596, which fixed it in
		// parseStringArrayField before this change replaced that function with
		// json.Unmarshal. Both inputs must still yield an empty slice, not a
		// slice holding one empty string.
		{
			name:    "empty array",
			args:    args{v: `[]`},
			want:    []any{},
			wantErr: false,
		},
		{
			name:    "empty array with interior whitespace",
			args:    args{v: `[  ]`},
			want:    []any{},
			wantErr: false,
		},
		// Boundary cases from !3536 by @ihopenre-eng, which pinned the same
		// string-versus-array question from the regex side. Unquoted integers are
		// not a JSON array of strings, so they parse as numbers; quoting is how a
		// caller asks for string elements.
		{
			name:    "digit elements parse as numbers, not strings",
			args:    args{v: `[1, 2]`},
			want:    []any{json.Number("1"), json.Number("2")},
			wantErr: false,
		},
		{
			name:    "quoted digit elements stay strings",
			args:    args{v: `["1", "2"]`},
			want:    []any{"1", "2"},
			wantErr: false,
		},
		{
			name:    "quoted element containing a comma is one element",
			args:    args{v: `["one, two", "three"]`},
			want:    []any{"one, two", "three"},
			wantErr: false,
		},
		{
			name:    "mixed quoted and unquoted is not valid JSON",
			args:    args{v: `["one, two", three]`},
			want:    nil,
			wantErr: true,
		},
		{
			name: "placeholder inside a JSON object is expanded",
			args: args{
				v: `{"projectPath":":fullpath"}`,
				opts: &options{
					io: ios,
					baseRepo: func() (glrepo.Interface, error) {
						return glrepo.New("glab-cli", "test", glinstance.DefaultHostname), nil
					},
				},
			},
			want:    map[string]any{"projectPath": "glab-cli/test"},
			wantErr: false,
		},
		{
			// Go randomizes map iteration order, so resolving this by last
			// writer wins would drop one of the two fields non-deterministically
			// between runs of the same command. Reject it instead.
			name: "two keys expanding to the same name are rejected",
			args: args{
				v: `{":repo":"from-placeholder","test":"from-literal"}`,
				opts: &options{
					io: ios,
					baseRepo: func() (glrepo.Interface, error) {
						return glrepo.New("glab-cli", "test", glinstance.DefaultHostname), nil
					},
				},
			},
			want:            nil,
			wantErr:         true,
			wantErrContains: `keys ":repo" and "test" both expand to "test"`,
		},
		{
			name: "a key expanding onto an unrelated name is left alone",
			args: args{
				v: `{":repo":"a","other":"b"}`,
				opts: &options{
					io: ios,
					baseRepo: func() (glrepo.Interface, error) {
						return glrepo.New("glab-cli", "test", glinstance.DefaultHostname), nil
					},
				},
			},
			want:    map[string]any{"test": "a", "other": "b"},
			wantErr: false,
		},
		{
			name: "placeholder inside a JSON array is expanded",
			args: args{
				v: `[":branch"]`,
				opts: &options{
					io: ios,
					baseRepo: func() (glrepo.Interface, error) {
						return glrepo.New("glab-cli", "test", glinstance.DefaultHostname), nil
					},
					branch: func() (string, error) {
						return "feature/foo", nil
					},
				},
			},
			want:    []any{"feature/foo"},
			wantErr: false,
		},
		{
			// git accepts a double quote in a refname, so expanding into the raw
			// JSON text before decoding would let a branch name close the string
			// and inject structure. Expansion happens after decoding, so the
			// quote stays data: one key, one string value, no injected field.
			name: "quote in an expanded branch name cannot inject JSON structure",
			args: args{
				v: `{"ref":":branch"}`,
				opts: &options{
					io: ios,
					baseRepo: func() (glrepo.Interface, error) {
						return glrepo.New("glab-cli", "test", glinstance.DefaultHostname), nil
					},
					branch: func() (string, error) {
						return `x","admin":"true`, nil
					},
				},
			},
			want:    map[string]any{"ref": `x","admin":"true`},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := magicFieldValue(tt.args.v, tt.args.opts)
			if (err != nil) != tt.wantErr {
				t.Errorf("magicFieldValue() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr {
				if tt.wantErrContains != "" {
					require.ErrorContains(t, err, tt.wantErrContains)
					assert.NotContains(t, err.Error(), "invalid JSON",
						"the decoder message and the field-name prefix already say the value did not parse")
				}
				return
			}
			assert.Equal(t, tt.want, got)
		})
	}
}

func Test_openUserFile(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "gitlab-test")
	require.NoError(t, err)
	_, err = f.WriteString("file contents")
	require.NoError(t, err)
	require.NoError(t, f.Close())

	file, length, err := openUserFile(f.Name(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	fb, err := io.ReadAll(file)
	if err != nil {
		t.Fatal(err)
	}

	assert.Equal(t, int64(13), length)
	assert.Equal(t, "file contents", string(fb))
}

func Test_fillPlaceholders(t *testing.T) {
	type args struct {
		value string
		opts  *options
	}
	tests := []struct {
		name    string
		args    args
		want    string
		wantErr bool
	}{
		{
			name: "no changes",
			args: args{
				value: "projects/namespace%2Frepo/releases",
				opts: &options{
					baseRepo: nil,
				},
			},
			want:    "projects/namespace%2Frepo/releases",
			wantErr: false,
		},
		{
			name: "has substitutes",
			args: args{
				value: "projects/:namespace%2F:repo/releases",
				opts: &options{
					baseRepo: func() (glrepo.Interface, error) {
						return glrepo.New("gitlab-com", "www-gitlab-com", glinstance.DefaultHostname), nil
					},
				},
			},
			want:    "projects/gitlab-com%2Fwww-gitlab-com/releases",
			wantErr: false,
		},
		{
			name: "has branch placeholder",
			args: args{
				value: "projects/glab-cli%2Ftest/branches/:branch/.../.../",
				opts: &options{
					baseRepo: func() (glrepo.Interface, error) {
						return glrepo.New("glab-cli", "test", glinstance.DefaultHostname), nil
					},
					branch: func() (string, error) {
						return "master", nil
					},
				},
			},
			want:    "projects/glab-cli%2Ftest/branches/master/.../.../",
			wantErr: false,
		},
		{
			name: "branch placeholder URL-encodes slashes",
			args: args{
				value: "projects/:fullpath/protected_branches/:branch",
				opts: &options{
					baseRepo: func() (glrepo.Interface, error) {
						return glrepo.New("glab-cli", "test", glinstance.DefaultHostname), nil
					},
					branch: func() (string, error) {
						return "feature/foo", nil
					},
				},
			},
			want:    "projects/glab-cli%2Ftest/protected_branches/feature%2Ffoo",
			wantErr: false,
		},
		{
			name: "has branch placeholder and git is in detached head",
			args: args{
				value: "projects/:fullpath/branches/:branch",
				opts: &options{
					baseRepo: func() (glrepo.Interface, error) {
						return glrepo.New("glab-cli", "test", glinstance.DefaultHostname), nil
					},
					branch: func() (string, error) {
						return "", git.ErrNotOnAnyBranch
					},
				},
			},
			want:    "projects/:fullpath/branches/:branch",
			wantErr: true,
		},
		{
			name: "no greedy substitutes",
			args: args{
				value: ":namespaces/:repository",
				opts: &options{
					baseRepo: nil,
				},
			},
			want:    ":namespaces/:repository",
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := fillPlaceholders(tt.args.value, tt.args.opts, true)
			if (err != nil) != tt.wantErr {
				t.Errorf("fillPlaceholders() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("fillPlaceholders() got = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestFillPlaceholdersReturnsAPIClientError(t *testing.T) {
	t.Parallel()

	apiClientErr := errors.New("failed to create API client")
	tests := []struct {
		name     string
		value    string
		baseRepo func() (glrepo.Interface, error)
	}{
		{
			name:  "project ID",
			value: "projects/:id",
			baseRepo: func() (glrepo.Interface, error) {
				return glrepo.New("gitlab-com", "www-gitlab-com", glinstance.DefaultHostname), nil
			},
		},
		{
			name:  "current user",
			value: "users/:user",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			opts := &options{
				baseRepo: tt.baseRepo,
				apiClient: func(string) (*api.Client, error) {
					return nil, apiClientErr
				},
			}

			got, err := fillPlaceholders(tt.value, opts, true)

			require.ErrorIs(t, err, apiClientErr)
			assert.Equal(t, tt.value, got)
		})
	}
}

func Test_processResponse_bodyConsumption(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		options        options
		httpResponse   *http.Response
		expectedCursor string
		expectError    bool
		checkStdout    func(t *testing.T, stdout string)
		checkStderr    func(t *testing.T, stderr string)
	}{
		{
			name: "GraphQL pagination - body read once",
			options: options{
				requestPath: "graphql",
				paginate:    true,
			},
			httpResponse: &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body: io.NopCloser(bytes.NewBufferString(`{
					"data": {
						"issues": ["issue1", "issue2"],
						"pageInfo": {
							"endCursor": "CURSOR_123",
							"hasNextPage": true
						}
					}
				}`)),
			},
			expectedCursor: "CURSOR_123",
			expectError:    false,
			checkStdout: func(t *testing.T, stdout string) {
				t.Helper()
				assert.Contains(t, stdout, "issue")
			},
			checkStderr: func(t *testing.T, stderr string) {
				t.Helper()
				assert.Empty(t, stderr)
			},
		},
		{
			name: "GraphQL pagination - no next page",
			options: options{
				requestPath: "graphql",
				paginate:    true,
			},
			httpResponse: &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body: io.NopCloser(bytes.NewBufferString(`{
					"data": {
						"issues": ["issue3"],
						"pageInfo": {
							"endCursor": "CURSOR_END",
							"hasNextPage": false
						}
					}
				}`)),
			},
			expectedCursor: "",
			expectError:    false,
			checkStdout: func(t *testing.T, stdout string) {
				t.Helper()
				assert.Contains(t, stdout, "issue")
			},
		},
		{
			name: "GraphQL pagination - empty data array",
			options: options{
				requestPath: "graphql",
				paginate:    true,
			},
			httpResponse: &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body: io.NopCloser(bytes.NewBufferString(`{
					"data": {
						"issues": [],
						"pageInfo": {
							"endCursor": "",
							"hasNextPage": false
						}
					}
				}`)),
			},
			expectedCursor: "",
			expectError:    false,
			checkStdout: func(t *testing.T, stdout string) {
				t.Helper()
				assert.Contains(t, stdout, "issues")
			},
		},
		{
			name: "GraphQL error response with empty errors array",
			options: options{
				requestPath: "graphql",
				paginate:    true,
			},
			httpResponse: &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body: io.NopCloser(bytes.NewBufferString(`{
					"data": {
						"issues": ["issue1"],
						"pageInfo": {
							"endCursor": "CURSOR_XYZ",
							"hasNextPage": true
						}
					},
					"errors": []
				}`)),
			},
			expectedCursor: "CURSOR_XYZ",
			expectError:    false,
			checkStdout: func(t *testing.T, stdout string) {
				t.Helper()
				assert.Contains(t, stdout, "issue")
			},
		},
		{
			name: "GraphQL error response - body not consumed twice",
			options: options{
				requestPath: "graphql",
				paginate:    true,
			},
			httpResponse: &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body: io.NopCloser(bytes.NewBufferString(`{
					"data": {
						"issues": [],
						"pageInfo": {
							"endCursor": "",
							"hasNextPage": false
						}
					},
					"errors": [{"message": "Invalid query"}]
				}`)),
			},
			expectedCursor: "",
			expectError:    true,
			checkStdout: func(t *testing.T, stdout string) {
				t.Helper()
				assert.Contains(t, stdout, "errors")
			},
			checkStderr: func(t *testing.T, stderr string) {
				t.Helper()
				assert.Contains(t, stderr, "Invalid query")
			},
		},
		{
			name: "GraphQL error response with data and cursor",
			options: options{
				requestPath: "graphql",
				paginate:    true,
			},
			httpResponse: &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body: io.NopCloser(bytes.NewBufferString(`{
					"data": {
						"issues": [{"id": 1}],
						"pageInfo": {
							"endCursor": "CURSOR_AFTER_ERROR",
							"hasNextPage": true
						}
					},
					"errors": [
						{"message": "Field 'nonexistent' doesn't exist on type 'Issue'"},
						{"message": "Some other error"}
					]
				}`)),
			},
			expectedCursor: "",
			expectError:    true,
			checkStdout: func(t *testing.T, stdout string) {
				t.Helper()
				assert.Contains(t, stdout, "issues")
			},
			checkStderr: func(t *testing.T, stderr string) {
				t.Helper()
				assert.Contains(t, stderr, "nonexistent")
				assert.Contains(t, stderr, "Some other error")
			},
		},
		{
			name: "GraphQL with color output enabled",
			options: options{
				requestPath: "graphql",
				paginate:    true,
			},
			httpResponse: &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body: io.NopCloser(bytes.NewBufferString(`{
					"data": {
						"issues": [{"id": 1, "title": "Test Issue"}],
						"pageInfo": {
							"endCursor": "NEXT_CURSOR",
							"hasNextPage": true
						}
					}
				}`)),
			},
			expectedCursor: "NEXT_CURSOR",
			expectError:    false,
			checkStdout: func(t *testing.T, stdout string) {
				t.Helper()
				assert.Contains(t, stdout, "Test Issue")
			},
		},
		{
			name: "REST pagination - body not consumed twice",
			options: options{
				requestPath: "issues",
				paginate:    true,
			},
			httpResponse: &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(bytes.NewBufferString(`[{"id": 1}]`)),
			},
			expectedCursor: "",
			expectError:    false,
			checkStdout: func(t *testing.T, stdout string) {
				t.Helper()
				assert.Contains(t, stdout, "id")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ios, _, stdout, stderr := cmdtest.TestIOStreams()
			tt.options.io = ios
			tt.options.baseRepo = func() (glrepo.Interface, error) {
				return nil, fmt.Errorf("not supposed to be called")
			}
			tt.options.apiClient = func(repoHost string) (*api.Client, error) {
				return nil, fmt.Errorf("not supposed to be called")
			}

			endCursor, err := processResponse(tt.httpResponse, &tt.options, io.Discard)

			if tt.expectError {
				require.Error(t, err, "expected error but got none")
			} else {
				require.NoError(t, err, "unexpected error: %v", err)
			}

			// Verify cursor extraction
			assert.Equal(t, tt.expectedCursor, endCursor, "cursor mismatch")

			// Run custom stdout/stderr checks if provided
			if tt.checkStdout != nil {
				tt.checkStdout(t, stdout.String())
			}
			if tt.checkStderr != nil {
				tt.checkStderr(t, stderr.String())
			}
		})
	}
}

// Test_findEndCursor_multiplePages verifies cursor extraction works across multiple pages
func Test_findEndCursor_multiplePages(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		jsonResponse   string
		expectedCursor string
	}{
		{
			name: "simple pagination",
			jsonResponse: `{
				"data": {
					"issues": [{"id": 1}],
					"pageInfo": {
						"endCursor": "ABC123",
						"hasNextPage": true
					}
				}
			}`,
			expectedCursor: "ABC123",
		},
		{
			name: "nested pagination",
			jsonResponse: `{
				"data": {
					"project": {
						"issues": {
							"nodes": [{"id": 1}],
							"pageInfo": {
								"endCursor": "XYZ789",
								"hasNextPage": true
							}
						}
					}
				}
			}`,
			expectedCursor: "XYZ789",
		},
		{
			name: "no next page",
			jsonResponse: `{
				"data": {
					"issues": [{"id": 1}],
					"pageInfo": {
						"endCursor": "LAST",
						"hasNextPage": false
					}
				}
			}`,
			expectedCursor: "",
		},
		{
			name: "missing pageInfo",
			jsonResponse: `{
				"data": {
					"issues": [{"id": 1}]
				}
			}`,
			expectedCursor: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			reader := bytes.NewBufferString(tt.jsonResponse)
			cursor := findEndCursor(reader)
			if cursor != tt.expectedCursor {
				t.Errorf("expected cursor %q, got %q", tt.expectedCursor, cursor)
			}
		})
	}
}
