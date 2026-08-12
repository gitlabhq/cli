//go:build !integration

package importcmd

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v2/testing"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

// alreadyTakenErr mimics the *gitlab.ErrorResponse GitLab returns for a
// duplicate variable key: HTTP 400 with a Rails-style validation message.
func alreadyTakenErr() error {
	return &gitlab.ErrorResponse{
		StatusCode: http.StatusBadRequest,
		Message:    "{key: [has already been taken]}",
	}
}

func writeVariablesFile(t *testing.T, contents string) string {
	t.Helper()
	file := filepath.Join(t.TempDir(), "variables.json")
	require.NoError(t, os.WriteFile(file, []byte(contents), 0o600))
	return file
}

func newOptions(t *testing.T, testClient *gitlabtesting.TestClient, file string) (*options, func() string) {
	t.Helper()
	io, _, stdout, _ := cmdtest.TestIOStreams()
	opts := &options{
		apiClient: func(repoHost string) (*api.Client, error) {
			return cmdtest.NewTestApiClient(t, nil, "", "gitlab.com", api.WithGitLabClient(testClient.Client)), nil
		},
		baseRepo: func() (glrepo.Interface, error) {
			return glrepo.New("owner", "repo", "gitlab.com"), nil
		},
		io:        io,
		inputFile: file,
	}
	return opts, stdout.String
}

func Test_importRun_project(t *testing.T) {
	// GIVEN
	testClient := gitlabtesting.NewTestClient(t)
	testClient.MockProjectVariables.EXPECT().
		CreateVariable("owner/repo", gomock.Any(), gomock.Any()).
		Return(&gitlab.ProjectVariable{}, nil, nil).
		Times(2)

	file := writeVariablesFile(t, `[
		{"key":"ONE","value":"1","variable_type":"env_var","environment_scope":"*"},
		{"key":"TWO","value":"2","variable_type":"env_var","environment_scope":"*"}
	]`)
	opts, stdout := newOptions(t, testClient, file)

	// WHEN
	err := opts.run(t.Context())

	// THEN
	require.NoError(t, err)
	out := stdout()
	assert.Contains(t, out, "Imported variable ONE.")
	assert.Contains(t, out, "Imported variable TWO.")
	assert.Contains(t, out, "Imported 2 variables into owner/repo.")
}

func Test_importRun_group(t *testing.T) {
	// GIVEN
	testClient := gitlabtesting.NewTestClient(t)
	testClient.MockGroupVariables.EXPECT().
		CreateVariable("mygroup", gomock.Any(), gomock.Any()).
		Return(&gitlab.GroupVariable{}, nil, nil)

	file := writeVariablesFile(t, `[{"key":"ONE","value":"1","variable_type":"env_var","environment_scope":"*"}]`)
	opts, stdout := newOptions(t, testClient, file)
	opts.group = "mygroup"

	// WHEN
	err := opts.run(t.Context())

	// THEN
	require.NoError(t, err)
	assert.Contains(t, stdout(), "Imported 1 variables into mygroup.")
}

func Test_importRun_skipExisting(t *testing.T) {
	// GIVEN
	testClient := gitlabtesting.NewTestClient(t)
	testClient.MockProjectVariables.EXPECT().
		CreateVariable("owner/repo", gomock.Any(), gomock.Any()).
		Return(nil, nil, alreadyTakenErr())

	file := writeVariablesFile(t, `[{"key":"DUP","value":"x","variable_type":"env_var","environment_scope":"*"}]`)
	opts, stdout := newOptions(t, testClient, file)
	opts.skipExisting = true

	// WHEN
	err := opts.run(t.Context())

	// THEN
	require.NoError(t, err)
	out := stdout()
	assert.Contains(t, out, "Skipped existing variable DUP.")
	assert.Contains(t, out, "Imported 0 variables into owner/repo (1 skipped).")
}

func Test_importRun_failsOnExistingByDefault(t *testing.T) {
	// GIVEN
	testClient := gitlabtesting.NewTestClient(t)
	testClient.MockProjectVariables.EXPECT().
		CreateVariable("owner/repo", gomock.Any(), gomock.Any()).
		Return(nil, nil, alreadyTakenErr())

	file := writeVariablesFile(t, `[{"key":"DUP","value":"x","variable_type":"env_var","environment_scope":"*"}]`)
	opts, _ := newOptions(t, testClient, file)

	// WHEN
	err := opts.run(t.Context())

	// THEN
	require.Error(t, err)
	assert.ErrorContains(t, err, "DUP")
}

func Test_importRun_emptyInput(t *testing.T) {
	// GIVEN
	testClient := gitlabtesting.NewTestClient(t)
	file := writeVariablesFile(t, `[]`)
	opts, stdout := newOptions(t, testClient, file)

	// WHEN
	err := opts.run(t.Context())

	// THEN
	require.NoError(t, err)
	assert.Contains(t, stdout(), "No variables to import.")
}

func Test_importRun_invalidKey(t *testing.T) {
	// GIVEN
	testClient := gitlabtesting.NewTestClient(t)
	// No CreateVariable expectation: an invalid key must be caught before any
	// variable is created, so a later bad entry can't leave a partial import.
	file := writeVariablesFile(t, `[
		{"key":"ONE","value":"1","variable_type":"env_var","environment_scope":"*"},
		{"key":"not a valid key!","value":"2","variable_type":"env_var","environment_scope":"*"}
	]`)
	opts, _ := newOptions(t, testClient, file)

	// WHEN
	err := opts.run(t.Context())

	// THEN
	require.Error(t, err)
	assert.ErrorContains(t, err, "not a valid key!")
}

