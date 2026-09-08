//go:build !integration

package agentutils

import (
	"testing"

	"github.com/stretchr/testify/require"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
)

func TestAgent_GetKasK8SProxyURL(t *testing.T) {
	// GIVEN
	testcases := []struct {
		name                   string
		externalURL            string
		externalK8SProxyURL    string
		expectedKasK8SProxyURL string
	}{
		{
			name:                   "GitLab >= 17.6, GitLab.com",
			externalURL:            "wss://kas.gitlab.com",
			externalK8SProxyURL:    "https://kas.gitlab.com/k8s-proxy",
			expectedKasK8SProxyURL: "https://kas.gitlab.com/k8s-proxy",
		},
		{
			name:                   "GitLab < 17.6, Without Subdomain",
			externalURL:            "wss://example.com",
			expectedKasK8SProxyURL: "https://example.com/k8s-proxy",
		},
		{
			name:                   "GitLab < 17.6, On subpath",
			externalURL:            "wss://example.com/-/kubernetes-agent/",
			expectedKasK8SProxyURL: "https://example.com/-/kubernetes-agent/k8s-proxy",
		},
		{
			name:                   "GitLab < 17.6, On port",
			externalURL:            "wss://example.com:4242",
			expectedKasK8SProxyURL: "https://example.com:4242/k8s-proxy",
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			// WHEN
			m := &gitlab.Metadata{
				KAS: struct {
					Enabled             bool   `json:"enabled"`
					ExternalURL         string `json:"externalUrl"`
					ExternalK8SProxyURL string `json:"externalK8sProxyUrl"`
					Version             string `json:"version"`
				}{
					Enabled:             true,
					ExternalURL:         tc.externalURL,
					ExternalK8SProxyURL: tc.expectedKasK8SProxyURL,
				},
			}

			actualKasProxyUrl, err := GetKasK8SProxyURL(m)
			require.NoError(t, err)

			// THEN
			require.Equal(t, tc.expectedKasK8SProxyURL, actualKasProxyUrl)
		})
	}
}
