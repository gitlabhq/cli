//go:build !integration

package config

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"
)

// The sentinel name must be unique per probe so concurrent probes never contend
// for one entry. go-keyring's mock replaces unconditionally, so the failure a
// shared name causes on some backends cannot be reproduced in-process;
// uniqueness is the invariant that prevents it, so that is what these tests pin.
func TestKeyringProbeService_IsUniquePerCall(t *testing.T) {
	first, second := keyringProbeService(), keyringProbeService()

	assert.NotEqual(t, first, second)
	for _, name := range []string{first, second} {
		assert.True(t, strings.HasPrefix(name, keyringProbePrefix+":"),
			"%q should carry the probe prefix so leaked sentinels stay identifiable", name)
		assert.Contains(t, name, fmt.Sprintf(":%d:", os.Getpid()),
			"%q should be scoped to this process", name)
	}
}

func TestKeyringProbeService_IsUniqueAcrossGoroutines(t *testing.T) {
	const n = 64

	var (
		mu    sync.Mutex
		wg    sync.WaitGroup
		names = make(map[string]struct{}, n)
	)
	for range n {
		wg.Go(func() {
			name := keyringProbeService()
			mu.Lock()
			defer mu.Unlock()
			names[name] = struct{}{}
		})
	}
	wg.Wait()

	assert.Len(t, names, n, "every concurrent probe must get its own sentinel name")
}

func TestKeyringWriteError_ReturnsTheBackendError(t *testing.T) {
	sentinel := errors.New("keychain said no")
	keyring.MockInitWithError(sentinel)
	t.Cleanup(keyring.MockInit)

	assert.ErrorIs(t, keyringWriteError(), sentinel)
}

func TestKeyringWriteError_CleansUpItsSentinel(t *testing.T) {
	keyring.MockInit()
	t.Cleanup(keyring.MockInit)

	// Predict the name the probe will use rather than calling keyringProbeService,
	// which would consume the sequence and leave this looking up a name the probe
	// never wrote.
	next := fmt.Sprintf("%s:%d:%d", keyringProbePrefix, os.Getpid(), keyringProbeSeq.Load()+1)
	require.NoError(t, keyringWriteError())

	_, err := keyring.Get(next, "")
	assert.ErrorIs(t, err, keyring.ErrNotFound, "the probe must not leave a sentinel behind")
}

// A keyring failure must carry the backend's own error. Without it the message
// reads as an unexplained permissions problem.
func TestCredentialWriteProbe_SurfacesTheKeyringError(t *testing.T) {
	keyring.MockInitWithError(errors.New("exit status 45"))
	t.Cleanup(keyring.MockInit)

	cfg := NewBlankConfigInDir(t.TempDir())
	require.NoError(t, cfg.Set("gitlab.com", "use_keyring", "true"))

	err := CredentialWriteProbe(cfg, "gitlab.com")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not accepting writes")
	assert.Contains(t, err.Error(), "exit status 45",
		"the backend's own error should be reported, not just that the write failed")
}
