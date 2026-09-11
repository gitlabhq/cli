package config

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestEnvKeyEquivalence(t *testing.T) {
	tests := []struct {
		autologinEnabled bool
		inCi             bool
		givenKey         string
		expectedKeys     []string
	}{
		{
			autologinEnabled: false,
			inCi:             false,
			givenKey:         "api_host",
			expectedKeys:     []string{"GITLAB_API_HOST"},
		},
		{
			autologinEnabled: true,
			inCi:             false,
			givenKey:         "api_host",
			expectedKeys:     []string{"GITLAB_API_HOST"},
		},
		{
			autologinEnabled: false,
			inCi:             true,
			givenKey:         "api_host",
			expectedKeys:     []string{"GITLAB_API_HOST"},
		},
		{
			autologinEnabled: true,
			inCi:             true,
			givenKey:         "api_host",
			expectedKeys:     []string{"CI_SERVER_FQDN"},
		},
		{
			autologinEnabled: false,
			inCi:             false,
			givenKey:         "api_protocol",
			expectedKeys:     []string{"GLAB_API_PROTOCOL", "API_PROTOCOL"},
		},
		{
			autologinEnabled: true,
			inCi:             false,
			givenKey:         "api_protocol",
			expectedKeys:     []string{"GLAB_API_PROTOCOL", "API_PROTOCOL"},
		},
		{
			autologinEnabled: false,
			inCi:             true,
			givenKey:         "api_protocol",
			expectedKeys:     []string{"GLAB_API_PROTOCOL", "API_PROTOCOL"},
		},
		{
			autologinEnabled: true,
			inCi:             true,
			givenKey:         "api_protocol",
			expectedKeys:     []string{"CI_SERVER_PROTOCOL"},
		},
		{
			autologinEnabled: false,
			inCi:             false,
			givenKey:         "host",
			expectedKeys:     []string{"GITLAB_HOST", "GITLAB_URI", "GL_HOST"},
		},
		{
			autologinEnabled: true,
			inCi:             false,
			givenKey:         "host",
			expectedKeys:     []string{"GITLAB_HOST", "GITLAB_URI", "GL_HOST"},
		},
		{
			autologinEnabled: false,
			inCi:             true,
			givenKey:         "host",
			expectedKeys:     []string{"GITLAB_HOST", "GITLAB_URI", "GL_HOST"},
		},
		{
			autologinEnabled: true,
			inCi:             true,
			givenKey:         "host",
			expectedKeys:     []string{"CI_SERVER_FQDN"},
		},
		{
			autologinEnabled: false,
			inCi:             false,
			givenKey:         "job_token",
			expectedKeys:     []string{"JOB_TOKEN"},
		},
		{
			autologinEnabled: true,
			inCi:             false,
			givenKey:         "job_token",
			expectedKeys:     []string{"JOB_TOKEN"},
		},
		{
			autologinEnabled: false,
			inCi:             true,
			givenKey:         "job_token",
			expectedKeys:     []string{"JOB_TOKEN"},
		},
		{
			autologinEnabled: true,
			inCi:             true,
			givenKey:         "job_token",
			expectedKeys:     []string{"CI_JOB_TOKEN"},
		},
		{
			autologinEnabled: false,
			inCi:             false,
			givenKey:         "ca_cert",
			expectedKeys:     []string{"GLAB_CA_CERT", "CA_CERT"},
		},
		{
			autologinEnabled: true,
			inCi:             false,
			givenKey:         "ca_cert",
			expectedKeys:     []string{"GLAB_CA_CERT", "CA_CERT"},
		},
		{
			autologinEnabled: false,
			inCi:             true,
			givenKey:         "ca_cert",
			expectedKeys:     []string{"GLAB_CA_CERT", "CA_CERT"},
		},
		{
			autologinEnabled: true,
			inCi:             true,
			givenKey:         "ca_cert",
			expectedKeys:     []string{"CI_SERVER_TLS_CA_FILE"},
		},
		{
			autologinEnabled: false,
			inCi:             false,
			givenKey:         "client_cert",
			expectedKeys:     []string{"GLAB_CLIENT_CERT", "CLIENT_CERT"},
		},
		{
			autologinEnabled: true,
			inCi:             false,
			givenKey:         "client_cert",
			expectedKeys:     []string{"GLAB_CLIENT_CERT", "CLIENT_CERT"},
		},
		{
			autologinEnabled: false,
			inCi:             true,
			givenKey:         "client_cert",
			expectedKeys:     []string{"GLAB_CLIENT_CERT", "CLIENT_CERT"},
		},
		{
			autologinEnabled: true,
			inCi:             true,
			givenKey:         "client_cert",
			expectedKeys:     []string{"CI_SERVER_TLS_CERT_FILE"},
		},
		{
			autologinEnabled: false,
			inCi:             false,
			givenKey:         "client_key",
			expectedKeys:     []string{"GLAB_CLIENT_KEY", "CLIENT_KEY"},
		},
		{
			autologinEnabled: true,
			inCi:             false,
			givenKey:         "client_key",
			expectedKeys:     []string{"GLAB_CLIENT_KEY", "CLIENT_KEY"},
		},
		{
			autologinEnabled: false,
			inCi:             true,
			givenKey:         "client_key",
			expectedKeys:     []string{"GLAB_CLIENT_KEY", "CLIENT_KEY"},
		},
		{
			autologinEnabled: true,
			inCi:             true,
			givenKey:         "client_key",
			expectedKeys:     []string{"CI_SERVER_TLS_KEY_FILE"},
		},
		{
			autologinEnabled: false,
			inCi:             false,
			givenKey:         "subfolder",
			expectedKeys:     []string{"GITLAB_SUBFOLDER"},
		},
		{
			autologinEnabled: true,
			inCi:             false,
			givenKey:         "subfolder",
			expectedKeys:     []string{"GITLAB_SUBFOLDER"},
		},
		{
			autologinEnabled: false,
			inCi:             true,
			givenKey:         "subfolder",
			expectedKeys:     []string{"GITLAB_SUBFOLDER"},
		},
		{
			autologinEnabled: true,
			inCi:             true,
			givenKey:         "subfolder",
			expectedKeys:     []string{"GITLAB_SUBFOLDER", "CI_SERVER_URL"},
		},
		{
			autologinEnabled: false,
			inCi:             false,
			givenKey:         "ssh_host",
			expectedKeys:     []string{"GITLAB_SSH_HOST"},
		},
		{
			autologinEnabled: true,
			inCi:             false,
			givenKey:         "ssh_host",
			expectedKeys:     []string{"GITLAB_SSH_HOST"},
		},
		{
			autologinEnabled: false,
			inCi:             true,
			givenKey:         "ssh_host",
			expectedKeys:     []string{"GITLAB_SSH_HOST"},
		},
		{
			autologinEnabled: true,
			inCi:             true,
			givenKey:         "ssh_host",
			expectedKeys:     []string{"GITLAB_SSH_HOST", "CI_SERVER_SHELL_SSH_HOST"},
		},
		{
			autologinEnabled: false,
			inCi:             false,
			givenKey:         "duo_cli_binary_path",
			expectedKeys:     []string{"GLAB_DUO_CLI_BINARY_PATH"},
		},
		{
			autologinEnabled: false,
			inCi:             false,
			givenKey:         "orbit_local_binary_path",
			expectedKeys:     []string{"GLAB_ORBIT_LOCAL_BINARY_PATH"},
		},
		{
			autologinEnabled: false,
			inCi:             false,
			givenKey:         "user",
			expectedKeys:     []string{"GLAB_USER"},
		},
	}

	// clear potentially set keys that we use during tests
	t.Setenv("GLAB_ENABLE_CI_AUTOLOGIN", "")
	t.Setenv("GITLAB_CI", "")

	for _, tt := range tests {
		t.Run(fmt.Sprintf("autologin=%t ci=%t keys=%s -> %v", tt.autologinEnabled, tt.inCi, tt.givenKey, tt.expectedKeys), func(t *testing.T) {
			if tt.autologinEnabled {
				t.Setenv("GLAB_ENABLE_CI_AUTOLOGIN", "true")
			}
			if tt.inCi {
				t.Setenv("GITLAB_CI", "true")
			}

			actualKeys := EnvKeyEquivalence(tt.givenKey)

			assert.ElementsMatch(t, tt.expectedKeys, actualKeys)
		})
	}
}
