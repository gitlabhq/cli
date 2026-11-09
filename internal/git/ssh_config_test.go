//go:build !integration

package git

import (
	"bytes"
	"fmt"
	"io"
	"net/url"
	"path"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/stretchr/testify/require"
)

func Test_sshParser_read(t *testing.T) {
	testFiles := map[string]string{
		"/etc/ssh/config": heredoc.Doc(`
			Include sites/*
		`),
		"/etc/ssh/sites/cfg1": heredoc.Doc(`
			Host s1
			Hostname=site1.net
		`),
		"/etc/ssh/sites/cfg2": heredoc.Doc(`
			Host s2
			Hostname = site2.net
		`),
		"HOME/.ssh/config": heredoc.Doc(`
			Host *
			Host gl gitlabopen
				Hostname gitlab.com
				#Hostname example.com
			Host ex
			  Include ex_config/*
		`),
		"HOME/.ssh/ex_config/ex_cfg": heredoc.Doc(`
			Hostname example.com
		`),
	}
	globResults := map[string][]string{
		"/etc/ssh/sites/*":      {"/etc/ssh/sites/cfg1", "/etc/ssh/sites/cfg2"},
		"HOME/.ssh/ex_config/*": {"HOME/.ssh/ex_config/ex_cfg"},
	}

	p := &sshParser{
		homeDir: "HOME",
		open: func(s string) (io.Reader, error) {
			if contents, ok := testFiles[filepath.ToSlash(s)]; ok {
				return bytes.NewBufferString(contents), nil
			} else {
				return nil, fmt.Errorf("no test file stub found: %q", s)
			}
		},
		glob: func(p string) ([]string, error) {
			if results, ok := globResults[filepath.ToSlash(p)]; ok {
				return results, nil
			} else {
				return nil, fmt.Errorf("no glob stubs found: %q", p)
			}
		},
	}

	if err := p.read("/etc/ssh/config"); err != nil {
		t.Fatalf("read(global config) = %v", err)
	}
	if err := p.read("HOME/.ssh/config"); err != nil {
		t.Fatalf("read(user config) = %v", err)
	}

	if got := p.aliasMap["gl"]; got != "gitlab.com" {
		t.Errorf("expected alias %q to expand to %q, got %q", "gl", "gitlab.com", got)
	}
	if got := p.aliasMap["gitlabopen"]; got != "gitlab.com" {
		t.Errorf("expected alias %q to expand to %q, got %q", "gitlabopen", "gitlab.com", got)
	}
	if got := p.aliasMap["example.com"]; got != "" {
		t.Errorf("expected alias %q to expand to %q, got %q", "example.com", "", got)
	}
	if got := p.aliasMap["ex"]; got != "example.com" {
		t.Errorf("expected alias %q to expand to %q, got %q", "ex", "example.com", got)
	}
	if got := p.aliasMap["s1"]; got != "site1.net" {
		t.Errorf("expected alias %q to expand to %q, got %q", "s1", "site1.net", got)
	}
}

func Test_sshParser_readContinuesAfterLongLine(t *testing.T) {
	config := "#" + strings.Repeat("x", 70*1024) + "\nHost gl\nHostname gitlab.com\n"
	p := &sshParser{
		open: func(string) (io.Reader, error) {
			return strings.NewReader(config), nil
		},
	}

	err := p.read("config")

	require.NoError(t, err)
	require.Equal(t, "gitlab.com", p.aliasMap["gl"])
}

func Test_sshParser_readFirstHostNameWins(t *testing.T) {
	tests := map[string]struct {
		files map[string]string
		order []string
		want  SSHAliasMap
	}{
		"second block for the same alias in one file": {
			files: map[string]string{
				"HOME/.ssh/config": heredoc.Doc(`
					Host gl
						HostName first.example.com
					Host gl
						HostName second.example.com
				`),
			},
			order: []string{"HOME/.ssh/config"},
			want:  SSHAliasMap{"gl": "first.example.com"},
		},
		"alias listed again with another alias": {
			files: map[string]string{
				"HOME/.ssh/config": heredoc.Doc(`
					Host gl
						HostName %h.example.com
					Host gl gh
						HostName shared.example.com
				`),
			},
			order: []string{"HOME/.ssh/config"},
			want:  SSHAliasMap{"gl": "gl.example.com", "gh": "shared.example.com"},
		},
		"user config wins over system config": {
			files: map[string]string{
				"HOME/.ssh/config": heredoc.Doc(`
					Host gl
						HostName user.example.com
				`),
				"/etc/ssh/ssh_config": heredoc.Doc(`
					Host gl
						HostName system.example.com
					Host sys
						HostName only-system.example.com
				`),
			},
			order: []string{"HOME/.ssh/config", "/etc/ssh/ssh_config"},
			want:  SSHAliasMap{"gl": "user.example.com", "sys": "only-system.example.com"},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			p := newStubSSHParser(tt.files)
			for _, file := range tt.order {
				require.NoError(t, p.read(file))
			}
			require.Equal(t, tt.want, p.aliasMap)
		})
	}
}

