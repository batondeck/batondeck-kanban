package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/core"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// doctor reports the configuration and which engine is live, as the connection itself answers
// (`SELECT sqlite_version()` / `SELECT version()`), not as the config names it, and checks what serve
// refuses to start on: the schema, the master key and the blob dir. Each is a line, ok, WARN or
// FAIL; it exits 1 when any fails. It creates nothing it checks: not the SQLite file, not
// master.key, not the blob dir, and it writes nothing to the database.
func doctor(args []string, stdout, stderr io.Writer) int {
	var cfgPath string
	fs := commonFlags("doctor", &cfgPath, stderr)
	serverHelp(fs, "Checks the config, the store (the engine the connection answers as, and its schema), the master key and the blob dir.")
	if code, ok := parseFlags(fs, args, stdout); !ok {
		return code
	}
	if !noArgs(fs) {
		return 2
	}
	cfg, err := loadConfig(cfgPath)
	if err != nil {
		fmt.Fprintf(stdout, "FAIL config       %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, "ok   config")
	failed := false
	line := func(status, what, format string, a ...any) {
		if status == "FAIL" {
			failed = true
		}
		fmt.Fprintf(stdout, "%-5s%-13s%s\n", status, what, fmt.Sprintf(format, a...))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	doctorStore(ctx, cfg, line)
	doctorKeyring(cfg, cfgPath, line)
	doctorBlobs(cfg.BlobDir(), line)
	if failed {
		return 1
	}
	return 0
}

// doctorLine prints one of doctor's lines: its status (ok, WARN or FAIL), what it checked, and what
// it found.
type doctorLine func(status, what, format string, a ...any)

// doctorStore is doctor's store and schema lines. A SQLite file not created yet is not a failure,
// and nor is a schema not migrated yet, which serve migrates as it starts.
func doctorStore(ctx context.Context, cfg *core.Config, line doctorLine) {
	// doctor diagnoses; it does not create the SQLite file it is asked about.
	st, err := openStore(ctx, cfg, false)
	if errors.Is(err, os.ErrNotExist) && cfg.Engine() == core.EngineSQLite {
		line("ok", "store", "sqlite at %s: not created yet; serve creates it", cfg.SQLitePath())
		return
	}
	if err != nil {
		line("FAIL", "store", "%s: %v", cfg.Engine(), err)
		return
	}
	defer st.Close()
	v, err := st.ServerVersion(ctx)
	if err != nil {
		line("FAIL", "store", "%s: %v", cfg.Engine(), err)
		return
	}
	line("ok", "store", "%s, the connection reports: %s", dataLine(cfg), v)
	if cfg.Engine() == core.EngineSQLite {
		if w := sqliteModeWarning(cfg.SQLitePath()); w != "" {
			line("WARN", "store", "%s", w)
		}
	}
	var version *store.SchemaVersionError
	switch err := st.SchemaCurrent(ctx); {
	case err == nil:
		line("ok", "schema", "migrated to this binary's version")
	case errors.As(err, &version) && version.Current < version.Target:
		line("WARN", "schema", "at version %d, this binary's is %d: serve migrates it as it starts; user and token need it migrated first (batondeck-kanban migrate)", version.Current, version.Target)
	default:
		line("FAIL", "schema", "%v", err)
	}
}

// doctorKeyring is doctor's master key line: BATONDECK_MASTER_KEY decodes to 32 bytes, or the key
// file serve would read (keyFilePath) is private and 32 bytes, or is not there yet (serve makes it).
// On Postgres with neither, serve runs without a keyring, which is a WARN.
func doctorKeyring(cfg *core.Config, cfgPath string, line doctorLine) {
	path, none := keyFilePath(cfg, cfgPath, os.LookupEnv)
	if none {
		line("WARN", "keyring", "none: serve runs without one, refusing attach_file and wake-session inbox tokens; set %s, or run with -config", core.MasterKeyEnv)
		return
	}
	fromEnv, missing, err := core.CheckMasterKey(path, os.LookupEnv)
	switch {
	case err != nil:
		line("FAIL", "keyring", "%v", err)
	case fromEnv:
		line("ok", "keyring", "%s", core.MasterKeyEnv)
	case missing:
		line("ok", "keyring", "master.key at %s: not created yet; serve creates it", path)
	default:
		line("ok", "keyring", "master.key at %s", path)
	}
}

// doctorBlobs is doctor's blob dir line: the directory serve's blob store opens is one it can write
// in, or, not made yet, the nearest directory above it that exists is one serve can make it in.
// Writing is tried with a temporary directory, removed at once: a permission check alone passes for
// root where a mkdir still fails.
func doctorBlobs(dir string, line doctorLine) {
	probe := dir
	for {
		fi, err := os.Stat(probe)
		if err == nil {
			if !fi.IsDir() {
				line("FAIL", "blobs", "%s: %s is not a directory", dir, probe)
				return
			}
			break
		}
		parent := filepath.Dir(probe)
		if !errors.Is(err, fs.ErrNotExist) || parent == probe {
			line("FAIL", "blobs", "%s: %v", dir, err)
			return
		}
		probe = parent
	}
	tmp, err := os.MkdirTemp(probe, ".batondeck-doctor-")
	if err != nil {
		line("FAIL", "blobs", "%s: serve cannot make it or write in it: %v", dir, err)
		return
	}
	os.Remove(tmp)
	if probe != dir {
		line("ok", "blobs", "%s: not created yet; serve creates it", dir)
		return
	}
	line("ok", "blobs", "%s", dir)
}
