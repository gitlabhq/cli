//go:build !integration

package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
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
				rawFields:           []string(nil),
				magicFields:         []string(nil),
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
				rawFields:           []string(nil),
				magicFields:         []string(nil),
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
				rawFields:           []string{"query=QUERY"},
				magicFields:         []string{"body=@file.txt"},
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
				rawFields:           []string(nil),
				magicFields:         []string(nil),
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
				rawFields:           []string(nil),
				magicFields:         []string(nil),
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
				rawFields:           []string(nil),
				magicFields:         []string(nil),
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
				rawFields:           []string(nil),
				magicFields:         []string(nil),
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
				rawFields:           []string(nil),
				magicFields:         []string(nil),
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
				rawFields:           []string(nil),
				magicFields:         []string(nil),
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
				rawFields:           []string(nil),
				magicFields:         []string(nil),
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
				rawFields:           []string(nil),
				magicFields:         []string(nil),
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
				assert.Equal(t, tt.wants.rawFields, o.rawFields)
				assert.Equal(t, tt.wants.magicFields, o.magicFields)
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
			stderr: "glab: map[message:map[password:[is too short (minimum is 8 characters)]]]+\n",
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
				rawFields:        []string{"a=b", "c=d"},

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

func Test_parseFields(t *testing.T) {
	ios, stdin, _, _ := cmdtest.TestIOStreams()
	fmt.Fprint(stdin, "pasted contents")

	opts := options{
		io: ios,
		rawFields: []string{
			"robot=Hubot",
			"destroyer=false",
			"helper=true",
			"location=@work",
		},
		magicFields: []string{
			"input=@-",
			"enabled=true",
			"victories=123",
		},
	}

	params, err := parseFields(&opts)
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
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := magicFieldValue(tt.args.v, tt.args.opts)
			if (err != nil) != tt.wantErr {
				t.Errorf("magicFieldValue() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr {
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
