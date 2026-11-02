package crypto

import (
	"strings"
	"testing"
)

func TestTokenFormatAndHash(t *testing.T) {
	plain, hash, prefix, last4, err := GenerateToken()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(plain, "sk-company-") {
		t.Fatal(plain)
	}
	if !strings.HasPrefix(plain, prefix) || !strings.HasSuffix(plain, last4) {
		t.Fatal("prefix/last4 mismatch")
	}
	if hash != HashToken(plain) || len(hash) != 64 {
		t.Fatal("hash mismatch")
	}
	if !IsCompanyToken(plain) {
		t.Fatal("IsCompanyToken false")
	}
}

func TestTokensAreUnique(t *testing.T) {
	a, _, _, _, _ := GenerateToken()
	b, _, _, _, _ := GenerateToken()
	if a == b {
		t.Fatal("duplicate token generated")
	}
}
