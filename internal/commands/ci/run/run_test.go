//go:build !integration

package run

import (
	"fmt"
	"os/exec"
	"testing"

	"git.sr.ht/~timofurrer/ugh"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v2/testing"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/run"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
	"gitlab.com/gitlab-org/cli/test"
)

func TestParseVarArg_EmptyKey(t *testing.T) {
	t.Parallel()

	for _, input := range []string{":value", "   :value"} {
		_, err := parseVarArg(input)

		require.EqualError(t, err, "variable key cannot be empty")
	}
}

func TestParseVarArg_TrimsKey(t *testing.T) {
	t.Parallel()

	got, err := parseVarArg(" FOO :value")

	require.NoError(t, err)
	require.NotNil(t, got.Key)
	assert.Equal(t, "FOO", *got.Key)
}

func TestCIRun(t *testing.T) {
	tests := []struct {
		name        string
		cli         string
		expectedRef string
		expectedOut string
		expectedErr string
		setupMock   func(tc *gitlabtesting.TestClient, expectedRef string)
	}{
		{
			name:        "when running `ci run` without any parameter, defaults to current branch",
			cli:         "",
			expectedRef: "custom-branch-123",
			expectedOut: "Created pipeline (id: 123), status: created, ref: custom-branch-123, weburl: https://gitlab.com/OWNER/REPO/-/pipelines/123\n",
			setupMock: func(tc *gitlabtesting.TestClient, expectedRef string) {
				tc.MockPipelines.EXPECT().
					CreatePipeline("OWNER/REPO", gomock.Any()).
					DoAndReturn(func(pid any, opt *gitlab.CreatePipelineOptions, options ...gitlab.RequestOptionFunc) (*gitlab.Pipeline, *gitlab.Response, error) {
						return &gitlab.Pipeline{
							ID:     123,
							IID:    123,
							Status: "created",
							Ref:    *opt.Ref,
							WebURL: "https://gitlab.com/OWNER/REPO/-/pipelines/123",
						}, nil, nil
					})
			},
		},
		{
			name:        "when running `ci run` with branch parameter, run CI at branch",
			cli:         "-b ci-cd-improvement-399",
			expectedRef: "ci-cd-improvement-399",
			expectedOut: "Created pipeline (id: 123), status: created, ref: ci-cd-improvement-399, weburl: https://gitlab.com/OWNER/REPO/-/pipelines/123\n",
			setupMock: func(tc *gitlabtesting.TestClient, expectedRef string) {
				tc.MockPipelines.EXPECT().
					CreatePipeline("OWNER/REPO", gomock.Any()).
					DoAndReturn(func(pid any, opt *gitlab.CreatePipelineOptions, options ...gitlab.RequestOptionFunc) (*gitlab.Pipeline, *gitlab.Response, error) {
						return &gitlab.Pipeline{
							ID:     123,
							IID:    123,
							Status: "created",
							Ref:    *opt.Ref,
							WebURL: "https://gitlab.com/OWNER/REPO/-/pipelines/123",
						}, nil, nil
					})
			},
		},
		{
			name:        "when running `ci run` with --web opens the browser",
			cli:         "-b web-branch --web",
			expectedRef: "web-branch",
			expectedErr: "Opening gitlab.com/OWNER/REPO/-/pipelines/123 in your browser.\n",
			setupMock: func(tc *gitlabtesting.TestClient, expectedRef string) {
				tc.MockPipelines.EXPECT().
					CreatePipeline("OWNER/REPO", gomock.Any()).
					DoAndReturn(func(pid any, opt *gitlab.CreatePipelineOptions, options ...gitlab.RequestOptionFunc) (*gitlab.Pipeline, *gitlab.Response, error) {
						return &gitlab.Pipeline{
							ID:     123,
							IID:    123,
							Status: "created",
							Ref:    *opt.Ref,
							WebURL: "https://gitlab.com/OWNER/REPO/-/pipelines/123",
						}, nil, nil
					})
			},
		},
		{
			name:        "when running `ci run` with variables",
			cli:         "-b main --variables FOO:bar",
			expectedRef: "main",
			expectedOut: "Created pipeline (id: 123), status: created, ref: main, weburl: https://gitlab.com/OWNER/REPO/-/pipelines/123\n",
			setupMock: func(tc *gitlabtesting.TestClient, expectedRef string) {
				tc.MockPipelines.EXPECT().
					CreatePipeline("OWNER/REPO", gomock.Any()).
					DoAndReturn(func(pid any, opt *gitlab.CreatePipelineOptions, options ...gitlab.RequestOptionFunc) (*gitlab.Pipeline, *gitlab.Response, error) {
						// Verify variables are passed
						require.Len(t, *opt.Variables, 1)
						assert.Equal(t, "FOO", *(*opt.Variables)[0].Key)
						assert.Equal(t, "bar", *(*opt.Variables)[0].Value)
						return &gitlab.Pipeline{
							ID:     123,
							IID:    123,
							Status: "created",
							Ref:    *opt.Ref,
							WebURL: "https://gitlab.com/OWNER/REPO/-/pipelines/123",
						}, nil, nil
					})
			},
		},
		{
			name:        "when running `ci run` with multiple variables",
			cli:         "-b main --variables FOO:bar --variables BAR:xxx",
			expectedRef: "main",
			expectedOut: "Created pipeline (id: 123), status: created, ref: main, weburl: https://gitlab.com/OWNER/REPO/-/pipelines/123\n",
			setupMock: func(tc *gitlabtesting.TestClient, expectedRef string) {
				tc.MockPipelines.EXPECT().
					CreatePipeline("OWNER/REPO", gomock.Any()).
					DoAndReturn(func(pid any, opt *gitlab.CreatePipelineOptions, options ...gitlab.RequestOptionFunc) (*gitlab.Pipeline, *gitlab.Response, error) {
						require.Len(t, *opt.Variables, 2)
						assert.Equal(t, "FOO", *(*opt.Variables)[0].Key)
						assert.Equal(t, "bar", *(*opt.Variables)[0].Value)
						assert.Equal(t, "BAR", *(*opt.Variables)[1].Key)
						assert.Equal(t, "xxx", *(*opt.Variables)[1].Value)
						return &gitlab.Pipeline{
							ID:     123,
							IID:    123,
							Status: "created",
							Ref:    *opt.Ref,
							WebURL: "https://gitlab.com/OWNER/REPO/-/pipelines/123",
						}, nil, nil
					})
			},
		},
		{
			name:        "when running `ci run` with variables-env",
			cli:         "-b main --variables-env FOO:bar",
			expectedRef: "main",
			expectedOut: "Created pipeline (id: 123), status: created, ref: main, weburl: https://gitlab.com/OWNER/REPO/-/pipelines/123\n",
			setupMock: func(tc *gitlabtesting.TestClient, expectedRef string) {
				tc.MockPipelines.EXPECT().
					CreatePipeline("OWNER/REPO", gomock.Any()).
					DoAndReturn(func(pid any, opt *gitlab.CreatePipelineOptions, options ...gitlab.RequestOptionFunc) (*gitlab.Pipeline, *gitlab.Response, error) {
						require.Len(t, *opt.Variables, 1)
						assert.Equal(t, "FOO", *(*opt.Variables)[0].Key)
						assert.Equal(t, "bar", *(*opt.Variables)[0].Value)
						return &gitlab.Pipeline{
							ID:     123,
							IID:    123,
							Status: "created",
							Ref:    *opt.Ref,
							WebURL: "https://gitlab.com/OWNER/REPO/-/pipelines/123",
						}, nil, nil
					})
			},
		},
		{
			name:        "when running `ci run` with multiple variables-env",
			cli:         "-b main --variables-env FOO:bar --variables-env BAR:xxx",
			expectedRef: "main",
			expectedOut: "Created pipeline (id: 123), status: created, ref: main, weburl: https://gitlab.com/OWNER/REPO/-/pipelines/123\n",
			setupMock: func(tc *gitlabtesting.TestClient, expectedRef string) {
				tc.MockPipelines.EXPECT().
					CreatePipeline("OWNER/REPO", gomock.Any()).
					DoAndReturn(func(pid any, opt *gitlab.CreatePipelineOptions, options ...gitlab.RequestOptionFunc) (*gitlab.Pipeline, *gitlab.Response, error) {
						require.Len(t, *opt.Variables, 2)
						return &gitlab.Pipeline{
							ID:     123,
							IID:    123,
							Status: "created",
							Ref:    *opt.Ref,
							WebURL: "https://gitlab.com/OWNER/REPO/-/pipelines/123",
						}, nil, nil
					})
			},
		},
		{
			name:        "when running `ci run` with mixed variables-env and variables",
			cli:         "-b main --variables-env FOO:bar --variables BAR:xxx",
			expectedRef: "main",
			expectedOut: "Created pipeline (id: 123), status: created, ref: main, weburl: https://gitlab.com/OWNER/REPO/-/pipelines/123\n",
			setupMock: func(tc *gitlabtesting.TestClient, expectedRef string) {
				tc.MockPipelines.EXPECT().
					CreatePipeline("OWNER/REPO", gomock.Any()).
					DoAndReturn(func(pid any, opt *gitlab.CreatePipelineOptions, options ...gitlab.RequestOptionFunc) (*gitlab.Pipeline, *gitlab.Response, error) {
						require.Len(t, *opt.Variables, 2)
						return &gitlab.Pipeline{
							ID:     123,
							IID:    123,
							Status: "created",
							Ref:    *opt.Ref,
							WebURL: "https://gitlab.com/OWNER/REPO/-/pipelines/123",
						}, nil, nil
					})
			},
		},
		{
			name:        "when running `ci run` with untyped input",
			cli:         "-b main -i key1:val1 --input key2:val2",
			expectedRef: "main",
			expectedOut: "Created pipeline (id: 123), status: created, ref: main, weburl: https://gitlab.com/OWNER/REPO/-/pipelines/123\n",
			setupMock: func(tc *gitlabtesting.TestClient, expectedRef string) {
				tc.MockPipelines.EXPECT().
					CreatePipeline("OWNER/REPO", gomock.Any()).
					DoAndReturn(func(pid any, opt *gitlab.CreatePipelineOptions, options ...gitlab.RequestOptionFunc) (*gitlab.Pipeline, *gitlab.Response, error) {
						// Verify inputs are passed
						require.NotNil(t, opt.Inputs)
						assert.Equal(t, gitlab.PipelineInputValue[string]{Value: "val1"}, opt.Inputs["key1"])
						assert.Equal(t, gitlab.PipelineInputValue[string]{Value: "val2"}, opt.Inputs["key2"])
						return &gitlab.Pipeline{
							ID:     123,
							IID:    123,
							Status: "created",
							Ref:    *opt.Ref,
							WebURL: "https://gitlab.com/OWNER/REPO/-/pipelines/123",
						}, nil, nil
					})
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// GIVEN
			testClient := gitlabtesting.NewTestClient(t)
			tc.setupMock(testClient, tc.expectedRef)

			execFunc := cmdtest.SetupCmdForTest(t, NewCmdRun, true,
				cmdtest.WithGitLabClient(testClient.Client),
				cmdtest.WithBranch("custom-branch-123"),
			)
			restoreCmd := run.SetPrepareCmd(func(cmd *exec.Cmd) run.Runnable {
				return &test.OutputStub{}
			})
			t.Cleanup(restoreCmd)

			// WHEN
			out, err := execFunc(tc.cli)

			// THEN
			require.NoErrorf(t, err, "error running command `ci run %s`: %v", tc.cli, err)

			assert.Equal(t, tc.expectedOut, out.OutBuf.String())
			assert.Equal(t, tc.expectedErr, out.ErrBuf.String())
		})
	}
}