func Test_importRun_defaultsEmptyEnvironmentScope(t *testing.T) {
	// GIVEN
	testClient := gitlabtesting.NewTestClient(t)
	testClient.MockProjectVariables.EXPECT().
		CreateVariable("owner/repo", gomock.Cond(func(opt *gitlab.CreateProjectVariableOptions) bool {
			return opt.EnvironmentScope != nil && *opt.EnvironmentScope == "*"
		}), gomock.Any()).
		Return(&gitlab.ProjectVariable{}, nil, nil)

	file := writeVariablesFile(t, `[{"key":"ONE","value":"1","variable_type":"env_var"}]`)
	opts, _ := newOptions(t, testClient, file)

	// WHEN
	err := opts.run(t.Context())

	// THEN
	require.NoError(t, err)
}

func Test_importRun_skipsHiddenWithEmptyValue(t *testing.T) {
	// GIVEN
	testClient := gitlabtesting.NewTestClient(t)
	// No CreateVariable expectation: a hidden variable with no value (as
	// `export` produces, since hidden values aren't retrievable) is skipped
	// rather than recreated empty.

	file := writeVariablesFile(t, `[{"key":"SECRET","value":"","variable_type":"env_var","environment_scope":"*","hidden":true}]`)
	opts, stdout := newOptions(t, testClient, file)

	// WHEN
	err := opts.run(t.Context())

	// THEN
	require.NoError(t, err)
	out := stdout()
	assert.Contains(t, out, "Skipped SECRET: hidden variables' values aren't included")
	assert.Contains(t, out, "Imported 0 variables into owner/repo (1 skipped).")
}

func Test_importRun_project_masked_and_hidden_omitted(t *testing.T) {
	testClient := gitlabtesting.NewTestClient(t)

	var capturedOpts *gitlab.CreateProjectVariableOptions
	testClient.MockProjectVariables.EXPECT().
		CreateVariable("owner/repo", gomock.Any(), gomock.Any()).
		DoAndReturn(func(pid any, opts *gitlab.CreateProjectVariableOptions, _ ...gitlab.RequestOptionFunc) (*gitlab.ProjectVariable, *gitlab.Response, error) {
			capturedOpts = opts
			return &gitlab.ProjectVariable{Key: "MY_VAR"}, nil, nil
		})

	file := writeVariablesFile(t, `[{"key":"MY_VAR","value":"val","variable_type":"env_var","environment_scope":"*"}]`)
	opts, _ := newOptions(t, testClient, file)

	require.NoError(t, opts.run(t.Context()))
	assert.Nil(t, capturedOpts.MaskedAndHidden)
}

func Test_importRun_project_masked_and_hidden_sent(t *testing.T) {
	testClient := gitlabtesting.NewTestClient(t)

	var capturedOpts *gitlab.CreateProjectVariableOptions
	testClient.MockProjectVariables.EXPECT().
		CreateVariable("owner/repo", gomock.Any(), gomock.Any()).
		DoAndReturn(func(pid any, opts *gitlab.CreateProjectVariableOptions, _ ...gitlab.RequestOptionFunc) (*gitlab.ProjectVariable, *gitlab.Response, error) {
			capturedOpts = opts
			return &gitlab.ProjectVariable{Key: "MY_VAR"}, nil, nil
		})

	file := writeVariablesFile(t, `[{"key":"MY_VAR","value":"val","variable_type":"env_var","environment_scope":"*","hidden":true}]`)
	opts, _ := newOptions(t, testClient, file)

	require.NoError(t, opts.run(t.Context()))
	require.NotNil(t, capturedOpts.MaskedAndHidden)
	assert.True(t, *capturedOpts.MaskedAndHidden)
}

func Test_importRun_group_masked_and_hidden_omitted(t *testing.T) {
	testClient := gitlabtesting.NewTestClient(t)

	var capturedOpts *gitlab.CreateGroupVariableOptions
	testClient.MockGroupVariables.EXPECT().
		CreateVariable("mygroup", gomock.Any(), gomock.Any()).
		DoAndReturn(func(gid any, opts *gitlab.CreateGroupVariableOptions, _ ...gitlab.RequestOptionFunc) (*gitlab.GroupVariable, *gitlab.Response, error) {
			capturedOpts = opts
			return &gitlab.GroupVariable{Key: "MY_VAR"}, nil, nil
		})

	file := writeVariablesFile(t, `[{"key":"MY_VAR","value":"val","variable_type":"env_var","environment_scope":"*"}]`)
	opts, _ := newOptions(t, testClient, file)
	opts.group = "mygroup"

	require.NoError(t, opts.run(t.Context()))
	assert.Nil(t, capturedOpts.MaskedAndHidden)
}

func Test_importRun_group_masked_and_hidden_sent(t *testing.T) {
	testClient := gitlabtesting.NewTestClient(t)

	var capturedOpts *gitlab.CreateGroupVariableOptions
	testClient.MockGroupVariables.EXPECT().
		CreateVariable("mygroup", gomock.Any(), gomock.Any()).
		DoAndReturn(func(gid any, opts *gitlab.CreateGroupVariableOptions, _ ...gitlab.RequestOptionFunc) (*gitlab.GroupVariable, *gitlab.Response, error) {
			capturedOpts = opts
			return &gitlab.GroupVariable{Key: "MY_VAR"}, nil, nil
		})

	file := writeVariablesFile(t, `[{"key":"MY_VAR","value":"val","variable_type":"env_var","environment_scope":"*","hidden":true}]`)
	opts, _ := newOptions(t, testClient, file)
	opts.group = "mygroup"

	require.NoError(t, opts.run(t.Context()))
	require.NotNil(t, capturedOpts.MaskedAndHidden)
	assert.True(t, *capturedOpts.MaskedAndHidden)
}
