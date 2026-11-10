package core

import (
	"bytes"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

// Processes making the key file at once (two replicas' first start, on Postgres with no data-dir
// lock) all end on the one key the file holds: one creates it, the rest read it. Each wrote its own
// key over the others', so a process kept sealing under a key that was no longer on disk. Only the
// one that made the file says it created it.
func TestConcurrentFirstStartsAgreeOnTheKeyOnDisk(t *testing.T) {
	const rounds, racers = 20, 8
	for r := 0; r < rounds; r++ {
		path := filepath.Join(t.TempDir(), "master.key")
		keys := make([]*Keyring, racers)
		errs := make([]error, racers)
		start := make(chan struct{})
		var wg sync.WaitGroup
		for i := range racers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				keys[i], errs[i] = OpenKeyring(path, func(string) (string, bool) { return "", false })
			}()
		}
		close(start)
		wg.Wait()
		onDisk, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		created := 0
		for i, kr := range keys {
			if errs[i] != nil {
				t.Fatalf("round %d, racer %d: %v", r, i, errs[i])
			}
			if !bytes.Equal(kr.key, onDisk) {
				t.Fatalf("round %d, racer %d runs on a key that is not the one in %s", r, i, path)
			}
			if kr.Created() {
				created++
			}
		}
		if created != 1 {
			t.Fatalf("round %d: %d racers say they created the key file, want 1", r, created)
		}
		// No temporary file is left beside it.
		if entries, _ := os.ReadDir(filepath.Dir(path)); len(entries) != 1 {
			t.Fatalf("round %d: the key's directory holds %d entries, want master.key alone", r, len(entries))
		}
	}
}

// A key file a crash left empty (made, then killed before the write) is refused with the remedy,
// not as a bare byte count; a reopened file and an environment key were not created.
func TestKeyringNamesAnEmptyFileAndWhoCreated(t *testing.T) {
	path := filepath.Join(t.TempDir(), "master.key")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := OpenKeyring(path, func(string) (string, bool) { return "", false })
	if err == nil || !strings.Contains(err.Error(), "is empty") || !strings.Contains(err.Error(), "interrupted") {
		t.Fatalf("an empty key file: %v", err)
	}
	path = filepath.Join(t.TempDir(), "master.key")
	first, err := OpenKeyring(path, func(string) (string, bool) { return "", false })
	if err != nil || !first.Created() {
		t.Fatalf("the first open: created %v, %v", first != nil && first.Created(), err)
	}
	again, err := OpenKeyring(path, func(string) (string, bool) { return "", false })
	if err != nil || again.Created() {
		t.Fatalf("a reopen: created %v, %v", again != nil && again.Created(), err)
	}
	env := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{3}, 32))
	if kr, err := OpenKeyring(path, func(string) (string, bool) { return env, true }); err != nil || kr.Created() {
		t.Fatalf("an environment key: %v", err)
	}
}
