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

func mocks(t *testing.T, tc *gitlab_testing.TestClient) {
	t.Helper()
	tc.MockProjects.EXPECT().
		GetProject("OWNER/REPO", gomock.Any(), gomock.Any()).
		Return(&gitlab.Project{
			PathWithNamespace: "OWNER/REPO",
		}, &gitlab.Response{}, nil).
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

	mocks(t, tc)

	mockExec.EXPECT().LookPath(gomock.Any()).Return("/usr/bin/cosign", nil)
	mockExec.EXPECT().ExecWithCombinedOutput(gomock.Any(), "/usr/bin/cosign", cmdtest.SliceMatch[string](
		"verify-blob-attestation",
		"--new-bundle-format",
		"--bundle",
		gomock.Any(),
		"--type",
		"slsaprovenance1",
		"./testdata/example_artifact.txt",
		"--certificate-identity-regexp",
		"^https://gitlab.com/OWNER/REPO/",
		"--certificate-oidc-issuer",
		"https://gitlab.com",
	), nil)

	output, err := exec("OWNER/REPO ./testdata/example_artifact.txt")

	require.NoError(t, err)
	assert.Contains(t, output.String(), "Artifact provenance successfully verified")
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

	mocks(t, tc)

	mockExec.EXPECT().LookPath(gomock.Any()).Return("/usr/bin/cosign", nil)
	mockExec.EXPECT().ExecWithCombinedOutput(gomock.Any(), "/usr/bin/cosign", gomock.Any(), nil).Return(nil, errors.New("some error"))

	_, err := exec("OWNER/REPO ./testdata/example_artifact.txt")

	assert.EqualError(t, err, "some error: ")
}
