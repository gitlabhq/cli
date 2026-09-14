//go:build !integration

package job

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

var tests = []struct {
	name        string
	args        string
	expectedOut string
	expectedErr string
}{
	{
		name:        "when no args should display the help message",
		args:        "",
		expectedOut: "Use \"job [command] --help\" for more information about a command.\n",
		expectedErr: "",
	},
}

func TestJobCmd(t *testing.T) {
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			wantedErr := ""
			if test.expectedErr != "" {
				wantedErr = test.expectedErr
			}

			ios, _, stdout, stderr := cmdtest.TestIOStreams()
			f := cmdtest.NewTestFactory(ios)

			cmd := NewCmdJob(f)
			cmd.SetOut(stdout)
			cmd.SetErr(stderr)

			err := cmd.Execute()

			if assert.NoErrorf(t, err, "error running `job %s`: %v", test.args, err) {
				assert.Contains(t, stderr.String(), wantedErr)
				assert.Contains(t, stdout.String(), test.expectedOut)
			}
		})
	}
}