func Test_sshParser_readMatchAndInclude(t *testing.T) {
	tests := map[string]struct {
		files map[string]string
		want  SSHAliasMap
	}{
		"Match line ends the Host block": {
			files: map[string]string{
				"HOME/.ssh/config": heredoc.Doc(`
					Host gl
					Match user bob
						HostName special.example.com
					Host gl
						HostName normal.example.com
				`),
			},
			want: SSHAliasMap{"gl": "normal.example.com"},
		},
		"Include in a Match block is skipped": {
			files: map[string]string{
				"HOME/.ssh/config": heredoc.Doc(`
					Host gl
					Match user bob
						Include extra.conf
					Host gl
						HostName normal.example.com
				`),
				"HOME/.ssh/extra.conf": heredoc.Doc(`
					Host gl
						HostName special.example.com
				`),
			},
			want: SSHAliasMap{"gl": "normal.example.com"},
		},
		"Include is read where it appears": {
			files: map[string]string{
				"HOME/.ssh/config": heredoc.Doc(`
					Include extra.conf
					Host gl
						HostName main.example.com
				`),
				"HOME/.ssh/extra.conf": heredoc.Doc(`
					Host gl
						HostName included.example.com
				`),
			},
			want: SSHAliasMap{"gl": "included.example.com"},
		},
		"Host line in an included file does not end the including block": {
			files: map[string]string{
				"HOME/.ssh/config": heredoc.Doc(`
					Host gl
						Include extra.conf
						HostName main.example.com
				`),
				"HOME/.ssh/extra.conf": heredoc.Doc(`
					Host *.internal
						User git
				`),
			},
			want: SSHAliasMap{"gl": "main.example.com"},
		},
		"Match line in an included file does not end the including block": {
			files: map[string]string{
				"HOME/.ssh/config": heredoc.Doc(`
					Host gl
						Include extra.conf
						HostName main.example.com
				`),
				"HOME/.ssh/extra.conf": heredoc.Doc(`
					Match user bob
						HostName special.example.com
				`),
			},
			want: SSHAliasMap{"gl": "main.example.com"},
		},
		"Include of two files returns to the including block after each": {
			files: map[string]string{
				"HOME/.ssh/config": heredoc.Doc(`
					Host gl
						Include a.conf b.conf
				`),
				"HOME/.ssh/a.conf": heredoc.Doc(`
					Host *.internal
						User git
				`),
				"HOME/.ssh/b.conf": heredoc.Doc(`
					HostName main.example.com
				`),
			},
			want: SSHAliasMap{"gl": "main.example.com"},
		},
		"Include pattern matching two files returns to the including block after each": {
			files: map[string]string{
				"HOME/.ssh/config": heredoc.Doc(`
					Host gl
						Include conf.d/*
				`),
				"HOME/.ssh/conf.d/a.conf": heredoc.Doc(`
					Host *.internal
						User git
				`),
				"HOME/.ssh/conf.d/b.conf": heredoc.Doc(`
					HostName main.example.com
				`),
			},
			want: SSHAliasMap{"gl": "main.example.com"},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			p := newStubSSHParser(tt.files)
			require.NoError(t, p.read("HOME/.ssh/config"))
			require.Equal(t, tt.want, p.aliasMap)
		})
	}
}

func newStubSSHParser(files map[string]string) *sshParser {
	return &sshParser{
		homeDir: "HOME",
		open: func(s string) (io.Reader, error) {
			if contents, ok := files[filepath.ToSlash(s)]; ok {
				return strings.NewReader(contents), nil
			}
			return nil, fmt.Errorf("no test file stub found: %q", s)
		},
		glob: func(pattern string) ([]string, error) {
			var matches []string
			for name := range files {
				if ok, _ := path.Match(filepath.ToSlash(pattern), name); ok {
					matches = append(matches, name)
				}
			}
			if len(matches) == 0 {
				return nil, fmt.Errorf("no glob stub found: %q", pattern)
			}
			// Map order is random, and filepath.Glob returns lexical order.
			slices.Sort(matches)
			return matches, nil
		},
	}
}

func Test_sshParser_absolutePath(t *testing.T) {
	dir := "HOME"
	p := &sshParser{homeDir: dir}

	tests := map[string]struct {
		parentFile string
		arg        string
		want       string
		wantErr    bool
	}{
		"absolute path": {
			parentFile: "/etc/ssh/ssh_config",
			arg:        "/etc/ssh/config",
			want:       "/etc/ssh/config",
		},
		"system relative path": {
			parentFile: "/etc/ssh/config",
			arg:        "configs/*.conf",
			want:       filepath.Join(systemSSHDir, "configs", "*.conf"),
		},
		"user relative path": {
			parentFile: filepath.Join(dir, ".ssh", "ssh_config"),
			arg:        "configs/*.conf",
			want:       filepath.Join(dir, ".ssh", "configs", "*.conf"),
		},
		"shell-like ~ rerefence": {
			parentFile: filepath.Join(dir, ".ssh", "ssh_config"),
			arg:        "~/.ssh/*.conf",
			want:       filepath.Join(dir, ".ssh", "*.conf"),
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if got := p.absolutePath(tt.parentFile, tt.arg); got != tt.want {
				t.Errorf("absolutePath(): %q, wants %q", got, tt.want)
			}
		})
	}
}

func Test_Translator(t *testing.T) {
	t.Run("alias resolved", func(t *testing.T) {
		tr := SSHAliasMap{"gl": "gitlab.com"}.Translator()
		u, err := url.Parse("ssh://gl/o/r")
		require.NoError(t, err)
		require.Equal(t, "ssh://gitlab.com/o/r", tr(u).String())
	})

	t.Run("non-ssh scheme untouched", func(t *testing.T) {
		tr := SSHAliasMap{"gl": "gitlab.com"}.Translator()
		u, err := url.Parse("https://gl/o/r")
		require.NoError(t, err)
		require.Equal(t, "https://gl/o/r", tr(u).String())
	})
}

func eq(t *testing.T, got any, expected any) {
	t.Helper()
	if !reflect.DeepEqual(got, expected) {
		t.Errorf("expected: %v, got: %v", expected, got)
	}
}
