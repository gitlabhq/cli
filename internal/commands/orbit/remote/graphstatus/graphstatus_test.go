//go:build !integration

package graphstatus

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v2/testing"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/commands/orbit/internal/orbiterr"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestGraphStatus_FullPath_HappyPath(t *testing.T) {
	t.Parallel()
	// GIVEN the API returns indexing progress for a full_path
	testClient := gitlabtesting.NewTestClient(t)
	testClient.MockOrbit.EXPECT().
		GetGraphStatus(gomock.AssignableToTypeOf(&gitlab.GetGraphStatusOptions{}), gomock.Any()).
		DoAndReturn(func(opts *gitlab.GetGraphStatusOptions, _ ...gitlab.RequestOptionFunc) (*gitlab.OrbitGraphStatus, *gitlab.Response, error) {
			require.NotNil(t, opts)
			require.NotNil(t, opts.FullPath)
			assert.Equal(t, "gitlab-org/gitlab", *opts.FullPath)
			assert.Nil(t, opts.NamespaceID)
			assert.Nil(t, opts.ProjectID)
			assert.Nil(t, opts.ResponseFormat, "response_format should not be sent unless --response-format is given")

			return &gitlab.OrbitGraphStatus{
					Projects: &gitlab.OrbitGraphStatusProjects{Indexed: 5, TotalKnown: 10},
					Domains: []*gitlab.OrbitGraphStatusDomain{
						{Name: "SDLC", Items: []*gitlab.OrbitGraphStatusDomainItem{
							{Name: "MergeRequest", Count: 42},
						}},
					},
					Indexing: &gitlab.OrbitGraphStatusIndexing{State: "indexed"},
				},
				&gitlab.Response{Response: &http.Response{StatusCode: http.StatusOK}}, nil
		})

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "", "", api.WithGitLabClient(testClient.Client))),
	)

	// WHEN `glab orbit remote graph-status --full-path gitlab-org/gitlab` runs
	out, err := exec("--full-path gitlab-org/gitlab")

	// THEN the typed response is printed as JSON with expected fields
	require.NoError(t, err)

	var result gitlab.OrbitGraphStatus
	require.NoError(t, json.Unmarshal(out.OutBuf.Bytes(), &result))
	require.NotNil(t, result.Projects)
	assert.Equal(t, int64(5), result.Projects.Indexed)
	require.Len(t, result.Domains, 1)
	assert.Equal(t, "SDLC", result.Domains[0].Name)
	require.NotNil(t, result.Indexing)
	assert.Equal(t, "indexed", result.Indexing.State)
}

func TestGraphStatus_NamespaceID(t *testing.T) {
	t.Parallel()
	// GIVEN the user supplies --namespace-id
	testClient := gitlabtesting.NewTestClient(t)
	testClient.MockOrbit.EXPECT().
		GetGraphStatus(gomock.AssignableToTypeOf(&gitlab.GetGraphStatusOptions{}), gomock.Any()).
		DoAndReturn(func(opts *gitlab.GetGraphStatusOptions, _ ...gitlab.RequestOptionFunc) (*gitlab.OrbitGraphStatus, *gitlab.Response, error) {
			require.NotNil(t, opts.NamespaceID)
			assert.Equal(t, int64(9970), *opts.NamespaceID)
			assert.Nil(t, opts.ProjectID)
			assert.Nil(t, opts.FullPath)
			return &gitlab.OrbitGraphStatus{},
				&gitlab.Response{Response: &http.Response{StatusCode: http.StatusOK}}, nil
		})

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "", "", api.WithGitLabClient(testClient.Client))),
	)

	// WHEN
	_, err := exec("--namespace-id 9970")

	// THEN
	require.NoError(t, err)
}

func TestGraphStatus_ProjectID_ResponseFormatLLM(t *testing.T) {
	t.Parallel()
	// GIVEN the user supplies --project-id and --response-format llm
	testClient := gitlabtesting.NewTestClient(t)
	testClient.MockOrbit.EXPECT().
		GetGraphStatus(gomock.AssignableToTypeOf(&gitlab.GetGraphStatusOptions{}), gomock.Any()).
		DoAndReturn(func(opts *gitlab.GetGraphStatusOptions, _ ...gitlab.RequestOptionFunc) (*gitlab.OrbitGraphStatus, *gitlab.Response, error) {
			require.NotNil(t, opts.ProjectID)
			assert.Equal(t, int64(278964), *opts.ProjectID)
			require.NotNil(t, opts.ResponseFormat)
			assert.Equal(t, gitlab.OrbitResponseFormatLLM, *opts.ResponseFormat)
			return &gitlab.OrbitGraphStatus{},
				&gitlab.Response{Response: &http.Response{StatusCode: http.StatusOK}}, nil
		})

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "", "", api.WithGitLabClient(testClient.Client))),
	)

	// WHEN
	_, err := exec("--project-id 278964 --response-format llm")

	// THEN
	require.NoError(t, err)
}

