//go:build !integration

package oauth2

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateClientID(t *testing.T) {
	t.Parallel()

	validID := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

	t.Run("valid 64 hex characters", func(t *testing.T) {
		t.Parallel()
		require.NoError(t, ValidateClientID(validID))
	})

	t.Run("valid with uppercase hex", func(t *testing.T) {
		t.Parallel()
		require.NoError(t, ValidateClientID(strings.ToUpper(validID)))
	})

	t.Run("valid with surrounding whitespace", func(t *testing.T) {
		t.Parallel()
		require.NoError(t, ValidateClientID("  "+validID+"  \n"))
	})

	t.Run("empty value", func(t *testing.T) {
		t.Parallel()
		err := ValidateClientID("")
		require.Error(t, err)
		assert.Equal(t, "OAuth application ID cannot be empty", err.Error())
	})

	t.Run("whitespace only", func(t *testing.T) {
		t.Parallel()
		err := ValidateClientID("   \t\n")
		require.Error(t, err)
		assert.Equal(t, "OAuth application ID cannot be empty", err.Error())
	})

	t.Run("personal access token prefix glpat-", func(t *testing.T) {
		t.Parallel()
		err := ValidateClientID("glpat-abcdef1234567890abcdef1234567890abcdef1234567890abcdef123456")
		require.Error(t, err)
		assert.Equal(t, "value is a personal access token (glpat-), not an OAuth application ID", err.Error())
	})

	t.Run("uppercase personal access token prefix GLPAT-", func(t *testing.T) {
		t.Parallel()
		err := ValidateClientID("GLPAT-abcdef1234567890abcdef1234567890abcdef1234567890abcdef123456")
		require.Error(t, err)
		assert.Equal(t, "value is a personal access token (glpat-), not an OAuth application ID", err.Error())
	})

	t.Run("application secret prefix gloas-", func(t *testing.T) {
		t.Parallel()
		err := ValidateClientID("gloas-abcdef1234567890abcdef1234567890abcdef1234567890abcdef123456")
		require.Error(t, err)
		assert.Equal(t, "value is an OAuth application secret (gloas-), not the Application ID (glab is a public client and does not use a secret)", err.Error())
	})

	t.Run("mixed case application secret prefix Gloas-", func(t *testing.T) {
		t.Parallel()
		err := ValidateClientID("Gloas-abcdef1234567890abcdef1234567890abcdef1234567890abcdef123456")
		require.Error(t, err)
		assert.Equal(t, "value is an OAuth application secret (gloas-), not the Application ID (glab is a public client and does not use a secret)", err.Error())
	})

	t.Run("generic gitlab token prefix", func(t *testing.T) {
		t.Parallel()
		err := ValidateClientID("glcbt-abcdef1234567890abcdef1234567890abcdef1234567890abcdef12345")
		require.Error(t, err)
		assert.Equal(t, "value appears to be a GitLab token, not an OAuth application ID", err.Error())
	})

	t.Run("too short", func(t *testing.T) {
		t.Parallel()
		err := ValidateClientID("12345")
		require.Error(t, err)
		assert.Equal(t, "OAuth application ID must be 64 hexadecimal characters", err.Error())
	})

	t.Run("too long", func(t *testing.T) {
		t.Parallel()
		err := ValidateClientID(validID + "ab")
		require.Error(t, err)
		assert.Equal(t, "OAuth application ID must be 64 hexadecimal characters", err.Error())
	})

	t.Run("non-hex characters", func(t *testing.T) {
		t.Parallel()
		invalid := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdeg"
		err := ValidateClientID(invalid)
		require.Error(t, err)
		assert.Equal(t, "OAuth application ID must be 64 hexadecimal characters", err.Error())
	})
}

func TestValidateTokenPrefix(t *testing.T) {
	t.Parallel()

	t.Run("valid arbitrary client id without token prefix", func(t *testing.T) {
		t.Parallel()
		require.NoError(t, validateTokenPrefix("custom-client-id-12345"))
	})

	t.Run("allows custom client id starting with gl", func(t *testing.T) {
		t.Parallel()
		require.NoError(t, validateTokenPrefix("gitlab-custom-client-id"))
		require.NoError(t, validateTokenPrefix("global-client-id"))
		require.NoError(t, validateTokenPrefix("gl-custom-id"))
	})

	t.Run("empty value", func(t *testing.T) {
		t.Parallel()
		require.NoError(t, validateTokenPrefix(""))
	})

	t.Run("whitespace only", func(t *testing.T) {
		t.Parallel()
		require.NoError(t, validateTokenPrefix("   \t\n"))
	})

	t.Run("personal access token prefix glpat-", func(t *testing.T) {
		t.Parallel()
		err := validateTokenPrefix("glpat-abcdef1234567890abcdef1234567890abcdef1234567890abcdef123456")
		require.Error(t, err)
		assert.Equal(t, "value is a personal access token (glpat-), not an OAuth application ID", err.Error())
	})

	t.Run("case insensitive personal access token prefix", func(t *testing.T) {
		t.Parallel()
		err := validateTokenPrefix("GLPAT-abcdef1234567890abcdef1234567890abcdef1234567890abcdef123456")
		require.Error(t, err)
		assert.Equal(t, "value is a personal access token (glpat-), not an OAuth application ID", err.Error())
	})

	t.Run("application secret prefix gloas-", func(t *testing.T) {
		t.Parallel()
		err := validateTokenPrefix("gloas-abcdef1234567890abcdef1234567890abcdef1234567890abcdef123456")
		require.Error(t, err)
		assert.Equal(t, "value is an OAuth application secret (gloas-), not the Application ID (glab is a public client and does not use a secret)", err.Error())
	})

	t.Run("case insensitive application secret prefix", func(t *testing.T) {
		t.Parallel()
		err := validateTokenPrefix("Gloas-abcdef1234567890abcdef1234567890abcdef1234567890abcdef123456")
		require.Error(t, err)
		assert.Equal(t, "value is an OAuth application secret (gloas-), not the Application ID (glab is a public client and does not use a secret)", err.Error())
	})

	t.Run("generic gitlab token prefix", func(t *testing.T) {
		t.Parallel()
		err := validateTokenPrefix("glcbt-abcdef1234567890abcdef1234567890abcdef1234567890abcdef12345")
		require.Error(t, err)
		assert.Equal(t, "value appears to be a GitLab token, not an OAuth application ID", err.Error())
	})

	t.Run("case insensitive generic token prefix", func(t *testing.T) {
		t.Parallel()
		err := validateTokenPrefix("GLCBT-abcdef1234567890abcdef1234567890abcdef1234567890abcdef12345")
		require.Error(t, err)
		assert.Equal(t, "value appears to be a GitLab token, not an OAuth application ID", err.Error())
	})
}