func TestCIRunMrPipeline(t *testing.T) {
	tests := []struct {
		name        string
		cli         string
		expectedOut string
		expectedErr string
		mrIid       int
	}{
		{
			name:        "bare mr flag",
			cli:         "--mr",
			expectedOut: "Created pipeline (id: 21370), status: created, ref: , weburl: https://gitlab.com/OWNER/REPO/-/pipelines/21370\n",
			mrIid:       2137,
		},
		{
			name:        "mr flag with branch specified",
			cli:         "--mr --branch branchy",
			expectedOut: "Created pipeline (id: 7350), status: created, ref: , weburl: https://gitlab.com/OWNER/REPO/-/pipelines/7350\n",
			mrIid:       735,
		},
		{
			name:        "mr flag with branch specified & multiple MRs",
			cli:         "--mr --branch my_branch_with_a_myriad_of_mrs",
			expectedOut: "Created pipeline (id: 12340), status: created, ref: , weburl: https://gitlab.com/OWNER/REPO/-/pipelines/12340\n",
			mrIid:       1234,
		},
		{
			name:        "mr flag with branch specified & no MRs",
			cli:         "--mr --branch branch_without_mrs",
			expectedErr: "branch_without_mrs",
			mrIid:       1234,
		},
		{
			name:        "MR with variable flag",
			cli:         "--mr --variables key:val",
			expectedErr: "if any flags in the group [mr variables] are set none of the others can be",
			mrIid:       1235,
		},
		{
			name:        "MR with input flag",
			cli:         "--mr --input key:val",
			expectedErr: "if any flags in the group [mr input] are set none of the others can be",
			mrIid:       1236,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// GIVEN
			testClient := gitlabtesting.NewTestClient(t)

			if tc.expectedErr == "" {
				iid := int64(tc.mrIid)
				pipelineId := iid * 10
				testClient.MockMergeRequests.EXPECT().
					CreateMergeRequestPipeline("OWNER/REPO", iid).
					Return(&gitlab.PipelineInfo{
						ID:     pipelineId,
						Status: "created",
						WebURL: fmt.Sprintf("https://gitlab.com/OWNER/REPO/-/pipelines/%d", pipelineId),
					}, nil, nil)
			}

			api.ListMRs = func(client *gitlab.Client, projectID any, opts *gitlab.ListProjectMergeRequestsOptions, listOpts ...api.CliListMROption) ([]*gitlab.BasicMergeRequest, error) {
				if *opts.SourceBranch == "custom-branch-123" {
					return []*gitlab.BasicMergeRequest{
						{
							IID: 2137,
							Author: &gitlab.BasicUser{
								Username: "Huan Pablo Secundo",
							},
						},
					}, nil
				}
				if *opts.SourceBranch == "branchy" {
					return []*gitlab.BasicMergeRequest{
						{
							IID: 735,
							Author: &gitlab.BasicUser{
								Username: "Franciszek",
							},
						},
					}, nil
				}
				if *opts.SourceBranch == "my_branch_with_a_myriad_of_mrs" {
					return []*gitlab.BasicMergeRequest{
						{
							IID: 1234,
							Author: &gitlab.BasicUser{
								Username: "Chris Harms",
							},
						},
						{
							IID: 666,
							Author: &gitlab.BasicUser{
								Username: "Bruce Dickinson",
							},
						},
					}, nil
				}
				if *opts.SourceBranch == "branch_without_mrs" {
					return []*gitlab.BasicMergeRequest{}, nil
				}
				return nil, fmt.Errorf("unexpected branch in this mock :(")
			}

			factoryOpts := []cmdtest.FactoryOption{
				cmdtest.WithGitLabClient(testClient.Client),
				cmdtest.WithBranch("custom-branch-123"),
			}

			if tc.cli == "--mr --branch my_branch_with_a_myriad_of_mrs" {
				c := ugh.New(t)
				c.Expect(ugh.SelectRegexp("Multiple merge requests exist for this branch")).
					Do(ugh.SelectIndex(0))
				factoryOpts = append(factoryOpts, cmdtest.WithConsole(t, c))
			}

			execFunc := cmdtest.SetupCmdForTest(t, NewCmdRun, true,
				factoryOpts...,
			)
			restoreCmd := run.SetPrepareCmd(func(cmd *exec.Cmd) run.Runnable {
				return &test.OutputStub{}
			})
			t.Cleanup(restoreCmd)

			// WHEN
			out, err := execFunc(tc.cli)

			// THEN
			if tc.expectedErr == "" {
				require.NoErrorf(t, err, "error running command `ci run %s`: %v", tc.cli, err)

				assert.Contains(t, out.OutBuf.String(), tc.expectedOut)
				assert.Equal(t, tc.expectedErr, out.ErrBuf.String())
			} else {
				require.ErrorContains(t, err, tc.expectedErr)
			}
		})
	}
}
