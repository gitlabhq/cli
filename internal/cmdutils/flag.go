package cmdutils

import (
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var groupViper = func() *viper.Viper {
	inst := viper.New()
	inst.SetEnvPrefix("GITLAB")
	inst.MustBindEnv("group")
	return inst
}()

// GroupOverride determines which group to use based repo/group flags and the environment.
func GroupOverride(cmd *cobra.Command) (string, error) {
	repo, err := cmd.Flags().GetString("repo")
	if err != nil {
		return "", err
	}
	if repo != "" {
		// Ignore any groups if the repo flag is set
		return "", nil
	}

	group, err := cmd.Flags().GetString("group")
	if err != nil {
		return "", err
	}
	if group != "" {
		// Use the group flag if set and repo flag is not set
		return group, nil
	}

	// Consider environment if no repo or group flags are set
	return groupViper.GetString("group"), nil
}
