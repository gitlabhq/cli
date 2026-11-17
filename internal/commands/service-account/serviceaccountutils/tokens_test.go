//go:build !integration

package serviceaccountutils

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"

	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestListTokens(t *testing.T) {
	t.Parallel()

	t.Run("collects every page of a group account", func(t *testing.T) {
		t.Parallel()

		tc := gitlabtesting.NewTestClient(t)
		gomock.InOrder(
			tc.MockGroups.EXPECT().
				ListServiceAccountPersonalAccessTokens("my-group", int64(41), gomock.Cond(func(o *gitlab.ListServiceAccountPersonalAccessTokensOptions) bool {
					return o.PerPage == 100 && o.State == nil
				}), gomock.Any(), gomock.Any()).
				Return([]*gitlab.PersonalAccessToken{{ID: 7}}, &gitlab.Response{NextPage: 2}, nil),
			tc.MockGroups.EXPECT().
				ListServiceAccountPersonalAccessTokens("my-group", int64(41), gomock.Any(), gomock.Any(), gomock.Any()).
				Return([]*gitlab.PersonalAccessToken{{ID: 8}}, noMorePages(), nil),
		)

		tokens, err := ListTokens(t.Context(), tc.Client, Scope{Group: "my-group"}, 41, false)
		require.NoError(t, err)
		require.Len(t, tokens, 2)
		assert.Equal(t, int64(7), tokens[0].ID)
		assert.Equal(t, int64(8), tokens[1].ID)
	})

	t.Run("asks a project for active tokens only", func(t *testing.T) {
		t.Parallel()

		tc := gitlabtesting.NewTestClient(t)
		tc.MockProjects.EXPECT().
			ListProjectServiceAccountPersonalAccessTokens("OWNER/REPO", int64(51), gomock.Cond(func(o *gitlab.ListProjectServiceAccountPersonalAccessTokensOptions) bool {
				return o.PerPage == 100 && o.State != nil && *o.State == "active"
			}), gomock.Any(), gomock.Any()).
			Return([]*gitlab.PersonalAccessToken{{ID: 7}}, noMorePages(), nil)

		tokens, err := ListTokens(t.Context(), tc.Client, Scope{Project: "OWNER/REPO"}, 51, true)
		require.NoError(t, err)
		require.Len(t, tokens, 1)
		assert.Equal(t, int64(7), tokens[0].ID)
	})
}

