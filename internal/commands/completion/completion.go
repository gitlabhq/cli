package completion

import (
	"fmt"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"

	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
)

func NewCmdCompletion(io *iostreams.IOStreams) *cobra.Command {
	var (
		shellType string

		// description will not be added if true
		excludeDesc = false
	)

	completionCmd := &cobra.Command{
		Use:   "completion",
		Short: "Generate shell completion scripts.",
		Long: heredoc.Docf(`
		This command outputs code meant to be saved to a file, or immediately
		evaluated by an interactive shell. To load completions:

		### Bash

		To load completions in your current shell session:

		%[2]sshell
		source <(glab completion -s bash)
		%[2]s

		To load completions for every new session, run this command one time:

		#### Linux

		%[2]sshell
		glab completion -s bash > /etc/bash_completion.d/glab
		%[2]s

		#### macOS

		%[2]sshell
		glab completion -s bash > /usr/local/etc/bash_completion.d/glab
		%[2]s

		### Zsh

		If shell completion is not already enabled in your environment you must
		enable it. Run this command one time:

		%[2]sshell
		echo "autoload -U compinit; compinit" >> ~/.zshrc
		%[2]s

		To load completions in your current shell session:

		%[2]sshell
		source <(glab completion -s zsh); compdef _glab glab
		%[2]s

		To load completions for every new session, run this command one time:

		#### Linux

		%[2]sshell
		glab completion -s zsh > "${fpath[1]}/_glab"
		%[2]s

		#### macOS

		For older versions of macOS, you might need this command:

		%[2]sshell
		glab completion -s zsh > /usr/local/share/zsh/site-functions/_glab
		%[2]s

		The Homebrew version of glab should install completions automatically.

		### fish

		To load completions in your current shell session:

		%[2]sshell
		glab completion -s fish | source
		%[2]s

		To load completions for every new session, run this command one time:

		%[2]sshell
		glab completion -s fish > ~/.config/fish/completions/glab.fish
		%[2]s

		### PowerShell

		To load completions in your current shell session:

		%[2]sshell
		glab completion -s powershell | Out-String | Invoke-Expression
		%[2]s

		To load completions for every new session, add the output of the above command
		to your PowerShell profile.

		When installing glab through a package manager, however, you might not need
		more shell configuration to support completions.
		For Homebrew, see [brew shell completion](https://docs.brew.sh/Shell-Completion)
		`, "`", "```"),
		Example: heredoc.Doc(`
			# Load completions in the current zsh session
			source <(glab completion -s zsh); compdef _glab glab

			# Print the bash completion script
			glab completion -s bash`),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			mcpannotations.Safe: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			out := io.StdOut
			rootCmd := cmd.Parent()

			switch shellType {
			case "bash":
				return rootCmd.GenBashCompletionV2(out, !excludeDesc)
			case "zsh":
				if excludeDesc {
					return rootCmd.GenZshCompletionNoDesc(out)
				}
				return rootCmd.GenZshCompletion(out)
			case "powershell":
				if excludeDesc {
					return rootCmd.GenPowerShellCompletion(out)
				}
				return rootCmd.GenPowerShellCompletionWithDesc(out)
			case "fish":
				return rootCmd.GenFishCompletion(out, !excludeDesc)
			default:
				return fmt.Errorf("unsupported shell type %q", shellType)
			}
		},
	}

	completionCmd.Flags().StringVarP(&shellType, "shell", "s", "bash", "Shell type: bash, zsh, fish, powershell.")
	completionCmd.Flags().BoolVarP(&excludeDesc, "no-desc", "", false, "Do not include shell completion description.")
	return completionCmd
}
