package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	cryptorand "crypto/rand"
	"errors"
	"fmt"
	"io"
)

const (
	envelopeVersion = byte(1)
	keyIDCurrent    = byte(0)
	keyIDPrevious   = byte(1)
	nonceSize       = 12
	headerSize      = 2
)

// Cipher encrypts and decrypts AES-256-GCM envelopes.
type Cipher struct {
	current  cipher.AEAD
	previous cipher.AEAD
}

// NewCipher creates a cipher from a current 32-byte key and an optional
// previous key used only for decryption during key rotation.
func NewCipher(current, previous []byte) (*Cipher, error) {
	if len(current) != 32 {
		return nil, fmt.Errorf("crypto: current key must be 32 bytes, got %d", len(current))
	}
	cur, err := newAEAD(current)
	if err != nil {
		return nil, err
	}
	c := &Cipher{current: cur}
	if len(previous) > 0 {
		if len(previous) != 32 {
			return nil, fmt.Errorf("crypto: previous key must be 32 bytes, got %d", len(previous))
		}
		prev, err := newAEAD(previous)
		if err != nil {
			return nil, err
		}
		c.previous = prev
	}
	return c, nil
}

func newAEAD(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("crypto: aes: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("crypto: gcm: %w", err)
	}
	return aead, nil
}

// Seal encrypts plaintext and binds it to aad.
// Output: version(1) | key_id(1) | nonce(12) | ciphertext+tag.
func (c *Cipher) Seal(plaintext []byte, aad string) ([]byte, error) {
	nonce := make([]byte, nonceSize)
	if _, err := io.ReadFull(cryptorand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("crypto: nonce: %w", err)
	}
	out := make([]byte, 0, headerSize+nonceSize+len(plaintext)+c.current.Overhead())
	out = append(out, envelopeVersion, keyIDCurrent)
	out = append(out, nonce...)
	out = c.current.Seal(out, nonce, plaintext, []byte(aad))
	return out, nil
}

// Open decrypts an envelope and verifies aad.
func (c *Cipher) Open(envelope []byte, aad string) ([]byte, error) {
	if len(envelope) < headerSize+nonceSize {
		return nil, errors.New("crypto: envelope too short")
	}
	if envelope[0] != envelopeVersion {
		return nil, fmt.Errorf("crypto: unsupported envelope version %d", envelope[0])
	}
	nonce := envelope[headerSize : headerSize+nonceSize]
	ciphertext := envelope[headerSize+nonceSize:]

	switch envelope[1] {
	case keyIDCurrent:
		plaintext, err := c.current.Open(nil, nonce, ciphertext, []byte(aad))
		if err == nil {
			return plaintext, nil
		}
		// During rotation, ciphertext written before the key change also
		// carries key id 0 but was sealed with the previous key.
		if c.previous != nil {
			if plaintext, err := c.previous.Open(nil, nonce, ciphertext, []byte(aad)); err == nil {
				return plaintext, nil
			}
		}
		return nil, errors.New("crypto: decryption failed")
	case keyIDPrevious:
		if c.previous == nil {
			return nil, errors.New("crypto: previous key not configured")
		}
		plaintext, err := c.previous.Open(nil, nonce, ciphertext, []byte(aad))
		if err != nil {
			return nil, errors.New("crypto: decryption failed")
		}
		return plaintext, nil
	default:
		return nil, fmt.Errorf("crypto: unknown key id %d", envelope[1])
	}
}
