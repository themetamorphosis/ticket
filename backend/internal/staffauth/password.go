// Package staffauth authenticates staff with a password (Argon2id) and a
// WebAuthn security key or passkey (plan section 7). Accounts exist only by
// invitation; a staff session is valid only while its epoch matches, so
// revoking a person or changing their role ends every session they hold.
package staffauth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Argon2id parameters (RFC 9106 second recommendation, memory-constrained).
const (
	argonTime    = 3
	argonMemory  = 64 * 1024 // KiB
	argonThreads = 2
	argonKeyLen  = 32
	saltLen      = 16

	// MinPasswordLen is the shortest password accepted.
	MinPasswordLen = 12
)

var b64 = base64.RawStdEncoding

// HashPassword returns an Argon2id PHC string.
func HashPassword(password string) string {
	salt := make([]byte, saltLen)
	_, _ = rand.Read(salt)
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads, b64.EncodeToString(salt), b64.EncodeToString(key))
}

// CheckPassword reports whether password matches a PHC string from
// HashPassword, in constant time for the comparison.
func CheckPassword(phc, password string) bool {
	parts := strings.Split(phc, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	var version int
	var memory, iterations uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[2]+" "+parts[3], "v=%d m=%d,t=%d,p=%d", &version, &memory, &iterations, &threads); err != nil {
		return false
	}
	salt, err1 := b64.DecodeString(parts[4])
	want, err2 := b64.DecodeString(parts[5])
	if err1 != nil || err2 != nil || version != argon2.Version || len(want) == 0 {
		return false
	}
	got := argon2.IDKey([]byte(password), salt, iterations, memory, threads, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

// dummyHash is checked for unknown emails so a sign-in attempt takes the same
// time whether or not the account exists.
var dummyHash = HashPassword("no such account, but spend the same time")
