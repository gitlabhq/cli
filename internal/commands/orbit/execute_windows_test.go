//go:build windows && !integration

package orbit

import (
	"errors"
	"runtime"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestWrapExecError_emulationHintOnARM64(t *testing.T) {
	t.Parallel()
	if runtime.GOARCH != "arm64" {
		t.Skip("hint only applies on arm64")
	}
	err := wrapExecError(syscall.Errno(193))
	assert.ErrorContains(t, err, "x64 emulation")
}

func TestWrapExecError_corruptHintOnAMD64(t *testing.T) {
	t.Parallel()
	if runtime.GOARCH != "amd64" {
		t.Skip("hint only applies on amd64")
	}
	err := wrapExecError(syscall.Errno(193))
	assert.ErrorContains(t, err, "corrupted")
}

func TestWrapExecError_genericMessage(t *testing.T) {
	t.Parallel()
	err := wrapExecError(errors.New("some other failure"))
	assert.ErrorContains(t, err, "failed to execute Orbit CLI")
}
