//go:build !integration

package api

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestEffectiveMethod(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "defaults to GET", args: []string{"/user"}, want: "GET"},
		{name: "explicit method", args: []string{"/user", "--method", "DELETE"}, want: "DELETE"},
		{name: "lowercase method is normalised", args: []string{"/user", "--method", "patch"}, want: "PATCH"},
		{name: "field implies POST", args: []string{"/user", "--field", "a=1"}, want: "POST"},
		{name: "raw-field implies POST", args: []string{"/user", "--raw-field", "a=1"}, want: "POST"},
		{name: "form implies POST", args: []string{"/user", "--form", "a=1"}, want: "POST"},
		{name: "input implies POST", args: []string{"/user", "--input", "body.json"}, want: "POST"},
		{name: "explicit method beats implied POST", args: []string{"/user", "--method", "PUT", "--field", "a=1"}, want: "PUT"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, EffectiveMethod(parseAPIFlags(t, tt.args)))
		})
	}
}

func parseAPIFlags(t *testing.T, args []string) *cobra.Command {
	t.Helper()

	ios, _, _, _ := cmdtest.TestIOStreams()
	cmd := NewCmdApi(cmdtest.NewTestFactory(ios), nil)

	require.NoError(t, cmd.Flags().Parse(args))
	return cmd
}
