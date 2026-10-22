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
- `Run(t, newStore)`: judges one engine by lifecycle and then every area of `areas` (all 24 of ADR-0003 B2,
  listed in `conformance.go` with the task that owns each), in that order, each a subtest named for the area
  whose own subtests start from a fresh store. An area whose battery file has not landed skips, naming its
  owner; a battery whose engine methods are still unbuilt (`store.UnbuiltTask`) skips, naming the task that
  builds them.

| Area (file) | Subtests |
|---|---|
| lifecycle (`conformance.go`) | `MigrateUpDownUp`; `ASecondMigrateAppliesNothing`; `TheSchemaIsCurrentOnceMigrated`; `ServerVersionIsReadFromTheConnection`; `AStoreUsedAfterItsAtomicallyReturnedWritesNothing`; `AtomicallyReturnsItsCallbacksErrorAndNests` |
| projects (`conformance_projects.go`) | create, read, list, settings (transitionTable refused), recursive delete; each with its NOT_FOUND and VALIDATION cases |
| members (`conformance_projects.go`) | add (an upsert), list, look up and remove members; each with its NOT_FOUND and VALIDATION cases |
| boards and columns (`conformance_boards.go`) | default and given columns, idempotent create, reads keyed by project, list, column add, update and remove, each with its refusals |
| counters (`conformance_counters.go`) | `T-n` per project, 100 concurrent calls handed T-1..T-100 once, rolled back with its transaction, NOT_FOUND |
| idempotency (`conformance_idempotency.go`) | replay inside the window, a second after it, scoped by identity, nothing kept on failure, one run under concurrency |
| users, sessions, tokens (`conformance_{users,sessions,tokens}.go`) | create and read back, lists in creation order, disable and revoke (the first time stands), ErrNotFound |
| tasks (`conformance_tasks.go`) | `T-n` per project, create and read back, patch, status and order writes at a version (STALE otherwise, one winner of ten), pages by cursor, filters, counts, lookups by id, delete; the board's counts and column removal over real tasks; each with its refusals |
| claims (`conformance_claims.go`) | claim by id and from the frontier, heartbeat, release, expired leases, the frontier's order and filters; CONFLICT_LOCKED, LEASE_EXPIRED, INVALID_TRANSITION; 50 concurrent `ClaimNext` callers, 50 distinct winners |
| events (`conformance_events.go`) | task and board events newest first, the board feed, cursors, keyed by project, gone with their task; each with its refusals |
| changelog (`conformance_changelog.go`) | rising ids, reads after a cursor of every scope and of one, nothing kept by a rollback, the retention delete; each with its refusals |

The two-statement pin on `GetBoard` is `internal/store/boards_test.go`; task counts over real tasks and
the refusal to remove a column holding a task are there too, and in the tasks battery.

## Its callers

`TestMemstoreConformance` (`memstore_test.go`) judges `internal/store/memstore`, the test-only in-memory
store. `TestSQLiteConformance` (`sqlite_test.go`) opens a file under `t.TempDir()`. `TestPostgresConformance`
(`postgres_test.go`) creates a database `bk_conf_<n>` on the server `BATONDECK_TEST_POSTGRES_DSN` names
for each store the suite asks for, after `DROP DATABASE IF EXISTS` on that name; it skips, printing why,
when the variable is unset, and fails when it is set and the server does not answer. `make conformance`
(a step of `make check`) runs each by name.

## How an area plugs in

An area is `func <area>(t *testing.T, newStore Factory)` in its own file of this package
(`conformance_<area>.go`), handed to `register("<area>", <area>)` from that file's `init`. Its line in
`areas` is already there, so a new area adds a file and no line; `register` panics on a name not in `areas`
or registered twice. Each subtest asks `newStore` for its own store (or
`migrated(t, newStore)` for a migrated one) and calls it only through `store.Store`: no engine branch,
so a statement that differs between SQLite and Postgres fails in one of the two callers.

## What it does not do

It does not prove behaviour between processes: `TestWriteTransactionsOnOneFileSerialiseAtBegin`
(`internal/store/sqlite_test.go`) holds SQLite's `BEGIN IMMEDIATE` across stores on one file for
writes, and `TestFirstMigrationsOnOneFileBothSucceed` (`internal/cli/cli_test.go`) holds the
data-dir lock around two concurrent first migrations.
