//go:build !integration

package list

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/acarl005/stripansi"
	"github.com/google/shlex"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	cmdTestUtils "gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestNewCmdReleaseList(t *testing.T) {
	oldGetRelease := getRelease
	timer, _ := time.Parse(time.RFC3339, "2014-11-12T11:45:26.371Z")
	getRelease = func(client *gitlab.Client, projectID any, tag string) (*gitlab.Release, error) {
		if projectID == "" || projectID == "WRONG_REPO" {
			return nil, fmt.Errorf("error expected")
		}
		return &gitlab.Release{
			TagName:     tag,
			Name:        tag,
			Description: "Dummy description for " + tag,
			Author: gitlab.BasicUser{
				ID:       1,
				Name:     "John Dev Wick",
				Username: "jdwick",
			},
			CreatedAt: &timer,
		}, nil
	}

	oldListReleases := listReleases
	listReleases = func(client *gitlab.Client, projectID any, opts *gitlab.ListReleasesOptions) ([]*gitlab.Release, error) {
		if projectID == "" || projectID == "WRONG_REPO" {
			return nil, errors.New("fatal: wrong Repository")
		}
		return append([]*gitlab.Release{}, &gitlab.Release{
			TagName:     "0.1.0",
			Name:        "Initial Release",
			Description: "Dummy description for 0.1.0",
			Author: gitlab.BasicUser{
				ID:       1,
				Name:     "John Dev Wick",
				Username: "jdwick",
			},
			CreatedAt: &timer,
		}), nil
	}

	tests := []struct {
		name       string
		args       string
		stdOutFunc func(t *testing.T, out string)
		stdErr     string
		wantErr    bool
	}{
		{
			name:    "releases list on test repo",
			wantErr: false,
			stdOutFunc: func(t *testing.T, out string) {
				t.Helper()

				assert.Contains(t, out, "Showing 1 release on cli-automated-testing/test")
			},
		},
		{
			name:    "get release by tag on test repo",
			wantErr: false,
			args:    "--tag v0.0.1-beta",
			stdOutFunc: func(t *testing.T, out string) {
				t.Helper()

				assert.Contains(t, out, "Dummy description for v0.0.1-beta")
			},
		},
		{
			name:    "releases list on custom repo",
			wantErr: false,
			args:    "-R profclems/glab",
			stdOutFunc: func(t *testing.T, out string) {
				t.Helper()

				assert.Contains(t, out, "Showing 1 release on profclems/glab")
			},
		},
		{
			name:    "ERR - wrong repo",
			wantErr: true,
			args:    "-R WRONG_REPO",
		},
		{
			name:    "ERR - wrong repo with tag",
			wantErr: true,
			args:    "-R WRONG_REPO --tag v0.0.1-beta",
		},
	}

	io, _, stdout, stderr := cmdTestUtils.TestIOStreams(cmdTestUtils.WithTestIOStreamsAsTTY(true))
	f := cmdTestUtils.NewTestFactory(io, cmdTestUtils.WithBaseRepo("cli-automated-testing", "test", ""))

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := NewCmdReleaseList(f)
			cmdutils.EnableRepoOverride(cmd, f)

			argv, err := shlex.Split(tt.args)
			if err != nil {
				t.Fatal(err)
			}
			cmd.SetArgs(argv)
			_, err = cmd.ExecuteC()
			if err != nil {
				if tt.wantErr {
					require.Error(t, err)
					return
				} else {
					t.Fatal(err)
				}
			}

			out := stripansi.Strip(stdout.String())
			outErr := stripansi.Strip(stderr.String())

			tt.stdOutFunc(t, out)
			assert.Contains(t, outErr, tt.stdErr)
		})
	}

	getRelease = oldGetRelease
	listReleases = oldListReleases
}

func TestReleaseList_JSON(t *testing.T) {
	t.Parallel()

	oldListReleases := listReleases
	timer, _ := time.Parse(time.RFC3339, "2014-11-12T11:45:26.371Z")
	listReleases = func(client *gitlab.Client, projectID any, opts *gitlab.ListReleasesOptions) ([]*gitlab.Release, error) {
		return []*gitlab.Release{
			{
				TagName:     "v1.0.0",
				Name:        "First Release",
				Description: "Test release description",
				Author: gitlab.BasicUser{
					ID:       1,
					Name:     "John Dev Wick",
					Username: "jdwick",
				},
				CreatedAt: &timer,
			},
		}, nil
	}
	defer func() { listReleases = oldListReleases }()

	io, _, stdout, stderr := cmdTestUtils.TestIOStreams()
	f := cmdTestUtils.NewTestFactory(io, cmdTestUtils.WithBaseRepo("cli-automated-testing", "test", ""))

	cmd := NewCmdReleaseList(f)
	cmdutils.EnableRepoOverride(cmd, f)

	argv, err := shlex.Split("-F json")
	require.NoError(t, err)
	cmd.SetArgs(argv)
	_, err = cmd.ExecuteC()
	require.NoError(t, err)

	out := stdout.String()
	assert.Contains(t, out, `"tag_name":"v1.0.0"`)
	assert.Contains(t, out, `"name":"First Release"`)
	assert.Contains(t, out, `"description":"Test release description"`)
	assert.Empty(t, stderr.String())
}
