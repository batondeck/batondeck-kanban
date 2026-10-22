# internal/store/conformance

The single test suite every `store.Store` engine must pass. SQLite and Postgres are two implementations
of one interface; this package is how they are kept from drifting apart, because both are judged by the
same tests. It is ordinary non-test Go (its files do not end in `_test.go`) that imports `testing`, so a
test in another package can call it. Copied from hdtp-gateway's `internal/core/store/conformance`, with
every HDTP area removed.

## What it holds

- `Migratable`: `store.Store` plus `MigrateDown`, which the suite needs for the up/down/up check and which
  is not on the interface.
- `Factory`: `func(t *testing.T) Migratable`, returning a new, empty, unmigrated store. The suite migrates
  it; clean-up is the factory's.
- `Run(t, newStore)`: judges one engine by every area, in a fixed order, each area a set of subtests that
  start from a fresh store.

## What it does not do yet

It holds no area and has no caller: the `Store` interface holds only its lifecycle, no migration exists,
and no test calls `Run`. A green run of this package proves nothing about either engine.

### How an engine plugs in

Write a `Factory` that opens the engine on an empty database (a fresh file, or a fresh database on a
shared server) and close it in `t.Cleanup`; return it from a test that calls `conformance.Run`:

```go
func TestMyEngineConformance(t *testing.T) {
	conformance.Run(t, func(t *testing.T) conformance.Migratable {
		s := openMyEngine(t) // empty and unmigrated
		t.Cleanup(func() { s.Close() })
		return s
	})
}
```