func TestGraphStatus_NoScopeFlag_Errors(t *testing.T) {
	t.Parallel()
	// GIVEN no scope flag is provided
	testClient := gitlabtesting.NewTestClient(t)
	// no API call expected

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "", "", api.WithGitLabClient(testClient.Client))),
	)

	// WHEN
	_, err := exec("")

	// THEN cobra rejects the invocation (MarkFlagsOneRequired)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "namespace-id")
}

func TestGraphStatus_MultipleScopeFlags_Errors(t *testing.T) {
	t.Parallel()
	// GIVEN both --project-id and --full-path are provided
	testClient := gitlabtesting.NewTestClient(t)
	// no API call expected

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "", "", api.WithGitLabClient(testClient.Client))),
	)

	// WHEN
	_, err := exec("--project-id 1 --full-path gitlab-org/gitlab")

	// THEN cobra rejects the invocation (MarkFlagsMutuallyExclusive)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "namespace-id")
}

func TestGraphStatus_InvalidFormatFlag(t *testing.T) {
	t.Parallel()
	// GIVEN an invalid --response-format value
	// NewEnumValue rejects unknown values at flag parsing time, before RunE runs.
	testClient := gitlabtesting.NewTestClient(t)
	// no API call expected

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "", "", api.WithGitLabClient(testClient.Client))),
	)

	// WHEN
	_, err := exec("--full-path gitlab-org/gitlab --response-format yaml")

	// THEN cobra rejects the flag before RunE executes
	require.Error(t, err)
	assert.Contains(t, err.Error(), "yaml")
}

func TestGraphStatus_FeatureFlagOff(t *testing.T) {
	t.Parallel()
	// GIVEN the API returns 404 because the knowledge_graph FF is off
	testClient := gitlabtesting.NewTestClient(t)
	testClient.MockOrbit.EXPECT().
		GetGraphStatus(gomock.Any(), gomock.Any()).
		Return(nil,
			&gitlab.Response{Response: &http.Response{StatusCode: http.StatusNotFound}},
			&gitlab.ErrorResponse{
				Response: &http.Response{StatusCode: http.StatusNotFound},
				Message:  "404 Not Found",
			})

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "", "", api.WithGitLabClient(testClient.Client))),
	)

	// WHEN
	_, err := exec("--full-path gitlab-org/gitlab")

	// THEN the error maps to ExitOrbitUnavailable (exit code 2)
	require.Error(t, err)
	var exitErr *cmdutils.ExitError
	require.ErrorAs(t, err, &exitErr)
	assert.Equal(t, orbiterr.ExitOrbitUnavailable, exitErr.Code)
}

func TestGraphStatus_OrbitServiceUnavailable(t *testing.T) {
	t.Parallel()
	// GIVEN the underlying Orbit service is down (HTTP 503)
	testClient := gitlabtesting.NewTestClient(t)
	testClient.MockOrbit.EXPECT().
		GetGraphStatus(gomock.Any(), gomock.Any()).
		Return(nil,
			&gitlab.Response{Response: &http.Response{StatusCode: http.StatusServiceUnavailable}},
			&gitlab.ErrorResponse{
				Response: &http.Response{StatusCode: http.StatusServiceUnavailable},
				Message:  "503 Service Unavailable",
			})

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "", "", api.WithGitLabClient(testClient.Client))),
	)

	// WHEN
	_, err := exec("--full-path gitlab-org/gitlab")

	// THEN the error is mapped to a generic exit code 1 with a descriptive
	// message — the shared translator does not handle 503.
	require.Error(t, err)
	var exitErr *cmdutils.ExitError
	require.ErrorAs(t, err, &exitErr)
	assert.Equal(t, 1, exitErr.Code)
	assert.Contains(t, err.Error(), "service unavailable")
}

// TestGraphStatus_DeprecatedFormatFlagStillWorks verifies that the deprecated
// --format flag still works via the deprecation shim and produces the
// correct server-side response_format override.
func TestGraphStatus_DeprecatedFormatFlagStillWorks(t *testing.T) {
	t.Parallel()
	// GIVEN the user supplies --project-id and the deprecated --format llm
	testClient := gitlabtesting.NewTestClient(t)
	testClient.MockOrbit.EXPECT().
		GetGraphStatus(gomock.AssignableToTypeOf(&gitlab.GetGraphStatusOptions{}), gomock.Any()).
		DoAndReturn(func(opts *gitlab.GetGraphStatusOptions, _ ...gitlab.RequestOptionFunc) (*gitlab.OrbitGraphStatus, *gitlab.Response, error) {
			require.NotNil(t, opts.ProjectID)
			assert.Equal(t, int64(278964), *opts.ProjectID)
			require.NotNil(t, opts.ResponseFormat)
			assert.Equal(t, gitlab.OrbitResponseFormatLLM, *opts.ResponseFormat)
			return &gitlab.OrbitGraphStatus{},
				&gitlab.Response{Response: &http.Response{StatusCode: http.StatusOK}}, nil
		})

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "", "", api.WithGitLabClient(testClient.Client))),
	)

	// WHEN using the deprecated --format flag
	_, err := exec("--project-id 278964 --format llm")

	// THEN the deprecated flag still works
	require.NoError(t, err)
}
