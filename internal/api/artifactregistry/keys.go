package artifactregistry

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
)

var errMalformedKey = errors.New("malformed OpenPGP public key")

// PublicKeys are a repository's public keys, checked against the fingerprints
// it selects.
type PublicKeys struct {
	// Armored holds the checked keys, armored again by glab, one block per key.
	// A file written from it holds only what was checked, whatever else the
	// served file carried.
	Armored []byte
	// Fingerprints are the keys' fingerprints, in served order.
	Fingerprints []string
}

// KeyMismatchError is returned when the served keys are not exactly the
// expected ones. During a key rotation, the key file and the repository's
// settings can be read on either side of a change.
type KeyMismatchError struct {
	Served   []string
	Expected []string
}

func (e *KeyMismatchError) Error() string {
	return fmt.Sprintf("the key file serves keys %s, but the repository selects %s",
		strings.Join(e.Served, ", "), strings.Join(e.Expected, ", "))
}

// CheckPublicKeys parses armored, one or more armored public key blocks, and
// returns the keys only if they are exactly expected: every served key must be
// expected, and every expected key served. expected holds uppercase
// hexadecimal v4 fingerprints.
//
// The Artifact Registry generates primary keys with no subkey, so a key with a
// subkey is refused.
func CheckPublicKeys(armored []byte, expected []string) (*PublicKeys, error) {
	entities, err := readArmoredKeys(armored)
	if err != nil {
		return nil, err
	}

	var out bytes.Buffer
	served := make([]string, 0, len(entities))
	for _, entity := range entities {
		fingerprint := strings.ToUpper(hex.EncodeToString(entity.PrimaryKey.Fingerprint))
		if slices.Contains(served, fingerprint) {
			return nil, fmt.Errorf("%w: key %s is served twice", errMalformedKey, fingerprint)
		}
		if len(entity.Subkeys) > 0 {
			return nil, fmt.Errorf("%w: key %s has a subkey", errMalformedKey, fingerprint)
		}
		served = append(served, fingerprint)
		if err := armorEntity(&out, entity); err != nil {
			return nil, fmt.Errorf("armoring key %s: %w", fingerprint, err)
		}
	}

	if len(served) != len(expected) || slices.ContainsFunc(served, func(f string) bool { return !slices.Contains(expected, f) }) {
		return nil, &KeyMismatchError{Served: served, Expected: expected}
	}
	return &PublicKeys{Armored: out.Bytes(), Fingerprints: served}, nil
}

// readArmoredKeys reads every armored block in data. openpgp.ReadArmoredKeyRing
// reads only the first, and the registry serves each key in a block of its
// own. armor.Decode may read past the end of a block, so data is cut after
// each end line first.
func readArmoredKeys(data []byte) (openpgp.EntityList, error) {
	var entities openpgp.EntityList
	for _, chunk := range bytes.SplitAfter(data, []byte("-----END "+openpgp.PublicKeyType+"-----")) {
		if len(bytes.TrimSpace(chunk)) == 0 {
			continue
		}
		block, err := armor.Decode(bytes.NewReader(chunk))
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("%w: text outside an armored block", errMalformedKey)
		}
		if err != nil {
			return nil, fmt.Errorf("%w: %w", errMalformedKey, err)
		}
		if block.Type != openpgp.PublicKeyType {
			return nil, fmt.Errorf("%w: a %q block", errMalformedKey, block.Type)
		}
		keys, err := openpgp.ReadKeyRing(block.Body)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", errMalformedKey, err)
		}
		entities = append(entities, keys...)
	}
	if len(entities) == 0 {
		return nil, fmt.Errorf("%w: no armored public key", errMalformedKey)
	}
	return entities, nil
}

func armorEntity(out *bytes.Buffer, entity *openpgp.Entity) error {
	w, err := armor.Encode(out, openpgp.PublicKeyType, nil)
	if err != nil {
		return err
	}
	if err := entity.Serialize(w); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	out.WriteByte('\n')
	return nil
}
