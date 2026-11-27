package sealbox

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"testing"
)

func key() []byte {
	k := make([]byte, 32)
	_, _ = rand.Read(k)
	return k
}

func TestRoundTripAndTamperDetection(t *testing.T) {
	b, err := New(key())
	if err != nil {
		t.Fatal(err)
	}
	sealed := b.Seal([]byte("credential"))
	if bytes.Contains(sealed, []byte("credential")) {
		t.Fatal("plaintext visible")
	}
	if got, err := b.Open(sealed); err != nil || string(got) != "credential" {
		t.Fatalf("open %q %v", got, err)
	}
	if bytes.Equal(sealed, b.Seal([]byte("credential"))) {
		t.Fatal("nonce reused")
	}
	tampered := append([]byte{}, sealed...)
	tampered[len(tampered)-1] ^= 1
	other, _ := New(key())
	for name, s := range map[string][]byte{
		"tampered": tampered, "short": sealed[:5], "version": append([]byte{9}, sealed[1:]...), "other key": nil,
	} {
		box := b
		if name == "other key" {
			box, s = other, sealed
		}
		if _, err := box.Open(s); !errors.Is(err, ErrOpen) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestKeys(t *testing.T) {
	k := key()
	for _, s := range []string{base64.StdEncoding.EncodeToString(k), base64.RawURLEncoding.EncodeToString(k)} {
		if got, err := ParseKey(s); err != nil || !bytes.Equal(got, k) {
			t.Errorf("ParseKey(%s) = %v", s, err)
		}
	}
	for _, s := range []string{"", "not base64!", base64.StdEncoding.EncodeToString(k[:16])} {
		if _, err := ParseKey(s); err == nil {
			t.Errorf("ParseKey(%q) accepted", s)
		}
	}
	if _, err := New(k[:16]); err == nil {
		t.Fatal("short key accepted")
	}
}
