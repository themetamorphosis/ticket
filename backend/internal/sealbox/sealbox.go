// Package sealbox encrypts small secrets at rest (ticket credentials) with
// AES-256-GCM. The key comes from the runtime environment, never the database,
// so a database or backup leak alone does not reveal valid tickets. Back the
// key up separately: without it, stored tickets cannot be re-rendered.
package sealbox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
)

const version = 1

// ErrOpen means sealed data was corrupt, truncated or sealed with another key.
var ErrOpen = errors.New("sealbox: cannot open sealed data")

// Box seals and opens data with one key.
type Box struct{ aead cipher.AEAD }

// ParseKey decodes a base64 (standard or URL) 32-byte key.
func ParseKey(s string) ([]byte, error) {
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if k, err := enc.DecodeString(s); err == nil && len(k) == 32 {
			return k, nil
		}
	}
	return nil, fmt.Errorf("sealbox: key must be 32 bytes, base64 encoded")
}

// New returns a Box for a 32-byte key.
func New(key []byte) (*Box, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("sealbox: key must be 32 bytes, got %d", len(key))
	}
	block, _ := aes.NewCipher(key)
	aead, _ := cipher.NewGCM(block)
	return &Box{aead: aead}, nil
}

// Seal returns version || nonce || ciphertext.
func (b *Box) Seal(plain []byte) []byte {
	nonce := make([]byte, b.aead.NonceSize())
	_, _ = rand.Read(nonce)
	out := append([]byte{version}, nonce...)
	return b.aead.Seal(out, nonce, plain, []byte{version})
}

// Open reverses Seal.
func (b *Box) Open(sealed []byte) ([]byte, error) {
	n := b.aead.NonceSize()
	if len(sealed) < 1+n || sealed[0] != version {
		return nil, ErrOpen
	}
	plain, err := b.aead.Open(nil, sealed[1:1+n], sealed[1+n:], []byte{version})
	if err != nil {
		return nil, ErrOpen
	}
	return plain, nil
}
