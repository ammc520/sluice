package crypto

import (
	cryptorand "crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"strings"
)

const tokenPrefixLiteral = "sk-company-"

// GenerateToken creates a company token and its storage metadata.
// The plaintext is returned exactly once for display to the administrator.
func GenerateToken() (plain, hash, prefix, last4 string, err error) {
	raw := make([]byte, 32)
	if _, err = io.ReadFull(cryptorand.Reader, raw); err != nil {
		return "", "", "", "", fmt.Errorf("crypto: token randomness: %w", err)
	}
	plain = tokenPrefixLiteral + base64.RawURLEncoding.EncodeToString(raw)
	hash = HashToken(plain)
	prefix = plain[:15]
	last4 = plain[len(plain)-4:]
	return plain, hash, prefix, last4, nil
}

// HashToken returns the lowercase SHA-256 hex digest of a token.
func HashToken(plain string) string {
	sum := sha256.Sum256([]byte(plain))
	return hex.EncodeToString(sum[:])
}

// IsCompanyToken reports whether the value looks like a gateway token.
func IsCompanyToken(value string) bool {
	return strings.HasPrefix(value, tokenPrefixLiteral)
}
