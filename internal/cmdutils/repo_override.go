package cmdutils

import (
	"os"

	"github.com/spf13/cobra"
)

// RepoOverrideValue returns the repository set with -R/--repo or, failing
// that, GITLAB_REPO. It is empty when the repository is not overridden.
func RepoOverrideValue(cmd *cobra.Command) string {
	if flag := cmd.Flags().Lookup("repo"); flag != nil && flag.Value.String() != "" {
		return flag.Value.String()
	}
	return os.Getenv("GITLAB_REPO")
}

func ApplyRepoOverride(cmd *cobra.Command, f Factory) error {
	if repoOverride := RepoOverrideValue(cmd); repoOverride != "" {
		return f.RepoOverride(repoOverride)
	}
	return nil
}

func EnableRepoOverride(cmd *cobra.Command, f Factory) {
	// if the flag doesn't exist yet, create it
	if cmd.PersistentFlags().Lookup("repo") == nil {
		cmd.PersistentFlags().StringP("repo", "R", "", "Select another repository. You can use either OWNER/REPO or GROUP/NAMESPACE/REPO. The full URL or Git URL is also accepted.")
		cobra.CheckErr(cmd.RegisterFlagCompletionFunc("repo", cobra.NoFileCompletions))
	}

	// unhide the flag for this command and its children
	if flag := cmd.PersistentFlags().Lookup("repo"); flag != nil {
		flag.Hidden = false
	}

	cmd.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		return ApplyRepoOverride(cmd, f)
	}
}

// AddGlobalRepoOverride adds the -R flag globally but keeps it hidden
func AddGlobalRepoOverride(cmd *cobra.Command, f Factory) {
	cmd.PersistentFlags().StringP("repo", "R", "", "Select another repository. Can use either `OWNER/REPO` or `GROUP/NAMESPACE/REPO` format. Also accepts full URL or Git URL.")
	_ = cmd.PersistentFlags().MarkHidden("repo")
	cobra.CheckErr(cmd.RegisterFlagCompletionFunc("repo", cobra.NoFileCompletions))

	originalPreRunE := cmd.PersistentPreRunE
	cmd.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		if originalPreRunE != nil {
			if err := originalPreRunE(cmd, args); err != nil {
				return err
			}
		}

		return ApplyRepoOverride(cmd, f)
	}
}
