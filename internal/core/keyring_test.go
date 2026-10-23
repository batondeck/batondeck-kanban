package core

import (
	"bytes"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
)

func TestKeyringGeneratesFileOnFirstRun(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keyring.key")
	kr, err := OpenKeyring(path, func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("key file mode = %o, want 600", fi.Mode().Perm())
	}
	// Reopening loads the same key: a value sealed now opens later.
	ct, err := kr.Encrypt([]byte("secret"), []byte("col:tokens"))
	if err != nil {
		t.Fatal(err)
	}
	kr2, err := OpenKeyring(path, func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatal(err)
	}
	pt, err := kr2.Decrypt(ct, []byte("col:tokens"))
	if err != nil || !bytes.Equal(pt, []byte("secret")) {
		t.Fatalf("reopen round-trip failed: %v %q", err, pt)
	}
}

func TestKeyringEnvBeatsFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "keyring.key")
	envKey := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))
	kr, err := OpenKeyring(path, func(k string) (string, bool) {
		if k == "BATONDECK_MASTER_KEY" {
			return envKey, true
		}
		return "", false
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("env-supplied key must not create a key file")
	}
	ct, _ := kr.Encrypt([]byte("x"), nil)
	if _, err := kr.Decrypt(ct, nil); err != nil {
		t.Fatal(err)
	}
}

func TestKeyringRoundTripAndAADBinding(t *testing.T) {
	kr := testKeyring(t)
	ct, err := kr.Encrypt([]byte("refresh-token"), []byte("integrations.oauth"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(ct, []byte("refresh-token")) {
		t.Fatal("ciphertext contains plaintext")
	}
	if _, err := kr.Decrypt(ct, []byte("wrong-aad")); err == nil {
		t.Fatal("decrypt with wrong AAD must fail")
	}
	pt, err := kr.Decrypt(ct, []byte("integrations.oauth"))
	if err != nil || string(pt) != "refresh-token" {
		t.Fatalf("round trip: %v %q", err, pt)
	}
}

func TestKeyringWrongKeyFailsCleanly(t *testing.T) {
	kr1 := testKeyring(t)
	kr2 := testKeyring(t)
	ct, _ := kr1.Encrypt([]byte("s"), nil)
	if _, err := kr2.Decrypt(ct, nil); err == nil {
		t.Fatal("decrypt under a different key must fail")
	}
}

func TestKeyringRefusesWorldReadableFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keyring.key")
	if err := os.WriteFile(path, bytes.Repeat([]byte{1}, 32), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenKeyring(path, func(string) (string, bool) { return "", false }); err == nil {
		t.Fatal("world-readable key file must be refused")
	}
}

func TestKeyringRejectsBadEnvKey(t *testing.T) {
	for _, bad := range []string{"not-base64!!", base64.StdEncoding.EncodeToString([]byte("short"))} {
		_, err := OpenKeyring(filepath.Join(t.TempDir(), "k"), func(k string) (string, bool) {
			if k == "BATONDECK_MASTER_KEY" {
				return bad, true
			}
			return "", false
		})
		if err == nil {
			t.Fatalf("bad env key %q accepted", bad)
		}
	}
}

func testKeyring(t *testing.T) *Keyring {
	t.Helper()
	kr, err := OpenKeyring(filepath.Join(t.TempDir(), "keyring.key"),
		func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatal(err)
	}
	return kr
}

// TestKeyringDeriveKey: one label under one master key is one key, read back after a reopen; another
// label, or another master key, is another key.
func TestKeyringDeriveKey(t *testing.T) {
	open := func(b byte) *Keyring {
		t.Helper()
		env := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{b}, 32))
		kr, err := OpenKeyring("", func(string) (string, bool) { return env, true })
		if err != nil {
			t.Fatal(err)
		}
		return kr
	}
	derive := func(kr *Keyring, label string) []byte {
		t.Helper()
		k, err := kr.DeriveKey(label)
		if err != nil || len(k) != 32 {
			t.Fatalf("DeriveKey(%q) = %x, %v; want 32 bytes", label, k, err)
		}
		return k
	}
	a := derive(open(7), "blob-signing")
	if !bytes.Equal(a, derive(open(7), "blob-signing")) {
		t.Error("one label under one master key derived two keys")
	}
	if bytes.Equal(a, derive(open(7), "other")) || bytes.Equal(a, derive(open(8), "blob-signing")) {
		t.Error("another label or another master key derived the same key")
	}
	if bytes.Equal(a, bytes.Repeat([]byte{7}, 32)) {
		t.Error("the derived key is the master key")
	}
}
