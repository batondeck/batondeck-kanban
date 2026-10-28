# Architecture decision records

Each file here is one decision the code, the tests and `docs/release/plan-2026-10-22.md` cite by id
(`ADR-NNNN` is `NNNN-slug.md`). Where an ADR and the plan disagree, the ADR wins: the plan of record
is kept as written, with a "Superseded by" banner on each passage an ADR replaced.

The records were written in the maintainers' planning workspace and are copied here unchanged,
except for amendments marked in place with their date. The task ids they name (`R0.23`, `P-100` and
the like) and the reports they cite are the workspace's; they are kept for the history, and nothing
in this repository depends on them.

`go test ./internal/arch/` (the `arch` step of `make check`) fails when any file in the tree cites an
`ADR-NNNN` that has no record here.
