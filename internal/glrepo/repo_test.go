//go:build !integration

package glrepo

import (
	"errors"
	"fmt"
	"net/url"
	"testing"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/git"
	"gitlab.com/gitlab-org/cli/internal/glinstance"
)

func Test_RemoteURL(t *testing.T) {
	type args struct {
		project  *gitlab.Project
		protocol string
	}

	for _, tt := range []struct {
		name    string
		args    args
		want    string
		wantErr bool
	}{
		{
			name: "is_https",
			args: args{
				project: &gitlab.Project{
					SSHURLToRepo:  "git@gitlab.com:profclems/glab.git",
					HTTPURLToRepo: "https://gitlab.com/profclems/glab.git",
				},
				protocol: "https",
			},
			want: "https://gitlab.com/profclems/glab.git",
		},
		{
			name: "host_is_http",
			args: args{
				project: &gitlab.Project{
					SSHURLToRepo:  "git@gitlabedd.com:profclems/glab.git",
					HTTPURLToRepo: "http://gitlabedd.com/profclems/glab.git",
				},
				protocol: "http",
			},
			want: "http://gitlabedd.com/profclems/glab.git",
		},
		{
			name: "is_ssh",
			args: args{
				project: &gitlab.Project{
					SSHURLToRepo: "git@gitlab.com:profclems/glab.git",
				},
				protocol: "ssh",
			},
			want: "git@gitlab.com:profclems/glab.git",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := RemoteURL(tt.args.project, tt.args.protocol)
			if got != tt.want {
				t.Errorf("RemoteURL() got = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestWikiRemoteURL(t *testing.T) {
	t.Parallel()

	project := &gitlab.Project{
		SSHURLToRepo:  "git@gitlab.com:profclems/docs.wiki.git",
		HTTPURLToRepo: "https://gitlab.com/profclems/docs.wiki.git",
	}

	assert.Equal(t, "git@gitlab.com:profclems/docs.wiki.wiki.git", WikiRemoteURL(project, "ssh"))
	assert.Equal(t, "https://gitlab.com/profclems/docs.wiki.wiki.git", WikiRemoteURL(project, "https"))
}

func Test_repoFromURL(t *testing.T) {
	cfg := config.NewFromString(`---
hosts:
  my.host.com:
    token: OTOKEN
    api_host: my.host.com/git
  gdk.test:
    token: OTOKEN
    api_host: gdk.test:3443
  git.host.com:
    token: OTOKEN
    api_host: git-api.host.com
`)

	tests := []struct {
		name   string
		input  string
		result string
		host   string
		err    error
	}{
		{
			name:   "gitlab.com URL",
			input:  "https://gitlab.com/monalisa/octo-cat.git",
			result: "monalisa/octo-cat",
			host:   "gitlab.com",
			err:    nil,
		},
		{
			name:   "gitlab.com URL with trailing slash",
			input:  "https://gitlab.com/monalisa/octo-cat/",
			result: "monalisa/octo-cat",
			host:   "gitlab.com",
			err:    nil,
		},
		{
			name:   "gitlab.com dot-git URL with trailing slash",
			input:  "https://gitlab.com/monalisa/octo-cat.git/",
			result: "monalisa/octo-cat",
			host:   "gitlab.com",
			err:    nil,
		},
		{
			name:   "www.gitlab.com URL",
			input:  "http://www.GITLAB.com/monalisa/octo-cat.git",
			result: "monalisa/octo-cat",
			host:   "gitlab.com",
			err:    nil,
		},
		{
			name:   "group namespacing",
			input:  "https://gitlab.com/monalisa/octo-cat/minor",
			result: "monalisa/octo-cat/minor",
			host:   "gitlab.com",
			err:    nil,
		},
		{
			name:   "non-GitLab hostname",
			input:  "https://example.com/one/two",
			result: "one/two",
			host:   "example.com",
			err:    nil,
		},
		{
			name:   "non-GitLab hostname with api_host",
			input:  "https://git.host.com/one/two",
			result: "one/two",
			host:   "git.host.com",
			err:    nil,
		},
		{
			name:   "GDK with api_host",
			input:  "https://gdk.test:3443/one/two",
			result: "one/two",
			host:   "gdk.test:3443",
			err:    nil,
		},
		{
			name:   "non-standard port without api_host",
			input:  "https://example.com:8443/owner/repo",
			result: "owner/repo",
			host:   "example.com:8443",
			err:    nil,
		},
		{
			name:   "non-GitLab hostname with subdirectory and api_host",
			input:  "https://my.host.com/git/one/two",
			result: "one/two",
			host:   "my.host.com",
			err:    nil,
		},
		{
			name:   "filesystem path",
			input:  "/path/to/file",
			result: "",
			host:   "",
			err:    errors.New("no hostname detected"),
		},
		{
			name:   "filesystem path with scheme",
			input:  "file:///path/to/file",
			result: "",
			host:   "",
			err:    errors.New("no hostname detected"),
		},
		{
			name:   "gitlab.com SSH URL",
			input:  "ssh://gitlab.com/monalisa/octo-cat.git",
			result: "monalisa/octo-cat",
			host:   "gitlab.com",
			err:    nil,
		},
		{
			name:   "gitlab.com HTTPS+SSH URL",
			input:  "https+ssh://gitlab.com/monalisa/octo-cat.git",
			result: "monalisa/octo-cat",
			host:   "gitlab.com",
			err:    nil,
		},
		{
			name:   "gitlab.com git URL",
			input:  "git://gitlab.com/monalisa/octo-cat.git",
			result: "monalisa/octo-cat",
			host:   "gitlab.com",
			err:    nil,
		},
		{
			name:   "gitlab.com deep nested",
			input:  "git://gitlab.com/owner/subgroup/subgroup1/subgroup2/subgroup3/namespace/repo.git",
			result: "owner/subgroup/subgroup1/subgroup2/subgroup3/namespace/repo",
			host:   "gitlab.com",
			err:    nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u, err := url.Parse(tt.input)
			if err != nil {
				t.Fatalf("got error %q", err)
			}

			repo, err := FromURL(u, glinstance.DefaultHostname, cfg)
			if err != nil {
				if tt.err == nil {
					t.Fatalf("got error %q", err)
				} else if tt.err.Error() == err.Error() {
					return
				}
				t.Fatalf("got error %q", err)
			}

			got := repo.FullName()
			if tt.result != got {
				t.Errorf("expected %q, got %q", tt.result, got)
			}
			if tt.host != repo.RepoHost() {
				t.Errorf("expected %q, got %q", tt.host, repo.RepoHost())
			}
		})
	}
}

func TestFromFullName(t *testing.T) {
	cfg := config.NewFromString(`---
hosts:
  gitlab.com:
    token: xxxxxxxxxxxxxxxxxxxx
    git_protocol: ssh
    api_protocol: https
  example.org:
    token: xxxxxxxxxxxxxxxxxxxxx
`)
	tests := []struct {
		name          string
		input         string
		wantOwner     string
		wantName      string
		wantHost      string
		wantFullname  string
		wantGroup     string
		wantNamespace string
		wantErr       error
	}{
		{
			name:          "OWNER/REPO combo",
			input:         "OWNER/REPO",
			wantHost:      "gitlab.com",
			wantOwner:     "OWNER",
			wantName:      "REPO",
			wantFullname:  "OWNER/REPO",
			wantNamespace: "OWNER",
			wantErr:       nil,
		},
		{
			name:    "too few elements",
			input:   "OWNER",
			wantErr: errors.New(`expected the "[HOST/]OWNER/[NAMESPACE/]REPO" format, got "OWNER"`),
		},
		{
			name:          "group namespace",
			input:         "example.org/b/c/d",
			wantHost:      "example.org",
			wantOwner:     "b/c",
			wantName:      "d",
			wantFullname:  "b/c/d",
			wantNamespace: "c",
			wantGroup:     "b",
			wantErr:       nil,
		},
		{
			name:          "with group namespace",
			input:         "gitlab.com/owner/namespace/repo",
			wantHost:      "gitlab.com",
			wantOwner:     "owner/namespace",
			wantName:      "repo",
			wantFullname:  "owner/namespace/repo",
			wantNamespace: "namespace",
			wantGroup:     "owner",
			wantErr:       nil,
		},
		{
			name:    "blank value",
			input:   "a/",
			wantErr: errors.New(`expected the "[HOST/]OWNER/[NAMESPACE/]REPO" format, got "a/"`),
		},
		{
			name:    "blank value inner",
			input:   "a//c",
			wantErr: errors.New(`expected the "[HOST/]OWNER/[NAMESPACE/]REPO" format, got "a//c"`),
		},
		{
			name:          "with hostname",
			input:         "example.org/OWNER/REPO",
			wantHost:      "example.org",
			wantOwner:     "OWNER",
			wantName:      "REPO",
			wantFullname:  "OWNER/REPO",
			wantNamespace: "OWNER",
			wantGroup:     "",
			wantErr:       nil,
		},
		{
			name:          "group name has dot",
			input:         "my.group/sub.group/repo",
			wantHost:      "gitlab.com",
			wantOwner:     "my.group/sub.group",
			wantName:      "repo",
			wantFullname:  "my.group/sub.group/repo",
			wantNamespace: "sub.group",
			wantGroup:     "my.group",
			wantErr:       nil,
		},
		{
			name:          "full URL",
			input:         "https://example.org/OWNER/REPO.git",
			wantHost:      "example.org",
			wantOwner:     "OWNER",
			wantName:      "REPO",
			wantFullname:  "OWNER/REPO",
			wantNamespace: "OWNER",
			wantGroup:     "",
			wantErr:       nil,
		},
		{
			name:          "SSH URL",
			input:         "git@example.org:OWNER/REPO.git",
			wantHost:      "example.org",
			wantOwner:     "OWNER",
			wantName:      "REPO",
			wantFullname:  "OWNER/REPO",
			wantNamespace: "OWNER",
			wantGroup:     "",
			wantErr:       nil,
		},
		{
			name:          "Deep Nested Groups",
			input:         "git@example.org:GROUP/SUBGROUP1/SUBGROUP2/SUBGROUP3/SUBGROUP4/REPO.git",
			wantHost:      "example.org",
			wantOwner:     "GROUP/SUBGROUP1/SUBGROUP2/SUBGROUP3/SUBGROUP4",
			wantName:      "REPO",
			wantFullname:  "GROUP/SUBGROUP1/SUBGROUP2/SUBGROUP3/SUBGROUP4/REPO",
			wantNamespace: "SUBGROUP1/SUBGROUP2/SUBGROUP3/SUBGROUP4",
			wantGroup:     "GROUP",
			wantErr:       nil,
		},
		{
			name:    "invalid URL",
			input:   "git@example.com/%/url",
			wantErr: errors.New(`parse "git@example.com/%/url": invalid URL escape "%/u"`),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, err := FromFullName(tt.input, glinstance.DefaultHostname, cfg)
			if tt.wantErr != nil {
				if err == nil {
					t.Fatalf("no error in result, expected %v", tt.wantErr)
				} else if err.Error() != tt.wantErr.Error() {
					t.Fatalf("expected error %q, got %q", tt.wantErr.Error(), err.Error())
				}
				return
			}
			if err != nil {
				t.Fatalf("got error %v", err)
			}
			if r.RepoHost() != tt.wantHost {
				t.Errorf("expected host %q, got %q", tt.wantHost, r.RepoHost())
			}
			if r.RepoOwner() != tt.wantOwner {
				t.Errorf("expected owner %q, got %q", tt.wantOwner, r.RepoOwner())
			}
			if r.RepoName() != tt.wantName {
				t.Errorf("expected name %q, got %q", tt.wantName, r.RepoName())
			}
			if r.RepoGroup() != tt.wantGroup {
				t.Errorf("expected group %q, got %q", tt.wantGroup, r.RepoGroup())
			}
			if r.FullName() != tt.wantFullname {
				t.Errorf("expected fullname %q, got %q", tt.wantFullname, r.FullName())
			}
			if r.RepoNamespace() != tt.wantNamespace {
				t.Errorf("expected namespace %q, got %q", tt.wantNamespace, r.RepoNamespace())
			}
		})
	}
}

func TestFullNameFromURL(t *testing.T) {
	tests := []struct {
		remoteURL string
		want      string
		wantErr   error
	}{
		{
			remoteURL: "gitlab.com/profclems/glab.git",
			wantErr:   errors.New("cannot parse remote: gitlab.com/profclems/glab.git"),
		},
		{
			remoteURL: "ssh://https://gitlab.com/owner/repo",
			wantErr:   errors.New(`cannot parse remote: ssh://https://gitlab.com/owner/repo`),
		},
		{
			remoteURL: "https://gitlab.com/profclems/glab.git",
			want:      "profclems/glab",
			wantErr:   nil,
		},
		{
			remoteURL: "https://gitlab.com/profclems/glab",
			want:      "profclems/glab",
			wantErr:   nil,
		},
		{
			remoteURL: "https://gitlab.com/profclems/glab/",
			want:      "profclems/glab",
			wantErr:   nil,
		},
		{
			remoteURL: "https://gitlab.com/profclems/glab.git/",
			want:      "profclems/glab",
			wantErr:   nil,
		},
		{
			remoteURL: "https://gitlab.com/owner/namespace/repo.git",
			want:      "owner/namespace/repo",
			wantErr:   nil,
		},
		{
			remoteURL: "git@gitlab.com:owner/namespace/repo.git",
			want:      "owner/namespace/repo",
			wantErr:   nil,
		},
		{
			remoteURL: "git@gitlab.com:owner/subgroup/subgroup1/subgroup2/subgroup3/namespace/repo.git",
			want:      "owner/subgroup/subgroup1/subgroup2/subgroup3/namespace/repo",
			wantErr:   nil,
		},
	}
	for i, tt := range tests {
		t.Run(fmt.Sprintf("%d - %s", i, tt.remoteURL), func(t *testing.T) {
			got, err := FullNameFromURL(tt.remoteURL)
			if tt.wantErr != nil {
				if err == nil {
					t.Fatalf("no error in result, expected %v", tt.wantErr)
				} else if err.Error() != tt.wantErr.Error() {
					t.Fatalf("expected error %q, got %q", tt.wantErr.Error(), err.Error())
				}
				return
			}
			if got != tt.want {
				t.Errorf("FullNameFromURL() got = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_NewWitHost(t *testing.T) {
	testCases := []struct {
		name          string
		input         []string
		wantHostname  string
		wantOwner     string
		wantGroup     string
		wantNamespace string
		wantName      string
		wantFullname  string
	}{
		{
			name:          "Simple",
			input:         []string{"profclems", "glab", "gitlab.com"},
			wantHostname:  "gitlab.com",
			wantNamespace: "profclems",
			wantOwner:     "profclems",
			wantName:      "glab",
			wantFullname:  "profclems/glab",
		},
		{
			name:          "group",
			input:         []string{"company/profclems", "glab", "gitlab.com"},
			wantHostname:  "gitlab.com",
			wantNamespace: "profclems",
			wantOwner:     "company/profclems",
			wantGroup:     "company",
			wantName:      "glab",
			wantFullname:  "company/profclems/glab",
		},
	}
	for _, tC := range testCases {
		t.Run(tC.name, func(t *testing.T) {
			got := NewWithHost(tC.input[0], tC.input[1], tC.input[2])
			if tC.wantHostname != "" {
				assert.Equal(t, tC.wantHostname, got.RepoHost())
			}
			if tC.wantOwner != "" {
				assert.Equal(t, tC.wantOwner, got.RepoOwner())
			}
			if tC.wantGroup != "" {
				assert.Equal(t, tC.wantGroup, got.RepoGroup())
			}
			if tC.wantNamespace != "" {
				assert.Equal(t, tC.wantNamespace, got.RepoNamespace())
			}
			if tC.wantName != "" {
				assert.Equal(t, tC.wantName, got.RepoName())
			}
			if tC.wantFullname != "" {
				assert.Equal(t, tC.wantFullname, got.FullName())
			}
		})
	}
}

// FromURL resolves a remote URL against a host entry in the config. The entry is
// keyed exactly as `auth login` writes it, which is `u.Host` — the hostname plus
// the port when one is given. This matrix covers the combinations a self-managed
// instance can be reached on: default or custom port, root or subfolder install,
// and the `subfolder` key or the older `api_host`-with-path spelling.
//
// RepoHost is asserted alongside the project path because it is the key every
// later config lookup uses (protocol, token, API host). A resolution that gets
// the path right but the host wrong still sends unauthenticated requests to the
// wrong port.
func Test_FromURL_PortAndSubfolderMatrix(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name         string
		cfg          string
		url          string
		wantFullName string
		wantHost     string
	}{
		{
			name: "no port, no subfolder",
			cfg: heredoc.Doc(`
				hosts:
				  example.com:
				    token: TOKEN
			`),
			url:          "https://example.com/group/repo.git",
			wantFullName: "group/repo",
			wantHost:     "example.com",
		},
		{
			name: "custom port, no subfolder",
			cfg: heredoc.Doc(`
				hosts:
				  example.com:8080:
				    token: TOKEN
			`),
			url:          "http://example.com:8080/group/repo.git",
			wantFullName: "group/repo",
			wantHost:     "example.com:8080",
		},
		{
			name: "no port, subfolder key",
			cfg: heredoc.Doc(`
				hosts:
				  example.com:
				    token: TOKEN
				    subfolder: gitlab
			`),
			url:          "https://example.com/gitlab/group/repo.git",
			wantFullName: "group/repo",
			wantHost:     "example.com",
		},
		{
			name: "custom port, subfolder key",
			cfg: heredoc.Doc(`
				hosts:
				  example.com:8080:
				    token: TOKEN
				    subfolder: gitlab
			`),
			url:          "http://example.com:8080/gitlab/group/repo.git",
			wantFullName: "group/repo",
			wantHost:     "example.com:8080",
		},
		{
			name: "no port, nested subfolder key",
			cfg: heredoc.Doc(`
				hosts:
				  example.com:
				    token: TOKEN
				    subfolder: apps/gitlab
			`),
			url:          "https://example.com/apps/gitlab/group/repo.git",
			wantFullName: "group/repo",
			wantHost:     "example.com",
		},
		{
			name: "custom port, nested subfolder key",
			cfg: heredoc.Doc(`
				hosts:
				  example.com:8080:
				    token: TOKEN
				    subfolder: apps/gitlab
			`),
			url:          "http://example.com:8080/apps/gitlab/group/repo.git",
			wantFullName: "group/repo",
			wantHost:     "example.com:8080",
		},
		{
			name: "no port, api_host backward compatibility",
			cfg: heredoc.Doc(`
				hosts:
				  example.com:
				    token: TOKEN
				    api_host: example.com/gitlab
			`),
			url:          "https://example.com/gitlab/group/repo.git",
			wantFullName: "group/repo",
			wantHost:     "example.com",
		},
		{
			name: "custom port, api_host backward compatibility",
			cfg: heredoc.Doc(`
				hosts:
				  example.com:8080:
				    token: TOKEN
				    api_host: example.com:8080/gitlab
			`),
			url:          "http://example.com:8080/gitlab/group/repo.git",
			wantFullName: "group/repo",
			wantHost:     "example.com:8080",
		},
		{
			name: "custom port, subfolder key wins over api_host",
			cfg: heredoc.Doc(`
				hosts:
				  example.com:8080:
				    token: TOKEN
				    subfolder: gitlab
				    api_host: example.com:8080/wrong
			`),
			url:          "http://example.com:8080/gitlab/group/repo.git",
			wantFullName: "group/repo",
			wantHost:     "example.com:8080",
		},
		{
			name: "custom port, subgroup under a subfolder",
			cfg: heredoc.Doc(`
				hosts:
				  example.com:8080:
				    token: TOKEN
				    subfolder: gitlab
			`),
			url:          "http://example.com:8080/gitlab/group/subgroup/repo.git",
			wantFullName: "group/subgroup/repo",
			wantHost:     "example.com:8080",
		},
		{
			// The subfolder is only a prefix to strip. A group that happens to
			// share its name must survive when it appears deeper in the path.
			name: "custom port, group name repeats the subfolder",
			cfg: heredoc.Doc(`
				hosts:
				  example.com:8080:
				    token: TOKEN
				    subfolder: gitlab
			`),
			url:          "http://example.com:8080/gitlab/gitlab/repo.git",
			wantFullName: "gitlab/repo",
			wantHost:     "example.com:8080",
		},
		{
			// A host entry for the portless name must not answer for a ported
			// remote: they are different instances as far as the config is
			// concerned, and the API client treats them that way too.
			name: "port on the remote, config keyed without it, is not applied",
			cfg: heredoc.Doc(`
				hosts:
				  example.com:
				    token: TOKEN
				    subfolder: gitlab
			`),
			url:          "http://example.com:8080/gitlab/group/repo.git",
			wantFullName: "gitlab/group/repo",
			wantHost:     "example.com:8080",
		},
		{
			name: "config for an unrelated host does not leak",
			cfg: heredoc.Doc(`
				hosts:
				  other.example.com:8080:
				    token: TOKEN
				    subfolder: gitlab
			`),
			url:          "http://example.com:8080/gitlab/group/repo.git",
			wantFullName: "gitlab/group/repo",
			wantHost:     "example.com:8080",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			u, err := url.Parse(tc.url)
			require.NoError(t, err)

			repo, err := FromURL(u, glinstance.DefaultHostname, config.NewFromString(tc.cfg))
			require.NoError(t, err)
			assert.Equal(t, tc.wantFullName, repo.FullName())
			assert.Equal(t, tc.wantHost, repo.RepoHost())
		})
	}
}

// SSH remotes reach FromURL after git.ParseURL has normalized SCP-style syntax.
// git.ParseURL deliberately drops the SSH port (see its TestParseURL "ssh with
// port" case), because an SSH port is not the port the API is served on: the
// host entry a remote resolves against is keyed on the HTTP host. So an SSH
// remote always looks up the portless entry, whatever port Git connects on.
//
// FromURL is scheme-agnostic, so the subfolder segment in these paths is a
// probe: stripping it proves which host entry answered the lookup. Real SSH
// remotes do not carry one — see Test_remoteResolver_SSHRemoteOnPortedSubfolderHost.
func Test_FromURL_SSHSubfolderMatrix(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name         string
		cfg          string
		remote       string
		wantFullName string
		wantHost     string
	}{
		{
			name: "scp-style, subfolder",
			cfg: heredoc.Doc(`
				hosts:
				  example.com:
				    token: TOKEN
				    subfolder: gitlab
			`),
			remote:       "git@example.com:gitlab/group/repo.git",
			wantFullName: "group/repo",
			wantHost:     "example.com",
		},
		{
			name: "scp-style, nested subfolder and subgroup",
			cfg: heredoc.Doc(`
				hosts:
				  example.com:
				    token: TOKEN
				    subfolder: apps/gitlab
			`),
			remote:       "git@example.com:apps/gitlab/group/subgroup/repo.git",
			wantFullName: "group/subgroup/repo",
			wantHost:     "example.com",
		},
		{
			// The SSH port is dropped, so the portless host entry applies and
			// the subfolder is still stripped.
			name: "ssh:// with port, subfolder",
			cfg: heredoc.Doc(`
				hosts:
				  example.com:
				    token: TOKEN
				    subfolder: gitlab
			`),
			remote:       "ssh://git@example.com:2222/gitlab/group/repo.git",
			wantFullName: "group/repo",
			wantHost:     "example.com",
		},
		{
			name: "ssh:// with port, no subfolder",
			cfg: heredoc.Doc(`
				hosts:
				  example.com:
				    token: TOKEN
			`),
			remote:       "ssh://git@example.com:2222/group/repo.git",
			wantFullName: "group/repo",
			wantHost:     "example.com",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			u, err := git.ParseURL(tc.remote)
			require.NoError(t, err)

			repo, err := FromURL(u, glinstance.DefaultHostname, config.NewFromString(tc.cfg))
			require.NoError(t, err)
			assert.Equal(t, tc.wantFullName, repo.FullName())
			assert.Equal(t, tc.wantHost, repo.RepoHost())
		})
	}
}

func Test_glRepo_Project_cachesTheFetch(t *testing.T) {
	// A value receiver on Project() made the cache write a no-op, so every
	// caller re-fetched. Loops such as `glab ci cancel` refetched per item.
	originalGetProject := api.GetProject
	t.Cleanup(func() { api.GetProject = originalGetProject })

	calls := 0
	api.GetProject = func(_ *gitlab.Client, projectID any) (*gitlab.Project, error) {
		calls++
		return &gitlab.Project{ID: 42, PathWithNamespace: fmt.Sprint(projectID)}, nil
	}

	repo := NewWithHost("OWNER", "REPO", "gitlab.com")

	first, err := repo.Project(nil)
	require.NoError(t, err)
	_, err = repo.Project(nil)
	require.NoError(t, err)

	assert.Equal(t, 1, calls)
	assert.Equal(t, "OWNER/REPO", first.PathWithNamespace)
}
