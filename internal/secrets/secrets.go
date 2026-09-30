// Package secrets encrypts API keys and other credentials at rest using
// AES-256-GCM with a key derived from LLMB_SECRET_KEY.
package secrets

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

const (
	prefix      = "v1:"
	keyFileName = "secret.key"
)

// ErrDecrypt means a value could not be decrypted, typically because the
// secret key changed since it was stored.
var ErrDecrypt = errors.New("cannot decrypt stored secret (was LLMB_SECRET_KEY changed?)")

// Box encrypts and decrypts short secrets.
type Box struct {
	aead cipher.AEAD
}

// New derives an AES-256 key from passphrase.
func New(passphrase string) (*Box, error) {
	if passphrase == "" {
		return nil, errors.New("secret key must not be empty")
	}
	key := sha256.Sum256([]byte("llmbench:" + passphrase))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Box{aead: aead}, nil
}

// Load returns a Box for passphrase, or, if it is empty, for a random key
// persisted in dataDir (generated on first use). generated reports whether
// a new key file was created.
func Load(passphrase, dataDir string) (box *Box, generated bool, err error) {
	if passphrase != "" {
		b, err := New(passphrase)
		return b, false, err
	}
	path := filepath.Join(dataDir, keyFileName)
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		b, err := New(strings.TrimSpace(string(data)))
		return b, false, err
	case !errors.Is(err, os.ErrNotExist):
		return nil, false, fmt.Errorf("reading %s: %w", path, err)
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return nil, false, err
	}
	key := hex.EncodeToString(raw)
	if err := os.WriteFile(path, []byte(key+"\n"), 0o600); err != nil {
		return nil, false, fmt.Errorf("writing %s: %w", path, err)
	}
	b, err := New(key)
	return b, true, err
}

// Encrypt returns an opaque string for plaintext. An empty plaintext encrypts
// to an empty string.
func (b *Box) Encrypt(plaintext string) (string, error) {
	if plaintext == "" {
		return "", nil
	}
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := b.aead.Seal(nonce, nonce, []byte(plaintext), nil)
	return prefix + base64.StdEncoding.EncodeToString(sealed), nil
}

// Decrypt reverses Encrypt.
func (b *Box) Decrypt(ciphertext string) (string, error) {
	if ciphertext == "" {
		return "", nil
	}
	enc, ok := strings.CutPrefix(ciphertext, prefix)
	if !ok {
		return "", ErrDecrypt
	}
	raw, err := base64.StdEncoding.DecodeString(enc)
	if err != nil || len(raw) < b.aead.NonceSize() {
		return "", ErrDecrypt
	}
	nonce, sealed := raw[:b.aead.NonceSize()], raw[b.aead.NonceSize():]
	plain, err := b.aead.Open(nil, nonce, sealed, nil)
	if err != nil {
		return "", ErrDecrypt
	}
	return string(plain), nil
}
