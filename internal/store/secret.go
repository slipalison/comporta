package store

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Cipher encrypts connection strings at rest with AES-256-GCM.
type Cipher struct{ aead cipher.AEAD }

const cipherPrefix = "v1:"

// LoadKey derives the key from secret when given; otherwise it reads (or
// creates) a random key file in dataDir.
func LoadKey(secret, dataDir string) ([]byte, error) {
	if secret != "" {
		sum := sha256.Sum256([]byte(secret))
		return sum[:], nil
	}
	path := filepath.Join(dataDir, "secret.key")
	if b, err := os.ReadFile(path); err == nil {
		key, err := hex.DecodeString(strings.TrimSpace(string(b)))
		if err != nil || len(key) != 32 {
			return nil, fmt.Errorf("invalid key file %s", path)
		}
		return key, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, []byte(hex.EncodeToString(key)), 0o600); err != nil {
		return nil, fmt.Errorf("writing key file: %w", err)
	}
	return key, nil
}

func NewCipher(key []byte) (*Cipher, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Cipher{aead: aead}, nil
}

func (c *Cipher) Encrypt(plain string) (string, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := c.aead.Seal(nonce, nonce, []byte(plain), nil)
	return cipherPrefix + base64.StdEncoding.EncodeToString(sealed), nil
}

func (c *Cipher) Decrypt(s string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(s, cipherPrefix))
	if err != nil {
		return "", err
	}
	ns := c.aead.NonceSize()
	if len(raw) < ns {
		return "", errors.New("ciphertext too short")
	}
	plain, err := c.aead.Open(nil, raw[:ns], raw[ns:], nil)
	if err != nil {
		return "", errors.New("cannot decrypt connection string: DBMCP_SECRET_KEY changed?")
	}
	return string(plain), nil
}
