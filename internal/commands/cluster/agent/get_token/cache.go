package get_token

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/zalando/go-keyring"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
)

const keyringService = "glab"

var (
	errNotFound            = errors.New("not found")
	errTokenExpired        = errors.New("token expired")
	errTokenRevoked        = errors.New("token revoked")
	errUnsupportedPlatform = errors.New("unsupported platform")
)

// storage defines the interface for token storage backends
type storage interface {
	get(id string) ([]byte, error)
	set(id string, data []byte) error
}

// keyringStorage implements storage using the system keyring
type keyringStorage struct{}

func (k *keyringStorage) get(id string) ([]byte, error) {
	data, err := keyring.Get(keyringService, id)
	switch {
	case err == nil:
		return []byte(data), nil
	case errors.Is(err, keyring.ErrNotFound):
		return nil, errNotFound
	case errors.Is(err, keyring.ErrUnsupportedPlatform):
		return nil, errUnsupportedPlatform
	default:
		return nil, err
	}
}

func (k *keyringStorage) set(id string, data []byte) error {
	if err := keyring.Set(keyringService, id, string(data)); err != nil {
		if errors.Is(err, keyring.ErrUnsupportedPlatform) {
			return errUnsupportedPlatform
		}
		return err
	}
	return nil
}

// fileStorage implements storage using the filesystem
type fileStorage struct {
	root *os.Root
}

func newFileStorage() (*fileStorage, error) {
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return nil, err
	}

	gitlabCacheDir := filepath.Join(cacheDir, "gitlab")
	err = os.MkdirAll(gitlabCacheDir, 0o700)
	if err != nil {
		return nil, err
	}

	root, err := os.OpenRoot(gitlabCacheDir)
	if err != nil {
		return nil, err
	}

	return &fileStorage{root: root}, nil
}

func (f *fileStorage) get(id string) ([]byte, error) {
	file, err := f.root.Open(id)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, errNotFound
		}
		return nil, err
	}
	defer file.Close()

	return io.ReadAll(file)
}

func (f *fileStorage) set(id string, data []byte) (err error) {
	var file *os.File
	file, err = f.root.OpenFile(id, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer func() {
		cerr := file.Close()
		if err == nil {
			err = cerr
		}
	}()

	if _, err := file.Write(data); err != nil {
		return err
	}

	if err := file.Sync(); err != nil {
		return err
	}

	return nil
}

func (f *fileStorage) close() error {
	return f.root.Close()
}

type cache struct {
	id             string
	createFunc     func() (*gitlab.PersonalAccessToken, error)
	isTokenRevoked func(t *gitlab.PersonalAccessToken) (bool, error)
	storage        storage
}

func (c *cache) isTokenExpired(token *gitlab.PersonalAccessToken) bool {
	return time.Time(*token.ExpiresAt).Before(time.Now().UTC())
}

func (c *cache) get() (*gitlab.PersonalAccessToken, error) {
	token, err := c.getCachedToken()
	switch {
	case err == nil:
		return token, nil
	case errors.Is(err, errNotFound):
		fallthrough
	case errors.Is(err, errTokenExpired), errors.Is(err, errTokenRevoked):
		return c.createAndCacheToken()
	default:
		return nil, err
	}
}

func (c *cache) getCachedToken() (*gitlab.PersonalAccessToken, error) {
	data, err := c.storage.get(c.id)
	if err != nil {
		return nil, err
	}

	var token gitlab.PersonalAccessToken
	if err := json.Unmarshal(data, &token); err != nil {
		return nil, err
	}

	if c.isTokenExpired(&token) {
		return nil, errTokenExpired
	}

	if token.Revoked {
		fmt.Fprintln(os.Stderr, "Cached token has been revoked, creating new one") //nolint:forbidigo // cache has no IOStreams reference, writes directly to os.Stderr
		return nil, errTokenRevoked
	}

	isRevoked, err := c.isTokenRevoked(&token)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Failed to check if token is revoked: %v. Using cached token anyway.\n", err) //nolint:forbidigo // cache has no IOStreams reference, writes directly to os.Stderr
		return &token, nil
	}

	if isRevoked {
		fmt.Fprintln(os.Stderr, "Cached token has been revoked, creating new one") //nolint:forbidigo // cache has no IOStreams reference, writes directly to os.Stderr
		return nil, errTokenRevoked
	}

	return &token, nil
}

func (c *cache) createAndCacheToken() (*gitlab.PersonalAccessToken, error) {
	token, err := c.createFunc()
	if err != nil {
		return nil, err
	}

	data, err := json.Marshal(token) //nolint:forbidigo // cached token is stored in keyring, not stdout
	if err != nil {
		return nil, err
	}

	if err := c.storage.set(c.id, data); err != nil {
		return nil, err
	}

	return token, nil
}
