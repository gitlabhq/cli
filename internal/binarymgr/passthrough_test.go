//go:build !integration

package binarymgr

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSplitFlags(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		args          []string
		wantYes       bool
		wantInstall   bool
		wantUpdate    bool
		wantHelpOnly  bool
		wantForwarded []string
	}{
		{
			name:          "verbatim forwarding of a command with args",
			args:          []string{"query", "-"},
			wantForwarded: []string{"query", "-"},
		},
		{
			name:          "verbatim forwarding of a top-level command",
			args:          []string{"version"},
			wantForwarded: []string{"version"},
		},
		{
			name:          "yes long flag is consumed and dropped",
			args:          []string{"--yes", "install", "."},
			wantYes:       true,
			wantForwarded: []string{"install", "."},
		},
		{
			name:          "yes short flag is consumed and dropped",
			args:          []string{"-y", "query", "-"},
			wantYes:       true,
			wantForwarded: []string{"query", "-"},
		},
		{
			name:        "install flag is consumed and dropped",
			args:        []string{"--install", "--yes"},
			wantYes:     true,
			wantInstall: true,
		},
		{
			name:       "update flag is consumed and dropped",
			args:       []string{"--update"},
			wantUpdate: true,
		},
		{
			name:         "no args forwards nothing so the binary prints its own usage",
			wantHelpOnly: true,
		},
		{
			name:          "lone help is forwarded to the binary",
			args:          []string{"--help"},
			wantHelpOnly:  true,
			wantForwarded: []string{"--help"},
		},
		{
			name:          "lone short help is forwarded to the binary",
			args:          []string{"-h"},
			wantHelpOnly:  true,
			wantForwarded: []string{"-h"},
		},
		{
			name:          "help after a glab flag is forwarded to the binary",
			args:          []string{"--yes", "--help"},
			wantYes:       true,
			wantHelpOnly:  true,
			wantForwarded: []string{"--help"},
		},
		{
			name:          "help with a forwarded command is not help-only",
			args:          []string{"query", "--help"},
			wantForwarded: []string{"query", "--help"},
		},
		{
			name:          "glab flag after the subcommand is forwarded to the binary",
			args:          []string{"setup", "claude", "--yes"},
			wantForwarded: []string{"setup", "claude", "--yes"},
		},
		{
			name:          "double dash forwards the rest verbatim",
			args:          []string{"--", "--yes", "status"},
			wantForwarded: []string{"--yes", "status"},
		},
		{
			name:          "help before a command is forwarded to the binary",
			args:          []string{"--help", "status"},
			wantHelpOnly:  true,
			wantForwarded: []string{"--help", "status"},
		},
		{
			name:          "unknown leading flag forwards everything to the binary",
			args:          []string{"--log-level", "debug", "status"},
			wantForwarded: []string{"--log-level", "debug", "status"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			flags := SplitFlags(tt.args)
			assert.Equal(t, tt.wantYes, flags.Yes)
			assert.Equal(t, tt.wantInstall, flags.Install)
			assert.Equal(t, tt.wantUpdate, flags.Update)
			assert.Equal(t, tt.wantHelpOnly, flags.HelpOnly())
			assert.Equal(t, tt.wantForwarded, flags.Forwarded)
		})
	}
}

func TestFlags_HelpOnly(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		flags Flags
		want  bool
	}{
		{
			name:  "install is never help-only",
			flags: Flags{Install: true},
			want:  false,
		},
		{
			name:  "update is never help-only",
			flags: Flags{Update: true},
			want:  false,
		},
		{
			name:  "install wins over a lone help arg",
			flags: Flags{Install: true, Forwarded: []string{"--help"}},
			want:  false,
		},
		{
			name:  "no forwarded args is help-only",
			flags: Flags{},
			want:  true,
		},
		{
			name:  "lone long help is help-only",
			flags: Flags{Forwarded: []string{"--help"}},
			want:  true,
		},
		{
			name:  "lone short help is help-only",
			flags: Flags{Forwarded: []string{"-h"}},
			want:  true,
		},
		{
			name:  "help after a command is not help-only",
			flags: Flags{Forwarded: []string{"status", "--help"}},
			want:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, tt.flags.HelpOnly())
		})
	}
}
