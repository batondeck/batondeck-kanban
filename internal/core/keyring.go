package core

// Keyring: the master key and the ONLY write path for secrets at rest. Master key sourcing
// chain: BATONDECK_MASTER_KEY env var (base64, 32 bytes) > 0600 key file, generated on first run.
//
// Values are sealed with AES-256-GCM; the caller-supplied AAD binds a ciphertext to
// its column/context so a value copied between columns fails to open.

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
)

const MasterKeyEnv = "BATONDECK_MASTER_KEY"

// Keyring seals and opens secret values under the master key with AES-256-GCM. It is the
// only write path for secrets at rest. Build one with OpenKeyring.
type Keyring struct {
	aead cipher.AEAD
	key  []byte
}

// DeriveKey is a 32-byte key for one purpose, named by label: HKDF-SHA256 of the master key, so a
// derived key reveals nothing of the master key or of another label's key.
func (k *Keyring) DeriveKey(label string) ([]byte, error) {
	d, err := hkdf.Key(sha256.New, k.key, nil, label, 32)
	if err != nil {
		return nil, fmt.Errorf("keyring: %w", err)
	}
	return d, nil
}

// OpenKeyring resolves the master key (env > file; the file is created 0600 on first
// run) and returns a ready keyring. lookup is injectable for tests; production passes
// os.LookupEnv.
func OpenKeyring(path string, lookup func(string) (string, bool)) (*Keyring, error) {
	var key []byte
	if v, ok := lookup(MasterKeyEnv); ok {
		k, err := base64.StdEncoding.DecodeString(v)
		if err != nil {
			return nil, fmt.Errorf("keyring: %s is not base64: %w", MasterKeyEnv, err)
		}
		if len(k) != 32 {
			return nil, fmt.Errorf("keyring: %s must decode to 32 bytes, got %d", MasterKeyEnv, len(k))
		}
		key = k
	} else {
		k, err := loadOrCreateKeyFile(path)
		if err != nil {
			return nil, err
		}
		key = k
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("keyring: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("keyring: %w", err)
	}
	return &Keyring{aead: aead, key: key}, nil
}

func loadOrCreateKeyFile(path string) ([]byte, error) {
	fi, err := os.Stat(path)
	switch {
	case err == nil:
		if fi.Mode().Perm()&0o077 != 0 {
			return nil, fmt.Errorf("keyring: %s mode %o is group/world accessible; require 0600", path, fi.Mode().Perm())
		}
		k, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("keyring: %w", err)
		}
		if len(k) != 32 {
			return nil, fmt.Errorf("keyring: %s holds %d bytes, want 32", path, len(k))
		}
		return k, nil
	case os.IsNotExist(err):
		k := make([]byte, 32)
		if _, err := rand.Read(k); err != nil {
			return nil, fmt.Errorf("keyring: %w", err)
		}
		if err := os.WriteFile(path, k, 0o600); err != nil {
			return nil, fmt.Errorf("keyring: %w", err)
		}
		return k, nil
	default:
		return nil, fmt.Errorf("keyring: %w", err)
	}
}

// Encrypt seals plaintext under the master key, bound to aad. Output layout:
// nonce ‖ ciphertext (GCM tag included).
func (k *Keyring) Encrypt(plaintext, aad []byte) ([]byte, error) {
	nonce := make([]byte, k.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("keyring: %w", err)
	}
	return append(nonce, k.aead.Seal(nil, nonce, plaintext, aad)...), nil
}

// Decrypt opens a value produced by Encrypt with the same aad.
func (k *Keyring) Decrypt(sealed, aad []byte) ([]byte, error) {
	ns := k.aead.NonceSize()
	if len(sealed) < ns+k.aead.Overhead() {
		return nil, errors.New("keyring: sealed value too short")
	}
	pt, err := k.aead.Open(nil, sealed[:ns], sealed[ns:], aad)
	if err != nil {
		return nil, fmt.Errorf("keyring: decrypt failed: %w", err)
	}
	return pt, nil
}
