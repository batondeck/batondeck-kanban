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
	"sync/atomic"
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

// volume makes statFree report a volume of size bytes on which only dir/tmp's files take space.
func volume(t *testing.T, size int64) {
	t.Helper()
	orig := statFree
	statFree = func(dir string) (int64, error) {
		used := int64(0)
		ents, _ := os.ReadDir(filepath.Join(dir, "tmp"))
		for _, e := range ents {
			if fi, err := e.Info(); err == nil {
				used += fi.Size()
			}
		}
		return size - used, nil
	}
	t.Cleanup(func() { statFree = orig })
}

// Uploads leave Reserve free on the store's volume: a body that would take it below is cut off with
// ErrFull and leaves no file, and Room answers the same for a size before any byte is written, so
// attachments cannot fill the disk the database shares.
func TestStageKeepsTheReserveFree(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s.Reserve = 1 << 20
	volume(t, 3<<20)
	if _, err := s.Stage(bytes.NewReader(make([]byte, 4<<20))); !errors.Is(err, ErrFull) {
		t.Fatalf("staging 4 MiB on a 3 MiB volume with 1 MiB reserved: %v, want ErrFull", err)
	}
	// A body read in small pieces (an HTTP body) is cut off once it reaches the reserve.
	if _, err := s.Stage(struct{ io.Reader }{bytes.NewReader(make([]byte, 4<<20))}); !errors.Is(err, ErrFull) {
		t.Fatalf("staging 4 MiB piecewise: %v, want ErrFull", err)
	}
	if left, _ := os.ReadDir(filepath.Join(s.Dir(), "tmp")); len(left) != 0 {
		t.Fatalf("%d staged files left behind", len(left))
	}
	if err := s.Room(2<<20 + 1); !errors.Is(err, ErrFull) {
		t.Fatalf("Room past the reserve: %v, want ErrFull", err)
	}
	t.Run("Control", func(t *testing.T) {
		if err := s.Room(2 << 20); err != nil {
			t.Fatalf("Room up to the reserve: %v", err)
		}
		st, err := s.Stage(bytes.NewReader(make([]byte, 1<<20)))
		if err != nil {
			t.Fatalf("staging 1 MiB above the reserve: %v", err)
		}
		st.Discard()
		s.Reserve = 0
		st, err = s.Stage(bytes.NewReader(make([]byte, 4<<20)))
		if err != nil {
			t.Fatalf("no reserve: %v", err)
		}
		st.Discard()
	})
}

// Uploads running at the same time share the room above the reserve: each holds the bytes it is
// about to write, from before its first byte, so many slow bodies, each smaller than the stretch
// Stage writes between two looks at the volume, cannot together take it past the reserve. Room
// counts what they hold, and a body that starts at the reserve is refused before its first byte.
func TestConcurrentStagesKeepTheReserveFree(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	const reserve, room, uploads, body = 64 << 20, 1 << 20, 16, 600 << 10
	s.Reserve = reserve
	volume(t, reserve+room)
	type result struct {
		st  *Staged
		err error
	}
	results := make(chan result, uploads)
	release := make(chan struct{})
	var returned atomic.Int32
	for range uploads {
		pr, pw := io.Pipe()
		go func() {
			<-release
			_, _ = pw.Write(make([]byte, body))
			_ = pw.Close()
		}()
		go func() {
			st, err := s.Stage(pr)
			returned.Add(1)
			_ = pr.Close() // a refused Stage read nothing: let its body's writer go
			results <- result{st, err}
		}()
	}
	// Every body is held until each Stage has opened its temp file or returned.
	tmp := filepath.Join(s.Dir(), "tmp")
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(time.Millisecond) {
		ents, _ := os.ReadDir(tmp)
		if len(ents)+int(returned.Load()) >= uploads {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d temp files and %d returned Stage calls after 10 s, want %d in all", len(ents), returned.Load(), uploads)
		}
	}
	if err := s.Room(1); !errors.Is(err, ErrFull) {
		t.Errorf("Room(1) while the uploads in flight hold the room above the reserve: %v, want ErrFull", err)
	}
	close(release)
	var staged []*Staged
	var total int64
	for range uploads {
		r := <-results
		switch {
		case r.err == nil:
			staged = append(staged, r.st)
			total += r.st.Bytes
		case !errors.Is(r.err, ErrFull):
			t.Errorf("Stage: %v, want nil or ErrFull", r.err)
		}
	}
	if total > room {
		t.Errorf("%d of %d uploads staged at once, %d bytes past the reserve", len(staged), uploads, total-room)
	}
	if len(staged) == 0 {
		t.Errorf("no upload staged, yet the room above the reserve holds one")
	}
	for _, st := range staged {
		st.Discard()
	}
	if left, _ := os.ReadDir(tmp); len(left) != 0 {
		t.Fatalf("%d temp files left behind", len(left))
	}
	// A body that starts with the volume at its reserve is refused before its first byte.
	volume(t, reserve)
	if st, err := s.Stage(struct{ io.Reader }{bytes.NewReader(make([]byte, body))}); !errors.Is(err, ErrFull) {
		t.Fatalf("staging %d bytes at the reserve: %v, want ErrFull", body, err)
	} else if st != nil {
		st.Discard()
	}
	if left, _ := os.ReadDir(tmp); len(left) != 0 {
		t.Fatalf("%d temp files left behind", len(left))
	}
}
