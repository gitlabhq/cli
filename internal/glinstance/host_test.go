//go:build !integration

package glinstance

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsSelfHosted(t *testing.T) {
	type args struct {
		h string
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{
			name: "self_hosted subdomain",
			args: args{h: "gitlab.example.com"},
			want: true,
		},
		{
			name: "gitlab.com",
			args: args{h: "gitlab.com"},
			want: false,
		},
		{
			name: "is a gitlab.com subdomain",
			args: args{h: "example.gitlab.com"},
			want: true,
		},
		{
			name: "is a gitlab.com staging",
			args: args{h: "staging.gitlab.com"},
			want: true,
		},
		{
			name: "self hosted",
			args: args{h: "example.com"},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsSelfHosted(tt.args.h); got != tt.want {
				t.Errorf("IsSelfHosted() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNormalizeHostname(t *testing.T) {
	tests := []struct {
		host string
		want string
	}{
		{
			host: "GitLab.com",
			want: "gitlab.com",
		},
		{
			host: "subdomain.gitlab.com",
			want: "subdomain.gitlab.com",
		},
		{
			host: "ssh.gitlab.com",
			want: "ssh.gitlab.com",
		},
		{
			host: "upload.gitlab.com",
			want: "upload.gitlab.com",
		},
		{
			host: "staging.gitlab.com",
			want: "staging.gitlab.com",
		},
		{
			host: "EXAMPLE.COM",
			want: "example.com",
		},
		{
			host: "gitlab.my.org",
			want: "gitlab.my.org",
		},
	}
	for _, tt := range tests {
		t.Run(tt.host, func(t *testing.T) {
			if got := NormalizeHostname(tt.host); got != tt.want {
				t.Errorf("NormalizeHostname() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAPIEndpoint(t *testing.T) {
	tests := []struct {
		host      string
		apiHost   string
		subfolder string
		protocol  string
		want      string
	}{
		{
			host:     "gitlab.com",
			protocol: "https",
			want:     "https://gitlab.com/api/v4/",
		},
		{
			host:     "fake-host.com",
			protocol: "https",
			apiHost:  "api.fake-host.com",
			want:     "https://api.fake-host.com/api/v4/",
		},
		{
			host:     "staging.gitlab.com",
			protocol: "https",
			want:     "https://staging.gitlab.com/api/v4/",
		},
		{
			host:     "ghe.io",
			protocol: "https",
			want:     "https://ghe.io/api/v4/",
		},
		{
			host: "ghe.io",
			want: "https://ghe.io/api/v4/",
		},
		{
			host:     "salsa.debian.com",
			protocol: "http",
			want:     "http://salsa.debian.com/api/v4/",
		},
		{
			host:     "salsa.debian.com",
			protocol: "http",
			apiHost:  "api.salsa.debian.com",
			want:     "http://api.salsa.debian.com/api/v4/",
		},
		{
			host:     "myserver.net",
			protocol: "http",
			apiHost:  "myserver.net/gitlab",
			want:     "http://myserver.net/gitlab/api/v4/",
		},
	}
	for i, tt := range tests {
		t.Run(fmt.Sprintf("%d - %s", i, tt.host), func(t *testing.T) {
			if got := APIEndpoint(tt.host, tt.protocol, tt.apiHost, tt.subfolder); got != tt.want {
				t.Errorf("APIEndpoint() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDefault(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{
			name: "default_hostname",
			want: "gitlab.com",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DefaultHostname; got != tt.want {
				t.Errorf("Default() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestStripHostProtocol(t *testing.T) {
	tests := []struct {
		name         string
		hostname     string
		wantHostname string
		wantProtocol string
	}{
		{
			name:         "url with https protocol",
			hostname:     "https://gitlab.com",
			wantHostname: "gitlab.com",
			wantProtocol: "https",
		},
		{
			name:         "https url with ending slash",
			hostname:     "https://gitlab.com/",
			wantHostname: "gitlab.com",
			wantProtocol: "https",
		},
		{
			name:         "url with http protocol",
			hostname:     "http://gitlab.com/",
			wantHostname: "gitlab.com",
			wantProtocol: "http",
		},
		{
			name:         "http url with ending slash",
			hostname:     "http://gitlab.com/",
			wantHostname: "gitlab.com",
			wantProtocol: "http",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotHostname, gotProtocol := StripHostProtocol(tt.hostname)
			if gotHostname != tt.wantHostname {
				t.Errorf("StripHostProtocol() gotHostname = %v, want %v", gotHostname, tt.wantHostname)
			}
			if gotProtocol != tt.wantProtocol {
				t.Errorf("StripHostProtocol() gotProtocol = %v, want %v", gotProtocol, tt.wantProtocol)
			}
		})
	}
}

func TestHostnameValidator(t *testing.T) {
	testCases := []struct {
		name     string
		hostname any
		expected string
	}{
		{
			name:     "valid",
			hostname: "localhost",
		},
		{
			name:     "invalid/not-string",
			hostname: 1,
			expected: "hostname is not a string",
		},
		{
			name:     "invalid/empty-string",
			hostname: "",
			expected: "a value is required",
		},
		{
			name:     "invalid/has-forward-slash",
			hostname: "local/host",
			expected: "invalid hostname",
		},
		{
			name:     "invalid/has-colon",
			hostname: "local:host",
			expected: "invalid hostname",
		},
	}
	for _, tC := range testCases {
		t.Run(tC.name, func(t *testing.T) {
			err := HostnameValidator(tC.hostname)
			if tC.expected == "" {
				assert.NoError(t, err)
			} else {
				assert.Error(t, err, tC.expected)
			}
		})
	}
}

func Test_GraphQLEndpoint(t *testing.T) {
	testCases := []struct {
		name      string
		protocol  string
		hostname  string
		apiHost   string
		subfolder string
		output    string
	}{
		{
			name:     "OfficialInstance/https",
			protocol: "https",
			hostname: "gitlab.com",
			output:   "https://gitlab.com/api/graphql/",
		},
		{
			name:     "OfficialInstance/any-protocol-is-https",
			protocol: "NoExistProtocol",
			hostname: "gitlab.com",
			output:   "https://gitlab.com/api/graphql/",
		},
		{
			name:     "OfficialInstance/no-protocol-default-to-https",
			protocol: "",
			hostname: "gitlab.com",
			output:   "https://gitlab.com/api/graphql/",
		},
		{
			name:     "SelfHosted/https",
			protocol: "https",
			hostname: "gitlab.alpinelinux.org",
			output:   "https://gitlab.alpinelinux.org/api/graphql/",
		},
		{
			name:     "SelfHost/http",
			protocol: "http",
			hostname: "gitlab.alpinelinux.org",
			output:   "http://gitlab.alpinelinux.org/api/graphql/",
		},
		{
			name:     "SelfHosted/no-protocol-default-to-https",
			protocol: "",
			hostname: "gitlab.alpinelinux.org",
			output:   "https://gitlab.alpinelinux.org/api/graphql/",
		},
	}
	for _, tC := range testCases {
		t.Run(tC.name, func(t *testing.T) {
			got := GraphQLEndpoint(tC.hostname, tC.protocol, tC.apiHost, tC.subfolder)
			assert.Equal(t, tC.output, got)
		})
	}
}

func TestAuthEndpoint(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "https://gitlab.example.com", AuthEndpoint("gitlab.example.com", "", ""))
	assert.Equal(t, "http://gitlab.example.com/gitlab", AuthEndpoint("gitlab.example.com", "http", "/gitlab/"))
	assert.Equal(t, "https://gitlab.example.com/apps/gitlab", AuthEndpoint("gitlab.example.com", "https", "apps/gitlab"))
}
