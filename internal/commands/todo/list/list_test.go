//go:build !integration

package list

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestTodoList(t *testing.T) {
	t.Parallel()
	type testCase struct {
		name        string
		cli         string
		expectedOut string
		wantErr     bool
		setupMock   func(tc *gitlabtesting.TestClient)
	}

	createdAt := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	testTodo := &gitlab.Todo{
		ID:         42,
		ActionName: gitlab.TodoAssigned,
		TargetType: gitlab.TodoTargetMergeRequest,
		Target: &gitlab.TodoTarget{
			Title: "Fix the bug",
		},
		Project: &gitlab.BasicProject{
			PathWithNamespace: "mygroup/myproject",
		},
		State:     "pending",
		CreatedAt: &createdAt,
	}

	testCases := []testCase{
		{
			name:        "lists pending todos",
			cli:         "",
			expectedOut: "ID\tAction\tType\tTitle\tProject\tCreated\n42\tassigned\tMergeRequest\tFix the bug\tmygroup/myproject\t2025-01-01 00:00:00 +0000 UTC\n\n",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockTodos.EXPECT().
					ListTodos(gomock.Any()).
					Return([]*gitlab.Todo{testTodo}, nil, nil)
			},
		},
		{
			name:        "lists done todos with --state=done",
			cli:         "--state=done",
			expectedOut: "ID\tAction\tType\tTitle\tProject\tCreated\n42\tassigned\tMergeRequest\tFix the bug\tmygroup/myproject\t2025-01-01 00:00:00 +0000 UTC\n\n",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockTodos.EXPECT().
					ListTodos(gomock.Any()).
					Return([]*gitlab.Todo{testTodo}, nil, nil)
			},
		},
		{
			name:        "filters by action",
			cli:         "--action=assigned",
			expectedOut: "ID\tAction\tType\tTitle\tProject\tCreated\n42\tassigned\tMergeRequest\tFix the bug\tmygroup/myproject\t2025-01-01 00:00:00 +0000 UTC\n\n",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockTodos.EXPECT().
					ListTodos(gomock.Any()).
					Return([]*gitlab.Todo{testTodo}, nil, nil)
			},
		},
		{
			name:        "filters by type",
			cli:         "--type=MergeRequest",
			expectedOut: "ID\tAction\tType\tTitle\tProject\tCreated\n42\tassigned\tMergeRequest\tFix the bug\tmygroup/myproject\t2025-01-01 00:00:00 +0000 UTC\n\n",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockTodos.EXPECT().
					ListTodos(gomock.Any()).
					Return([]*gitlab.Todo{testTodo}, nil, nil)
			},
		},
		{
			name:        "returns empty output when no todos",
			cli:         "",
			expectedOut: "\n",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockTodos.EXPECT().
					ListTodos(gomock.Any()).
					Return([]*gitlab.Todo{}, nil, nil)
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// GIVEN
			testClient := gitlabtesting.NewTestClient(t)
			tc.setupMock(testClient)
			exec := cmdtest.SetupCmdForTest(
				t,
				NewCmd,
				false,
				cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "", "", api.WithGitLabClient(testClient.Client))),
			)

			// WHEN
			out, err := exec(tc.cli)

			// THEN
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.expectedOut, out.OutBuf.String())
			assert.Empty(t, out.ErrBuf.String())
		})
	}
}

func TestTodoList_JSON(t *testing.T) {
	t.Parallel()

	createdAt := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	testTodo := &gitlab.Todo{
		ID:         42,
		ActionName: gitlab.TodoAssigned,
		TargetType: gitlab.TodoTargetMergeRequest,
		Target: &gitlab.TodoTarget{
			Title: "Fix the bug",
		},
		Project: &gitlab.BasicProject{
			PathWithNamespace: "mygroup/myproject",
		},
		State:     "pending",
		CreatedAt: &createdAt,
	}

	testClient := gitlabtesting.NewTestClient(t)
	testClient.MockTodos.EXPECT().
		ListTodos(gomock.Any()).
		Return([]*gitlab.Todo{testTodo}, nil, nil)

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "", "", api.WithGitLabClient(testClient.Client))),
	)

	out, err := exec("--output json")
	require.NoError(t, err)

	assert.Contains(t, out.String(), `"id":42`)
	assert.Contains(t, out.String(), `"action_name":"assigned"`)
	assert.Contains(t, out.String(), `"target_type":"MergeRequest"`)
	assert.Empty(t, out.Stderr())
}
