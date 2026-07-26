//go:build !integration

package run_trig

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v2/testing"

	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestParseVarArg_EmptyKey(t *testing.T) {
	t.Parallel()

	for _, input := range []string{":value", "   :value"} {
		_, _, err := parseVarArg(input)

		require.EqualError(t, err, "variable key cannot be empty")
	}
}

func TestParseVarArg_TrimsKey(t *testing.T) {
	t.Parallel()

	key, value, err := parseVarArg(" FOO :value")

	require.NoError(t, err)
	assert.Equal(t, "FOO", key)
	assert.Equal(t, "value", value)
}

func TestCIRunTrig(t *testing.T) {
	// Cannot use t.Parallel() because subtests use t.Setenv()

	tests := []struct {
		name        string
		cli         string
		ciJobToken  string
		expectedRef string
		expectedOut string
	}{
		{
			name:        "when running `ci run-trig` without branch parameter, defaults to current branch",
			cli:         "-t foobar",
			ciJobToken:  "",
			expectedRef: "custom-branch-123",
			expectedOut: "Created pipeline (ID: 123), status: created, ref: custom-branch-123, weburl: https://gitlab.com/OWNER/REPO/-/pipelines/123\n",
		},
		{
			name:        "when running `ci run-trig` with branch parameter, run CI at branch",
			cli:         "-t foobar -b ci-cd-improvement-399",
			ciJobToken:  "",
			expectedRef: "ci-cd-improvement-399",
			expectedOut: "Created pipeline (ID: 123), status: created, ref: ci-cd-improvement-399, weburl: https://gitlab.com/OWNER/REPO/-/pipelines/123\n",
		},
		{
			name:        "when running `ci run-trig` without any parameter, takes trigger token from env variable",
			cli:         "",
			ciJobToken:  "foobar",
			expectedRef: "custom-branch-123",
			expectedOut: "Created pipeline (ID: 123), status: created, ref: custom-branch-123, weburl: https://gitlab.com/OWNER/REPO/-/pipelines/123\n",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("CI_JOB_TOKEN", tc.ciJobToken)

			testClient := gitlabtesting.NewTestClient(t)

			testClient.MockPipelineTriggers.EXPECT().
				RunPipelineTrigger("OWNER/REPO", gomock.Any()).
				DoAndReturn(func(pid any, opts *gitlab.RunPipelineTriggerOptions, options ...gitlab.RequestOptionFunc) (*gitlab.Pipeline, *gitlab.Response, error) {
					// Verify the token is passed correctly
					assert.Equal(t, "foobar", *opts.Token)
					// Verify the ref matches expected
					assert.Equal(t, tc.expectedRef, *opts.Ref)

					return &gitlab.Pipeline{
						ID:     123,
						IID:    123,
						Status: "created",
						Ref:    *opts.Ref,
						WebURL: "https://gitlab.com/OWNER/REPO/-/pipelines/123",
					}, nil, nil
				})

			exec := cmdtest.SetupCmdForTest(t, NewCmdRunTrig, false,
				cmdtest.WithGitLabClient(testClient.Client),
				cmdtest.WithBaseRepo("OWNER", "REPO", ""),
				cmdtest.WithBranch("custom-branch-123"),
			)

			output, err := exec(tc.cli)
			require.NoError(t, err)

			assert.Equal(t, tc.expectedOut, output.String())
			assert.Empty(t, output.Stderr())
		})
	}
}

func TestCIRunTrigWithInputs(t *testing.T) {
	// NOTE: This test cannot run in parallel because NewCmdRunTrig modifies global pflag state.

	testClient := gitlabtesting.NewTestClient(t)

	testClient.MockPipelineTriggers.EXPECT().
		RunPipelineTrigger("OWNER/REPO", gomock.Any()).
		DoAndReturn(func(pid any, opts *gitlab.RunPipelineTriggerOptions, options ...gitlab.RequestOptionFunc) (*gitlab.Pipeline, *gitlab.Response, error) {
			// Verify the token
			assert.Equal(t, "foobar", *opts.Token)
			// Verify the ref
			assert.Equal(t, "custom-branch-123", *opts.Ref)
			// Verify inputs are passed (check keys and values exist)
			assert.Len(t, opts.Inputs, 2)
			assert.Contains(t, opts.Inputs, "key1")
			assert.Contains(t, opts.Inputs, "key2")

			return &gitlab.Pipeline{
				ID:     123,
				IID:    123,
				Status: "created",
				Ref:    *opts.Ref,
				WebURL: "https://gitlab.com/OWNER/REPO/-/pipelines/123",
			}, nil, nil
		})

	exec := cmdtest.SetupCmdForTest(t, NewCmdRunTrig, false,
		cmdtest.WithGitLabClient(testClient.Client),
		cmdtest.WithBaseRepo("OWNER", "REPO", ""),
		cmdtest.WithBranch("custom-branch-123"),
	)

	output, err := exec("-t foobar -i key1:val1 --input key2:val2")
	require.NoError(t, err)

	assert.Equal(t, "Created pipeline (ID: 123), status: created, ref: custom-branch-123, weburl: https://gitlab.com/OWNER/REPO/-/pipelines/123\n", output.String())
	assert.Empty(t, output.Stderr())
}

func TestCIRunTrigWithVariables(t *testing.T) {
	// NOTE: This test cannot run in parallel because NewCmdRunTrig modifies global pflag state.

	testClient := gitlabtesting.NewTestClient(t)

	testClient.MockPipelineTriggers.EXPECT().
		RunPipelineTrigger("OWNER/REPO", gomock.Any()).
		DoAndReturn(func(pid any, opts *gitlab.RunPipelineTriggerOptions, options ...gitlab.RequestOptionFunc) (*gitlab.Pipeline, *gitlab.Response, error) {
			// Verify the token
			assert.Equal(t, "foobar", *opts.Token)
			// Verify variables are passed
			assert.Equal(t, map[string]string{"VAR1": "value1", "VAR2": "value2"}, opts.Variables)

			return &gitlab.Pipeline{
				ID:     123,
				IID:    123,
				Status: "created",
				Ref:    *opts.Ref,
				WebURL: "https://gitlab.com/OWNER/REPO/-/pipelines/123",
			}, nil, nil
		})

	exec := cmdtest.SetupCmdForTest(t, NewCmdRunTrig, false,
		cmdtest.WithGitLabClient(testClient.Client),
		cmdtest.WithBaseRepo("OWNER", "REPO", ""),
		cmdtest.WithBranch("main"),
	)

	output, err := exec("-t foobar --variables VAR1:value1,VAR2:value2")
	require.NoError(t, err)

	assert.Equal(t, "Created pipeline (ID: 123), status: created, ref: main, weburl: https://gitlab.com/OWNER/REPO/-/pipelines/123\n", output.String())
	assert.Empty(t, output.Stderr())
}

func TestCIRunTrigMissingToken(t *testing.T) {
	// Cannot use t.Parallel() because test uses t.Setenv()

	// Ensure CI_JOB_TOKEN is not set
	t.Setenv("CI_JOB_TOKEN", "")

	testClient := gitlabtesting.NewTestClient(t)

	// No mock expectation since the command should fail before making API call

	exec := cmdtest.SetupCmdForTest(t, NewCmdRunTrig, false,
		cmdtest.WithGitLabClient(testClient.Client),
		cmdtest.WithBaseRepo("OWNER", "REPO", ""),
		cmdtest.WithBranch("main"),
	)

	_, err := exec("")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "`--token` parameter can be omitted only if `CI_JOB_TOKEN` environment variable is set")
}
