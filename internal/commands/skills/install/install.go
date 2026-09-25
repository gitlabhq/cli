package install

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/commands/skills/registry"
	"gitlab.com/gitlab-org/cli/internal/commands/skills/skill"
	"gitlab.com/gitlab-org/cli/internal/git"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
	"gitlab.com/gitlab-org/cli/internal/text"
)

// skillsRelDir is the conventional directory for agent skills,
// as defined by the Agent Skills specification (https://agentskills.io).
var skillsRelDir = filepath.Join(".agents", "skills")

// defaultSkillName is the skill installed when no positional argument
// is given. Keeping the default to the single core `glab` skill
// avoids polluting an agent's context with descriptions of bundled
// skills the user may not need.
const defaultSkillName = "glab"

type options struct {
	io        *iostreams.IOStreams
	global    bool
	path      string
	force     bool
	requested string
	targetDir string
}

func NewCmdInstall(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		io: f.IO(),
	}

	cmd := &cobra.Command{
		Use:   "install [name]",
		Short: "Install glab's bundled agent skills. (EXPERIMENTAL)",
		Long: heredoc.Docf(`
			Install bundled %[1]sSKILL.md%[1]s files to %[1]s.agents/skills/%[1]s, the
			cross-agent standard defined by the Agent Skills specification. This works
			with GitLab Duo Agent Platform, Claude Code, Codex, Gemini CLI, and any
			other compliant agent.

			By default, only the core %[1]sglab%[1]s skill is installed. Pass a positional
			%[1]sname%[1]s argument to install a specific bundled skill instead. Run
			%[1]sglab skills list%[1]s to see what is available.

			Install scope:

			- By default, skills are installed for the current project, in %[1]s.agents/skills/%[1]s
			  at the root of the current Git repository.
			- Use %[1]s--global%[1]s to install skills for the current user, in
			  %[1]s~/.agents/skills/%[1]s.
			- Use %[1]s--path%[1]s to install skills to a custom directory. The path is resolved
			  relative to the current working directory, not the repository root.

			To overwrite an existing skill file, use %[1]s--force%[1]s.
		`, "`") + text.ExperimentalString,
		Example: heredoc.Doc(`
			# Install the core glab skill in the current project (default)
			glab skills install

			# Install a specific bundled skill by name
			glab skills install glab-stack

			# Install the core skill globally (user scope)
			glab skills install --global

			# Install a skill to a custom directory
			glab skills install glab-stack --path /path/to/skills

			# Overwrite an existing skill file
			glab skills install --force
		`),
		Args: cobra.MaximumNArgs(1),
		Annotations: map[string]string{
			mcpannotations.Destructive: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.complete(args); err != nil {
				return err
			}
			return opts.run()
		},
	}

	fl := cmd.Flags()
	fl.BoolVarP(&opts.global, "global", "g", false, "Install skills at user scope (~/.agents/skills/).")
	fl.StringVar(&opts.path, "path", "", "Install skills to the directory at <path>.")
	fl.BoolVarP(&opts.force, "force", "f", false, "Overwrite existing skill files.")
	cmd.MarkFlagsMutuallyExclusive("global", "path")

	return cmd
}

func (o *options) complete(args []string) error {
	if len(args) == 1 {
		o.requested = args[0]
	}

	if o.path != "" {
		o.targetDir = o.path
		return nil
	}

	if o.global {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("could not determine home directory: %w", err)
		}
		o.targetDir = filepath.Join(home, skillsRelDir)
		return nil
	}

	repoRoot, err := git.ToplevelDir()
	if err != nil {
		return fmt.Errorf("not in a Git repository. Use --global or --path to specify a target: %w", err)
	}
	o.targetDir = filepath.Join(repoRoot, skillsRelDir)
	return nil
}

func (o *options) run() error {
	skills, err := o.resolveSkills()
	if err != nil {
		return err
	}

	var errs []error
	for _, s := range skills {
		if err := o.installOne(s); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (o *options) resolveSkills() ([]skill.Skill, error) {
	name := o.requested
	if name == "" {
		name = defaultSkillName
	}
	s, err := registry.Get(name)
	if err != nil {
		return nil, err
	}
	return []skill.Skill{s}, nil
}

func (o *options) installOne(s skill.Skill) error {
	skillDir := filepath.Join(o.targetDir, s.Name)
	skillMDPath := filepath.Join(skillDir, skill.FileName)
	_, statErr := os.Stat(skillMDPath)
	exists := statErr == nil

	c := o.io.Color()
	if exists && !o.force {
		o.io.LogErrorf("%s %s already exists. Use --force to overwrite.\n", c.WarnIcon(), skillMDPath)
		return nil
	}

	for rel, content := range s.Files {
		destPath := filepath.Join(skillDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
			return fmt.Errorf("creating directory for %s: %w", destPath, err)
		}
		if err := os.WriteFile(destPath, content, 0o644); err != nil {
			return fmt.Errorf("writing %s: %w", destPath, err)
		}
	}

	if exists {
		o.io.LogInfof("%s Overwrote %s\n", c.GreenCheck(), skillDir)
	} else {
		o.io.LogInfof("%s Installed %s\n", c.GreenCheck(), skillDir)
	}
	return nil
}
