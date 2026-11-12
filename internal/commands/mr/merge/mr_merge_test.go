//go:build !integration

package merge

import (
	"errors"
	"net/http"
	"testing"

	"git.sr.ht/~timofurrer/ugh"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"

	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestMrMerge(t *testing.T) {
	type testCase struct {
		name        string
		cli         string
		expectedOut string
		wantErr     bool
		wantStderr  string
		setupMock   func(tc *gitlabtesting.TestClient)
	}

	mergedMR := &gitlab.MergeRequest{
		BasicMergeRequest: gitlab.BasicMergeRequest{
			ID:        190608322,
			IID:       123,
			ProjectID: 37777023,
			Title:     "foo",
			State:     "merged",
			WebURL:    "https://gitlab.com/OWNER/REPO/-/merge_requests/123",
		},
	}
	mergedPipelineMR := &gitlab.MergeRequest{
		BasicMergeRequest: gitlab.BasicMergeRequest{
			ID:        190608322,
			IID:       123,
			ProjectID: 37777023,
			Title:     "foo",
			State:     "merged",
			WebURL:    "https://gitlab.com/OWNER/REPO/-/merge_requests/123",
		},
		Pipeline: &gitlab.PipelineInfo{Status: "success"},
	}

	testCases := []testCase{
		{
			name: "Merge MR by ID without pipeline",
			cli:  "123",
			// Note: The test verifies the merge flow works correctly
			// The "No pipeline has been run on" warning appears when Pipeline is nil
			// Note: SourceBranch and Pipeline appear empty when using mock client
			// This test verifies the merge flow works; other tests cover pipeline scenarios
			expectedOut: "! No pipeline has been run on \n✓ Merged!\nhttps://gitlab.com/OWNER/REPO/-/merge_requests/123\n",
			setupMock: func(tc *gitlabtesting.TestClient) {
				getMR := &gitlab.MergeRequest{
					BasicMergeRequest: gitlab.BasicMergeRequest{
						ID:                  190608322,
						IID:                 123,
						ProjectID:           37777023,
						Title:               "foo",
						State:               "opened",
						SourceBranch:        "1-issue-20",
						WebURL:              "https://gitlab.com/OWNER/REPO/-/merge_requests/123",
						DetailedMergeStatus: "mergeable",
					},
					User: gitlab.MergeRequestUser{
						CanMerge: true,
					},
				}
				tc.MockMergeRequests.EXPECT().
					GetMergeRequest("OWNER/REPO", int64(123), gomock.Any()).
					Return(getMR, nil, nil)
				tc.MockMergeRequests.EXPECT().
					AcceptMergeRequest("OWNER/REPO", int64(123), gomock.Any()).
					Return(mergedMR, nil, nil)
			},
		},
		{
			name:       "returns transport error without a response",
			cli:        "123",
			wantErr:    true,
			wantStderr: "network unavailable",
			setupMock: func(tc *gitlabtesting.TestClient) {
				getMR := &gitlab.MergeRequest{
					BasicMergeRequest: gitlab.BasicMergeRequest{
						IID:                 123,
						State:               "opened",
						DetailedMergeStatus: "mergeable",
					},
					User: gitlab.MergeRequestUser{CanMerge: true},
				}
				tc.MockMergeRequests.EXPECT().
					GetMergeRequest("OWNER/REPO", int64(123), gomock.Any()).
					Return(getMR, nil, nil)
				tc.MockMergeRequests.EXPECT().
					AcceptMergeRequest("OWNER/REPO", int64(123), gomock.Any()).
					Return(nil, nil, errors.New("network unavailable"))
			},
		},
		{
			name: "Auto-merge armed on successful pipeline does not report merged",
			cli:  "123",
			// The pipeline succeeded but the MR still requires an approval, so the
			// API only arms auto-merge and the MR stays open. We must NOT print
			// "Merged!" here — only that auto-merge is enabled.
			expectedOut: "✓ Pipeline succeeded.\n✓ Auto-merge enabled\nhttps://gitlab.com/OWNER/REPO/-/merge_requests/123\n",
			setupMock: func(tc *gitlabtesting.TestClient) {
				getMR := &gitlab.MergeRequest{
					BasicMergeRequest: gitlab.BasicMergeRequest{
						ID:                  190608322,
						IID:                 123,
						ProjectID:           37777023,
						Title:               "foo",
						State:               "opened",
						SourceBranch:        "1-issue-20",
						WebURL:              "https://gitlab.com/OWNER/REPO/-/merge_requests/123",
						DetailedMergeStatus: "not_approved",
					},
					Pipeline: &gitlab.PipelineInfo{Status: "success"},
					User: gitlab.MergeRequestUser{
						CanMerge: true,
					},
				}
				// AcceptMergeRequest with auto-merge returns the still-open MR
				// (armed for auto-merge), not a merged one.
				armedMR := &gitlab.MergeRequest{
					BasicMergeRequest: gitlab.BasicMergeRequest{
						ID:                        190608322,
						IID:                       123,
						ProjectID:                 37777023,
						Title:                     "foo",
						State:                     "opened",
						SourceBranch:              "1-issue-20",
						WebURL:                    "https://gitlab.com/OWNER/REPO/-/merge_requests/123",
						DetailedMergeStatus:       "not_approved",
						MergeWhenPipelineSucceeds: true,
					},
					Pipeline: &gitlab.PipelineInfo{Status: "success"},
					User: gitlab.MergeRequestUser{
						CanMerge: true,
					},
				}
				tc.MockMergeRequests.EXPECT().
					GetMergeRequest("OWNER/REPO", int64(123), gomock.Any()).
					Return(getMR, nil, nil)
				tc.MockMergeRequests.EXPECT().
					AcceptMergeRequest("OWNER/REPO", int64(123), gomock.Any()).
					Return(armedMR, nil, nil)
			},
		},
		{
			name: "Open response after immediate merge reports the merge status",
			cli:  "123",
			// AcceptMergeRequest returned an open MR that is not armed for
			// auto-merge, so the merge did not complete synchronously. Report
			// the API's merge status verbatim rather than claiming "Merged!".
			expectedOut: "! No pipeline has been run on 1-issue-20\n! Merge status: mergeable\nhttps://gitlab.com/OWNER/REPO/-/merge_requests/123\n",
			setupMock: func(tc *gitlabtesting.TestClient) {
				getMR := &gitlab.MergeRequest{
					BasicMergeRequest: gitlab.BasicMergeRequest{
						IID:                 123,
						State:               "opened",
						SourceBranch:        "1-issue-20",
						WebURL:              "https://gitlab.com/OWNER/REPO/-/merge_requests/123",
						DetailedMergeStatus: "mergeable",
					},
					User: gitlab.MergeRequestUser{CanMerge: true},
				}
				asyncMR := &gitlab.MergeRequest{
					BasicMergeRequest: gitlab.BasicMergeRequest{
						IID:                 123,
						State:               "opened",
						SourceBranch:        "1-issue-20",
						WebURL:              "https://gitlab.com/OWNER/REPO/-/merge_requests/123",
						DetailedMergeStatus: "mergeable",
					},
				}
				tc.MockMergeRequests.EXPECT().
					GetMergeRequest("OWNER/REPO", int64(123), gomock.Any()).
					Return(getMR, nil, nil)
				tc.MockMergeRequests.EXPECT().
					AcceptMergeRequest("OWNER/REPO", int64(123), gomock.Any()).
					DoAndReturn(func(_ any, _ int64, opts *gitlab.AcceptMergeRequestOptions, _ ...gitlab.RequestOptionFunc) (*gitlab.MergeRequest, *gitlab.Response, error) {
						assert.Nil(t, opts.AutoMerge)
						return asyncMR, nil, nil
					})
			},
		},
		{
			name: "Open response with auto-merge disabled reports the merge status",
			cli:  "123 --auto-merge=false",
			// With auto-merge disabled and an open response, the merge did not
			// complete synchronously; report the merge status.
			expectedOut: "! Merge status: mergeable\nhttps://gitlab.com/OWNER/REPO/-/merge_requests/123\n",
			setupMock: func(tc *gitlabtesting.TestClient) {
				getMR := &gitlab.MergeRequest{
					BasicMergeRequest: gitlab.BasicMergeRequest{
						IID:                 123,
						State:               "opened",
						WebURL:              "https://gitlab.com/OWNER/REPO/-/merge_requests/123",
						DetailedMergeStatus: "mergeable",
					},
					Pipeline: &gitlab.PipelineInfo{Status: "success"},
					User:     gitlab.MergeRequestUser{CanMerge: true},
				}
				asyncMR := &gitlab.MergeRequest{
					BasicMergeRequest: gitlab.BasicMergeRequest{
						IID:                 123,
						State:               "opened",
						WebURL:              "https://gitlab.com/OWNER/REPO/-/merge_requests/123",
						DetailedMergeStatus: "mergeable",
					},
				}
				tc.MockMergeRequests.EXPECT().
					GetMergeRequest("OWNER/REPO", int64(123), gomock.Any()).
					Return(getMR, nil, nil)
				tc.MockMergeRequests.EXPECT().
					AcceptMergeRequest("OWNER/REPO", int64(123), gomock.Any()).
					DoAndReturn(func(_ any, _ int64, opts *gitlab.AcceptMergeRequestOptions, _ ...gitlab.RequestOptionFunc) (*gitlab.MergeRequest, *gitlab.Response, error) {
						assert.Nil(t, opts.AutoMerge)
						return asyncMR, nil, nil
					})
			},
		},
		{
			name:        "Auto-merge with successful pipeline reports merged when requirements are met",
			cli:         "123",
			expectedOut: "✓ Pipeline succeeded.\n✓ Merged!\nhttps://gitlab.com/OWNER/REPO/-/merge_requests/123\n",
			setupMock: func(tc *gitlabtesting.TestClient) {
				getMR := &gitlab.MergeRequest{
					BasicMergeRequest: gitlab.BasicMergeRequest{
						IID:                 123,
						State:               "opened",
						WebURL:              "https://gitlab.com/OWNER/REPO/-/merge_requests/123",
						DetailedMergeStatus: "mergeable",
					},
					Pipeline: &gitlab.PipelineInfo{Status: "success"},
					User:     gitlab.MergeRequestUser{CanMerge: true},
				}
				tc.MockMergeRequests.EXPECT().
					GetMergeRequest("OWNER/REPO", int64(123), gomock.Any()).
					Return(getMR, nil, nil)
				tc.MockMergeRequests.EXPECT().
					AcceptMergeRequest("OWNER/REPO", int64(123), gomock.Any()).
					DoAndReturn(func(_ any, _ int64, opts *gitlab.AcceptMergeRequestOptions, _ ...gitlab.RequestOptionFunc) (*gitlab.MergeRequest, *gitlab.Response, error) {
						require.NotNil(t, opts.AutoMerge)
						assert.True(t, *opts.AutoMerge)
						return mergedPipelineMR, nil, nil
					})
			},
		},
		{
			name:        "Auto-merge with running pipeline reports armed",
			cli:         "123",
			expectedOut: "! Pipeline status: running\n✓ Auto-merge enabled\nhttps://gitlab.com/OWNER/REPO/-/merge_requests/123\n",
			setupMock: func(tc *gitlabtesting.TestClient) {
				getMR := &gitlab.MergeRequest{
					BasicMergeRequest: gitlab.BasicMergeRequest{
						IID:                 123,
						State:               "opened",
						WebURL:              "https://gitlab.com/OWNER/REPO/-/merge_requests/123",
						DetailedMergeStatus: "checking",
					},
					Pipeline: &gitlab.PipelineInfo{Status: "running"},
					User:     gitlab.MergeRequestUser{CanMerge: true},
				}
				armedMR := &gitlab.MergeRequest{
					BasicMergeRequest: gitlab.BasicMergeRequest{
						IID:                       123,
						State:                     "opened",
						WebURL:                    "https://gitlab.com/OWNER/REPO/-/merge_requests/123",
						MergeWhenPipelineSucceeds: true,
					},
					Pipeline: &gitlab.PipelineInfo{Status: "running"},
				}
				tc.MockMergeRequests.EXPECT().
					GetMergeRequest("OWNER/REPO", int64(123), gomock.Any()).
					Return(getMR, nil, nil)
				tc.MockMergeRequests.EXPECT().
					AcceptMergeRequest("OWNER/REPO", int64(123), gomock.Any()).
					DoAndReturn(func(_ any, _ int64, opts *gitlab.AcceptMergeRequestOptions, _ ...gitlab.RequestOptionFunc) (*gitlab.MergeRequest, *gitlab.Response, error) {
						require.NotNil(t, opts.AutoMerge)
						assert.True(t, *opts.AutoMerge)
						return armedMR, nil, nil
					})
			},
		},
		{
			name:       "Rejects --sha with --rebase",
			cli:        "123 --sha abc123 --rebase",
			wantErr:    true,
			wantStderr: "none of the others can be",
			setupMock:  func(tc *gitlabtesting.TestClient) {},
		},
		{
			name:       "Immediate merge conflict returns an error without success output",
			cli:        "123 --auto-merge=false",
			wantErr:    true,
			wantStderr: "Branch cannot be merged",
			setupMock: func(tc *gitlabtesting.TestClient) {
				getMR := &gitlab.MergeRequest{
					BasicMergeRequest: gitlab.BasicMergeRequest{
						IID:                 123,
						State:               "opened",
						WebURL:              "https://gitlab.com/OWNER/REPO/-/merge_requests/123",
						DetailedMergeStatus: "mergeable",
					},
					User: gitlab.MergeRequestUser{CanMerge: true},
				}
				tc.MockMergeRequests.EXPECT().
					GetMergeRequest("OWNER/REPO", int64(123), gomock.Any()).
					Return(getMR, nil, nil)
				tc.MockMergeRequests.EXPECT().
					AcceptMergeRequest("OWNER/REPO", int64(123), gomock.Any()).
					DoAndReturn(func(_ any, _ int64, opts *gitlab.AcceptMergeRequestOptions, _ ...gitlab.RequestOptionFunc) (*gitlab.MergeRequest, *gitlab.Response, error) {
						assert.Nil(t, opts.AutoMerge)
						return nil, &gitlab.Response{Response: &http.Response{StatusCode: http.StatusMethodNotAllowed}}, errors.New("Branch cannot be merged")
					})
			},
		},
		{
			name: "Open, unarmed response with a blocker status reports the merge status",
			cli:  "123",
			// AcceptMergeRequest succeeded (2xx) but returned an open MR that was
			// neither merged nor armed for auto-merge, and DetailedMergeStatus
			// reports an outstanding approval. We surface that status verbatim
			// instead of claiming "Merged!".
			expectedOut: "! No pipeline has been run on 1-issue-20\n! Merge status: not_approved\nhttps://gitlab.com/OWNER/REPO/-/merge_requests/123\n",
			setupMock: func(tc *gitlabtesting.TestClient) {
				getMR := &gitlab.MergeRequest{
					BasicMergeRequest: gitlab.BasicMergeRequest{
						IID:                 123,
						State:               "opened",
						SourceBranch:        "1-issue-20",
						WebURL:              "https://gitlab.com/OWNER/REPO/-/merge_requests/123",
						DetailedMergeStatus: "mergeable",
					},
					User: gitlab.MergeRequestUser{CanMerge: true},
				}
				blockedMR := &gitlab.MergeRequest{
					BasicMergeRequest: gitlab.BasicMergeRequest{
						IID:                 123,
						State:               "opened",
						SourceBranch:        "1-issue-20",
						WebURL:              "https://gitlab.com/OWNER/REPO/-/merge_requests/123",
						DetailedMergeStatus: "not_approved",
					},
				}
				tc.MockMergeRequests.EXPECT().
					GetMergeRequest("OWNER/REPO", int64(123), gomock.Any()).
					Return(getMR, nil, nil)
				tc.MockMergeRequests.EXPECT().
					AcceptMergeRequest("OWNER/REPO", int64(123), gomock.Any()).
					DoAndReturn(func(_ any, _ int64, opts *gitlab.AcceptMergeRequestOptions, _ ...gitlab.RequestOptionFunc) (*gitlab.MergeRequest, *gitlab.Response, error) {
						assert.Nil(t, opts.AutoMerge)
						return blockedMR, nil, nil
					})
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// GIVEN
			testClient := gitlabtesting.NewTestClient(t)
			tc.setupMock(testClient)
			exec := cmdtest.SetupCmdForTest(
				t,
				NewCmdMerge,
				false,
				cmdtest.WithGitLabClient(testClient.Client),
			)

			// WHEN
			out, err := exec(tc.cli)

			// THEN
			if tc.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantStderr)
				assert.Equal(t, tc.expectedOut, out.OutBuf.String())
				assert.Empty(t, out.ErrBuf.String())
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.expectedOut, out.OutBuf.String())
			assert.Empty(t, out.ErrBuf.String())
		})
	}
}

