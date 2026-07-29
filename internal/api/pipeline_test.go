//go:build !integration

package api

import (
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"
)

func TestJobSort_EqualCreatedAtUsesAscendingID(t *testing.T) {
	t.Parallel()

	createdAt := time.Date(2026, time.July, 27, 12, 0, 0, 0, time.UTC)
	jobs := []*gitlab.Job{
		{ID: 30, CreatedAt: &createdAt},
		{ID: 10, CreatedAt: &createdAt},
		{ID: 20, CreatedAt: &createdAt},
	}

	sort.Sort(jobSort{Jobs: jobs})

	assert.Equal(t, int64(10), jobs[0].ID)
	assert.Equal(t, int64(20), jobs[1].ID)
	assert.Equal(t, int64(30), jobs[2].ID)
}

func TestBridgeSort_EqualCreatedAtUsesAscendingID(t *testing.T) {
	t.Parallel()

	createdAt := time.Date(2026, time.July, 27, 12, 0, 0, 0, time.UTC)
	bridges := []*gitlab.Bridge{
		{ID: 30, CreatedAt: &createdAt},
		{ID: 10, CreatedAt: &createdAt},
		{ID: 20, CreatedAt: &createdAt},
	}

	sort.Sort(bridgeSort{Bridges: bridges})

	assert.Equal(t, int64(10), bridges[0].ID)
	assert.Equal(t, int64(20), bridges[1].ID)
	assert.Equal(t, int64(30), bridges[2].ID)
}
