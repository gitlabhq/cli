//go:build !integration

package run

import (
	"bytes"
	"testing"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/acarl005/stripansi"
	"github.com/google/shlex"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func Test_ScheduleRun(t *testing.T) {
	io, _, stdout, stderr := cmdtest.TestIOStreams(cmdtest.WithTestIOStreamsAsTTY(true))
	f := cmdtest.NewTestFactory(io, cmdtest.WithConfig(config.NewFromString(heredoc.Doc(`
		hosts:
		  gitlab.com:
		    username: monalisa
		    token: OTOKEN
	`))))

	runSchedule = func(*gitlab.Client, string, int64, ...gitlab.RequestOptionFunc) error {
		_, err := f.BaseRepo()
		if err != nil {
			return err
		}
		return nil
	}

	testCases := []struct {
		Name        string
		ExpectedMsg []string
		wantErr     bool
		cli         string
		wantStderr  string
	}{
		{
			Name:        "Schedule 1 started",
			ExpectedMsg: []string{"Started schedule with ID 1"},
			cli:         "1",
		},
	}

	cmd := NewCmdRun(f)
	cmdutils.EnableRepoOverride(cmd, f)

	for _, tc := range testCases {
		t.Run(tc.Name, func(t *testing.T) {
			argv, err := shlex.Split(tc.cli)
			require.NoError(t, err)
			cmd.SetArgs(argv)

			_, err = cmd.ExecuteC()
			if err != nil {
				if tc.wantErr == true {
					if assert.Error(t, err) {
						assert.Equal(t, tc.wantStderr, err.Error())
					}
					return
				}
			}

			out := stripansi.Strip(stdout.String())

			for _, msg := range tc.ExpectedMsg {
				assert.Contains(t, out, msg)
				assert.Empty(t, stderr.String())
			}
		})
	}
}

func Test_ScheduleRunNoID(t *testing.T) {
	var buf bytes.Buffer
	cmd := NewCmdRun(cmdtest.NewTestFactory(nil))
	cmd.SetErr(&buf)

	require.Error(t, cmd.Execute())

	assert.Contains(t, buf.String(), "Error: accepts 1 arg(s), received 0\n")
}
