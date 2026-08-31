package proxy

import (
	"io"
	"os"

	"gitlab.com/gitlab-org/cli/internal/dbg"
)

// peekLimits bounds how much of an upload body is inspected.
//
// inMemory is the amount of an upload body buffered in memory to extract the
// package coordinate. npm packuments and twine multipart field parts live near
// the front of the body, so this is normally ample; larger bodies spill to a
// temp file rather than failing to match.
//
// max is the hard ceiling on an inspected upload body. A body larger than this
// is treated as un-inspectable and the request fails closed (blocked) instead
// of being passed through un-checked.
type peekLimits struct {
	inMemory int64
	max      int64
}

// defaultPeekLimits is the production configuration. Tests pass smaller values
// to exercise the spill and over-limit paths without allocating gigabytes.
var defaultPeekLimits = peekLimits{
	inMemory: 64 << 20, // 64 MiB
	max:      1 << 30,  // 1 GiB
}

// orDefault returns l when it is fully specified, otherwise defaultPeekLimits.
// A zero-value peekLimits (from a matcher constructed as NPMMatcher{}) resolves
// to the production limits.
func (l peekLimits) orDefault() peekLimits {
	if l.inMemory <= 0 || l.max <= 0 {
		return defaultPeekLimits
	}
	return l
}

// peekResult carries the outcome of buffering an upload body for inspection.
type peekResult struct {
	// data is the bytes available to the matcher for coordinate extraction.
	data []byte
	// body is the restored request body the caller must assign back to
	// req.Body so the upstream round trip still sees the full payload.
	body io.ReadCloser
	// overLimit is true when the body exceeded limits.max and could not
	// be fully inspected; the caller must fail closed (block) in that case.
	overLimit bool
}

// peekUploadBody buffers an upload body for coordinate inspection. Bodies up
// to limits.inMemory are held in memory; larger bodies spill to a temp
// file (removed when the returned body is closed) so the matcher can read
// the whole payload without exhausting memory. A body larger than
// limits.max sets overLimit so the caller fails closed. The returned
// body always restores the full original stream for the upstream round trip.
func peekUploadBody(original io.ReadCloser, limits peekLimits) peekResult {
	mem, err := io.ReadAll(io.LimitReader(original, limits.inMemory))
	if err != nil {
		return peekResult{data: mem, body: bodyWithPrefix(mem, original)}
	}

	// Probe one more byte: if the body ends at or below the in-memory limit
	// we can inspect and restore it entirely from memory.
	var probe [1]byte
	n, _ := io.ReadFull(original, probe[:])
	if n == 0 {
		return peekResult{data: mem, body: bodyWithPrefix(mem, original)}
	}

	// The body is larger than the in-memory window; spill the whole payload
	// to a temp file so we can both inspect it and stream it upstream.
	f, err := os.CreateTemp("", "glab-df-upload-*")
	if err != nil {
		dbg.Debugf("dependency firewall: failed to create upload spill file: %v", err)
		// Cannot inspect; fail closed.
		return peekResult{overLimit: true, body: bodyWithPrefix(append(mem, probe[:n]...), original)}
	}

	if _, err := f.Write(mem); err != nil {
		return spillError(f, err, mem, probe[:n], original)
	}
	if _, err := f.Write(probe[:n]); err != nil {
		return spillError(f, err, mem, probe[:n], original)
	}

	written := int64(len(mem) + n)
	remaining := limits.max - written
	copied, err := io.Copy(f, io.LimitReader(original, remaining+1))
	if err != nil {
		// The mem+probe bytes are already in the temp file (now abandoned) and
		// cannot be recovered into the restored body, so the body spillError
		// builds here is missing that prefix and is NOT safe to forward. This
		// is only safe because spillError sets overLimit, and an over-limit
		// upload is always blocked before the body would be sent upstream. If a
		// future caller ever forwards this body without honoring overLimit, it
		// would send a corrupt (truncated-at-the-front) payload.
		return spillError(f, err, nil, nil, original)
	}
	written += copied
	_ = original.Close()

	overLimit := written > limits.max
	if overLimit {
		dbg.Debugf("dependency firewall: upload body exceeds %d bytes; failing closed", limits.max)
	}

	if _, err := f.Seek(0, io.SeekStart); err != nil {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return peekResult{overLimit: true}
	}

	data, err := io.ReadAll(io.LimitReader(f, limits.inMemory))
	if err != nil {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return peekResult{overLimit: true}
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return peekResult{overLimit: true}
	}

	return peekResult{data: data, body: tempFileBody(f), overLimit: overLimit}
}

// spillError abandons a temp-file spill on write failure, restoring whatever
// was read so the caller can still forward the body, and signals fail-closed.
func spillError(f *os.File, err error, mem, probe []byte, original io.ReadCloser) peekResult {
	dbg.Debugf("dependency firewall: failed to spill upload body: %v", err)
	_ = f.Close()
	_ = os.Remove(f.Name())
	prefix := append(append([]byte{}, mem...), probe...)
	return peekResult{overLimit: true, body: bodyWithPrefix(prefix, original)}
}

// tempFileBody returns a ReadCloser that streams f from its current offset
// and removes the backing temp file when closed.
func tempFileBody(f *os.File) io.ReadCloser {
	return &tempFileReadCloser{f: f}
}

type tempFileReadCloser struct{ f *os.File }

func (t *tempFileReadCloser) Read(p []byte) (int, error) { return t.f.Read(p) }

func (t *tempFileReadCloser) Close() error {
	err := t.f.Close()
	if rmErr := os.Remove(t.f.Name()); rmErr != nil {
		dbg.Debugf("dependency firewall: failed to remove upload spill file %s: %v", t.f.Name(), rmErr)
	}
	return err
}
