//go:build !integration

package get

import (
	"fmt"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/commands/skills/bundled"
	"gitlab.com/gitlab-org/cli/internal/commands/skills/skill"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestNewCmd_DefaultAndExplicitManifestMatch(t *testing.T) {
	t.Parallel()

	exec := cmdtest.SetupCmdForTest(t, NewCmd, false)
	defaultOut, err := exec("glab")
	require.NoError(t, err)

	exec = cmdtest.SetupCmdForTest(t, NewCmd, false)
	explicitOut, err := exec("glab SKILL.md")
	require.NoError(t, err)

	assert.Equal(t, defaultOut.String(), explicitOut.String())
	assert.Contains(t, defaultOut.String(), "name: glab")
	assert.Empty(t, defaultOut.Stderr())
}

func TestNewCmd_SingleFileManifestHasNoFooter(t *testing.T) {
	t.Parallel()

	exec := cmdtest.SetupCmdForTest(t, NewCmd, false)
	out, err := exec("glab")

	require.NoError(t, err)
	assert.NotContains(t, out.String(), "You are viewing this via `glab`")
}

func TestNewCmd_MultiFileFooterOnlyOnManifest(t *testing.T) {
	t.Parallel()

	s := skill.Skill{
		Name: "example",
		Files: map[string][]byte{
			"SKILL.md":          []byte("manifest\n"),
			"scripts/run.sh":    []byte("#!/bin/sh\necho hello\n"),
			"references/api.md": []byte("API reference\n"),
		},
	}
	newTestCmd := func(f cmdutils.Factory) *cobra.Command {
		return newCmd(f,
			func(name string) (skill.Skill, error) {
				if name == s.Name {
					return s, nil
				}
				return skill.Skill{}, fmt.Errorf("%w: %s", bundled.ErrNotFound, name)
			},
			func() ([]skill.Skill, error) { return []skill.Skill{s}, nil },
		)
	}

	exec := cmdtest.SetupCmdForTest(t, newTestCmd, false)
	manifestOut, err := exec("example")
	require.NoError(t, err)
	assert.Contains(t, manifestOut.String(), "Fetch referenced files with `glab skills get example <path>`.")
	assert.NotContains(t, manifestOut.String(), "e.g.")

	exec = cmdtest.SetupCmdForTest(t, newTestCmd, false)
	scriptOut, err := exec("example scripts/run.sh")
	require.NoError(t, err)
	assert.Equal(t, "#!/bin/sh\necho hello\n", scriptOut.String())
	assert.NotContains(t, scriptOut.String(), "You are viewing this via")
}

func TestNewCmd_StartsPagerAfterLookup(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name    string
		args    string
		wantErr string
	}{
		{name: "known file", args: "glab", wantErr: "EOF found when expecting closing quote"},
		{name: "unknown file", args: "glab missing.md", wantErr: `unknown path "missing.md"`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			io, _, _, _ := cmdtest.TestIOStreams(cmdtest.WithTestIOStreamsAsTTY(true))
			io.SetPager(`"`)
			exec := cmdtest.SetupCmdForTest(t, NewCmd, true, cmdtest.WithIOStreamsOverride(io))
			out, err := exec(tt.args)

			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
			assert.Empty(t, out.String())
		})
	}
}

func TestNewCmd_UnknownName(t *testing.T) {
	t.Parallel()

	exec := cmdtest.SetupCmdForTest(t, NewCmd, false)
	out, err := exec("unknown")

	require.Error(t, err)
	assert.Equal(t, "unknown skill \"unknown\"; only bundled skills can be printed (available: glab, glab-stack), and remote skills from 'glab skills list' can be installed with 'glab skills install <name>'", err.Error())
	assert.Empty(t, out.String())
}

func TestNewCmd_MissingName(t *testing.T) {
	t.Parallel()

	exec := cmdtest.SetupCmdForTest(t, NewCmd, false)
	out, err := exec("")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "skill name is required")
	assert.Contains(t, err.Error(), "available bundled skills: glab, glab-stack")
	assert.Empty(t, out.String())
}

func TestNewCmd_UnknownPath(t *testing.T) {
	t.Parallel()

	exec := cmdtest.SetupCmdForTest(t, NewCmd, false)
	out, err := exec("glab missing.md")

	require.Error(t, err)
	assert.Contains(t, err.Error(), `unknown path "missing.md"`)
	assert.Contains(t, err.Error(), "available files: SKILL.md")
	assert.Empty(t, out.String())
}

func TestNewCmd_UnknownAbsoluteAndTraversalPaths(t *testing.T) {
	t.Parallel()

	for _, filePath := range []string{"../x", "/abs"} {
		t.Run(filePath, func(t *testing.T) {
			t.Parallel()

			exec := cmdtest.SetupCmdForTest(t, NewCmd, false)
			out, err := exec("glab " + filePath)

			require.Error(t, err)
			assert.Contains(t, err.Error(), fmt.Sprintf("unknown path %q", filePath))
			assert.Contains(t, err.Error(), "available files: SKILL.md")
			assert.Empty(t, out.String())
		})
	}
}
