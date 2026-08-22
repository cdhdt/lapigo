package ddl

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// TestEmit_GoldenApplies is the tier the golden comparison cannot be: it
// applies every committed .sql fixture to a real Postgres and fails on the
// server's own rejection. "It renders" is not the SQL analogue of "it
// compiles" -- applying is -- so an emitter change that produces invalid SQL
// (an unquoted keyword in a constraint position, a malformed CHECK, an FK
// naming a column the CREATE TABLE spells differently) fails here even though
// every byte-for-byte assertion in golden_test.go still passes, because none
// of those ask a database anything.
//
// The connection string comes from LAPIGO_TEST_DATABASE_URL, the variable
// .github/workflows/ci.yml already provisions a postgres:17 service for; the
// test skips cleanly when it is unset, so `make check` stays green on a
// machine with no Postgres and the Makefile needs no change.
//
// pgx is this repository's chosen driver for generated code (spec §2.3) and
// enters the generator's go.mod here, test-only, for the same reason: the
// apply tier is exactly where the generator's own tests need to speak
// Postgres the way the generated store will. Exec-ing psql instead would
// trade a declared module dependency for an undeclared environment one.
//
// Each fixture applies into its own fresh schema, dropped and recreated
// before use (several fixtures create the same table names) and dropped
// again on cleanup. conn.Exec sends the whole file as one simple-protocol
// batch, which Postgres runs in a single implicit transaction: the first
// rejected statement aborts the rest -- ON_ERROR_STOP semantics without
// psql -- and the returned error carries the server's own message.
func TestEmit_GoldenApplies(t *testing.T) {
	url := os.Getenv("LAPIGO_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("LAPIGO_TEST_DATABASE_URL not set; the apply tier needs a real Postgres")
	}

	for _, name := range goldenCases {
		t.Run(name, func(t *testing.T) {
			sql, err := os.ReadFile(filepath.Join("testdata", name+".sql"))
			if err != nil {
				t.Fatal(err)
			}

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			conn, err := pgx.Connect(ctx, url)
			if err != nil {
				t.Fatalf("connect: %v", err)
			}
			// Cleanups run LIFO: the schema drop registered second runs
			// before the connection close registered first.
			t.Cleanup(func() { _ = conn.Close(context.Background()) })

			// Fixture names are [a-z0-9_]+, so they embed in a schema name
			// verbatim; quoting anyway, as the DDL itself does everywhere.
			schema := "lapigo_apply_" + name
			if _, err := conn.Exec(ctx, fmt.Sprintf(
				`DROP SCHEMA IF EXISTS %s CASCADE; CREATE SCHEMA %s; SET search_path TO %s;`,
				quoteIdent(schema), quoteIdent(schema), quoteIdent(schema),
			)); err != nil {
				t.Fatalf("preparing fresh schema: %v", err)
			}
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				if _, err := conn.Exec(ctx, fmt.Sprintf("DROP SCHEMA IF EXISTS %s CASCADE", quoteIdent(schema))); err != nil {
					t.Errorf("dropping schema after test: %v", err)
				}
			})

			if _, err := conn.Exec(ctx, string(sql)); err != nil {
				t.Fatalf("fixture %s.sql was rejected by the server -- the emitted SQL does not apply:\n%v\n\n--- applied SQL ---\n%s",
					name, err, strings.TrimSpace(string(sql)))
			}
		})
	}
}
