//go:build !integration

package bootstrap

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v2/testing"
)

func TestSyncFileReturnsTransportErrorWithoutResponse(t *testing.T) {
	testClient := gitlabtesting.NewTestClient(t)
	testClient.MockRepositoryFiles.EXPECT().
		GetFileMetaData("OWNER/REPO", "config.yaml", gomock.Any()).
		Return(nil, nil, errors.New("network unavailable"))

	api := NewAPI(testClient.Client, "OWNER/REPO", nil)
	err := api.SyncFile(file{path: "config.yaml"}, "main")

	require.EqualError(t, err, "network unavailable")
}
