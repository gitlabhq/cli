//go:build !integration

package list

import (
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

func Test_ScheduleList(t *testing.T) {
	io, _, stdout, stderr := cmdtest.TestIOStreams(cmdtest.WithTestIOStreamsAsTTY(true))
	f := cmdtest.NewTestFactory(io, cmdtest.WithConfig(config.NewFromString(heredoc.Doc(`
		hosts:
		  gitlab.com:
		    username: monalisa
		    token: OTOKEN
	`))))

	getSchedules = func(client *gitlab.Client, l *gitlab.ListPipelineSchedulesOptions, repo string) ([]*gitlab.PipelineSchedule, error) {
		_, err := f.BaseRepo()
		if err != nil {
			return nil, err
		}

		return []*gitlab.PipelineSchedule{
			{
				ID:          1,
				Description: "foo",
				Cron:        "* * * * *",
				Owner: &gitlab.User{
					ID:       1,
					Username: "bar",
				},
				Active: true,
			},
		}, nil
	}

	cmd := NewCmdList(f)
	cmdutils.EnableRepoOverride(cmd, f)

	t.Run("Schedule exists", func(t *testing.T) {
		_, err := cmd.ExecuteC()
		if err != nil {
			t.Fatal(err)
		}

		out := stripansi.Strip(stdout.String())

		assert.Contains(t, out, "1\tfoo\t* * * * *\tbar\ttrue")
		assert.Empty(t, stderr.String())
	})
}

func Test_NoScheduleList(t *testing.T) {
	io, _, stdout, stderr := cmdtest.TestIOStreams(cmdtest.WithTestIOStreamsAsTTY(true))
	stubFactory := cmdtest.NewTestFactory(io, cmdtest.WithConfig(config.NewFromString(heredoc.Doc(`
		hosts:
		  gitlab.com:
		    username: monalisa
		    token: OTOKEN
	`))))

	getSchedules = func(*gitlab.Client, *gitlab.ListPipelineSchedulesOptions, string) ([]*gitlab.PipelineSchedule, error) {
		_, err := stubFactory.BaseRepo()
		if err != nil {
			return nil, err
		}

		return nil, nil
	}

	cmd := NewCmdList(stubFactory)
	cmdutils.EnableRepoOverride(cmd, stubFactory)

	t.Run("No schedules exist", func(t *testing.T) {
		_, err := cmd.ExecuteC()
		if err != nil {
			t.Fatal(err)
		}

		out := stripansi.Strip(stdout.String())

		assert.Contains(t, out, "No schedules available on")
		assert.Empty(t, stderr.String())
	})
}

func TestScheduleList_JSON(t *testing.T) {
	t.Parallel()

	io, _, stdout, stderr := cmdtest.TestIOStreams()
	f := cmdtest.NewTestFactory(io, cmdtest.WithConfig(config.NewFromString(heredoc.Doc(`
		hosts:
		  gitlab.com:
		    username: monalisa
		    token: OTOKEN
	`))))

	getSchedules = func(client *gitlab.Client, l *gitlab.ListPipelineSchedulesOptions, repo string) ([]*gitlab.PipelineSchedule, error) {
		return []*gitlab.PipelineSchedule{
			{
				ID:          1,
				Description: "foo",
				Cron:        "* * * * *",
				Owner: &gitlab.User{
					ID:       1,
					Username: "bar",
				},
				Active: true,
			},
		}, nil
	}

	cmd := NewCmdList(f)
	cmdutils.EnableRepoOverride(cmd, f)

	argv, err := shlex.Split("-F json")
	require.NoError(t, err)
	cmd.SetArgs(argv)

	_, err = cmd.ExecuteC()
	require.NoError(t, err)

	out := stdout.String()
	assert.Contains(t, out, `"id":1`)
	assert.Contains(t, out, `"description":"foo"`)
	assert.Contains(t, out, `"cron":"* * * * *"`)
	assert.Contains(t, out, `"active":true`)
	assert.Empty(t, stderr.String())
}
