package oncall

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

// The secret envelope (conductor src/domain/secret-envelope.ts:31-58): AES-256-GCM twice over. A
// fresh 32-byte DEK seals the plaintext, and the KEK (the keyring, core.Keyring) wraps the DEK, both
// bound to the same AAD. The packed string is conductor's seven base64url segments,
//
//	enc1:<ivKek>:<wrappedDek>:<tagKek>:<ivDek>:<ciphertext>:<tagDek>
//
// with the keyring's nonce ‖ ciphertext ‖ tag output split into its three parts.

// EnvelopePrefix starts every sealed value.
const EnvelopePrefix = "enc1:"

const (
	envelopeVersion = "enc1"
	gcmNonce        = 12
	gcmTag          = 16
	dekBytes        = 32
)

// KEK wraps and unwraps a DEK bound to aad: AES-256-GCM output as nonce ‖ ciphertext ‖ tag
// (core.Keyring's Encrypt and Decrypt).
type KEK interface {
	Encrypt(plaintext, aad []byte) ([]byte, error)
	Decrypt(sealed, aad []byte) ([]byte, error)
}

// ErrEnvelope is every failure to open an envelope: a wrong KEK, a wrong AAD and a tampered
// segment are one error by design (the GCM tag), so a failure tells an attacker nothing more.
var ErrEnvelope = errors.New("oncall: the secret envelope failed authentication")

// SecretAAD binds an integration's sealed secrets to its row: a value copied to another
// integration does not open.
func SecretAAD(integrationID string) string { return "integrations.auth:" + integrationID }

// Seal seals plaintext under a fresh DEK wrapped by kek, both bound to aad.
func Seal(kek KEK, plaintext []byte, aad string) (string, error) {
	dek := make([]byte, dekBytes)
	if _, err := rand.Read(dek); err != nil {
		return "", fmt.Errorf("oncall: %w", err)
	}
	wrapped, err := kek.Encrypt(dek, []byte(aad))
	if err != nil {
		return "", fmt.Errorf("oncall: wrap the DEK: %w", err)
	}
	if len(wrapped) != gcmNonce+dekBytes+gcmTag {
		return "", fmt.Errorf("oncall: the KEK wrapped a %d-byte DEK into %d bytes, want %d", dekBytes, len(wrapped), gcmNonce+dekBytes+gcmTag)
	}
	sealed, err := gcmSeal(dek, plaintext, []byte(aad))
	if err != nil {
		return "", err
	}
	enc := base64.RawURLEncoding.EncodeToString
	ivKek, wrappedDek, tagKek := split(wrapped)
	ivDek, ct, tagDek := split(sealed)
	return strings.Join([]string{envelopeVersion, enc(ivKek), enc(wrappedDek), enc(tagKek), enc(ivDek), enc(ct), enc(tagDek)}, ":"), nil
}

// Open opens an envelope Seal made with the same kek and aad.
func Open(kek KEK, envelope, aad string) ([]byte, error) {
	parts := strings.Split(envelope, ":")
	if len(parts) != 7 || parts[0] != envelopeVersion {
		return nil, ErrEnvelope
	}
	var seg [6][]byte
	for i, p := range parts[1:] {
		b, err := base64.RawURLEncoding.DecodeString(p)
		if err != nil {
			return nil, ErrEnvelope
		}
		seg[i] = b
	}
	dek, err := kek.Decrypt(join(seg[0], seg[1], seg[2]), []byte(aad))
	if err != nil || len(dek) != dekBytes {
		return nil, ErrEnvelope
	}
	pt, err := gcmOpen(dek, join(seg[3], seg[4], seg[5]), []byte(aad))
	if err != nil {
		return nil, ErrEnvelope
	}
	return pt, nil
}

func gcm(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("oncall: %w", err)
	}
	return cipher.NewGCM(block)
}

func gcmSeal(key, plaintext, aad []byte) ([]byte, error) {
	a, err := gcm(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcmNonce)
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("oncall: %w", err)
	}
	return append(nonce, a.Seal(nil, nonce, plaintext, aad)...), nil
}

func gcmOpen(key, sealed, aad []byte) ([]byte, error) {
	a, err := gcm(key)
	if err != nil {
		return nil, err
	}
	if len(sealed) < gcmNonce+gcmTag {
		return nil, ErrEnvelope
	}
	return a.Open(nil, sealed[:gcmNonce], sealed[gcmNonce:], aad)
}

// split is nonce ‖ ciphertext ‖ tag as its three parts.
func split(b []byte) (nonce, ct, tag []byte) {
	return b[:gcmNonce], b[gcmNonce : len(b)-gcmTag], b[len(b)-gcmTag:]
}

func join(nonce, ct, tag []byte) []byte {
	out := make([]byte, 0, len(nonce)+len(ct)+len(tag))
	return append(append(append(out, nonce...), ct...), tag...)
}
