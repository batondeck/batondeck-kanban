// Package servicetest is the real service over a chosen engine, for the transports' tests (ADR-0003
// B3, the join): the store, a service.Core over it, its Services bundle and a bus on its change log.
// internal/mcp and internal/api may not open an engine themselves (§C row 6), so they take one here.
// BATONDECK_TEST_ENGINE picks the engine: unset runs every engine (the Postgres one skips without
// BATONDECK_TEST_POSTGRES_DSN, as storetest does), memstore, sqlite or postgres runs that one alone,
// and a named postgres without a DSN fails rather than skipping.
package servicetest

import (
	"context"
	"io"
	"log"
	"os"
	"testing"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/blob"
	"github.com/batondeck/batondeck-kanban/internal/bus"
	"github.com/batondeck/batondeck-kanban/internal/core"
	"github.com/batondeck/batondeck-kanban/internal/service"
	"github.com/batondeck/batondeck-kanban/internal/store"
	"github.com/batondeck/batondeck-kanban/internal/store/memstore"
	"github.com/batondeck/batondeck-kanban/internal/store/storetest"
)

// EngineEnv names the engine the real-stack tests run on.
const EngineEnv = "BATONDECK_TEST_ENGINE"

// The engine names EngineEnv takes.
const (
	Memstore = "memstore"
	SQLite   = "sqlite"
	Postgres = "postgres"
)

// all is every engine, in the order an unset EngineEnv runs them.
var all = []string{Memstore, SQLite, Postgres}

// Engines is the engines EngineEnv selects; an unknown value fails t.
func Engines(t testing.TB) []string {
	t.Helper()
	v := os.Getenv(EngineEnv)
	if v == "" {
		return all
	}
	for _, e := range all {
		if v == e {
			return []string{e}
		}
	}
	t.Fatalf("%s=%q: want %s, %s or %s", EngineEnv, v, Memstore, SQLite, Postgres)
	return nil
}

// OpenStore is a migrated store of engine, closed (and on Postgres dropped) at t's end.
func OpenStore(t testing.TB, engine string) store.Store {
	t.Helper()
	switch engine {
	case Memstore:
		st := memstore.New()
		if _, err := st.Migrate(context.Background()); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = st.Close() })
		return st
	case SQLite:
		return storetest.SQLite(t)
	case Postgres:
		if os.Getenv(EngineEnv) == Postgres && os.Getenv("BATONDECK_TEST_POSTGRES_DSN") == "" {
			t.Fatalf("%s=postgres and BATONDECK_TEST_POSTGRES_DSN is unset: a skipped engine is not a green one", EngineEnv)
		}
		return storetest.Postgres(t)
	}
	t.Fatalf("no engine %q", engine)
	return nil
}

// Stack is the real service over one engine.
type Stack struct {
	Engine   string
	Store    store.Store
	Core     *service.Core
	Services service.Services
	Bus      *bus.Bus
	// Keys is the core's sealer: a keyring under a fixed test master key.
	Keys *core.Keyring
}

// testMasterKey is the stack's keyring master key: 32 bytes of a test fixture, base64.
const testMasterKey = "c2VydmljZXRlc3QgbWFzdGVyIGtleSwgMzIgYnl0ZXM="

// Keyring is a keyring under the fixed test master key.
func Keyring(t testing.TB) *core.Keyring {
	t.Helper()
	k, err := core.OpenKeyring("", func(name string) (string, bool) { return testMasterKey, name == core.MasterKeyEnv })
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// Open is the stack over a fresh store of engine. The bus wakes on the engine's commit
// (store.ChangeWatcher), as serve wires it; memstore has none, so it polls every 10 ms. The blobs
// live in t's temporary directory under a fixed test signing key, so attach_file is served. The
// tool calls are flushed by hand (ToolCallFlush -1). Everything closes at t's end.
func Open(t testing.TB, engine string) *Stack {
	t.Helper()
	st := OpenStore(t, engine)
	files, err := blob.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	signer, err := blob.NewSigner([]byte("servicetest signing key, 32 bytes or more"))
	if err != nil {
		t.Fatal(err)
	}
	media := service.MediaOptions{Files: files, Signer: signer, BaseURL: "http://servicetest.invalid"}
	keys := Keyring(t)
	c := service.New(st, service.Options{Logger: log.New(io.Discard, "", 0), ToolCallFlush: -1, Media: media, Sealer: keys})
	watcher, _ := st.(store.ChangeWatcher)
	b := bus.New(st, bus.Options{Watcher: watcher, PollInterval: 10 * time.Millisecond})
	t.Cleanup(func() {
		b.Close()
		_ = c.Close(context.Background())
	})
	return &Stack{Engine: engine, Store: st, Core: c, Services: ServicesOf(c), Bus: b, Keys: keys}
}

// ServicesOf is c's Services bundle, every interface the real service implements.
func ServicesOf(c *service.Core) service.Services {
	return service.Services{Projects: c.Projects(), Boards: c.Boards(), Tasks: c.Tasks(), Lifecycle: c.Lifecycle(),
		Context: c.Context(), Media: c.Media(), Artifacts: c.Artifacts(), Comments: c.Comments(), FollowUps: c.FollowUps(), Chronicle: c.Chronicle(), Feed: c.Feed(), Sessions: c.Sessions(), ToolCalls: c.ToolCalls(), Oncall: c.Oncall()}
}

// Run runs fn once per engine Engines selects, each a subtest named for its engine over a fresh
// Stack.
func Run(t *testing.T, fn func(t *testing.T, s *Stack)) {
	t.Helper()
	for _, e := range Engines(t) {
		t.Run(e, func(t *testing.T) { fn(t, Open(t, e)) })
	}
}
