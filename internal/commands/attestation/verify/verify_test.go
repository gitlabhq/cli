//go:build !integration

package verify

import (
	"errors"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
	gitlab_testing "gitlab.com/gitlab-org/api/client-go/v3/testing"

	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func mocks(t *testing.T, tc *gitlab_testing.TestClient, project *gitlab.Project) {
	t.Helper()
	tc.MockProjects.EXPECT().
		GetProject("OWNER/REPO", gomock.Any(), gomock.Any()).
		Return(project, &gitlab.Response{}, nil).
		Times(1)

	tc.MockAttestations.EXPECT().
		ListAttestations("OWNER/REPO", "f2d4bc357309c633154f1e94c6fda3583ae429f6adc882d4d9006380ea3a79da").
		Return([]*gitlab.Attestation{
			{
				ID:            1,
				IID:           1,
				CreatedAt:     new(time.Now().Add(-24 * time.Hour)),
				UpdatedAt:     new(time.Now().Add(-24 * time.Hour)),
				ExpireAt:      new(time.Now().Add(-24 * time.Hour)),
				ProjectID:     1,
				BuildID:       1,
				Status:        "success",
				PredicateKind: "provenance",
				PredicateType: "https://slsa.dev/provenance/v1",
				SubjectDigest: "76c34666f719ef14bd2b124a7db51e9c05e4db2e12a84800296d559064eebe2c",
				DownloadURL:   "https://gitlab.example.com/api/v4/projects/1/attestations/1/download",
			},
		}, &gitlab.Response{}, nil).
		Times(1)

	attestation, err := os.ReadFile("testdata/attestationDownload.json")
	require.NoError(t, err)

	tc.MockAttestations.EXPECT().
		DownloadAttestation("OWNER/REPO", int64(1)).
		Return(attestation, nil, nil).
		Times(1)
}

func Test_AttestationVerify(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	tc := gitlab_testing.NewTestClientWithCtrl(ctrl)
	mockExec := cmdtest.NewMockExecutor(ctrl)

	exec := cmdtest.SetupCmdForTest(t,
		NewCmd,
		false,
		cmdtest.WithGitLabClient(tc.Client),
		cmdtest.WithExecutor(mockExec),
	)

	mocks(t, tc, &gitlab.Project{
		WebURL:            "https://gitlab.com/OWNER/REPO",
		PathWithNamespace: "OWNER/REPO",
	})

	mockExec.EXPECT().LookPath(gomock.Any()).Return("/usr/bin/cosign", nil)
	mockExec.EXPECT().ExecWithCombinedOutput(gomock.Any(), "/usr/bin/cosign", cmdtest.SliceMatch[string](
		"verify-blob-attestation",
		"--bundle",
		gomock.Any(),
		"--type",
		"slsaprovenance1",
		"./testdata/example_artifact.txt",
		"--certificate-identity-regexp",
		`^https://gitlab\.com/OWNER/REPO/`,
		"--certificate-oidc-issuer",
		"https://gitlab.com",
	), nil)

	output, err := exec("OWNER/REPO ./testdata/example_artifact.txt")

	require.NoError(t, err)
	assert.Contains(t, output.String(), "Artifact provenance successfully verified")
}

func Test_AttestationVerify_SelfManaged(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		webURL         string
		path           string
		identityRegexp string
		issuer         string
	}{
		{
			name:           "http with a port and subgroups",
			webURL:         "http://gdk.test:3000/group/sub/project",
			path:           "group/sub/project",
			identityRegexp: `^http://gdk\.test:3000/group/sub/project/`,
			issuer:         "http://gdk.test:3000",
		},
		{
			name:           "relative URL root",
			webURL:         "https://example.com/gitlab/my.group/repo",
			path:           "my.group/repo",
			identityRegexp: `^https://example\.com/gitlab/my\.group/repo/`,
			issuer:         "https://example.com/gitlab",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctrl := gomock.NewController(t)
			tc := gitlab_testing.NewTestClientWithCtrl(ctrl)
			mockExec := cmdtest.NewMockExecutor(ctrl)

			exec := cmdtest.SetupCmdForTest(t,
				NewCmd,
				false,
				cmdtest.WithGitLabClient(tc.Client),
				cmdtest.WithExecutor(mockExec),
			)

			// The argument stays OWNER/REPO, so the expected identity and issuer can only come from the API response.
			mocks(t, tc, &gitlab.Project{
				WebURL:            tt.webURL,
				PathWithNamespace: tt.path,
			})

			mockExec.EXPECT().LookPath(gomock.Any()).Return("/usr/bin/cosign", nil)
			mockExec.EXPECT().ExecWithCombinedOutput(gomock.Any(), "/usr/bin/cosign", cmdtest.SliceMatch[string](
				"verify-blob-attestation",
				"--bundle",
				gomock.Any(),
				"--type",
				"slsaprovenance1",
				"./testdata/example_artifact.txt",
				"--certificate-identity-regexp",
				tt.identityRegexp,
				"--certificate-oidc-issuer",
				tt.issuer,
			), nil)

			output, err := exec("OWNER/REPO ./testdata/example_artifact.txt")

			require.NoError(t, err)
			assert.Contains(t, output.String(), "Artifact provenance successfully verified")
		})
	}
}

func Test_AttestationVerify_UnexpectedWebURL(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	tc := gitlab_testing.NewTestClientWithCtrl(ctrl)
	mockExec := cmdtest.NewMockExecutor(ctrl)

	exec := cmdtest.SetupCmdForTest(t,
		NewCmd,
		false,
		cmdtest.WithGitLabClient(tc.Client),
		cmdtest.WithExecutor(mockExec),
	)

	tc.MockProjects.EXPECT().
		GetProject("OWNER/REPO", gomock.Any(), gomock.Any()).
		Return(&gitlab.Project{
			WebURL:            "https://gitlab.example.com/other/path",
			PathWithNamespace: "OWNER/REPO",
		}, &gitlab.Response{}, nil).
		Times(1)

	_, err := exec("OWNER/REPO ./testdata/example_artifact.txt")

	assert.EqualError(t, err, `unexpected web URL "https://gitlab.example.com/other/path" for project OWNER/REPO`)
}

func Test_AttestationVerify_Failure(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	tc := gitlab_testing.NewTestClientWithCtrl(ctrl)
	mockExec := cmdtest.NewMockExecutor(ctrl)

	exec := cmdtest.SetupCmdForTest(t,
		NewCmd,
		false,
		cmdtest.WithGitLabClient(tc.Client),
		cmdtest.WithExecutor(mockExec),
	)

	mocks(t, tc, &gitlab.Project{
		WebURL:            "https://gitlab.com/OWNER/REPO",
		PathWithNamespace: "OWNER/REPO",
	})

	mockExec.EXPECT().LookPath(gomock.Any()).Return("/usr/bin/cosign", nil)
	mockExec.EXPECT().ExecWithCombinedOutput(gomock.Any(), "/usr/bin/cosign", gomock.Any(), nil).Return(nil, errors.New("some error"))

	_, err := exec("OWNER/REPO ./testdata/example_artifact.txt")

	assert.EqualError(t, err, "some error: ")
}
