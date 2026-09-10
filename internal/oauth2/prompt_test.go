//go:build !integration

package oauth2

import (
	"bytes"
	"io"
	"testing"

	"git.sr.ht/~timofurrer/ugh"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/testing/consoletest"
)

// newNonInteractiveIOStreams returns a real, non-nil *iostreams.IOStreams
// that reports itself as non-interactive, for callers that must never be
// handed a nil io (see StartFlow/StartDeviceFlow's doc comments).
func newNonInteractiveIOStreams() *iostreams.IOStreams {
	return iostreams.New(
		iostreams.WithStdin(io.NopCloser(&bytes.Buffer{}), false),
		iostreams.WithStdout(&bytes.Buffer{}, false),
		iostreams.WithStderr(&bytes.Buffer{}, false),
	)
}

func TestResolveClientID_NonInteractiveSelfManagedFallsBackToError(t *testing.T) {
	cfg := stubConfig{
		hosts: map[string]map[string]string{
			"salsa.debian.org": {},
		},
	}

	clientID, persist, err := resolveClientID(t.Context(), cfg, newNonInteractiveIOStreams(), "salsa.debian.org")
	require.Error(t, err)
	assert.Empty(t, clientID)
	assert.Nil(t, persist)
	assert.ErrorContains(t, err, "set 'client_id' first")
}

func TestResolveClientID_InteractivePasteChoiceDefersPersistUntilCalled(t *testing.T) {
	console := ugh.New(t)
	console.Expect(ugh.Select("How do you want to continue?")).
		Do(ugh.SelectIndex(0)) // clientIDChoicePaste
	console.Expect(ugh.Input("Paste the Application ID here:")).
		Do(ugh.Type("42"))

	io, cleanup := consoletest.IOStreamsWithConsole(t, console)
	t.Cleanup(cleanup)

	writes := 0
	cfg := stubConfig{
		hosts: map[string]map[string]string{
			"salsa.debian.org": {},
		},
		writes: &writes,
	}

	clientID, persist, err := resolveClientID(t.Context(), cfg, io, "salsa.debian.org")
	require.NoError(t, err)
	assert.Equal(t, "42", clientID)

	// A pasted value must not be written until the caller proves it works
	// (a completed OAuth round trip) by calling persist.
	persisted, err := cfg.Get("salsa.debian.org", "client_id")
	require.NoError(t, err)
	assert.Empty(t, persisted, "resolveClientID must not persist a pasted value on its own")
	assert.Zero(t, writes)

	require.NoError(t, persist())

	persisted, err = cfg.Get("salsa.debian.org", "client_id")
	require.NoError(t, err)
	assert.Equal(t, "42", persisted)
	assert.Equal(t, 1, writes, "expected persist() to flush the pasted client_id to disk")
}

func TestResolveClientID_InteractiveShowMeChoiceDoesNotPersist(t *testing.T) {
	console := ugh.New(t)
	console.Expect(ugh.Select("How do you want to continue?")).
		Do(ugh.SelectIndex(1)) // clientIDChoiceShow

	io, cleanup := consoletest.IOStreamsWithConsole(t, console)
	t.Cleanup(cleanup)

	writes := 0
	cfg := stubConfig{
		hosts: map[string]map[string]string{
			"salsa.debian.org": {},
		},
		writes: &writes,
	}

	clientID, persist, err := resolveClientID(t.Context(), cfg, io, "salsa.debian.org")
	require.ErrorContains(t, err, "set 'client_id' first")
	assert.Empty(t, clientID)
	assert.Nil(t, persist)

	persisted, err := cfg.Get("salsa.debian.org", "client_id")
	require.NoError(t, err)
	assert.Empty(t, persisted, "showing the registration instructions must not persist anything")
	assert.Zero(t, writes)
}

func TestResolveClientID_InteractiveCancelChoiceReturnsStandardError(t *testing.T) {
	console := ugh.New(t)
	console.Expect(ugh.Select("How do you want to continue?")).
		Do(ugh.SelectIndex(2)) // clientIDChoiceCancel

	io, cleanup := consoletest.IOStreamsWithConsole(t, console)
	t.Cleanup(cleanup)

	cfg := stubConfig{
		hosts: map[string]map[string]string{
			"salsa.debian.org": {},
		},
	}

	clientID, persist, err := resolveClientID(t.Context(), cfg, io, "salsa.debian.org")
	require.Error(t, err)
	assert.Empty(t, clientID)
	assert.Nil(t, persist)
	assert.ErrorContains(t, err, "set 'client_id' first")
}

func TestResolveClientID_CtrlCDuringChoiceReturnsStandardError(t *testing.T) {
	console := ugh.New(t)
	console.Expect(ugh.Select("How do you want to continue?")).
		Do(func(r *ugh.Response) { r.Send(ugh.CtrlC) })

	io, cleanup := consoletest.IOStreamsWithConsole(t, console)
	t.Cleanup(cleanup)

	cfg := stubConfig{
		hosts: map[string]map[string]string{
			"salsa.debian.org": {},
		},
	}

	clientID, persist, err := resolveClientID(t.Context(), cfg, io, "salsa.debian.org")
	require.Error(t, err)
	assert.Empty(t, clientID)
	assert.Nil(t, persist)
	assert.ErrorContains(t, err, "set 'client_id' first",
		"a cancelled prompt must leave the same next step as a non-interactive session, not a bare 'user cancelled' error")
}

func TestResolveClientID_CtrlCDuringPasteReturnsStandardError(t *testing.T) {
	console := ugh.New(t)
	console.Expect(ugh.Select("How do you want to continue?")).
		Do(ugh.SelectIndex(0)) // clientIDChoicePaste
	console.Expect(ugh.Input("Paste the Application ID here:")).
		Do(func(r *ugh.Response) { r.Send(ugh.CtrlC) })

	io, cleanup := consoletest.IOStreamsWithConsole(t, console)
	t.Cleanup(cleanup)

	writes := 0
	cfg := stubConfig{
		hosts: map[string]map[string]string{
			"salsa.debian.org": {},
		},
		writes: &writes,
	}

	clientID, persist, err := resolveClientID(t.Context(), cfg, io, "salsa.debian.org")
	require.ErrorContains(t, err, "set 'client_id' first")
	assert.Empty(t, clientID)
	assert.Nil(t, persist)
	assert.Zero(t, writes, "a cancelled paste must not persist a partial value")
}
