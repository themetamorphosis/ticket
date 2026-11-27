// Package tickets issues one ticket per paid place, renders each as its own
// PDF and reads them back for download.
package tickets

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"strings"
)

// NewCredential returns 128 random bits, URL-safe, for the QR code. It is
// embedded in the staff URL fragment, so it never reaches request paths.
func NewCredential() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// Digest is the SHA-256 stored in place of a credential or fallback code.
func Digest(s string) []byte {
	sum := sha256.Sum256([]byte(s))
	return sum[:]
}

// Crockford Base32 symbols, then the five extra check symbols.
const (
	symbols      = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
	checkSymbols = symbols + "*~$=U"
	codeLen      = 16
)

// NewFallbackCode returns 16 random Crockford Base32 characters (80 bits) in
// groups of four plus a check character, e.g. "7K2Q-HM9X-4DAB-T0RE-Q", for
// staff to type when a QR code will not scan.
func NewFallbackCode() string {
	b := make([]byte, codeLen)
	_, _ = rand.Read(b)
	raw := make([]byte, codeLen)
	for i, v := range b {
		raw[i] = symbols[v%32] // 256 is a multiple of 32: unbiased
	}
	return format(string(raw) + string(check(string(raw))))
}

func check(raw string) byte {
	r := 0
	for i := range len(raw) {
		r = (r*32 + strings.IndexByte(symbols, raw[i])) % 37
	}
	return checkSymbols[r]
}

func format(code string) string {
	var sb strings.Builder
	for i := 0; i < codeLen; i += 4 {
		sb.WriteString(code[i:i+4] + "-")
	}
	sb.WriteByte(code[codeLen])
	return sb.String()
}

var lookalikes = strings.NewReplacer("O", "0", "I", "1", "L", "1", "-", "", " ", "")

// NormalizeFallback turns what staff typed into the canonical 17-character
// form, forgiving case, spacing, hyphens and O/I/L look-alikes. It reports
// false for a wrong length, a stray symbol or a failed check character.
func NormalizeFallback(input string) (string, bool) {
	s := lookalikes.Replace(strings.ToUpper(input))
	if len(s) != codeLen+1 {
		return "", false
	}
	for i := range codeLen {
		if strings.IndexByte(symbols, s[i]) < 0 {
			return "", false
		}
	}
	if s[codeLen] != check(s[:codeLen]) {
		return "", false
	}
	return s, true
}
