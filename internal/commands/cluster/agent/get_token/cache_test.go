//go:build !integration

package get_token

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
)

type mockStorage struct {
	data map[string][]byte
}

func (m *mockStorage) get(id string) ([]byte, error) {
	if data, exists := m.data[id]; exists {
		return data, nil
	}
	return nil, errNotFound
}

func (m *mockStorage) set(id string, data []byte) error {
	if m.data == nil {
		m.data = make(map[string][]byte)
	}
	m.data[id] = data
	return nil
}

func TestCache_unpopulated(t *testing.T) {
	// GIVEN
	createdPAT := &gitlab.PersonalAccessToken{
		Name:      "sentinel",
		Token:     "redacted",
		ExpiresAt: new(gitlab.ISOTime(time.Now().UTC().Add(1 * time.Hour))),
	}
	c := &cache{
		id: "test-id",
		createFunc: func() (*gitlab.PersonalAccessToken, error) {
			return createdPAT, nil
		},
		storage: &mockStorage{},
	}

	// WHEN
	pat, err := c.get()

	// THEN
	require.NoError(t, err)
	require.Equal(t, createdPAT, pat)
}

func TestCache_createError(t *testing.T) {
	// GIVEN
	expectedErr := errors.New("sentinel")
	c := &cache{
		id: "test-id",
		createFunc: func() (*gitlab.PersonalAccessToken, error) {
			return nil, expectedErr
		},
		storage: &mockStorage{},
	}

	// WHEN
	_, err := c.get()

	// THEN
	require.ErrorIs(t, err, expectedErr)
}

func TestCache_hit(t *testing.T) {
	// GIVEN
	token := &gitlab.PersonalAccessToken{
		Token:     "any-token",
		ExpiresAt: new(gitlab.ISOTime(time.Now().UTC().Add(tokenExpiryDurationDefault))),
	}

	mockStore := &mockStorage{}

	// Manually populate cache with valid token
	tokenData, err := json.Marshal(token)
	require.NoError(t, err)
	err = mockStore.set("test-id", tokenData)
	require.NoError(t, err)

	c := &cache{
		id: "test-id",
		createFunc: func() (*gitlab.PersonalAccessToken, error) {
			return nil, errors.New("sentinel - should not be called")
		},
		isTokenRevoked: func(t *gitlab.PersonalAccessToken) (bool, error) {
			return false, nil
		},
		storage: mockStore,
	}

	// WHEN
	pat, err := c.get()

	// THEN
	require.NoError(t, err)
	require.Equal(t, "any-token", pat.Token)
}

func TestCache_tokenExpired(t *testing.T) {
	// GIVEN
	expiredToken := &gitlab.PersonalAccessToken{
		Token:     "expired-token",
		ExpiresAt: new(gitlab.ISOTime(time.Now().UTC().Add(-1 * time.Hour))),
	}

	newToken := &gitlab.PersonalAccessToken{
		Token:     "new-token",
		ExpiresAt: new(gitlab.ISOTime(time.Now().UTC().Add(tokenExpiryDurationDefault))),
	}

	mockStore := &mockStorage{}
	// Manually set expired token in storage
	expiredData, err := json.Marshal(expiredToken)
	require.NoError(t, err)
	err = mockStore.set("test-id", expiredData)
	require.NoError(t, err)

	c := cache{
		id: "test-id",
		createFunc: func() (*gitlab.PersonalAccessToken, error) {
			return newToken, nil
		},
		isTokenRevoked: func(t *gitlab.PersonalAccessToken) (bool, error) {
			return false, nil
		},
		storage: mockStore,
	}

	// WHEN
	pat, err := c.get()

	// THEN
	require.NoError(t, err)
	require.Equal(t, "new-token", pat.Token)
}

