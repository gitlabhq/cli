package skill

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseFrontmatter(t *testing.T) {
	t.Parallel()

	t.Run("valid", func(t *testing.T) {
		t.Parallel()

		fm, err := ParseFrontmatter([]byte("---\nname: foo\ndescription: bar baz\n---\nbody\n"))
		require.NoError(t, err)
		assert.Equal(t, "foo", fm.Name)
		assert.Equal(t, "bar baz", fm.Description)
	})

	t.Run("CRLF", func(t *testing.T) {
		t.Parallel()

		fm, err := ParseFrontmatter([]byte("---\r\nname: foo\r\ndescription: bar\r\n---\r\nbody\r\n"))
		require.NoError(t, err)
		assert.Equal(t, "foo", fm.Name)
		assert.Equal(t, "bar", fm.Description)
	})

	t.Run("missing leading delimiter", func(t *testing.T) {
		t.Parallel()

		_, err := ParseFrontmatter([]byte("name: foo\n"))
		require.Error(t, err)
	})

	t.Run("missing closing delimiter", func(t *testing.T) {
		t.Parallel()

		_, err := ParseFrontmatter([]byte("---\nname: foo\n"))
		require.Error(t, err)
	})
}

func TestSplitFrontmatter_JoinRoundTrips(t *testing.T) {
	t.Parallel()

	for _, in := range []string{
		"---\nname: foo\n---\nbody\n",
		"\n  ---\r\nname: foo\r\n---\r\nbody\r\n",
	} {
		parts, err := SplitFrontmatter([]byte(in))
		require.NoError(t, err)
		assert.Equal(t, in, string(parts.Join()))
	}
}
