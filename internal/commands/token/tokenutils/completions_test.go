//go:build !integration

package tokenutils

import (
	"errors"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestNameOrIDCompletions(t *testing.T) {
	t.Parallel()

	testProjectTokens := []*gitlab.ProjectAccessToken{
		{ID: 123, Name: "my-project-token", Active: true},
		{ID: 456, Name: "expired-project-token", Active: false},
		{ID: 789, Name: "other-project-token", Active: true},
	}
	testGroupTokens := []*gitlab.GroupAccessToken{
		{ID: 321, Name: "my-group-token", Active: true},
		{ID: 654, Name: "revoked-group-token", Active: false},
	}
	testPersonalTokens := []*gitlab.PersonalAccessToken{
		{ID: 111, Name: "my-personal-token", Active: true},
		{ID: 222, Name: "revoked-personal-token", Active: false},
	}
	testUser := &gitlab.User{ID: 926857, Username: "@me"}

	testCases := []struct {
		name          string
		args          []string
		flags         map[string]string
		baseRepoErr   error
		setupMock     func(tc *gitlabtesting.TestClient)
		wantComps     []cobra.Completion
		wantDirective cobra.ShellCompDirective
	}{
		{
			name: "Completes project token IDs and names by default",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockProjectAccessTokens.EXPECT().
					ListProjectAccessTokens("owner/repo", gomock.Any(), gomock.Any()).
					Return(testProjectTokens, &gitlab.Response{}, nil)
			},
			wantComps: []cobra.Completion{
				cobra.CompletionWithDesc("123", "my-project-token"), cobra.CompletionWithDesc("my-project-token", "123"),
				cobra.CompletionWithDesc("789", "other-project-token"), cobra.CompletionWithDesc("other-project-token", "789"),
			},
			wantDirective: cobra.ShellCompDirectiveNoFileComp,
		},
		{
			name:  "Completes project token IDs and names with --repo",
			flags: map[string]string{"repo": "other/proj"},
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockProjectAccessTokens.EXPECT().
					ListProjectAccessTokens("other/proj", gomock.Any(), gomock.Any()).
					Return(testProjectTokens, &gitlab.Response{}, nil)
			},
			wantComps: []cobra.Completion{
				cobra.CompletionWithDesc("123", "my-project-token"), cobra.CompletionWithDesc("my-project-token", "123"),
				cobra.CompletionWithDesc("789", "other-project-token"), cobra.CompletionWithDesc("other-project-token", "789"),
			},
			wantDirective: cobra.ShellCompDirectiveNoFileComp,
		},
		{
			name:  "Uses --repo over --group",
			flags: map[string]string{"repo": "other/proj", "group": "group/sub-group"},
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockProjectAccessTokens.EXPECT().
					ListProjectAccessTokens("other/proj", gomock.Any(), gomock.Any()).
					Return(testProjectTokens, &gitlab.Response{}, nil)
			},
			wantComps: []cobra.Completion{
				cobra.CompletionWithDesc("123", "my-project-token"), cobra.CompletionWithDesc("my-project-token", "123"),
				cobra.CompletionWithDesc("789", "other-project-token"), cobra.CompletionWithDesc("other-project-token", "789"),
			},
			wantDirective: cobra.ShellCompDirectiveNoFileComp,
		},
		{
			name:  "Completes group token IDs and names with --group",
			flags: map[string]string{"group": "group/sub-group"},
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockGroupAccessTokens.EXPECT().
					ListGroupAccessTokens("group/sub-group", gomock.Any(), gomock.Any()).
					Return(testGroupTokens, &gitlab.Response{}, nil)
			},
			wantComps: []cobra.Completion{
				cobra.CompletionWithDesc("321", "my-group-token"), cobra.CompletionWithDesc("my-group-token", "321"),
			},
			wantDirective: cobra.ShellCompDirectiveNoFileComp,
		},
		{
			name:  "Completes own personal token IDs and names with --user @me",
			flags: map[string]string{"user": "@me"},
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockUsers.EXPECT().
					CurrentUser(gomock.Any()).
					Return(testUser, nil, nil)
				tc.MockPersonalAccessTokens.EXPECT().
					ListPersonalAccessTokens(gomock.Any(), gomock.Any()).
					Return(testPersonalTokens, &gitlab.Response{}, nil)
			},
			wantComps: []cobra.Completion{
				cobra.CompletionWithDesc("111", "my-personal-token"), cobra.CompletionWithDesc("my-personal-token", "111"),
			},
			wantDirective: cobra.ShellCompDirectiveNoFileComp,
		},
		{
			name:          "No completions when an argument is already given",
			args:          []string{"123"},
			setupMock:     func(tc *gitlabtesting.TestClient) {},
			wantComps:     nil,
			wantDirective: cobra.ShellCompDirectiveNoFileComp,
		},
		{
			name:          "No project completions without a base repository",
			baseRepoErr:   errors.New("no git remotes found"),
			setupMock:     func(tc *gitlabtesting.TestClient) {},
			wantComps:     nil,
			wantDirective: cobra.ShellCompDirectiveNoFileComp,
		},
		{
			name: "No completions when listing project tokens fails",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockProjectAccessTokens.EXPECT().
					ListProjectAccessTokens(gomock.Any(), gomock.Any(), gomock.Any()).
					Return(nil, nil, errors.New("500 Internal Server Error"))
			},
			wantComps:     nil,
			wantDirective: cobra.ShellCompDirectiveNoFileComp,
		},
		{
			name:  "No completions when listing group tokens fails",
			flags: map[string]string{"group": "group/sub-group"},
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockGroupAccessTokens.EXPECT().
					ListGroupAccessTokens(gomock.Any(), gomock.Any(), gomock.Any()).
					Return(nil, nil, errors.New("500 Internal Server Error"))
			},
			wantComps:     nil,
			wantDirective: cobra.ShellCompDirectiveNoFileComp,
		},
		{
			name:  "No completions when the user cannot be resolved",
			flags: map[string]string{"user": "nonexistent-user"},
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockUsers.EXPECT().
					ListUsers(gomock.Any(), gomock.Any()).
					Return([]*gitlab.User{}, &gitlab.Response{}, nil)
			},
			wantComps:     nil,
			wantDirective: cobra.ShellCompDirectiveNoFileComp,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			testClient := gitlabtesting.NewTestClient(t)
			tc.setupMock(testClient)
			options := []cmdtest.FactoryOption{
				cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "", "", api.WithGitLabClient(testClient.Client))),
			}
			if tc.baseRepoErr != nil {
				options = append(options, cmdtest.WithBaseRepoError(tc.baseRepoErr))
			} else {
				options = append(options, cmdtest.WithBaseRepo("owner", "repo", "gitlab.com"))
			}
			f := cmdtest.NewTestFactory(nil, options...)

			cmd := &cobra.Command{Use: "rotate [<token-name|token-id>]"}
			cmd.Flags().StringP("user", "U", "", "")
			cmd.Flags().StringP("group", "g", "", "")
			cmd.Flags().StringP("repo", "R", "", "")
			for flag, value := range tc.flags {
				require.NoError(t, cmd.Flags().Set(flag, value))
			}
			cmd.SetContext(t.Context())

			completions, directive := NameOrIDCompletions(cmd.Context(), f, cmd, tc.args, "")

			assert.Equal(t, tc.wantComps, completions)
			assert.Equal(t, tc.wantDirective, directive)
		})
	}
}

func TestGroupCompletions(t *testing.T) {
	t.Parallel()

	comps, directive := GroupCompletions(&cobra.Command{}, nil, "")
	assert.Empty(t, comps)
	assert.Equal(t, cobra.ShellCompDirectiveNoFileComp, directive)
}

func TestUserCompletions(t *testing.T) {
	t.Parallel()

	comps, directive := UserCompletions(&cobra.Command{}, nil, "")
	assert.Equal(t, []cobra.Completion{cobra.CompletionWithDesc("@me", "Current user")}, comps)
	assert.Equal(t, cobra.ShellCompDirectiveNoFileComp, directive)
}
