package blob

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sync"
	"syscall"

	"github.com/batondeck/batondeck-kanban/internal/domain"
)

// Store is the blob files under one directory: each at dir/<first two hex digits>/<sha256>, written
// once through Stage and Commit, and dir/tmp for the uploads still being read. Lock serialises the
// commit of a hash with its removal inside this process, so a delete that judged the hash
// unreferenced cannot unlink a file an upload of the same content has just committed again.
// ponytail: an in-process lock is the ceiling for one binary on one disk; several processes on a
// shared directory need the lock in the database.
type Store struct {
	// Reserve is the free space, in bytes, uploads leave on the volume dir is on: Stage cuts off a
	// body that would take the volume below it, and Room refuses a size that would (ErrFull), so
	// attachments cannot fill the disk the database shares. Zero checks nothing.
	Reserve int64

	dir   string
	mu    sync.Mutex
	locks map[string]*hashLock
}

// ErrFull is Stage's and Room's refusal: the store's volume would be left with less than Reserve
// free.
var ErrFull = errors.New("blob: the volume is down to its reserve")

// statFree is the bytes an unprivileged writer may still use on the volume dir is on; a test swaps it.
var statFree = func(dir string) (int64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0, err
	}
	return int64(min(st.Bavail*uint64(st.Bsize), math.MaxInt64)), nil
}

// Room is ErrFull when n more bytes would leave the store's volume with less than Reserve free. A
// volume whose free space cannot be read is not refused.
func (s *Store) Room(n int64) error {
	if s.Reserve <= 0 {
		return nil
	}
	free, err := statFree(s.dir)
	if err != nil {
		return nil
	}
	if free-n < s.Reserve {
		return ErrFull
	}
	return nil
}

// roomEvery is how many bytes Stage writes between two looks at the volume's free space.
const roomEvery = 1 << 20

// roomWriter is w that looks at the store's volume every roomEvery bytes, before the write that
// crosses the mark: ErrFull when that write, or roomEvery bytes if it is smaller, would leave the
// volume under its reserve.
type roomWriter struct {
	w       io.Writer
	s       *Store
	pending int64
}

func (r *roomWriter) Write(p []byte) (int, error) {
	if r.pending += int64(len(p)); r.pending >= roomEvery {
		r.pending = 0
		if err := r.s.Room(max(int64(len(p)), roomEvery)); err != nil {
			return 0, err
		}
	}
	return r.w.Write(p)
}

type hashLock struct {
	mu   sync.Mutex
	refs int
}

// Open is the store under dir, creating dir and dir/tmp (0700) when missing.
func Open(dir string) (*Store, error) {
	if dir == "" {
		return nil, errors.New("blob: no directory")
	}
	if err := os.MkdirAll(filepath.Join(dir, "tmp"), 0o700); err != nil {
		return nil, fmt.Errorf("blob: %w", err)
	}
	return &Store{dir: dir, locks: map[string]*hashLock{}}, nil
}

// Dir is the store's directory.
func (s *Store) Dir() string { return s.dir }

func (s *Store) path(sha string) (string, error) {
	if !domain.ValidSHA256(sha) {
		return "", fmt.Errorf("blob: %q is not a sha256", sha)
	}
	return filepath.Join(s.dir, sha[:2], sha), nil
}

// Staged is a body read to a temporary file: its hash and size, not yet a blob.
type Staged struct {
	SHA256 string
	Bytes  int64
	tmp    string
}

// Stage reads r whole to a temporary file, hashing it. On any error the file is removed. With a
// Reserve it is ErrFull, the body cut off, once the volume is down to the reserve (Room).
func (s *Store) Stage(r io.Reader) (*Staged, error) {
	if err := s.Room(0); err != nil {
		return nil, err
	}
	f, err := os.CreateTemp(filepath.Join(s.dir, "tmp"), "upload-*")
	if err != nil {
		return nil, fmt.Errorf("blob: %w", err)
	}
	h := sha256.New()
	var w io.Writer = io.MultiWriter(f, h)
	if s.Reserve > 0 {
		w = &roomWriter{w: w, s: s}
	}
	n, err := io.Copy(w, r)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(f.Name())
		return nil, err
	}
	return &Staged{SHA256: hex.EncodeToString(h.Sum(nil)), Bytes: n, tmp: f.Name()}, nil
}

// Discard removes a staged file that will not be committed.
func (st *Staged) Discard() { _ = os.Remove(st.tmp) }

// Commit moves the staged file to its hash's path; a file already there has the same content and is
// replaced. Call it under Lock(st.SHA256).
func (s *Store) Commit(st *Staged) error {
	p, err := s.path(st.SHA256)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return fmt.Errorf("blob: %w", err)
	}
	if err := os.Rename(st.tmp, p); err != nil {
		return fmt.Errorf("blob: %w", err)
	}
	return nil
}

// OpenFile opens the blob's file for reading.
func (s *Store) OpenFile(sha string) (*os.File, error) {
	p, err := s.path(sha)
	if err != nil {
		return nil, err
	}
	return os.Open(p)
}

// Remove unlinks the blob's file; a file already gone is not an error. Call it under Lock(sha).
func (s *Store) Remove(sha string) error {
	p, err := s.path(sha)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("blob: %w", err)
	}
	return nil
}

// Lock holds sha's lock until the returned func is called.
func (s *Store) Lock(sha string) (unlock func()) {
	s.mu.Lock()
	l := s.locks[sha]
	if l == nil {
		l = &hashLock{}
		s.locks[sha] = l
	}
	l.refs++
	s.mu.Unlock()
	l.mu.Lock()
	return func() {
		l.mu.Unlock()
		s.mu.Lock()
		if l.refs--; l.refs == 0 {
			delete(s.locks, sha)
		}
		s.mu.Unlock()
	}
}
