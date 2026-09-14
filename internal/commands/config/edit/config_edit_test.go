//go:build !integration

package edit

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestNewCmdEdit(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		args      []string
		wantLocal bool
	}{
		{
			name:      "without flags",
			args:      []string{},
			wantLocal: false,
		},
		{
			name:      "with local flag",
			args:      []string{"--local"},
			wantLocal: true,
		},
		{
			name:      "with local flag short form",
			args:      []string{"-l"},
			wantLocal: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			io, _, _, _ := cmdtest.TestIOStreams()
			f := cmdtest.NewTestFactory(io)

			cmd := NewCmdEdit(f)
			require.NotNil(t, cmd)

			// Verify command metadata
			assert.Equal(t, "edit", cmd.Use)
			assert.NotEmpty(t, cmd.Short)
			assert.NotEmpty(t, cmd.Long)
			assert.NotEmpty(t, cmd.Example)

			// Verify flags exist
			localFlag := cmd.Flags().Lookup("local")
			require.NotNil(t, localFlag, "local flag should exist")
			assert.Equal(t, "l", localFlag.Shorthand)
			assert.Equal(t, "false", localFlag.DefValue)

			// Verify MCP annotation exists
			assert.NotEmpty(t, cmd.Annotations)
		})
	}
}

func TestConfigEdit_Options(t *testing.T) {
	t.Parallel()

	t.Run("options struct is initialized correctly", func(t *testing.T) {
		t.Parallel()

		io, _, _, _ := cmdtest.TestIOStreams()
		f := cmdtest.NewTestFactory(io)

		cmd := NewCmdEdit(f)

		err := cmd.ParseFlags([]string{"--local"})
		require.NoError(t, err)

		// Verify the flag was set
		localFlag, err := cmd.Flags().GetBool("local")
		require.NoError(t, err)
		assert.True(t, localFlag)
	})

	t.Run("global flag default value", func(t *testing.T) {
		t.Parallel()

		io, _, _, _ := cmdtest.TestIOStreams()
		f := cmdtest.NewTestFactory(io)

		cmd := NewCmdEdit(f)

		// Parse flags without setting local
		err := cmd.ParseFlags([]string{})
		require.NoError(t, err)

		// Verify the flag defaults to false
		localFlag, err := cmd.Flags().GetBool("local")
		require.NoError(t, err)
		assert.False(t, localFlag)
	})
}
