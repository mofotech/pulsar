// Package testutil provides helpers for integration tests that need real
// Postgres and etcd. Tests using them are skipped unless the backing
// services are configured:
//
//	PULSAR_TEST_PG_DSN  admin DSN of a Postgres server; each test gets its own
//	                    freshly migrated database, dropped afterwards
//	PULSAR_TEST_ETCD    etcd client endpoint, e.g. 127.0.0.1:52379
//
// `make test-integration` starts both (deploy/docker/docker-compose.test.yml)
// and sets these variables. With PULSAR_TEST_REQUIRE=1 (set in CI) missing
// services fail the test instead of skipping it, so they can't silently rot.
package testutil

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"

	"github.com/agomez/pulsar/internal/store/etcd"
	"github.com/agomez/pulsar/internal/store/postgres"
)

// DefaultOrgID is the built-in organisation seeded by migration 006.
const DefaultOrgID = "00000000-0000-0000-0000-000000000010"

// Postgres returns a handle to a brand-new database with all migrations
// applied. The database is dropped when the test finishes.
func Postgres(t testing.TB) *postgres.DB {
	t.Helper()
	db := EmptyPostgres(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := db.Migrate(ctx, zap.NewNop()); err != nil {
		t.Fatalf("migrate test database: %v", err)
	}
	return db
}

// EmptyPostgres returns a brand-new database with no schema. The database is
// dropped when the test finishes.
func EmptyPostgres(t testing.TB) *postgres.DB {
	t.Helper()
	adminDSN := os.Getenv("PULSAR_TEST_PG_DSN")
	if adminDSN == "" {
		skipOrFail(t, "PULSAR_TEST_PG_DSN not set; run `make test-integration`")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	admin, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		t.Fatalf("connect admin postgres: %v", err)
	}
	defer admin.Close(ctx)

	b := make([]byte, 6)
	_, _ = rand.Read(b)
	dbName := "pulsar_test_" + hex.EncodeToString(b)
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+dbName); err != nil { //nolint:gosec // dbName is generated hex, not input
		t.Fatalf("create test database: %v", err)
	}

	u, err := url.Parse(adminDSN)
	if err != nil {
		t.Fatalf("parse PULSAR_TEST_PG_DSN: %v", err)
	}
	u.Path = "/" + dbName
	db, err := postgres.New(u.String())
	if err != nil {
		t.Fatalf("connect test database: %v", err)
	}
	t.Cleanup(func() {
		db.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if c, err := pgx.Connect(ctx, adminDSN); err == nil {
			_, _ = c.Exec(ctx, "DROP DATABASE IF EXISTS "+dbName+" WITH (FORCE)") //nolint:gosec // generated name
			_ = c.Close(ctx)
		}
	})

	return db
}

// Etcd returns a client for the test etcd server. Callers should use random
// keys (handlers already do: JTIs, OIDC nonces) so tests don't collide.
func Etcd(t testing.TB) *etcd.Client {
	t.Helper()
	ep := os.Getenv("PULSAR_TEST_ETCD")
	if ep == "" {
		skipOrFail(t, "PULSAR_TEST_ETCD not set; run `make test-integration`")
	}
	c, err := etcd.NewClient([]string{ep})
	if err != nil {
		t.Fatalf("connect etcd: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := c.Get(ctx, "/pulsar/test/ping"); err != nil {
		t.Fatalf("etcd not reachable at %s: %v", ep, err)
	}
	t.Cleanup(c.Close)
	return c
}

func skipOrFail(t testing.TB, msg string) {
	t.Helper()
	if os.Getenv("PULSAR_TEST_REQUIRE") == "1" {
		t.Fatal(msg + " (PULSAR_TEST_REQUIRE=1)")
	}
	t.Skip(msg)
}
