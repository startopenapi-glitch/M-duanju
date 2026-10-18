// Package testutil provides destructive-test isolation in one explicitly allowed database.
package testutil

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

const DatabaseName = "drama_remediation_test_20261017"

func DB(t *testing.T) *sql.DB { return DBThrough(t, "999") }

// DBThrough builds a fresh, guarded schema through an explicit migration prefix. Empty means no tables.
func DBThrough(t *testing.T, max string) *sql.DB {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; isolated PostgreSQL tests require the guarded test database")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal("invalid test database configuration")
	}
	if cfg.Database != DatabaseName {
		t.Fatalf("refusing database other than %s", DatabaseName)
	}
	admin := stdlib.OpenDB(*cfg)
	var actual string
	if err := admin.QueryRow("SELECT current_database()").Scan(&actual); err != nil {
		admin.Close()
		t.Fatal("cannot connect to isolated test database")
	}
	if actual != DatabaseName {
		admin.Close()
		t.Fatal("connected database failed safety guard")
	}
	schema := "test_" + uuid.NewString()[:8] + uuid.NewString()[:8]
	quoted := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.Exec("CREATE SCHEMA " + quoted); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	cfg.RuntimeParams["search_path"] = schema
	db := stdlib.OpenDB(*cfg)
	db.SetMaxOpenConns(24)
	t.Cleanup(func() {
		db.Close()
		if _, err := admin.Exec("DROP SCHEMA " + quoted + " CASCADE"); err != nil {
			t.Error(err)
		}
		admin.Close()
	})
	_, source, _, _ := runtime.Caller(0)
	migrations, err := filepath.Glob(filepath.Join(filepath.Dir(source), "../../migrations/*.sql"))
	if err != nil || len(migrations) == 0 {
		t.Fatal("migration files unavailable")
	}
	for _, name := range migrations {
		if max == "" || filepath.Base(name) > max {
			continue
		}
		sqlBytes, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(context.Background(), string(sqlBytes)); err != nil {
			t.Fatalf("migration %s: %v", filepath.Base(name), err)
		}
	}
	return db
}
