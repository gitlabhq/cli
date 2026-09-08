//go:build !integration

package registryutils

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"

	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestParseID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    int64
		wantErr string
	}{
		{
			name:  "valid input",
			input: "123",
			want:  123,
		},
		{
			name:    "zero",
			input:   "0",
			wantErr: "repository ID must be a positive integer",
		},
		{
			name:    "negative",
			input:   "-1",
			wantErr: "repository ID must be a positive integer",
		},
		{
			name:    "non-numeric",
			input:   "abc",
			wantErr: "repository ID must be a positive integer",
		},
		{
			name:    "empty",
			input:   "",
			wantErr: "repository ID must be a positive integer",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := ParseID(tt.input, "repository ID")
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Equal(t, tt.wantErr, err.Error())
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestDisplayRepositories(t *testing.T) {
	t.Parallel()

	io, _, _, _ := cmdtest.TestIOStreams()

	t.Run("empty slice", func(t *testing.T) {
		t.Parallel()

		got := DisplayRepositories(io, []*gitlab.RegistryRepository{}, true)

		assert.Contains(t, got, "ID")
		assert.Contains(t, got, "Name")
		assert.Contains(t, got, "Path")
		assert.Contains(t, got, "Tags")
		assert.NotContains(t, got, "Status")
	})

	t.Run("without tags count", func(t *testing.T) {
		t.Parallel()

		got := DisplayRepositories(io, []*gitlab.RegistryRepository{
			{
				ID:   1,
				Name: "app",
				Path: "group/project/app",
			},
		}, false)

		assert.Contains(t, got, "group/project/app")
		assert.NotContains(t, got, "Tags")
		assert.NotContains(t, got, "\t0\t")
	})

	t.Run("nil fields", func(t *testing.T) {
		t.Parallel()

		got := DisplayRepositories(io, []*gitlab.RegistryRepository{
			{
				ID:        1,
				Name:      "app",
				Path:      "group/project/app",
				CreatedAt: nil,
			},
		}, true)

		assert.Contains(t, got, "group/project/app")
		assert.NotContains(t, got, "<nil>")
	})
}

func TestDisplayRepository(t *testing.T) {
	t.Parallel()

	io, _, _, _ := cmdtest.TestIOStreams()

	got := DisplayRepository(io, &gitlab.RegistryRepository{
		ID:                     1,
		Path:                   "group/project/app",
		Status:                 nil,
		CreatedAt:              nil,
		CleanupPolicyStartedAt: nil,
	})

	assert.Contains(t, got, "group/project/app")
	assert.Contains(t, got, "Cleanup policy started")
	assert.NotContains(t, got, "<nil>")
}

func TestDisplayTags(t *testing.T) {
	t.Parallel()

	t.Run("empty slice", func(t *testing.T) {
		t.Parallel()

		got := DisplayTags([]*gitlab.RegistryRepositoryTag{})

		assert.Contains(t, got, "Name")
		assert.Contains(t, got, "Location")
	})

	t.Run("nil fields", func(t *testing.T) {
		t.Parallel()

		got := DisplayTags([]*gitlab.RegistryRepositoryTag{
			{
				Name:      "latest",
				Path:      "group/project/app:latest",
				CreatedAt: nil,
			},
		})

		assert.Contains(t, got, "latest")
		assert.NotContains(t, got, "<nil>")
	})
}

func TestDisplayTagsWithDetails(t *testing.T) {
	t.Parallel()

	io, _, _, _ := cmdtest.TestIOStreams()

	t.Run("empty slice", func(t *testing.T) {
		t.Parallel()

		got := DisplayTagsWithDetails(io, []*gitlab.RegistryRepositoryTag{})

		assert.Contains(t, got, "Name")
		assert.Contains(t, got, "Digest")
	})

	t.Run("nil fields", func(t *testing.T) {
		t.Parallel()

		got := DisplayTagsWithDetails(io, []*gitlab.RegistryRepositoryTag{
			{
				Name:      "latest",
				Path:      "group/project/app:latest",
				CreatedAt: nil,
			},
		})

		assert.Contains(t, got, "latest")
		assert.NotContains(t, got, "<nil>")
	})
}

func TestDisplayTag(t *testing.T) {
	t.Parallel()

	io, _, _, _ := cmdtest.TestIOStreams()

	got := DisplayTag(io, &gitlab.RegistryRepositoryTag{
		Name:      "latest",
		Path:      "group/project/app:latest",
		CreatedAt: nil,
	})

	assert.Contains(t, got, "latest")
	assert.Contains(t, got, "Size")
	assert.NotContains(t, got, "<nil>")
}

func TestStatusString(t *testing.T) {
	t.Parallel()

	assert.Empty(t, statusString(nil))

	status := gitlab.ContainerRegistryStatusDeleteFailed
	assert.Equal(t, "delete_failed", statusString(&status))
}

func TestTimeAgo(t *testing.T) {
	t.Parallel()

	assert.Empty(t, timeAgo(nil))

	createdAt := time.Now().Add(-time.Hour)
	assert.NotEmpty(t, timeAgo(&createdAt))
}
