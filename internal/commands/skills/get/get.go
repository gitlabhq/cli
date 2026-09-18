package get

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/commands/skills/bundled"
	"gitlab.com/gitlab-org/cli/internal/commands/skills/skill"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
	"gitlab.com/gitlab-org/cli/internal/text"
)

type options struct {
	io        *iostreams.IOStreams
	getSkill  func(string) (skill.Skill, error)
	allSkills func() ([]skill.Skill, error)

	name     string
	filePath string
}

func NewCmd(f cmdutils.Factory) *cobra.Command {
	return newCmd(f, bundled.Get, bundled.All)
}

func newCmd(
	f cmdutils.Factory,
	getSkill func(string) (skill.Skill, error),
	allSkills func() ([]skill.Skill, error),
) *cobra.Command {
	opts := &options{
		io:        f.IO(),
		getSkill:  getSkill,
		allSkills: allSkills,
	}

	cmd := &cobra.Command{
		Use:   "get <name> [<path>]",
		Short: "Print a bundled agent skill file. (EXPERIMENTAL)",
		Long: heredoc.Docf(`
			Print a file from an agent skill bundled with this glab binary without installing it.

			The path is relative to the skill root and defaults to %[1]sSKILL.md%[1]s.
		`, "`") + text.ExperimentalString,
		Example: heredoc.Doc(`
			# Print the manifest for the bundled glab skill
			glab skills get glab

			# Print the other bundled skill
			glab skills get glab-stack

			# For skills that ship supporting files, use the path form
			# glab skills get <name> references/<file>.md
		`),
		Args: cobra.MaximumNArgs(2),
		Annotations: map[string]string{
			mcpannotations.Safe: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.complete(args)
			if err := opts.validate(); err != nil {
				return err
			}
			return opts.run()
		},
	}

	return cmd
}

func (o *options) complete(args []string) {
	if len(args) > 0 {
		o.name = args[0]
	}
	o.filePath = skill.FileName
	if len(args) > 1 {
		o.filePath = args[1]
	}
}

func (o *options) validate() error {
	if o.name != "" {
		return nil
	}
	names, err := o.bundledSkillNames()
	if err != nil {
		return err
	}
	return fmt.Errorf("skill name is required; available bundled skills: %s", strings.Join(names, ", "))
}

func (o *options) run() error {
	s, err := o.getSkill(o.name)
	if err != nil {
		if !errors.Is(err, bundled.ErrNotFound) {
			return err
		}
		names, listErr := o.bundledSkillNames()
		if listErr != nil {
			return listErr
		}
		return fmt.Errorf("unknown skill %q; only bundled skills can be printed (available: %s), and remote skills from 'glab skills list' can be installed with 'glab skills install <name>'", o.name, strings.Join(names, ", "))
	}

	files := sortedFileNames(s.Files)
	content, ok := s.Files[o.filePath]
	if !ok {
		return fmt.Errorf("unknown path %q for bundled skill %q; available files: %s", o.filePath, o.name, strings.Join(files, ", "))
	}
	if o.filePath == skill.FileName {
		for _, file := range files {
			if file == skill.FileName {
				continue
			}
			content = appendManifestFooter(content, s.Name)
			break
		}
	}

	if err := o.io.StartPager(); err != nil {
		return err
	}
	defer o.io.StopPager()

	_, err = o.io.StdOut.Write(content)
	return err
}

func (o *options) bundledSkillNames() ([]string, error) {
	skills, err := o.allSkills()
	if err != nil {
		return nil, err
	}
	names := make([]string, len(skills))
	for i, s := range skills {
		names[i] = s.Name
	}
	sort.Strings(names)
	return names, nil
}

func sortedFileNames(files map[string][]byte) []string {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func appendManifestFooter(content []byte, name string) []byte {
	footer := fmt.Sprintf("\n\n---\n\nYou are viewing this via `glab`; the links above are relative to the on-disk skill tree. Fetch referenced files with `glab skills get %s <path>`.\n", name)
	return append(append([]byte(nil), content...), footer...)
}
