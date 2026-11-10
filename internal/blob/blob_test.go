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
	"slices"
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

// TestWalkAndSweepTmp: Walk yields each committed file once, with its hash and modification time,
// and skips what is not a blob file at its hash's path (tmp, a stray file, a hash under the wrong
// prefix); SweepTmp removes only the upload temp files last written before its time.
func TestWalkAndSweepTmp(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * time.Hour).Truncate(time.Second)
	var want []string
	for _, b := range []string{"one", "two"} {
		st, err := s.Stage(strings.NewReader(b))
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Commit(st); err != nil {
			t.Fatal(err)
		}
		want = append(want, st.SHA256)
	}
	slices.Sort(want)
	if err := os.Chtimes(filepath.Join(dir, want[0][:2], want[0]), old, old); err != nil {
		t.Fatal(err)
	}
	misplaced := "ff" + want[0][2:]
	if err := os.MkdirAll(filepath.Join(dir, want[1][:2]), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{filepath.Join(dir, "stray"), filepath.Join(dir, want[1][:2], misplaced), filepath.Join(dir, want[1][:2], "notes.txt")} {
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var got []string
	mods := map[string]time.Time{}
	if err := s.Walk(func(sha string, mod time.Time) error { got = append(got, sha); mods[sha] = mod; return nil }); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, want) || !mods[want[0]].Equal(old) {
		t.Fatalf("Walk %v (mod %v), want %v (the first at %v)", got, mods[want[0]], want, old)
	}

	for _, b := range []string{"stale", "live"} {
		if _, err := s.Stage(strings.NewReader(b)); err != nil {
			t.Fatal(err)
		}
	}
	other := filepath.Join(dir, "tmp", "not-an-upload")
	if err := os.WriteFile(other, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	tmps, _ := filepath.Glob(filepath.Join(dir, "tmp", tmpPrefix+"*"))
	for _, p := range []string{tmps[0], other} {
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := s.SweepTmp(time.Now().Add(-time.Hour)); err != nil || n != 1 {
		t.Fatalf("SweepTmp: %d, %v; want 1 removed", n, err)
	}
	left, _ := filepath.Glob(filepath.Join(dir, "tmp", "*"))
	if !slices.Equal(left, []string{other, tmps[1]}) && !slices.Equal(left, []string{tmps[1], other}) {
		t.Fatalf("tmp holds %v, want %s and %s", left, other, tmps[1])
	}
}
