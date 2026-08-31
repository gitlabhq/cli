//go:build !integration

package proxy

import (
	"bytes"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func readAllAndClose(t *testing.T, r io.ReadCloser) []byte {
	t.Helper()
	b, err := io.ReadAll(r)
	require.NoError(t, err)
	require.NoError(t, r.Close())
	return b
}

func TestPeekUploadBodyInMemory(t *testing.T) {
	t.Parallel()
	payload := []byte("small body under the in-memory limit")

	got := peekUploadBody(io.NopCloser(bytes.NewReader(payload)), peekLimits{inMemory: 64, max: 1024})
	assert.False(t, got.overLimit)
	assert.Equal(t, payload, got.data)
	assert.Equal(t, payload, readAllAndClose(t, got.body), "restored body must equal the original")
}

func TestPeekUploadBodySpillsPastInMemory(t *testing.T) {
	t.Parallel()
	payload := bytes.Repeat([]byte("x"), 200) // > in-memory (16), < max (1024)

	got := peekUploadBody(io.NopCloser(bytes.NewReader(payload)), peekLimits{inMemory: 16, max: 1024})
	assert.False(t, got.overLimit)
	// data is bounded by the in-memory window even when spilled.
	assert.Len(t, got.data, 16)
	assert.Equal(t, payload, readAllAndClose(t, got.body), "restored body must stream the full payload from the spill file")
}

func TestPeekUploadBodyOverLimitFailsClosed(t *testing.T) {
	t.Parallel()
	payload := bytes.Repeat([]byte("x"), 200) // > max (64)

	got := peekUploadBody(io.NopCloser(bytes.NewReader(payload)), peekLimits{inMemory: 16, max: 64})
	assert.True(t, got.overLimit, "a body over the hard limit must set overLimit")
}
