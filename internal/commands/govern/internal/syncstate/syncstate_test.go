//go:build !integration

package syncstate

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStartAt(t *testing.T) {
	tests := map[string]struct {
		// position and start are the session's state before StartAt, where a
		// start of 0 records none.
		position, start int64
		offset          int64
		wantPosition    int64
		wantUploaded    bool
	}{
		"new session":                    {offset: 20, wantPosition: 20},
		"session that started elsewhere": {position: 10, start: 10, offset: 20, wantPosition: 20},
		"session with uploads":           {position: 10, offset: 20, wantPosition: 20, wantUploaded: true},
		"position past the offset":       {position: 30, start: 30, offset: 20, wantPosition: 30},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Setenv("GLAB_CONFIG_DIR", t.TempDir())
			if tc.start > 0 {
				require.NoError(t, StartAt("sess-1", tc.start))
			}
			require.NoError(t, WritePosition("sess-1", tc.position))

			require.NoError(t, StartAt("sess-1", tc.offset))

			position, err := ReadPosition("sess-1")
			require.NoError(t, err)
			assert.Equal(t, tc.wantPosition, position)
			uploaded, err := HasUploads("sess-1", position)
			require.NoError(t, err)
			assert.Equal(t, tc.wantUploaded, uploaded)
		})
	}
}

func TestCursorPath_RejectsPaths(t *testing.T) {
	t.Parallel()

	for _, id := range []string{"../escape", "a/b", `a\b`, "a.b"} {
		_, err := PositionPath(id)
		assert.Error(t, err, id)
	}
}
