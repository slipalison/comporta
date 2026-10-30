package store

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Cipher encrypts connection strings at rest with AES-256-GCM.
type Cipher struct {
	aead cipher.AEAD
	// legacy reads values written before v2 when the key came from
	// COMPORTA_SECRET_KEY (see Keys.Legacy). Nil otherwise.
	legacy cipher.AEAD
}

// v2 values use the current key. v1 (and unprefixed) values come from the
// former db-mcp; Open re-encrypts them as v2 on startup.
const (
	cipherPrefix  = "v2:"
	legacyPrefixV = "v1:"
)

// Keys is the key material for a Cipher.
type Keys struct {
	Current []byte
	// Legacy is the key v1 values were sealed with, when it differs from
	// Current (see LoadKeys).
	Legacy []byte
}

// Argon2id parameters for COMPORTA_SECRET_KEY (RFC 9106, second recommended
// option): 64 MiB and 3 passes, about 100 ms once per start. A copied volume
// then costs an attacker that much per guess instead of one SHA-256.
const (
	argonTime    = 3
	argonMemory  = 64 * 1024
	argonThreads = 4
)

// LoadKeys derives the key from secret when given, with Argon2id and a salt
// kept in dataDir; otherwise it reads (or creates) a random key file there.
//
// legacyHex is the key db-mcp sealed with when it ran with a secret (it used
// SHA-256 of the secret, which this code no longer computes). It is only
// needed once, on the first start over such a volume: Open seals those values
// again with the current key. Empty when there is nothing to migrate.
func LoadKeys(secret, legacyHex, dataDir string) (Keys, error) {
	var keys Keys
	if legacyHex != "" {
		legacy, err := hex.DecodeString(strings.TrimSpace(legacyHex))
		if err != nil || len(legacy) != 32 {
			return keys, errors.New("COMPORTA_LEGACY_KEY must be 64 hex characters (the SHA-256 of the former DBMCP_SECRET_KEY)")
		}
		keys.Legacy = legacy
	}
	if secret == "" {
		key, err := readOrCreateHex(filepath.Join(dataDir, "secret.key"), 32)
		keys.Current = key
		return keys, err
	}
	salt, err := readOrCreateHex(filepath.Join(dataDir, "secret.salt"), 16)
	if err != nil {
		return keys, err
	}
	keys.Current = argon2.IDKey([]byte(secret), salt, argonTime, argonMemory, argonThreads, 32)
	return keys, nil
}

// readOrCreateHex returns the n random bytes stored hex-encoded in path,
// creating the file (mode 0600) the first time.
func readOrCreateHex(path string, n int) ([]byte, error) {
	if b, err := os.ReadFile(path); err == nil { // #nosec G304 -- fixed name inside the configured data dir
		v, err := hex.DecodeString(strings.TrimSpace(string(b)))
		if err != nil || len(v) != n {
			return nil, fmt.Errorf("invalid file %s", path)
		}
		return v, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	v := make([]byte, n)
	if _, err := rand.Read(v); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, []byte(hex.EncodeToString(v)), 0o600); err != nil {
		return nil, fmt.Errorf("writing %s: %w", path, err)
	}
	return v, nil
}

func newAEAD(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// NewCipherKeys builds a Cipher that seals with keys.Current and can still
// open values sealed with keys.Legacy.
func NewCipherKeys(keys Keys) (*Cipher, error) {
	c, err := NewCipher(keys.Current)
	if err != nil || keys.Legacy == nil {
		return c, err
	}
	if c.legacy, err = newAEAD(keys.Legacy); err != nil {
		return nil, err
	}
	return c, nil
}

func NewCipher(key []byte) (*Cipher, error) {
	aead, err := newAEAD(key)
	if err != nil {
		return nil, err
	}
	return &Cipher{aead: aead}, nil
}

// Outdated reports whether s was sealed before v2 and should be re-encrypted.
func (c *Cipher) Outdated(s string) bool { return !strings.HasPrefix(s, cipherPrefix) }

func (c *Cipher) Encrypt(plain string) (string, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := c.aead.Seal(nonce, nonce, []byte(plain), nil)
	return cipherPrefix + base64.StdEncoding.EncodeToString(sealed), nil
}

func (c *Cipher) Decrypt(s string) (string, error) {
	aead := c.aead
	if c.Outdated(s) && c.legacy != nil {
		aead = c.legacy
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(strings.TrimPrefix(s, cipherPrefix), legacyPrefixV))
	if err != nil {
		return "", err
	}
	ns := aead.NonceSize()
	if len(raw) < ns {
		return "", errors.New("ciphertext too short")
	}
	plain, err := aead.Open(nil, raw[:ns], raw[ns:], nil)
	if err != nil {
		if c.Outdated(s) && c.legacy == nil {
			return "", errors.New("cannot decrypt a connection string written by db-mcp: if it ran with DBMCP_SECRET_KEY, start once with COMPORTA_LEGACY_KEY (see the README)")
		}
		return "", errors.New("cannot decrypt connection string: COMPORTA_SECRET_KEY changed?")
	}
	return string(plain), nil
}
