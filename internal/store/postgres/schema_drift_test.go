package postgres_test

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"

	"go.uber.org/zap"

	"github.com/agomez/pulsar/internal/store/postgres"
	"github.com/agomez/pulsar/internal/testutil"
)

// Docker and Helm bootstrap Postgres with an initdb script and then the
// controller runs migrations on start; systemd/external-Postgres installs run
// migrations only. Both paths must yield the same schema. Drift here has
// shipped real bugs (personal_access_tokens and identity_providers.slug
// existed only in initdb, so PATs and CreateIDP failed on migrated DBs).
func TestInitdbAndMigrationsProduceSameSchema(t *testing.T) {
	ctx := context.Background()

	migrated := testutil.Postgres(t)

	initdb := testutil.EmptyPostgres(t)
	sql, err := os.ReadFile("../../../deploy/docker/initdb/01_schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := initdb.Pool().Exec(ctx, string(sql)); err != nil {
		t.Fatalf("apply initdb script: %v", err)
	}
	if err := initdb.Migrate(ctx, zap.NewNop()); err != nil {
		t.Fatalf("migrate initdb-bootstrapped database: %v", err)
	}

	want, got := describe(t, migrated), describe(t, initdb)
	if missing, extra := diff(want, got); len(missing)+len(extra) > 0 {
		t.Fatalf("schema drift between migrations and deploy/docker/initdb/01_schema.sql\n"+
			"only in migrations:\n  %s\nonly in initdb+migrations:\n  %s",
			strings.Join(missing, "\n  "), strings.Join(extra, "\n  "))
	}
}

// describe lists columns and unique/primary keys (by column set, not index
// name, since names legitimately differ between the two paths).
func describe(t *testing.T, db *postgres.DB) []string {
	t.Helper()
	ctx := context.Background()
	var out []string
	rows, err := db.Pool().Query(ctx, `
		SELECT table_name, column_name, data_type, is_nullable, COALESCE(column_default, '')
		  FROM information_schema.columns
		 WHERE table_schema = 'public' AND table_name <> 'schema_migrations'`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var tbl, col, typ, null, def string
		if err := rows.Scan(&tbl, &col, &typ, &null, &def); err != nil {
			t.Fatal(err)
		}
		out = append(out, fmt.Sprintf("column %s.%s %s nullable=%s default=%s", tbl, col, typ, null, def))
	}
	rows.Close()

	rows, err = db.Pool().Query(ctx, `
		SELECT t.relname, array_to_string(array_agg(a.attname ORDER BY a.attname), ','),
		       pg_get_expr(i.indpred, i.indrelid) IS NOT NULL
		  FROM pg_index i
		  JOIN pg_class t ON t.oid = i.indrelid
		  JOIN pg_namespace n ON n.oid = t.relnamespace
		  JOIN pg_attribute a ON a.attrelid = t.oid AND a.attnum = ANY(i.indkey)
		 WHERE n.nspname = 'public' AND i.indisunique AND t.relname <> 'schema_migrations'
		 GROUP BY t.relname, i.indexrelid, i.indpred, i.indrelid`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var tbl, cols string
		var partial bool
		if err := rows.Scan(&tbl, &cols, &partial); err != nil {
			t.Fatal(err)
		}
		out = append(out, fmt.Sprintf("unique %s(%s) partial=%v", tbl, cols, partial))
	}
	rows.Close()
	sort.Strings(out)
	return out
}

func diff(a, b []string) (onlyA, onlyB []string) {
	inA, inB := map[string]bool{}, map[string]bool{}
	for _, s := range a {
		inA[s] = true
	}
	for _, s := range b {
		inB[s] = true
		if !inA[s] {
			onlyB = append(onlyB, s)
		}
	}
	for _, s := range a {
		if !inB[s] {
			onlyA = append(onlyA, s)
		}
	}
	return onlyA, onlyB
}