func TestMergeMethodSurvey(t *testing.T) {
	tests := []struct {
		name        string
		allowRebase bool
		want        MRMergeMethod
	}{
		{name: "offers rebase by default", allowRebase: true, want: MRMergeMethodRebase},
		{name: "omits rebase when not allowed", allowRebase: false, want: MRMergeMethodSquash},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			console := ugh.New(t)
			console.Expect(ugh.Select("What merge method do you want to use?")).
				Do(ugh.SelectIndex(1))
			ios, cleanup := cmdtest.TestIOStreamsWithConsole(t, console)
			t.Cleanup(cleanup)

			got, err := mergeMethodSurvey(t.Context(), ios, tt.allowRebase)

			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

// TestMrMerge_ShaPromptOmitsRebase pins that the command, not only
// mergeMethodSurvey, leaves "Rebase and merge" out of the prompt when --sha is
// set: a rebase would replace the head and the merge would then fail with 409.
func TestMrMerge_ShaPromptOmitsRebase(t *testing.T) {
	// NOTE: This test cannot run in parallel because the huh form library
	// uses global state (charmbracelet/bubbles runeutil sanitizer).
	testClient := gitlabtesting.NewTestClient(t)
	testClient.MockMergeRequests.EXPECT().
		GetMergeRequest("OWNER/REPO", int64(123), gomock.Any()).
		Return(&gitlab.MergeRequest{
			BasicMergeRequest: gitlab.BasicMergeRequest{
				IID:                 123,
				State:               "opened",
				WebURL:              "https://gitlab.com/OWNER/REPO/-/merge_requests/123",
				DetailedMergeStatus: "mergeable",
			},
			User: gitlab.MergeRequestUser{CanMerge: true},
		}, nil, nil)
	testClient.MockMergeRequests.EXPECT().
		AcceptMergeRequest("OWNER/REPO", int64(123), gomock.Any()).
		DoAndReturn(func(_ any, _ int64, opts *gitlab.AcceptMergeRequestOptions, _ ...gitlab.RequestOptionFunc) (*gitlab.MergeRequest, *gitlab.Response, error) {
			require.NotNil(t, opts.SHA)
			assert.Equal(t, "abc123", *opts.SHA)
			require.NotNil(t, opts.Squash)
			assert.True(t, *opts.Squash)
			return &gitlab.MergeRequest{BasicMergeRequest: gitlab.BasicMergeRequest{IID: 123, State: "merged"}}, nil, nil
		})

	console := ugh.New(t)
	// With rebase offered, index 1 would pick it and the unexpected
	// RebaseMergeRequest call would fail the test too.
	console.Expect(ugh.Select("What merge method do you want to use?")).
		Do(func(r *ugh.Response) {
			assert.NotContains(t, r.Screen(), "Rebase and merge")
			ugh.SelectIndex(1)(r)
		})
	console.Expect(ugh.Select("What's next?")).Do(ugh.SelectOption("Submit"))

	exec := cmdtest.SetupCmdForTest(t, NewCmdMerge, true,
		cmdtest.WithGitLabClient(testClient.Client),
		cmdtest.WithConsole(t, console),
	)

	_, err := exec("123 --sha abc123")
	require.NoError(t, err)
}
