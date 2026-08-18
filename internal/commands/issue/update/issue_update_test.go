//go:build !integration

package update

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v2/testing"

	"gitlab.com/gitlab-org/cli/internal/glinstance"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestIssueUpdate_DescriptionFile(t *testing.T) {
	const body = "# Heading\n\nMulti-line body with \"quotes\", $VARS and `backticks`.\n"

	tests := []struct {
		name     string
		useStdin bool
	}{
		{name: "reads the description from a file"},
		{name: "reads the description from standard input", useStdin: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tc := gitlabtesting.NewTestClient(t)
			tc.MockIssues.EXPECT().
				GetIssue("OWNER/REPO", int64(42)).
				Return(&gitlab.Issue{IID: 42, Title: "issueTitle"}, nil, nil)

			var gotDescription *string
			tc.MockIssues.EXPECT().
				UpdateIssue("OWNER/REPO", int64(42), gomock.Any()).
				DoAndReturn(func(_ any, _ int64, opts *gitlab.UpdateIssueOptions, _ ...gitlab.RequestOptionFunc) (*gitlab.Issue, *gitlab.Response, error) {
					gotDescription = opts.Description
					return &gitlab.Issue{IID: 42, Title: "issueTitle", WebURL: "https://gitlab.com/OWNER/REPO/-/issues/42"}, nil, nil
				})

			factoryOpts := []cmdtest.FactoryOption{
				cmdtest.WithGitLabClient(tc.Client),
				cmdtest.WithBaseRepo("OWNER", "REPO", glinstance.DefaultHostname),
			}

			path := "-"
			if tt.useStdin {
				factoryOpts = append(factoryOpts, cmdtest.WithStdin(body))
			} else {
				path = filepath.Join(t.TempDir(), "description.md")
				require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
			}

			exec := cmdtest.SetupCmdForTest(t, NewCmdUpdate, false, factoryOpts...)

			_, err := exec(fmt.Sprintf("42 --description-file %q", path))
			require.NoError(t, err)

			require.NotNil(t, gotDescription)
			assert.Equal(t, body, *gotDescription)
		})
	}
}

func TestIssueUpdate_DescriptionFileConflictsWithDescription(t *testing.T) {
	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmdUpdate,
		false,
		cmdtest.WithBaseRepo("OWNER", "REPO", glinstance.DefaultHostname),
	)

	_, err := exec(`42 --description inline --description-file some.md`)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "[description description-file]")
}
