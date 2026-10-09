//go:build integration

package clone

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/google/shlex"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
	"gitlab.com/gitlab-org/cli/test"
)

func runCommand(cmd *cobra.Command, cli string, stds ...*bytes.Buffer) (*test.CmdOut, error) {
	var stdin *bytes.Buffer
	var stderr *bytes.Buffer
	var stdout *bytes.Buffer

	for i, std := range stds {
		if std != nil {
			if i == 0 {
				stdin = std
			}
			if i == 1 {
				stdout = std
			}
			if i == 2 {
				stderr = std
			}
		}
	}
	cmd.SetIn(stdin)
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)

	argv, err := shlex.Split(cli)
	if err != nil {
		return nil, err
	}
	cmd.SetArgs(argv)
	_, err = cmd.ExecuteC()

	return &test.CmdOut{
		OutBuf: stdout,
		ErrBuf: stderr,
	}, err
}

func Test_repoClone_Integration(t *testing.T) {
	name := "gitlab-org/cli"
	url := "git clone -- git@gitlab.com:gitlab-org/cli.git"
	repoCloneTest(t, name, url, "")
}

func Test_repoClone_preserve_Integration(t *testing.T) {
	name := "gitlab-org/cli"
	url := "git clone -- git@gitlab.com:gitlab-org/cli.git gitlab-org/cli"
	additionalCli := "-p"
	repoCloneTest(t, name, url, additionalCli)
}

func Test_repoClone_http__Integration(t *testing.T) {
	name := "https://gitlab.com/gitlab-org/cli"
	url := "git clone -- https://gitlab.com/gitlab-org/cli.git"
	repoCloneTest(t, name, url, "")
}

func Test_repoClone_http__preserve_Integration(t *testing.T) {
	name := "https://gitlab.com/gitlab-org/cli"
	url := "git clone -- https://gitlab.com/gitlab-org/cli.git"
	additionalCli := "-p"
	repoCloneTest(t, name, url, additionalCli)
}

func Test_repoClone_dir_Integration(t *testing.T) {
	name := "gitlab-org/cli"
	url := "git clone -- git@gitlab.com:gitlab-org/cli.git tmp"
	additionalCli := "tmp"
	repoCloneTest(t, name, url, additionalCli)
}

func Test_repoClone_preserve_dir_Integration(t *testing.T) {
	name := "gitlab-org/cli"
	url := "git clone -- git@gitlab.com:gitlab-org/cli.git tmp/gitlab-org/cli"
	additionalCli := "-p tmp"
	repoCloneTest(t, name, url, additionalCli)
}

func repoCloneTest(t *testing.T, expectedRepoName string, expectedRepoUrl string, additionalCli string) {
	t.Helper()
	glTestHost := test.GetHostOrSkip(t)
	t.Setenv("GITLAB_HOST", glTestHost)

	io, stdin, stdout, stderr := cmdtest.TestIOStreams()
	fac := cmdtest.NewTestFactory(io,
		func(f *cmdtest.Factory) {
			f.ApiClientStub = func(repoHost string) (*api.Client, error) {
				return api.NewClientFromConfig(repoHost, f.Config(), false, "glab test client")
			}
		},
	)

	cs, restore := test.InitCmdStubber()
	// git clone
	cs.Stub("")
	// git remote add
	cs.Stub("")
	defer restore()

	cmd := NewCmdClone(fac, nil)
	cli := expectedRepoName
	if additionalCli != "" {
		cli += " " + additionalCli
	}
	out, err := runCommand(cmd, cli, stdin, stdout, stderr)
	if err != nil {
		t.Errorf("unexpected error: %q", err)
		return
	}

	assert.Equal(t, "", out.String())
	assert.Equal(t, "", out.Stderr())
	assert.Equal(t, 1, cs.Count)
	assert.Equal(t, expectedRepoUrl, strings.Join(cs.Calls[0].Args, " "))
}

