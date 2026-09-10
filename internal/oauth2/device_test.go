//go:build !integration

package oauth2

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestStartDeviceFlow_missingSelfHostedClientID(t *testing.T) {
	cfg := stubConfig{
		hosts: map[string]map[string]string{
			"salsa.debian.org": {},
		},
	}

	// Non-interactive: must fail with the config-set pointer rather than
	// attempt to open a prompt. io must still be non-nil per StartDeviceFlow's
	// contract; newNonInteractiveIOStreams (prompt_test.go) reports itself as
	// non-interactive so no prompt is attempted.
	token, err := StartDeviceFlow(t.Context(), cfg, newNonInteractiveIOStreams(), http.DefaultClient, "salsa.debian.org")

	assert.Empty(t, token)
	assert.ErrorContains(t, err, "set 'client_id' first")
}
