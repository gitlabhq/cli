//go:build !integration

package policy

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestInternalErrorTextIsSanitized(t *testing.T) {
	t.Parallel()
	c := Coordinate{Name: "org.evil\r\n\x1b[31mred:a", Version: "1\n2"}
	reason := InternalErrorResult(c).Reason
	phrase := InternalErrorPhrase(c)

	assert.NotContains(t, reason, "\r", "InternalErrorResult reason")
	assert.NotContains(t, reason, "\n", "InternalErrorResult reason")
	assert.NotContains(t, reason, "\x1b", "InternalErrorResult reason")
	assert.NotContains(t, phrase, "\r", "InternalErrorPhrase")
	assert.NotContains(t, phrase, "\n", "InternalErrorPhrase")
	assert.NotContains(t, phrase, "\x1b", "InternalErrorPhrase")
}
