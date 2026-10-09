//go:build !integration

package git

import (
	"testing"
)

func TestIsValidURL(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want bool
	}{
		{
			name: "scp-like",
			url:  "git@example.com:owner/repo",
			want: true,
		},
		{
			name: "scp-like with no user",
			url:  "example.com:owner/repo",
			want: false,
		},
		{
			name: "ssh",
			url:  "ssh://git@example.com/owner/repo",
			want: true,
		},
		{
			name: "git",
			url:  "git://example.com/owner/repo",
			want: true,
		},
		{
			name: "https",
			url:  "https://example.com/owner/repo.git",
			want: true,
		},
		{
			// URL schemes are case-insensitive (RFC 3986).
			name: "uppercase HTTPS scheme",
			url:  "HTTPS://example.com/owner/repo.git",
			want: true,
		},
		{
			name: "no protocol",
			url:  "example.com/owner/repo",
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsValidURL(tt.url); got != tt.want {
				t.Errorf("IsValidURL() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseURL(t *testing.T) {
	type url struct {
		Scheme string
		User   string
		Host   string
		Path   string
	}
	tests := []struct {
		name    string
		url     string
		want    url
		wantErr string
	}{
		{
			name: "HTTPS",
			url:  "https://example.com/owner/repo.git",
			want: url{
				Scheme: "https",
				User:   "",
				Host:   "example.com",
				Path:   "/owner/repo.git",
			},
		},
		{
			name: "HTTP",
			url:  "http://example.com/owner/repo.git",
			want: url{
				Scheme: "http",
				User:   "",
				Host:   "example.com",
				Path:   "/owner/repo.git",
			},
		},
		{
			// An uppercase scheme is valid (RFC 3986) and must not be mistaken
			// for scp-style SSH, which would make the host "HTTPS".
			name: "uppercase HTTPS scheme",
			url:  "HTTPS://example.com/owner/repo.git",
			want: url{
				Scheme: "https",
				User:   "",
				Host:   "example.com",
				Path:   "/owner/repo.git",
			},
		},
		{
			name: "git",
			url:  "git://example.com/owner/repo.git",
			want: url{
				Scheme: "git",
				User:   "",
				Host:   "example.com",
				Path:   "/owner/repo.git",
			},
		},
		{
			name: "ssh",
			url:  "ssh://git@example.com/owner/repo.git",
			want: url{
				Scheme: "ssh",
				User:   "git",
				Host:   "example.com",
				Path:   "/owner/repo.git",
			},
		},
		{
			name: "ssh with port",
			url:  "ssh://git@example.com:443/owner/repo.git",
			want: url{
				Scheme: "ssh",
				User:   "git",
				Host:   "example.com",
				Path:   "/owner/repo.git",
			},
		},
		{
			name: "git+ssh",
			url:  "git+ssh://example.com/owner/repo.git",
			want: url{
				Scheme: "ssh",
				User:   "",
				Host:   "example.com",
				Path:   "/owner/repo.git",
			},
		},
		{
			name: "scp-like",
			url:  "git@example.com:owner/repo.git",
			want: url{
				Scheme: "ssh",
				User:   "git",
				Host:   "example.com",
				Path:   "/owner/repo.git",
			},
		},
		{
			name: "scp-like, leading slash",
			url:  "git@example.com:/owner/repo.git",
			want: url{
				Scheme: "ssh",
				User:   "git",
				Host:   "example.com",
				Path:   "/owner/repo.git",
			},
		},
		{
			name: "file protocol",
			url:  "file:///example.com/owner/repo.git",
			want: url{
				Scheme: "file",
				User:   "",
				Host:   "",
				Path:   "/example.com/owner/repo.git",
			},
		},
		{
			name: "file path",
			url:  "/example.com/owner/repo.git",
			want: url{
				Scheme: "",
				User:   "",
				Host:   "",
				Path:   "/example.com/owner/repo.git",
			},
		},
		{
			name: "Windows file path",
			url:  "C:\\example.com\\owner\\repo.git",
			want: url{
				Scheme: "c",
				User:   "",
				Host:   "",
				Path:   "",
			},
		},
		{
			name:    "Invalid URL",
			url:     `git@example.com/%/url`,
			wantErr: `parse "git@example.com/%/url": invalid URL escape "%/u"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u, err := ParseURL(tt.url)
			if tt.wantErr != "" {
				if err.Error() != tt.wantErr {
					t.Errorf("expected error %s, got %s", tt.wantErr, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error %s", err)
				}
				if u.Scheme != tt.want.Scheme {
					t.Errorf("expected scheme %q, got %q", tt.want.Scheme, u.Scheme)
				}
				if u.User.Username() != tt.want.User {
					t.Errorf("expected user %q, got %q", tt.want.User, u.User.Username())
				}
				if u.Host != tt.want.Host {
					t.Errorf("expected host %q, got %q", tt.want.Host, u.Host)
				}
				if u.Path != tt.want.Path {
					t.Errorf("expected path %q, got %q", tt.want.Path, u.Path)
				}
			}
		})
	}
}

func TestValidateRemoteURL(t *testing.T) {
	tests := []struct {
		url     string
		wantErr bool
	}{
		{url: "", wantErr: true},
		{url: "ext::touch x", wantErr: true},
		{url: "ext::sh -c touch% /tmp/x", wantErr: true},
		{url: "fd::3", wantErr: true},
		{url: "--upload-pack=touch x", wantErr: true},
		{url: "-oProxyCommand=x", wantErr: true},
		{url: "file:///tmp/x", wantErr: true},
		{url: "/tmp/x", wantErr: true},
		{url: "-host:g/p.git", wantErr: true},
		{url: "git@-host:g/p.git", wantErr: true},
		{url: "http::http://x/p.git", wantErr: true},
		{url: "git@host:g/p.git\n--upload-pack=x", wantErr: true},
		{url: "https://gitlab.com/g/p.git ", wantErr: true},
		{url: "ssh://git@host/g\n", wantErr: true},
		{url: "gitlab_web:g/p.git"},
		{url: "git@my_host.local:g/p.git"},
		{url: "git@gitlab.com:g/p.git"},
		{url: "gitlab.example.com:g/p.git"},
		{url: "gitlab@host.example.com:g/p.git"},
		{url: "[git@2001:db8::1]:g/p.git"},
		{url: "https://gitlab.com/g/p.git"},
		{url: "HTTPS://gitlab.com/g/p.git"},
		{url: "ssh://git@host:2222/g/p.git"},
		{url: "http://localhost:3000/g/p.git"},
	}
	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			if err := ValidateRemoteURL(tt.url); (err != nil) != tt.wantErr {
				t.Errorf("ValidateRemoteURL(%q) error = %v, wantErr %v", tt.url, err, tt.wantErr)
			}
		})
	}
}
