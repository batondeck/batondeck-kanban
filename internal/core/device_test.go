package core

import (
	"os"
	"path/filepath"
	"testing"
)

// The device id is made once, 0600, and read back by every later call; a data dir of its own has
// another; a file holding no id is refused.
func TestDeviceID(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	id, err := DeviceID(dir)
	if err != nil || len(id) != 32 {
		t.Fatalf("DeviceID = %q, %v; want a 32-character id", id, err)
	}
	fi, err := os.Stat(filepath.Join(dir, DeviceFile))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("the device file: %v, mode %v; want 0600", err, fi.Mode())
	}
	if again, err := DeviceID(dir); err != nil || again != id {
		t.Fatalf("DeviceID again = %q, %v; want %q", again, err, id)
	}
	if other, err := DeviceID(t.TempDir()); err != nil || other == id {
		t.Fatalf("another data dir's DeviceID = %q, %v; want an id of its own", other, err)
	}
	if err := os.WriteFile(filepath.Join(dir, DeviceFile), []byte("\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := DeviceID(dir); err == nil {
		t.Fatalf("DeviceID of an empty file = %q, want an error", got)
	}
	if got, err := DeviceID(""); err == nil {
		t.Fatalf("DeviceID with no data dir = %q, want an error", got)
	}
}
