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
	"io/fs"
	"os"
	"path/filepath"
)

const MasterKeyEnv = "BATONDECK_MASTER_KEY"

// Keyring seals and opens secret values under the master key with AES-256-GCM. It is the
// only write path for secrets at rest. Build one with OpenKeyring.
type Keyring struct {
	aead cipher.AEAD
	key  []byte
	// created is set when this open made the key file.
	created bool
}

// Created reports whether OpenKeyring made the key file, generating a new master key, rather than
// reading the environment's key or a file already there.
func (k *Keyring) Created() bool { return k.created }

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
	key, fromEnv, err := envKey(lookup)
	if err != nil {
		return nil, err
	}
	var created bool
	if !fromEnv {
		if key, created, err = loadOrCreateKeyFile(path); err != nil {
			return nil, err
		}
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("keyring: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("keyring: %w", err)
	}
	return &Keyring{aead: aead, key: key, created: created}, nil
}

// envKey is BATONDECK_MASTER_KEY's key, when it is set (ok): base64 of 32 bytes, or an error.
func envKey(lookup func(string) (string, bool)) (key []byte, ok bool, err error) {
	v, ok := lookup(MasterKeyEnv)
	if !ok {
		return nil, false, nil
	}
	k, err := base64.StdEncoding.DecodeString(v)
	if err != nil {
		return nil, true, fmt.Errorf("keyring: %s is not base64: %w", MasterKeyEnv, err)
	}
	if len(k) != 32 {
		return nil, true, fmt.Errorf("keyring: %s must decode to 32 bytes, got %d", MasterKeyEnv, len(k))
	}
	return k, true, nil
}

// CheckMasterKey reports, creating nothing, whether OpenKeyring(path, lookup) would open: the
// environment's key decodes to 32 bytes, or else the key file at path is private and holds 32
// bytes. fromEnv says the key is the environment's; missing, that neither holds one, so
// OpenKeyring would create the file. doctor asks it.
func CheckMasterKey(path string, lookup func(string) (string, bool)) (fromEnv, missing bool, err error) {
	if _, fromEnv, err = envKey(lookup); fromEnv || err != nil {
		return fromEnv, false, err
	}
	if _, err = readKeyFile(path); errors.Is(err, fs.ErrNotExist) {
		return false, true, nil
	}
	return false, false, err
}

// loadOrCreateKeyFile reads the key file at path, or makes it when it is not there; created says it
// made it. The new key is written whole to a private temporary file beside path, synced, and then
// linked to path, which fails if the path exists: so the file never holds part of a key, and of
// processes making it at once (two replicas' first start, with no data-dir lock between them) one
// wins and the others read the winner's key instead of writing their own over it.
func loadOrCreateKeyFile(path string) (key []byte, created bool, err error) {
	if path == "" {
		return nil, false, fmt.Errorf("keyring: no key file and no %s", MasterKeyEnv)
	}
	k, err := readKeyFile(path)
	if !errors.Is(err, fs.ErrNotExist) {
		return k, false, err
	}
	k = make([]byte, 32)
	if _, err := rand.Read(k); err != nil {
		return nil, false, fmt.Errorf("keyring: %w", err)
	}
	won, err := createKeyFile(path, k)
	if err != nil {
		return nil, false, fmt.Errorf("keyring: %w", err)
	}
	if !won {
		k, err := readKeyFile(path)
		return k, false, err
	}
	return k, true, nil
}

// readKeyFile reads the key file at path: private (no group or other access) and 32 bytes. A missing
// file is an error wrapping fs.ErrNotExist.
func readKeyFile(path string) ([]byte, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("keyring: %w", err)
	}
	if fi.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("keyring: %s mode %o is group/world accessible; require 0600", path, fi.Mode().Perm())
	}
	k, err := os.ReadFile(path)
	switch {
	case err != nil:
		return nil, fmt.Errorf("keyring: %w", err)
	case len(k) == 0:
		return nil, fmt.Errorf("keyring: %s is empty: a first start interrupted before it wrote the key leaves it so; move it aside and start again, or put back master.key from a backup", path)
	case len(k) != 32:
		return nil, fmt.Errorf("keyring: %s holds %d bytes, want 32", path, len(k))
	}
	return k, nil
}

// createKeyFile makes path hold k, unless the path exists by then: won is false when another
// process made it first. Where the file system has no hard links, it falls back to creating path
// exclusively and writing it in place.
func createKeyFile(path string, k []byte) (won bool, err error) {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".master.key-*") // mode 0600
	if err != nil {
		return false, err
	}
	defer os.Remove(tmp.Name())
	_, err = tmp.Write(k)
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return false, err
	}
	switch err := os.Link(tmp.Name(), path); {
	case err == nil:
	case errors.Is(err, fs.ErrExist):
		return false, nil
	default:
		if won, err := writeKeyFileExclusive(path, k); err != nil || !won {
			return won, err
		}
	}
	// The link is durable once the directory is synced; a file system that cannot sync a
	// directory has nothing to sync.
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		d.Close()
	}
	return true, nil
}

// writeKeyFileExclusive is createKeyFile's fallback: path created exclusively, 0600, and k written
// into it; won is false when the path exists. It is not atomic: a process that loses to it can read
// the file before it is whole, and then fails rather than run on another key; a failed write
// removes the file.
func writeKeyFileExclusive(path string, k []byte) (won bool, err error) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, fs.ErrExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	_, err = f.Write(k)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(path)
		return false, err
	}
	return true, nil
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
