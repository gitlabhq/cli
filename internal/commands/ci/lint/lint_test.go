//go:build !integration

package lint

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"

	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func Test_lintRun(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name             string
		testFile         string
		cliArgs          string
		StdOut           string
		wantErr          bool
		errMsg           string
		showHaveBaseRepo bool
		setupMock        func(tc *gitlabtesting.TestClient)
	}

	tests := []testCase{
		{
			name:             "with invalid path specified",
			testFile:         "WRONG_PATH",
			StdOut:           "",
			wantErr:          true,
			errMsg:           "WRONG_PATH: no such file or directory",
			showHaveBaseRepo: true,
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockProjects.EXPECT().
					GetProject("OWNER/REPO", gomock.Any()).
					Return(&gitlab.Project{
						ID: 123,
					}, nil, nil)
			},
		},
		{
			name:             "without base repo",
			testFile:         ".gitlab.ci.yaml",
			StdOut:           "",
			wantErr:          true,
			errMsg:           "you must be in a GitLab project repository for this action: no base repo present",
			showHaveBaseRepo: false,
			setupMock: func(tc *gitlabtesting.TestClient) {
				// No mock needed - fails before API call
			},
		},
		{
			name:             "when a valid path is specified and yaml is valid",
			testFile:         ".gitlab-ci.yaml",
			StdOut:           "Validating...\n✓ CI/CD YAML is valid!\n",
			wantErr:          false,
			errMsg:           "",
			showHaveBaseRepo: true,
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockProjects.EXPECT().
					GetProject("OWNER/REPO", gomock.Any()).
					Return(&gitlab.Project{
						ID: 123,
					}, nil, nil)
				tc.MockValidate.EXPECT().
					ProjectNamespaceLint(int64(123), gomock.Any()).
					Return(&gitlab.ProjectLintResult{
						Valid: true,
					}, nil, nil)
			},
		},
		{
			name:             "when --dry-run is used without --ref",
			testFile:         ".gitlab-ci.yaml",
			cliArgs:          "--dry-run",
			StdOut:           "Validating...\n✓ CI/CD YAML is valid!\n",
			wantErr:          false,
			errMsg:           "",
			showHaveBaseRepo: true,
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockProjects.EXPECT().
					GetProject("OWNER/REPO", gomock.Any()).
					Return(&gitlab.Project{
						ID: 123,
					}, nil, nil)
				tc.MockValidate.EXPECT().
					ProjectNamespaceLint(int64(123), gomock.Any()).
					Return(&gitlab.ProjectLintResult{
						Valid: true,
					}, nil, nil)
			},
		},
		{
			name:             "when --dry-run is used with --ref",
			testFile:         ".gitlab-ci.yaml",
			cliArgs:          "--dry-run --ref=main",
			StdOut:           "Validating...\n✓ CI/CD YAML is valid!\n",
			wantErr:          false,
			errMsg:           "",
			showHaveBaseRepo: true,
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockProjects.EXPECT().
					GetProject("OWNER/REPO", gomock.Any()).
					Return(&gitlab.Project{
						ID: 123,
					}, nil, nil)
				tc.MockValidate.EXPECT().
					ProjectNamespaceLint(int64(123), gomock.Any()).
					Return(&gitlab.ProjectLintResult{
						Valid: true,
					}, nil, nil)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// GIVEN
			testClient := gitlabtesting.NewTestClient(t)
			tt.setupMock(testClient)

			_, filename, _, _ := runtime.Caller(0)
			args := path.Join(path.Dir(filename), "testdata", tt.testFile)
			if tt.cliArgs != "" {
				args += " " + tt.cliArgs
			}

			opts := []cmdtest.FactoryOption{
				cmdtest.WithGitLabClient(testClient.Client),
			}
			if !tt.showHaveBaseRepo {
				opts = append(opts, cmdtest.WithBaseRepoError(fmt.Errorf("no base repo present")))
			}

			exec := cmdtest.SetupCmdForTest(t, NewCmdLint, false, opts...)

			// WHEN
			result, err := exec(args)

			// THEN
			if tt.wantErr {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.errMsg)
				return
			}
			require.NoError(t, err)

			assert.Equal(t, tt.StdOut, result.String())
		})
	}
}

func Test_lintRun_remoteURL(t *testing.T) {
	t.Parallel()

	t.Run("a failing response is not linted", func(t *testing.T) {
		t.Parallel()

		// GIVEN a URL that does not serve a CI configuration.
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "not found", http.StatusNotFound)
		}))
		t.Cleanup(srv.Close)

		testClient := gitlabtesting.NewTestClient(t)
		testClient.MockProjects.EXPECT().
			GetProject("OWNER/REPO", gomock.Any()).
			Return(&gitlab.Project{ID: 123}, nil, nil)
		// No ProjectNamespaceLint call is expected: the error body must not
		// reach the lint API.

		exec := cmdtest.SetupCmdForTest(t, NewCmdLint, false, cmdtest.WithGitLabClient(testClient.Client))

		// WHEN
		_, err := exec(srv.URL + "/missing.yml")

		// THEN
		require.Error(t, err)
		assert.Contains(t, err.Error(), "404 Not Found")
	})

	t.Run("a successful response is linted", func(t *testing.T) {
		t.Parallel()

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, "job:\n  script: echo hello\n")
		}))
		t.Cleanup(srv.Close)

		testClient := gitlabtesting.NewTestClient(t)
		testClient.MockProjects.EXPECT().
			GetProject("OWNER/REPO", gomock.Any()).
			Return(&gitlab.Project{ID: 123}, nil, nil)
		testClient.MockValidate.EXPECT().
			ProjectNamespaceLint(int64(123), gomock.Any()).
			Return(&gitlab.ProjectLintResult{Valid: true}, nil, nil)

		exec := cmdtest.SetupCmdForTest(t, NewCmdLint, false, cmdtest.WithGitLabClient(testClient.Client))

		result, err := exec(srv.URL + "/.gitlab-ci.yml")

		require.NoError(t, err)
		assert.Equal(t, "Validating...\n✓ CI/CD YAML is valid!\n", result.String())
	})
}
