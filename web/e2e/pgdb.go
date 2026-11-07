//go:build ignore

// pgdb creates or drops one database on the Postgres server a DSN names. The web UI's Playwright
// suite (web/e2e/global-setup.ts) runs it with `go run web/e2e/pgdb.go` so that a run on
// BATONDECK_TEST_POSTGRES_DSN gets a database of its own, as the Go store tests do, and leaves none
// behind. The ignore constraint keeps it out of ./... (vet, staticcheck, the tests); it uses the
// module's own pgx, so it adds no dependency.
//
//	go run web/e2e/pgdb.go create|drop DSN NAME
package main

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5"
)

// name is the only form this creates or drops, so a mistyped argument cannot drop another database.
var name = regexp.MustCompile(`^bk_e2e_[a-z0-9]{1,40}$`)

func main() {
	if len(os.Args) != 4 || (os.Args[1] != "create" && os.Args[1] != "drop") {
		fmt.Fprintln(os.Stderr, "usage: go run web/e2e/pgdb.go create|drop DSN NAME")
		os.Exit(2)
	}
	op, dsn, db := os.Args[1], os.Args[2], os.Args[3]
	if !name.MatchString(db) {
		fmt.Fprintf(os.Stderr, "pgdb: refused %q: a database name must match %s\n", db, name)
		os.Exit(2)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pgdb: connect: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close(ctx)
	stmt := "CREATE DATABASE " + db
	if op == "drop" {
		stmt = "DROP DATABASE IF EXISTS " + db + " WITH (FORCE)"
	}
	if _, err := conn.Exec(ctx, stmt); err != nil {
		fmt.Fprintf(os.Stderr, "pgdb: %s: %v\n", stmt, err)
		os.Exit(1)
	}
	fmt.Printf("pgdb: %s\n", stmt)
}
