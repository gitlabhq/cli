//go:build !integration

package list

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"

	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func captureListOptions(tc *gitlabtesting.TestClient, gotOpts **gitlab.ListProjectPackagesOptions) {
	tc.MockPackages.EXPECT().
		ListProjectPackages("OWNER/REPO", gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ any, opt *gitlab.ListProjectPackagesOptions, _ ...gitlab.RequestOptionFunc) ([]*gitlab.Package, *gitlab.Response, error) {
			*gotOpts = opt
			return []*gitlab.Package{}, nil, nil
		})
}

func TestPackagesList(t *testing.T) {
	t.Parallel()

	t.Run("lists packages as JSON", func(t *testing.T) {
		t.Parallel()

		tc := gitlabtesting.NewTestClient(t)
		tc.MockPackages.EXPECT().
			ListProjectPackages("OWNER/REPO", gomock.Any(), gomock.Any()).
			Return([]*gitlab.Package{
				{ID: 1, Name: "my-package", Version: "1.0.0", PackageType: "generic"},
				{ID: 2, Name: "other-package", Version: "2.0.0", PackageType: "generic"},
			}, nil, nil)

		exec := cmdtest.SetupCmdForTest(t, NewCmd, false, cmdtest.WithGitLabClient(tc.Client))

		out, err := exec("")
		require.NoError(t, err)

		var packages []*gitlab.Package
		require.NoError(t, json.Unmarshal([]byte(out.String()), &packages))
		require.Len(t, packages, 2)
		assert.Equal(t, int64(1), packages[0].ID)
		assert.Equal(t, "my-package", packages[0].Name)
		assert.Equal(t, "1.0.0", packages[0].Version)
		assert.Equal(t, int64(2), packages[1].ID)
		assert.Equal(t, "other-package", packages[1].Name)
	})

	t.Run("filters output with --jq", func(t *testing.T) {
		t.Parallel()

		tc := gitlabtesting.NewTestClient(t)
		tc.MockPackages.EXPECT().
			ListProjectPackages("OWNER/REPO", gomock.Any(), gomock.Any()).
			Return([]*gitlab.Package{
				{ID: 1, Name: "my-package", Version: "1.0.0", PackageType: "generic"},
			}, nil, nil)

		exec := cmdtest.SetupCmdForTest(t, NewCmd, false, cmdtest.WithGitLabClient(tc.Client))

		out, err := exec(`--jq '.[].name'`)
		require.NoError(t, err)
		assert.Contains(t, out.String(), "my-package")
	})

	t.Run("forwards --name to the API", func(t *testing.T) {
		t.Parallel()

		tc := gitlabtesting.NewTestClient(t)
		var gotOpts *gitlab.ListProjectPackagesOptions
		captureListOptions(tc, &gotOpts)

		exec := cmdtest.SetupCmdForTest(t, NewCmd, false, cmdtest.WithGitLabClient(tc.Client))

		_, err := exec("--name my-package")
		require.NoError(t, err)
		require.NotNil(t, gotOpts.PackageName)
		assert.Equal(t, "my-package", *gotOpts.PackageName)
	})

	t.Run("forwards --package-type to the API", func(t *testing.T) {
		t.Parallel()

		tc := gitlabtesting.NewTestClient(t)
		var gotOpts *gitlab.ListProjectPackagesOptions
		captureListOptions(tc, &gotOpts)

		exec := cmdtest.SetupCmdForTest(t, NewCmd, false, cmdtest.WithGitLabClient(tc.Client))

		_, err := exec("--package-type npm")
		require.NoError(t, err)
		require.NotNil(t, gotOpts.PackageType)
		assert.Equal(t, "npm", *gotOpts.PackageType)
	})

	t.Run("leaves package type unset when --package-type is absent", func(t *testing.T) {
		t.Parallel()

		tc := gitlabtesting.NewTestClient(t)
		var gotOpts *gitlab.ListProjectPackagesOptions
		captureListOptions(tc, &gotOpts)

		exec := cmdtest.SetupCmdForTest(t, NewCmd, false, cmdtest.WithGitLabClient(tc.Client))

		_, err := exec("")
		require.NoError(t, err)
		assert.Nil(t, gotOpts.PackageType)
	})

	t.Run("forwards --page and --per-page to the API", func(t *testing.T) {
		t.Parallel()

		tc := gitlabtesting.NewTestClient(t)
		var gotOpts *gitlab.ListProjectPackagesOptions
		captureListOptions(tc, &gotOpts)

		exec := cmdtest.SetupCmdForTest(t, NewCmd, false, cmdtest.WithGitLabClient(tc.Client))

		_, err := exec("--page 2 --per-page 10")
		require.NoError(t, err)
		assert.Equal(t, int64(2), gotOpts.Page)
		assert.Equal(t, int64(10), gotOpts.PerPage)
	})

	t.Run("defaults page and per-page when flags are absent", func(t *testing.T) {
		t.Parallel()

		tc := gitlabtesting.NewTestClient(t)
		var gotOpts *gitlab.ListProjectPackagesOptions
		captureListOptions(tc, &gotOpts)

		exec := cmdtest.SetupCmdForTest(t, NewCmd, false, cmdtest.WithGitLabClient(tc.Client))

		_, err := exec("")
		require.NoError(t, err)
		assert.Equal(t, int64(1), gotOpts.Page)
		assert.Equal(t, int64(30), gotOpts.PerPage)
		assert.Nil(t, gotOpts.PackageName)
	})

	t.Run("surfaces API errors", func(t *testing.T) {
		t.Parallel()

		tc := gitlabtesting.NewTestClient(t)
		tc.MockPackages.EXPECT().
			ListProjectPackages("OWNER/REPO", gomock.Any(), gomock.Any()).
			Return(nil, nil, fmt.Errorf("GET https://gitlab.com/api/v4/projects/OWNER%%2FREPO/packages: 403"))

		exec := cmdtest.SetupCmdForTest(t, NewCmd, false, cmdtest.WithGitLabClient(tc.Client))

		_, err := exec("")
		require.Error(t, err)
		assert.Equal(t, "failed to list packages: GET https://gitlab.com/api/v4/projects/OWNER%2FREPO/packages: 403", err.Error())
	})
}
