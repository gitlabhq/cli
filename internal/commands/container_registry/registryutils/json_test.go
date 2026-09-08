//go:build !integration

package registryutils

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
)

func TestNewRepositoryJSONStatus(t *testing.T) {
	t.Parallel()

	status := gitlab.ContainerRegistryStatusDeleteFailed

	got := NewRepositoryJSON(&gitlab.RegistryRepository{
		ID:     1,
		Status: &status,
	}, false, false)

	require.NotNil(t, got.Status)
	assert.Equal(t, "delete_failed", *got.Status)
}

func TestNewRepositoryJSONNilStatus(t *testing.T) {
	t.Parallel()

	got := NewRepositoryJSON(&gitlab.RegistryRepository{ID: 1}, false, false)

	assert.Nil(t, got.Status)
}
