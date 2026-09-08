//go:build !integration

package catalog

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"

	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestPublishCatalog(t *testing.T) {
	// This test uses httptest.NewServer because the catalog publish API uses raw HTTP
	// via client.Do() rather than a GitLab client-go service interface.

	tests := []struct {
		name           string
		tagName        string
		isValidTagName bool

		wantOutput string
		wantBody   string
		wantErr    bool
		errMsg     string
	}{
		{
			name:           "valid tag",
			tagName:        "0.0.1",
			isValidTagName: true,
			wantBody: `{
				"version": "0.0.1",
				"metadata": {
					"components": [
						{
							"component_type": "template",
							"name": "component-1",
							"spec": {
								"inputs": {
									"compiler": {
										"default": "gcc"
									}
								}
							}
						},
						{
							"component_type": "template",
							"name": "component-2",
							"spec": null
						},
						{
							"component_type": "template",
							"name": "component-3",
							"spec": {
								"inputs": {
									"test_framework": {
										"default": "unittest"
									}
								}
							}
						}
					]
				}
			}`,
			wantOutput: `• Publishing release tag=0.0.1 to the GitLab CI/CD catalog for repo=OWNER/REPO...
✓ Release published: url=https://gitlab.example.com/explore/catalog/my-namespace/my-component-project`,
		},
		{
			name:    "missing tag",
			wantErr: true,
			errMsg:  "accepts 1 arg(s), received 0",
		},
		{
			name:           "invalid tag",
			tagName:        "6.6.6",
			isValidTagName: false,
			wantErr:        true,
			errMsg:         "invalid tag 6.6.6",
		},
	}

	t.Chdir(filepath.Join("testdata", "test-repo"))

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Create a test server that handles both tag validation and catalog publish
			testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodGet && r.URL.Path == "/api/v4/projects/OWNER/REPO/repository/tags/"+tc.tagName:
					if tc.isValidTagName {
						w.WriteHeader(http.StatusOK)
						_, _ = w.Write([]byte(`{"name": "` + tc.tagName + `"}`))
					} else {
						w.WriteHeader(http.StatusNotFound)
						_, _ = w.Write([]byte(`{"message": "404 Tag Not Found"}`))
					}

				case r.Method == http.MethodPost && r.URL.Path == "/api/v4/projects/OWNER/REPO/catalog/publish":
					if tc.wantBody != "" {
						var reqBody, expectedBody map[string]any
						err := json.NewDecoder(r.Body).Decode(&reqBody)
						assert.NoError(t, err)
						err = json.Unmarshal([]byte(tc.wantBody), &expectedBody)
						assert.NoError(t, err)

						reqBodyJSON, _ := json.Marshal(reqBody)
						expectedBodyJSON, _ := json.Marshal(expectedBody)
						assert.JSONEq(t, string(expectedBodyJSON), string(reqBodyJSON))
					}

					w.WriteHeader(http.StatusOK)
					_, _ = w.Write([]byte(`{
						"catalog_url": "https://gitlab.example.com/explore/catalog/my-namespace/my-component-project"
					}`))

				default:
					// For missing tag test case, we don't expect any HTTP calls
					if tc.tagName != "" {
						t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
					}
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer testServer.Close()

			// Create a GitLab client with the test server's URL
			gitlabClient, err := gitlab.NewClient("test-token", gitlab.WithBaseURL(testServer.URL+"/api/v4"))
			require.NoError(t, err)

			exec := cmdtest.SetupCmdForTest(t, NewCmdPublishCatalog, false,
				cmdtest.WithGitLabClient(gitlabClient),
				cmdtest.WithBaseRepo("OWNER", "REPO", ""),
			)

			output, err := exec(tc.tagName)

			if tc.wantErr {
				require.Error(t, err)
				assert.Equal(t, tc.errMsg, err.Error())
			} else {
				require.NoError(t, err)
				assert.Contains(t, output.String(), tc.wantOutput)
			}
		})
	}
}

func Test_fetchTemplates(t *testing.T) {
	wd := filepath.Join("testdata", "test-repo")

	want := map[string]string{
		"component-1": filepath.Join(wd, "templates/component-1.yml"),
		"component-2": filepath.Join(wd, "templates/component-2.yml"),
		"component-3": filepath.Join(wd, "templates/component-3", "template.yml"),
	}
	got, err := fetchTemplates(wd)
	require.NoError(t, err)

	for k, v := range want {
		require.Equal(t, got[k], v)
	}
}

func Test_extractComponentName(t *testing.T) {
	wd := filepath.Join("testdata", "test-repo")

	tests := []struct {
		name     string
		path     string
		expected string
	}{
		{
			name:     "valid component path",
			path:     filepath.Join(wd, "templates/component-1.yml"),
			expected: "component-1",
		},
		{
			name:     "valid component path in sub directory",
			path:     filepath.Join(wd, "templates/component-2", "template.yml"),
			expected: "component-2",
		},
		{
			name:     "invalid component path",
			path:     filepath.Join(wd, "abc_templates/component-3.yml"),
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := extractComponentName(wd, tt.path)
			require.NoError(t, err)
			require.Equal(t, tt.expected, got)
		})
	}
}
