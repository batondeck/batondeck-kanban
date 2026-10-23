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
- `Run(t, newStore)`: judges one engine by lifecycle and then every area of `areas` (the 24 of ADR-0003 B2 and R0.13's oauth,
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
| oauth (`conformance_oauth.go`) | clients read back, touched and collected when never used (their codes, families and tokens with them); codes and tokens consumed once (ErrOAuthUsed after, one winner of ten concurrent calls, nothing kept by a rollback); a family revoked once, read by every token of it; the foreign keys; ErrNotFound |
| tasks (`conformance_tasks.go`) | `T-n` per project, create and read back, patch, status and order writes at a version (STALE otherwise, one winner of ten), pages by cursor, filters, counts, lookups by id, delete; the board's counts and column removal over real tasks; each with its refusals |
| claims (`conformance_claims.go`) | claim by id and from the frontier, heartbeat, release, expired leases, the frontier's order and filters; CONFLICT_LOCKED, LEASE_EXPIRED, INVALID_TRANSITION; 50 concurrent `ClaimNext` callers, 50 distinct winners |
| events (`conformance_events.go`) | task and board events newest first, the board feed, cursors, keyed by project, gone with their task; each with its refusals |
| changelog (`conformance_changelog.go`) | rising ids, reads after a cursor of every scope and of one, nothing kept by a rollback, the retention delete; each with its refusals |
| context (`conformance_context.go`) | append, newest- and oldest-first paging by cursor, per-project task ids, per-task delete; NOT_FOUND task, VALIDATION kind/body/author/cursor |
| memory (`conformance_memory.go`) | the three task scopes and agent privacy, recall by project and by identity, a 1 s TTL readable at once and absent after 2 s before any sweep, the bounded sweep, per-task delete, a project entry keeping its task as provenance after the task is deleted (migration 0003); the routing refusals |
| summaries (`conformance_summaries.go`) | empty until set, set under the task's version without bumping it, STALE with expected/actual writing nothing, NOT_FOUND task |
| dependencies (`conformance_dependencies.go`) | an edge and both caches together, a refused cycle (two-, self- and three-task) leaving no partial edge or cache entry, rollback inside `Atomically`, relates edges, the repeated edge, removal, `SetCaches`, project isolation; NOT_FOUND and VALIDATION |
| subtasks (`conformance_subtasks.go`) | children in task-number order (T-2..T-11), none, project isolation, NOT_FOUND parent |
| audit (`conformance_audit.go`) | every field read back, newest first, five filters, paging by cursor; VALIDATION |
| toolcalls (`conformance_toolcalls.go`) | a batch lands whole, a bad row refuses the batch, the bounded delete by age; VALIDATION |
| agentsessions (`conformance_agentsessions.go`) | the connection upsert, the activity touch, listing, the idle delete, a touched session outliving a 2 s idle sweep, delete; NOT_FOUND and VALIDATION |
| wakesessions (`conformance_wakesessions.go`) | put, get, replace keeping created-at, delete, expiry at the read; NOT_FOUND and VALIDATION |
| attachments (`conformance_attachments.go`) | pending then ready on an existing blob, oldest-first list, per-task delete, project isolation; NOT_FOUND task/attachment/blob, VALIDATION |
| blobs (`conformance_blobs.go`) | put once, get, delete, a referenced blob kept, the unreferenced list by age and limit; VALIDATION |
| search (`conformance_search.go`) | ten pinned queries over one corpus with their ordered ids and counts, cursor paging through ranks and ties, no hit; VALIDATION |

The two-statement pin on `GetBoard` is `internal/store/boards_test.go`; task counts over real tasks and
the refusal to remove a column holding a task are there too, and in the tasks battery.

The twelve areas from context on are store contract II (`internal/store/contract_<area>.go`). Each
registers its battery from an init, and its subtest SKIPS, naming the task that builds it, while the
store's methods are unbuilt: the engines' stubs (`internal/store/stubs_contract_ii.go`) and memstore's
(`internal/store/memstore/unbuilt_contract_ii.go`, through `store.Unbuilt`) return INTERNAL with the task
under the detail `unbuilt` (`internal/store/unbuilt.go`), and `store.UnbuiltTask` is what `area`
(`conformance_seed.go`) checks; `noErr` and `wantErr` skip on it too. Every other outcome of a built area fails, including a missing seeder: a battery standing
on projects, boards and tasks takes them from the store's `seeder`, and a store that is built but offers
none fails the battery rather than skipping it. `WithSeeder(store)` (`conformance_seed.go`) is that
seeder, over the store's own `Projects`, `Boards` and `Tasks`; the three factories below return their
store through it. `TestContractIIBatteriesOnAReference` (`reference_test.go`) runs all twelve against a
map-backed reference that keeps the contract, which is where the batteries of the areas no store has
built yet are proven to pass and to fail.

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