func Test_repoClone_group_Integration(t *testing.T) {
	names := []string{"cli-automated-testing/test", "cli-automated-testing/homebrew-testing"}
	urls := []string{"git@gitlab.com:cli-automated-testing/test.git", "git@gitlab.com:cli-automated-testing/homebrew-testing.git"}
	repoCloneGroupTest(t, names, urls, 0, false, "")
}

func Test_repoClone_group_single_Integration(t *testing.T) {
	names := []string{"cli-automated-testing/test"}
	urls := []string{"git@gitlab.com:cli-automated-testing/test.git"}
	repoCloneGroupTest(t, names, urls, 1, false, "")
}

func Test_repoClone_group_paginate_Integration(t *testing.T) {
	names := []string{"cli-automated-testing/test", "cli-automated-testing/homebrew-testing"}
	urls := []string{"git@gitlab.com:cli-automated-testing/test.git", "git@gitlab.com:cli-automated-testing/homebrew-testing.git"}
	repoCloneGroupTest(t, names, urls, 1, true, "")
}

func Test_repoClone_group_dir_Integration(t *testing.T) {
	names := []string{"cli-automated-testing/test", "cli-automated-testing/homebrew-testing"}
	urls := []string{"git@gitlab.com:cli-automated-testing/test.git tmp/test", "git@gitlab.com:cli-automated-testing/homebrew-testing.git tmp/homebrew-testing"}
	additionalCli := "tmp"
	repoCloneGroupTest(t, names, urls, 0, false, additionalCli)
}

func Test_repoClone_group_dir_preserve_Integration(t *testing.T) {
	names := []string{"cli-automated-testing/test", "cli-automated-testing/homebrew-testing"}
	urls := []string{"git@gitlab.com:cli-automated-testing/test.git tmp/cli-automated-testing/test", "git@gitlab.com:cli-automated-testing/homebrew-testing.git tmp/cli-automated-testing/homebrew-testing"}
	additionalCli := "-p tmp"
	repoCloneGroupTest(t, names, urls, 0, false, additionalCli)
}

func repoCloneGroupTest(t *testing.T, expectedRepoNames []string, expectedRepoUrls []string, perPage int, paginate bool, additionalCli string) {
	t.Helper()

	assert.Equal(t, len(expectedRepoNames), len(expectedRepoUrls))

	glTestHost := test.GetHostOrSkip(t)
	t.Setenv("GITLAB_HOST", glTestHost)

	io, stdin, stdout, stderr := cmdtest.TestIOStreams()
	fac := cmdtest.NewTestFactory(io,
		func(f *cmdtest.Factory) {
			f.ApiClientStub = func(repoHost string) (*api.Client, error) {
				return api.NewClientFromConfig(repoHost, f.Config(), false, "glab test client")
			}
		},
	)

	cs, restore := test.InitCmdStubber()
	for range expectedRepoUrls {
		cs.Stub("")
	}

	defer restore()

	cmd := NewCmdClone(fac, nil)
	cli := "-g cli-automated-testing"
	if perPage != 0 {
		cli += fmt.Sprintf(" --per-page %d", perPage)
	}
	if paginate {
		cli += " --paginate"
	}
	if additionalCli != "" {
		cli += " " + additionalCli
	}

	// TODO: stub api.ListGroupProjects endpoint
	out, err := runCommand(cmd, cli, stdin, stdout, stderr)
	if err != nil {
		t.Errorf("unexpected error: %q", err)
		return
	}

	assert.Equal(t, "✓ "+strings.Join(expectedRepoNames, "\n✓ ")+"\n", out.String())
	assert.Equal(t, "", out.Stderr())
	assert.Equal(t, len(expectedRepoUrls), cs.Count)

	for i := range expectedRepoUrls {
		assert.Equal(t, fmt.Sprintf("git clone -- %s", expectedRepoUrls[i]), strings.Join(cs.Calls[i].Args, " "))
	}
}
