//go:build !integration

package systemd

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestQuote(t *testing.T) {
	t.Parallel()

	assert.Equal(t, `/opt/my glab/bin/glab`, Quote(`/opt/my glab/bin/glab`))
	assert.Equal(t, `100%% \"done\" C:\\x`, Quote(`100% "done" C:\x`))
}

func TestUnquote_ReversesQuote(t *testing.T) {
	t.Parallel()

	in := `100% "done" C:\x %h`
	assert.Equal(t, in, Unquote(Quote(in)))
}
