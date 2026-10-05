package commands

import (
	"strings"

	"github.com/spf13/cobra"
)

// walkCommandTree visits every command below the root. Path excludes
// the binary name.
func walkCommandTree(cmd *cobra.Command, path []string, visit func(*cobra.Command, []string)) {
	if cmd.HasParent() {
		name := strings.Fields(cmd.Use)[0]
		path = append(path, name)
		visit(cmd, path)
	}
	for _, sub := range cmd.Commands() {
		walkCommandTree(sub, path, visit)
	}
}
