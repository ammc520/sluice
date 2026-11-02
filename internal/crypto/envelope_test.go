package crypto

import (
	"bytes"
	"testing"
)

func TestCipherRoundTripAndAAD(t *testing.T) {
	key := bytes.Repeat([]byte{1}, 32)
	c, err := NewCipher(key, nil)
	if err != nil {
		t.Fatal(err)
	}
	enc, err := c.Seal([]byte("secret"), "req:prompt")
	if err != nil {
		t.Fatal(err)
	}
	got, err := c.Open(enc, "req:prompt")
	if err != nil || string(got) != "secret" {
		t.Fatalf("got %q err %v", got, err)
	}
	if _, err := c.Open(enc, "req:response"); err == nil {
		t.Fatal("expected AAD mismatch")
	}
	if _, err := c.Open(enc[:5], "req:prompt"); err == nil {
		t.Fatal("expected short envelope error")
	}
}

func TestCipherOpensWithPreviousKey(t *testing.T) {
	oldKey := bytes.Repeat([]byte{2}, 32)
	newKey := bytes.Repeat([]byte{3}, 32)

	oldCipher, _ := NewCipher(oldKey, nil)
	enc, err := oldCipher.Seal([]byte("before-rotation"), "aad")
	if err != nil {
		t.Fatal(err)
	}

	rotated, err := NewCipher(newKey, oldKey)
	if err != nil {
		t.Fatal(err)
	}
	got, err := rotated.Open(enc, "aad")
	if err != nil || string(got) != "before-rotation" {
		t.Fatalf("got %q err %v", got, err)
	}

	fresh, _ := rotated.Seal([]byte("after"), "aad")
	if fresh[1] != keyIDCurrent {
		t.Fatalf("new writes must use current key, got id %d", fresh[1])
	}
}

func TestNewCipherRejectsBadKeyLength(t *testing.T) {
	if _, err := NewCipher([]byte("short"), nil); err == nil {
		t.Fatal("expected error")
	}
}
