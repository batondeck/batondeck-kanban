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

| Area (file) | Subtests |
|---|---|
| lifecycle (`conformance.go`) | `MigrateUpDownUp`; `ASecondMigrateAppliesNothing`; `TheSchemaIsCurrentOnceMigrated`; `ServerVersionIsReadFromTheConnection`; `AtomicallyReturnsItsCallbacksErrorAndNests` |
| projects and members (`conformance_projects.go`) | create, read, list, settings (transitionTable refused), recursive delete; add (an upsert), list, look up and remove members; each with its NOT_FOUND and VALIDATION cases |
| boards and columns (`conformance_boards.go`) | default and given columns, idempotent create, reads keyed by project, list, column add, update and remove, each with its refusals |
| counters (`conformance_counters.go`) | `T-n` per project, 100 concurrent calls handed T-1..T-100 once, rolled back with its transaction, NOT_FOUND |
| idempotency (`conformance_idempotency.go`) | replay inside the window, a second after it, scoped by identity, nothing kept on failure, one run under concurrency |

Task counts over real tasks, the two-statement pin on `GetBoard` and the refusal to remove a column
holding a task are `internal/store/boards_test.go`, on both engines, until the tasks area can create a
task through the Store.

## Its callers

`TestSQLiteConformance` (`sqlite_test.go`) opens a file under `t.TempDir()`. `TestPostgresConformance`
(`postgres_test.go`) creates a database `bk_conf_<n>` on the server `BATONDECK_TEST_POSTGRES_DSN` names
for each store the suite asks for, after `DROP DATABASE IF EXISTS` on that name; it skips, printing why,
when the variable is unset, and fails when it is set and the server does not answer. `make conformance`
(a step of `make check`) runs each by name.

## How an area plugs in

An area is `func <area>(t *testing.T, newStore Factory)` in its own file of this package
(`conformance_<area>.go`), and one line in `Run`. Each subtest asks `newStore` for its own store (or
`migrated(t, newStore)` for a migrated one) and calls it only through `store.Store`: no engine branch,
so a statement that differs between SQLite and Postgres fails in one of the two callers.

## What it does not do

It does not prove behaviour between processes: `TestWriteTransactionsOnOneFileSerialiseAtBegin`
(`internal/store/sqlite_test.go`) holds SQLite's `BEGIN IMMEDIATE` across stores on one file for
writes, and `TestFirstMigrationsOnOneFileBothSucceed` (`internal/cli/cli_test.go`) holds the
data-dir lock around two concurrent first migrations.
