package binarymgr

import (
	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
)

// Flags holds the glab-owned flags split out of a pass-through command's args.
type Flags struct {
	Yes       bool
	Install   bool
	Update    bool
	Forwarded []string
}

// HelpOnly reports whether the invocation should fall back to glab's help text
// rather than run or install the managed binary.
func (f Flags) HelpOnly() bool {
	if f.Install || f.Update {
		return false
	}
	return len(f.Forwarded) == 0 || f.Forwarded[0] == "--help" || f.Forwarded[0] == "-h"
}

// SplitFlags separates the glab-owned flags from the args forwarded verbatim to
// the managed binary. Everything after the first positional arg or "--" is
// forwarded untouched.
func SplitFlags(args []string) Flags {
	var flags Flags
	for i := range len(args) {
		switch arg := args[i]; {
		case arg == "--yes" || arg == "-y":
			flags.Yes = true
		case arg == "--install":
			flags.Install = true
		case arg == "--update":
			flags.Update = true
		case arg == "--":
			flags.Forwarded = args[i+1:]
			return flags
		default:
			flags.Forwarded = args[i:]
			return flags
		}
	}
	return flags
}

// NewRunner builds a Runner for a pass-through command. The caller sets Executor.
func NewRunner(io *iostreams.IOStreams, cfg config.Config, spec Spec) *Runner {
	return &Runner{
		IO:      io,
		Cfg:     cfg,
		Spec:    spec,
		Manager: NewManager(io, spec),
	}
}
