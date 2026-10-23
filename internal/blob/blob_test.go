package blob

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func signer(t *testing.T, b byte) *Signer {
	t.Helper()
	s, err := NewSigner(bytes.Repeat([]byte{b}, 32))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// TestVerifyRefusesEveryAlteredTriple: the signature verifies for its own (id, purpose, expiry)
// before the expiry (the positive control), and is ErrBadSignature for another purpose, id, expiry,
// key, a flipped digit or none, and ErrExpired at and after its expiry.
func TestVerifyRefusesEveryAlteredTriple(t *testing.T) {
	s := signer(t, 1)
	now := time.Unix(1_000_000, 0)
	exp := now.Add(15 * time.Minute)
	e := "1000900"
	sig := s.Sign("P-1.T-1.A-1", Upload, exp)
	if err := s.Verify("P-1.T-1.A-1", Upload, e, sig, now); err != nil {
		t.Fatalf("the valid signature: %v", err)
	}
	flipped := []byte(sig)
	flipped[0] ^= 1
	for name, c := range map[string]struct {
		id, exp, sig string
		p            Purpose
		s            *Signer
	}{
		"another purpose": {"P-1.T-1.A-1", e, sig, Download, s},
		"another id":      {"P-1.T-1.A-2", e, sig, Upload, s},
		"another expiry":  {"P-1.T-1.A-1", "1000901", sig, Upload, s},
		"another key":     {"P-1.T-1.A-1", e, sig, Upload, signer(t, 2)},
		"a flipped digit": {"P-1.T-1.A-1", e, string(flipped), Upload, s},
		"no signature":    {"P-1.T-1.A-1", e, "", Upload, s},
		"no expiry":       {"P-1.T-1.A-1", "", sig, Upload, s},
	} {
		if err := c.s.Verify(c.id, c.p, c.exp, c.sig, now); !errors.Is(err, ErrBadSignature) {
			t.Errorf("%s: %v, want ErrBadSignature", name, err)
		}
	}
	for _, at := range []time.Time{exp, exp.Add(time.Second)} {
		if err := s.Verify("P-1.T-1.A-1", Upload, e, sig, at); !errors.Is(err, ErrExpired) {
			t.Errorf("at %v: %v, want ErrExpired", at.Unix(), err)
		}
	}
}

// TestURLCarriesWhatVerifyReads: the URL's path segment and query verify as signed.
func TestURLCarriesWhatVerifyReads(t *testing.T) {
	s := signer(t, 1)
	exp := time.Unix(2_000_000_000, 0)
	u, err := url.Parse(s.URL("http://h:1/", "P-1.T-2.A-3", Download, exp))
	if err != nil {
		t.Fatal(err)
	}
	if u.Path != "/blobs/P-1.T-2.A-3" {
		t.Fatalf("path %q", u.Path)
	}
	if err := s.Verify("P-1.T-2.A-3", Download, u.Query().Get("exp"), u.Query().Get("sig"), time.Unix(1, 0)); err != nil {
		t.Fatalf("Verify(URL) = %v", err)
	}
	if _, err := NewSigner(make([]byte, 31)); err == nil {
		t.Error("a 31-byte key was accepted")
	}
}

func TestRefRoundTripsAndRefusesOtherShapes(t *testing.T) {
	r := Ref{ProjectID: "P-ab", TaskID: "T-7", AttachmentID: "A-cd"}
	if got, ok := ParseRef(r.String()); !ok || got != r {
		t.Fatalf("ParseRef(%q) = %+v, %v", r.String(), got, ok)
	}
	for _, s := range []string{"", "P.T", "P.T.A.B", ".T.A", "P..A", "P.T."} {
		if _, ok := ParseRef(s); ok {
			t.Errorf("ParseRef(%q) accepted", s)
		}
	}
}

// TestStageCommitOpenRemove: a staged body is named by its hash, opens byte-identical after the
// commit, and is gone after Remove (a second Remove is not an error); a discarded stage and a failed
// read leave nothing under tmp; a name that is not a hash is refused before any path is built.
func TestStageCommitOpenRemove(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	body := []byte("hello blob")
	sum := sha256.Sum256(body)
	want := hex.EncodeToString(sum[:])
	st, err := s.Stage(bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if st.SHA256 != want || st.Bytes != int64(len(body)) {
		t.Fatalf("staged %s %d, want %s %d", st.SHA256, st.Bytes, want, len(body))
	}
	if err := s.Commit(st); err != nil {
		t.Fatal(err)
	}
	f, err := s.OpenFile(want)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(f)
	f.Close()
	if !bytes.Equal(got, body) {
		t.Fatalf("read back %q", got)
	}
	if _, err := os.Stat(filepath.Join(dir, want[:2], want)); err != nil {
		t.Fatalf("the file is not at its hash's path: %v", err)
	}
	if err := s.Remove(want); err != nil {
		t.Fatal(err)
	}
	if err := s.Remove(want); err != nil {
		t.Fatalf("a second Remove: %v", err)
	}
	if _, err := s.OpenFile(want); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("OpenFile after Remove: %v", err)
	}

	st2, err := s.Stage(strings.NewReader("discarded"))
	if err != nil {
		t.Fatal(err)
	}
	st2.Discard()
	if _, err := s.Stage(io.MultiReader(strings.NewReader("part"), failing{})); err == nil {
		t.Fatal("a failed read staged")
	}
	if left, _ := os.ReadDir(filepath.Join(dir, "tmp")); len(left) != 0 {
		t.Fatalf("tmp holds %v", left)
	}
	for _, bad := range []string{"../../etc/passwd", strings.Repeat("A", 64), ""} {
		if _, err := s.OpenFile(bad); err == nil || errors.Is(err, os.ErrNotExist) {
			t.Errorf("OpenFile(%q) = %v, want the hash refusal", bad, err)
		}
		if err := s.Remove(bad); err == nil {
			t.Errorf("Remove(%q) accepted", bad)
		}
	}
}

type failing struct{}

func (failing) Read([]byte) (int, error) { return 0, errors.New("read failed") }

// TestLockSerialisesOneHash: a second Lock of a held hash waits for its unlock; another hash does not.
func TestLockSerialisesOneHash(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a, b := strings.Repeat("a", 64), strings.Repeat("b", 64)
	unlock := s.Lock(a)
	s.Lock(b)()
	got := make(chan struct{})
	go func() { s.Lock(a)(); close(got) }()
	select {
	case <-got:
		t.Fatal("a second Lock of a held hash did not wait")
	case <-time.After(50 * time.Millisecond):
	}
	unlock()
	select {
	case <-got:
	case <-time.After(2 * time.Second):
		t.Fatal("the waiter never got the lock")
	}
	if len(s.locks) != 0 {
		t.Fatalf("locks left: %v", s.locks)
	}
}