func TestCache_tokenRevoked(t *testing.T) {
	// GIVEN
	revokedToken := &gitlab.PersonalAccessToken{
		Token:     "revoked-token",
		Revoked:   true,
		ExpiresAt: new(gitlab.ISOTime(time.Now().UTC().Add(-1 * time.Hour))),
	}

	newToken := &gitlab.PersonalAccessToken{
		Token:     "new-token",
		ExpiresAt: new(gitlab.ISOTime(time.Now().UTC().Add(tokenExpiryDurationDefault))),
	}

	mockStore := &mockStorage{}
	// Manually set revoked token in storage
	revokedData, err := json.Marshal(revokedToken)
	require.NoError(t, err)
	err = mockStore.set("test-id", revokedData)
	require.NoError(t, err)

	c := cache{
		id: "test-id",
		createFunc: func() (*gitlab.PersonalAccessToken, error) {
			return newToken, nil
		},
		isTokenRevoked: func(t *gitlab.PersonalAccessToken) (bool, error) {
			return true, nil
		},
		storage: mockStore,
	}

	// WHEN
	pat, err := c.get()

	// THEN
	require.NoError(t, err)
	require.Equal(t, "new-token", pat.Token)
}

func TestKeyringStorage_get(t *testing.T) {
	// GIVEN
	keyring.MockInit()

	// populate keyring
	err := keyring.Set(keyringService, "test-id", "any-data")
	require.NoError(t, err)
	s := keyringStorage{}

	// WHEN
	data, err := s.get("test-id")

	// THEN
	require.NoError(t, err)
	assert.Equal(t, []byte("any-data"), data)
}

func TestKeyringStorage_get_NotFound(t *testing.T) {
	// GIVEN
	keyring.MockInit()

	s := keyringStorage{}

	// WHEN
	_, err := s.get("test-id")

	// THEN
	require.ErrorIs(t, err, errNotFound)
}

func TestKeyringStorage_get_Unsupported(t *testing.T) {
	// GIVEN
	keyring.MockInitWithError(keyring.ErrUnsupportedPlatform)

	s := keyringStorage{}

	// WHEN
	_, err := s.get("test-id")

	// THEN
	require.ErrorIs(t, err, errUnsupportedPlatform)
}

func TestKeyringStorage_set(t *testing.T) {
	// GIVEN
	keyring.MockInit()

	s := keyringStorage{}

	// WHEN
	err := s.set("test-id", []byte("any-data"))

	// THEN
	require.NoError(t, err)
	setData, err := keyring.Get(keyringService, "test-id")
	require.NoError(t, err)
	assert.Equal(t, "any-data", setData)
}

func TestFileStorage_get(t *testing.T) {
	// GIVEN
	d := testDir(t)
	defer d.Close()
	f, err := d.OpenFile("test-id", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o400)
	require.NoError(t, err)
	_, err = f.WriteString("any-data")
	require.NoError(t, err)
	require.NoError(t, f.Sync())
	require.NoError(t, f.Close())

	s := fileStorage{root: d}

	// WHEN
	data, err := s.get("test-id")

	// THEN
	require.NoError(t, err)
	assert.Equal(t, []byte("any-data"), data)
}

func TestFileStorage_get_NotFound(t *testing.T) {
	// GIVEN
	d := testDir(t)
	defer d.Close()

	s := fileStorage{root: d}

	// WHEN
	_, err := s.get("test-id")

	// THEN
	require.ErrorIs(t, err, errNotFound)
}

func TestFileStorage_set(t *testing.T) {
	// GIVEN
	d := testDir(t)
	defer d.Close()

	s := fileStorage{root: d}

	// WHEN
	err := s.set("test-id", []byte("any-data"))

	// THEN
	require.NoError(t, err)
	assert.FileExists(t, filepath.Join(d.Name(), "test-id"))
	f, err := d.Open("test-id")
	require.NoError(t, err)
	data, err := io.ReadAll(f)
	require.NoError(t, err)
	assert.Equal(t, []byte("any-data"), data)
}

func testDir(t *testing.T) *os.Root {
	t.Helper()

	d, err := os.OpenRoot(t.TempDir())
	require.NoError(t, err)

	return d
}
