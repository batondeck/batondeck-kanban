package blob

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/domain"
)

// Store is the blob files under one directory: each at dir/<first two hex digits>/<sha256>, written
// once through Stage and Commit, and dir/tmp for the uploads still being read. Lock serialises the
// commit of a hash with its removal inside this process, so a delete that judged the hash
// unreferenced cannot unlink a file an upload of the same content has just committed again.
// ponytail: an in-process lock is the ceiling for one binary on one disk; several processes on a
// shared directory need the lock in the database.
type Store struct {
	dir   string
	mu    sync.Mutex
	locks map[string]*hashLock
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

// tmpPrefix starts the name of every temporary file Stage writes.
const tmpPrefix = "upload-"

// Stage reads r whole to a temporary file, hashing it, and syncs it to disk, so the row a commit
// writes after it never names bytes still only in the page cache. On any error the file is removed.
func (s *Store) Stage(r io.Reader) (*Staged, error) {
	f, err := os.CreateTemp(filepath.Join(s.dir, "tmp"), tmpPrefix+"*")
	if err != nil {
		return nil, fmt.Errorf("blob: %w", err)
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), r)
	if err == nil {
		err = f.Sync()
	}
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
// replaced. The directory entry is synced before it returns (and the store's directory too when
// the hash's directory is new), so the move survives a power loss once a row names the hash. Call
// it under Lock(st.SHA256).
func (s *Store) Commit(st *Staged) error {
	p, err := s.path(st.SHA256)
	if err != nil {
		return err
	}
	parent := filepath.Dir(p)
	_, statErr := os.Stat(parent)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return fmt.Errorf("blob: %w", err)
	}
	if err := os.Rename(st.tmp, p); err != nil {
		return fmt.Errorf("blob: %w", err)
	}
	if err := syncDir(parent); err != nil {
		return err
	}
	if errors.Is(statErr, fs.ErrNotExist) {
		return syncDir(s.dir)
	}
	return nil
}

// syncDir flushes dir's entries to disk.
func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("blob: %w", err)
	}
	err = d.Sync()
	if cerr := d.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("blob: syncing %s: %w", dir, err)
	}
	return nil
}

// Walk calls fn for each committed blob file, in hash order, with its hash and modification time
// (when its bytes were last written: the upload that committed it). Entries that are not a blob
// file at its hash's path are skipped. An error from fn stops the walk and is Walk's.
func (s *Store) Walk(fn func(sha string, modTime time.Time) error) error {
	subs, err := os.ReadDir(s.dir)
	if err != nil {
		return fmt.Errorf("blob: %w", err)
	}
	for _, sub := range subs {
		if !sub.IsDir() || len(sub.Name()) != 2 {
			continue
		}
		entries, err := os.ReadDir(filepath.Join(s.dir, sub.Name()))
		if err != nil {
			return fmt.Errorf("blob: %w", err)
		}
		for _, e := range entries {
			sha := e.Name()
			if !e.Type().IsRegular() || !domain.ValidSHA256(sha) || sha[:2] != sub.Name() {
				continue
			}
			info, err := e.Info()
			if errors.Is(err, fs.ErrNotExist) {
				continue // removed since the read
			} else if err != nil {
				return fmt.Errorf("blob: %w", err)
			}
			if err := fn(sha, info.ModTime()); err != nil {
				return err
			}
		}
	}
	return nil
}

// SweepTmp removes the temporary files Stage left that were last written before olderThan (an
// upload cut off by a crash or a kill, which no Discard reached), and answers how many it removed.
// An upload in flight writes its file as its body arrives, and its read deadline is its URL's
// expiry, so an olderThan well before that leaves every live upload alone.
func (s *Store) SweepTmp(olderThan time.Time) (int, error) {
	dir := filepath.Join(s.dir, "tmp")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, fmt.Errorf("blob: %w", err)
	}
	removed := 0
	var errs []error
	for _, e := range entries {
		if !e.Type().IsRegular() || !strings.HasPrefix(e.Name(), tmpPrefix) {
			continue
		}
		info, err := e.Info()
		if err != nil || !info.ModTime().Before(olderThan) {
			continue
		}
		if err := os.Remove(filepath.Join(dir, e.Name())); err == nil {
			removed++
		} else if !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	if err := errors.Join(errs...); err != nil {
		return removed, fmt.Errorf("blob: %w", err)
	}
	return removed, nil
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