func TestResolveTokenID(t *testing.T) {
	t.Parallel()

	// No expectation is set, so any API call fails the test.
	t.Run("numeric ID makes no request", func(t *testing.T) {
		t.Parallel()

		tc := gitlabtesting.NewTestClient(t)

		id, err := ResolveTokenID(t.Context(), tc.Client, Scope{Project: "OWNER/REPO"}, 51, "7")
		require.NoError(t, err)
		assert.Equal(t, int64(7), id)
	})

	t.Run("name found among active project tokens", func(t *testing.T) {
		t.Parallel()

		tc := gitlabtesting.NewTestClient(t)
		tc.MockProjects.EXPECT().
			ListProjectServiceAccountPersonalAccessTokens("OWNER/REPO", int64(51), gomock.Cond(func(o *gitlab.ListProjectServiceAccountPersonalAccessTokensOptions) bool {
				return o.PerPage == 100 && o.State != nil && *o.State == "active" && o.Search != nil && *o.Search == "ci"
			}), gomock.Any(), gomock.Any()).
			Return([]*gitlab.PersonalAccessToken{{ID: 6, Name: "ci-old"}, {ID: 7, Name: "ci"}}, noMorePages(), nil)

		id, err := ResolveTokenID(t.Context(), tc.Client, Scope{Project: "OWNER/REPO"}, 51, "ci")
		require.NoError(t, err)
		assert.Equal(t, int64(7), id)
	})

	t.Run("name found among active group tokens", func(t *testing.T) {
		t.Parallel()

		tc := gitlabtesting.NewTestClient(t)
		tc.MockGroups.EXPECT().
			ListServiceAccountPersonalAccessTokens("my-group", int64(41), gomock.Cond(func(o *gitlab.ListServiceAccountPersonalAccessTokensOptions) bool {
				return o.State != nil && *o.State == "active" && o.Search != nil && *o.Search == "deploy"
			}), gomock.Any(), gomock.Any()).
			Return([]*gitlab.PersonalAccessToken{{ID: 9, Name: "deploy"}}, noMorePages(), nil)

		id, err := ResolveTokenID(t.Context(), tc.Client, Scope{Group: "my-group"}, 41, "deploy")
		require.NoError(t, err)
		assert.Equal(t, int64(9), id)
	})

	// The search is partial and case-insensitive, so only an exact name counts.
	t.Run("a partial or differently cased name is not a match", func(t *testing.T) {
		t.Parallel()

		tc := gitlabtesting.NewTestClient(t)
		tc.MockProjects.EXPECT().
			ListProjectServiceAccountPersonalAccessTokens("OWNER/REPO", int64(51), gomock.Any(), gomock.Any(), gomock.Any()).
			Return([]*gitlab.PersonalAccessToken{{ID: 6, Name: "ci-old"}, {ID: 8, Name: "CI"}}, noMorePages(), nil)

		_, err := ResolveTokenID(t.Context(), tc.Client, Scope{Project: "OWNER/REPO"}, 51, "ci")
		require.EqualError(t, err, `no active token named "ci" for service account 51 in project OWNER/REPO`)
	})

	t.Run("duplicate active names", func(t *testing.T) {
		t.Parallel()

		tc := gitlabtesting.NewTestClient(t)
		tc.MockProjects.EXPECT().
			ListProjectServiceAccountPersonalAccessTokens("OWNER/REPO", int64(51), gomock.Any(), gomock.Any(), gomock.Any()).
			Return([]*gitlab.PersonalAccessToken{{ID: 7, Name: "ci"}, {ID: 8, Name: "ci"}}, noMorePages(), nil)

		_, err := ResolveTokenID(t.Context(), tc.Client, Scope{Project: "OWNER/REPO"}, 51, "ci")
		require.EqualError(t, err, `multiple active tokens named "ci" for service account 51; use the token ID instead`)
	})

	t.Run("invalid ID", func(t *testing.T) {
		t.Parallel()

		tc := gitlabtesting.NewTestClient(t)

		_, err := ResolveTokenID(t.Context(), tc.Client, Scope{Project: "OWNER/REPO"}, 51, "0")
		require.EqualError(t, err, `invalid token ID "0"`)
	})

	t.Run("a list error is returned", func(t *testing.T) {
		t.Parallel()

		tc := gitlabtesting.NewTestClient(t)
		tc.MockProjects.EXPECT().
			ListProjectServiceAccountPersonalAccessTokens("OWNER/REPO", int64(51), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(nil, nil, errors.New("403 Forbidden"))

		_, err := ResolveTokenID(t.Context(), tc.Client, Scope{Project: "OWNER/REPO"}, 51, "ci")
		require.EqualError(t, err, "403 Forbidden")
	})
}

func TestPrintToken(t *testing.T) {
	t.Parallel()

	expiresAt := gitlab.ISOTime(time.Date(2027, 1, 31, 0, 0, 0, 0, time.UTC))
	io, _, stdout, stderr := cmdtest.TestIOStreams()
	err := PrintToken(io, &gitlab.PersonalAccessToken{
		ID:        7,
		Name:      "ci",
		Scopes:    []string{"api"},
		ExpiresAt: &expiresAt,
		Token:     "glpat-secret",
	})

	require.NoError(t, err)
	assert.Equal(t, "glpat-secret\n", stdout.String())
	assert.Equal(t, "ID\t7\nName\tci\nDescription\t-\nScopes\tapi\nExpires At\t2027-01-31\n", stderr.String())
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("broken pipe") }

func TestPrintToken_StdoutWriteErrorIsReturned(t *testing.T) {
	t.Parallel()

	io, _, _, _ := cmdtest.TestIOStreams()
	io.StdOut = failingWriter{}

	err := PrintToken(io, &gitlab.PersonalAccessToken{ID: 7, Name: "ci", Token: "glpat-secret"})
	require.EqualError(t, err, "broken pipe")
}

func TestFormatScopes(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "-", FormatScopes(nil))
	assert.Equal(t, "api, read_repository", FormatScopes([]string{"api", "read_repository"}))
}

func TestFormatDate(t *testing.T) {
	t.Parallel()

	d := gitlab.ISOTime(time.Date(2027, 1, 31, 0, 0, 0, 0, time.UTC))
	assert.Equal(t, "-", FormatDate(nil))
	assert.Equal(t, "2027-01-31", FormatDate(&d))
}

func TestFormatTime(t *testing.T) {
	t.Parallel()

	ts := time.Date(2026, 11, 5, 10, 30, 0, 0, time.FixedZone("CET", 3600))
	assert.Equal(t, "-", FormatTime(nil))
	assert.Equal(t, "2026-11-05T09:30:00Z", FormatTime(&ts))
}
