//go:build !integration

package cmdutils

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testSecretEnv = "GLAB_TEST_SECRET_FILE"

func newSecretFile(t *testing.T, args ...string) *SecretFile {
	t.Helper()

	cmd := &cobra.Command{}
	s := AddSecretFileFlag(cmd, "token-file", "token", testSecretEnv, "to use")
	require.NoError(t, cmd.ParseFlags(args))
	return s
}

func writeSecret(t *testing.T, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "secret")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

func TestSecretFile_Help(t *testing.T) {
	t.Parallel()

	cmd := &cobra.Command{}
	AddSecretFileFlag(cmd, "token-file", "token", testSecretEnv, "to use")
	assert.Equal(t, "Path to a file with the token to use. Use - to read from standard input. Defaults to the "+testSecretEnv+" environment variable.", cmd.Flags().Lookup("token-file").Usage)
}

func TestSecretFile_HelpEnvironment(t *testing.T) {
	t.Parallel()

	cmd := &cobra.Command{Annotations: map[string]string{"other": "kept"}}
	AddSecretFileFlag(cmd, "token-file", "token", testSecretEnv, "to use")
	AddSecretFileFlag(cmd, "key-file", "key", "GLAB_TEST_KEY_FILE", "to sign with")
	assert.Equal(t, "kept", cmd.Annotations["other"])
	assert.Equal(t, testSecretEnv+": the token to use, used when --token-file is not set.\nGLAB_TEST_KEY_FILE: the key to sign with, used when --key-file is not set.\n", cmd.Annotations["help:environment"])
}

func TestSecretFile_ReadFile(t *testing.T) {
	t.Parallel()

	path := writeSecret(t, " s3cret\n")
	s := newSecretFile(t, "--token-file", path)

	got, err := s.Read(strings.NewReader("unused"))
	require.NoError(t, err)
	assert.Equal(t, "s3cret", got)
	assert.True(t, s.IsSet())
	assert.Equal(t, path, s.File())
	assert.False(t, s.FromStdin())
}

func TestSecretFile_ReadStdin(t *testing.T) {
	t.Parallel()

	s := newSecretFile(t, "--token-file", "-")

	got, err := s.Read(strings.NewReader("s3cret\n"))
	require.NoError(t, err)
	assert.Equal(t, "s3cret", got)
	assert.True(t, s.FromStdin())
}

func TestSecretFile_Errors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{name: "missing file", args: []string{"--token-file", "/nonexistent/secret"}, wantErr: "failed to read --token-file"},
		{name: "empty file", args: []string{"--token-file", writeSecret(t, " \n")}, wantErr: "the token is empty"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := newSecretFile(t, tc.args...).Read(strings.NewReader(""))
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

// The tests below set the environment, so they cannot run in parallel.

func TestSecretFile_ReadEnvironment(t *testing.T) {
	t.Setenv(testSecretEnv, "s3cret")
	s := newSecretFile(t)

	got, err := s.Read(strings.NewReader("unused"))
	require.NoError(t, err)
	assert.Equal(t, "s3cret", got)
	assert.True(t, s.IsSet())
	assert.Empty(t, s.File())
	assert.Equal(t, testSecretEnv, s.Env())
}

func TestSecretFile_FileOverridesEnvironment(t *testing.T) {
	t.Setenv(testSecretEnv, "from-env")
	s := newSecretFile(t, "--token-file", writeSecret(t, "from-file"))

	got, err := s.Read(strings.NewReader(""))
	require.NoError(t, err)
	assert.Equal(t, "from-file", got)
}

func TestSecretFile_NotSet(t *testing.T) {
	t.Setenv(testSecretEnv, "")
	s := newSecretFile(t)

	assert.False(t, s.IsSet())
	_, err := s.Read(strings.NewReader(""))
	var flagErr *FlagError
	require.ErrorAs(t, err, &flagErr)
	assert.EqualError(t, err, "pass --token-file or set "+testSecretEnv)
}
