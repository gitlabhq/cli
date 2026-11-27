//go:build !integration

package artifactregistry

import (
	"bytes"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/cli/internal/testing/rpmregistrytest"
)

const (
	keyA = rpmregistrytest.KeyAFingerprint
	keyB = rpmregistrytest.KeyBFingerprint
)

func TestCheckPublicKeys_OneBlockPerKey(t *testing.T) {
	t.Parallel()

	keys, err := CheckPublicKeys(append(rpmregistrytest.KeyB(), rpmregistrytest.KeyA()...), []string{keyA, keyB})

	require.NoError(t, err)
	assert.Equal(t, []string{keyB, keyA}, keys.Fingerprints)
	assert.Equal(t, 2, bytes.Count(keys.Armored, []byte("-----BEGIN PGP PUBLIC KEY BLOCK-----")))
}

func TestCheckPublicKeys_ArmoredHoldsOnlyTheCheckedKeys(t *testing.T) {
	t.Parallel()
	// Text a reader other than go-crypto could take for more key material.
	served := append([]byte("\n\n"), rpmregistrytest.KeyA()...)
	served = append(served, "\n\n"...)

	keys, err := CheckPublicKeys(served, []string{keyA})

	require.NoError(t, err)
	assert.True(t, bytes.HasPrefix(keys.Armored, []byte("-----BEGIN PGP PUBLIC KEY BLOCK-----")))
	assert.True(t, bytes.HasSuffix(keys.Armored, []byte("-----END PGP PUBLIC KEY BLOCK-----\n")))
}

func TestCheckPublicKeys_Mismatch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		served   []byte
		expected []string
	}{
		{"an extra served key", append(rpmregistrytest.KeyA(), rpmregistrytest.KeyB()...), []string{keyA}},
		{"a missing served key", rpmregistrytest.KeyA(), []string{keyA, keyB}},
		{"a different key", rpmregistrytest.KeyB(), []string{keyA}},
		{"a lowercase expected fingerprint", rpmregistrytest.KeyA(), []string{"6e81e6039e2b3e46675afd45680e0716a26fc847"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			keys, err := CheckPublicKeys(tc.served, tc.expected)

			_, ok := errors.AsType[*KeyMismatchError](err)
			assert.True(t, ok, "got %v", err)
			assert.Nil(t, keys)
		})
	}
}

func TestCheckPublicKeys_Malformed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		served []byte
		want   string
	}{
		{"empty", nil, "no armored public key"},
		{"a key served twice", append(rpmregistrytest.KeyA(), rpmregistrytest.KeyA()...), "is served twice"},
		{"a subkey", rpmregistrytest.KeySub(), "has a subkey"},
		{"text outside a block", append(rpmregistrytest.KeyA(), "trailing"...), "text outside an armored block"},
		{"a message block", []byte("-----BEGIN PGP MESSAGE-----\n\nwy4ECQMI\n=ABCD\n-----END PGP MESSAGE-----\n"), `a "PGP MESSAGE" block`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			keys, err := CheckPublicKeys(tc.served, []string{keyA})

			require.ErrorIs(t, err, errMalformedKey)
			require.ErrorContains(t, err, tc.want)
			assert.Nil(t, keys)
		})
	}
}
