package core

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// DeviceFile is the file in the data dir that holds this device's id.
const DeviceFile = "device-id"

// DeviceID is this device's id, which every wake directory row a serve registers names (P-167):
// serves on several devices may share one database (Postgres), and a row's pid names a process
// only on the device that registered it. It is read from DeviceFile in dir, the per-user data dir
// on this machine's disk, which is made with a random id the first time (0600, its directory 0700):
// a restart, and every serve of the same data dir, keeps the id; another machine has its own.
func DeviceID(dir string) (string, error) {
	if dir == "" {
		return "", errors.New("device id: no data dir")
	}
	path := filepath.Join(dir, DeviceFile)
	for range 2 {
		if b, err := os.ReadFile(path); err == nil {
			id := strings.TrimSpace(string(b))
			if id == "" || len(id) > 64 || strings.ContainsAny(id, " \t\r\n") {
				return "", fmt.Errorf("device id: %s holds %q: want one word of at most 64 bytes", path, id)
			}
			return id, nil
		} else if !errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("device id: %w", err)
		}
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return "", fmt.Errorf("device id: %w", err)
		}
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, fs.ErrExist) {
			continue // another serve made it first: read its id
		}
		if err != nil {
			return "", fmt.Errorf("device id: %w", err)
		}
		raw := make([]byte, 16)
		if _, err := rand.Read(raw); err != nil {
			f.Close()
			return "", fmt.Errorf("device id: %w", err)
		}
		id := hex.EncodeToString(raw)
		_, err = f.WriteString(id + "\n")
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			os.Remove(path)
			return "", fmt.Errorf("device id: %w", err)
		}
		return id, nil
	}
	return "", fmt.Errorf("device id: %s was made and removed while being read", path)
}
