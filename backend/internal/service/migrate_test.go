package service

import (
	"context"
	"database/sql"
	"drama-platform/internal/testutil"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func migrationDSN(t *testing.T, db *sql.DB) string {
	t.Helper()
	cfg, e := pgx.ParseConfig(os.Getenv("TEST_DATABASE_URL"))
	if e != nil {
		t.Fatal("invalid isolated config")
	}
	if cfg.Database != testutil.DatabaseName {
		t.Fatal("wrong database")
	}
	var schema string
	if e = db.QueryRow(`SELECT current_schema()`).Scan(&schema); e != nil {
		t.Fatal(e)
	}
	cfg.RuntimeParams["search_path"] = schema
	registered := stdlib.RegisterConnConfig(cfg)
	t.Cleanup(func() { stdlib.UnregisterConnConfig(registered) })
	return registered
}
func TestMigrationEmptyLegacyAndReentrant(t *testing.T) {
	for _, baseline := range []string{"", "007_zz", "017_zz"} {
		t.Run(baseline, func(t *testing.T) {
			dbBaseline := baseline
			if baseline == "017_zz" {
				dbBaseline = ""
			}
			db := testutil.DBThrough(t, dbBaseline)
			dsn := migrationDSN(t, db)
			if baseline == "017_zz" {
				// A deployed 017 database has a migration ledger. Build it using the real runner.
				older := t.TempDir()
				entries, e := os.ReadDir("../../migrations")
				if e != nil {
					t.Fatal(e)
				}
				for _, entry := range entries {
					if !entry.IsDir() && entry.Name() < "018_" {
						b, e := os.ReadFile(filepath.Join("../../migrations", entry.Name()))
						if e != nil {
							t.Fatal(e)
						}
						if e = os.WriteFile(filepath.Join(older, entry.Name()), b, 0600); e != nil {
							t.Fatal(e)
						}
					}
				}
				if e := RunMigrations(dsn, older); e != nil {
					t.Fatal(e)
				}
			}
			mustExec(t, db, `CREATE TABLE hack_test(keep_text text);INSERT INTO hack_test VALUES('keep')`)
			if e := RunMigrations(dsn, "../../migrations"); e != nil {
				t.Fatal(e)
			}
			if e := RunMigrations(dsn, "../../migrations"); e != nil {
				t.Fatalf("repeat startup: %v", e)
			}
			files, e := filepath.Glob("../../migrations/*.sql")
			if e != nil || len(files) == 0 {
				t.Fatal("migration inventory unavailable", e)
			}
			var n int
			if e := db.QueryRow(`SELECT count(*) FROM schema_migrations`).Scan(&n); e != nil || n != len(files) {
				t.Fatal("missing migration ledger", n, e)
			}
			var kept string
			if e := db.QueryRow(`SELECT keep_text FROM hack_test`).Scan(&kept); e != nil || kept != "keep" {
				t.Fatal("unrelated table damaged")
			}
		})
	}
}
func TestMigrationFailureChecksumAndConcurrent(t *testing.T) {
	db := testutil.DBThrough(t, "")
	dsn := migrationDSN(t, db)
	dir := t.TempDir()
	if e := os.WriteFile(filepath.Join(dir, "001_first.sql"), []byte(`CREATE TABLE sentinel(id int PRIMARY KEY);INSERT INTO sentinel VALUES(1);`), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(dir, "002_fail.sql"), []byte(`CREATE TABLE partial_write(id int);SELECT 1/0;`), 0600); e != nil {
		t.Fatal(e)
	}
	if e := RunMigrations(dsn, dir); e == nil {
		t.Fatal("failure accepted")
	}
	var exists bool
	if e := db.QueryRow(`SELECT to_regclass('partial_write') IS NOT NULL`).Scan(&exists); e != nil || exists {
		t.Fatal("partial migration survived rollback", e)
	}
	if e := os.WriteFile(filepath.Join(dir, "002_fail.sql"), []byte(`CREATE TABLE partial_write(id int);`), 0600); e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 6)
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- RunMigrations(dsn, dir) }()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Error(e)
		}
	}
	var n int
	if e := db.QueryRow(`SELECT count(*) FROM schema_migrations`).Scan(&n); e != nil || n != 2 {
		t.Fatal("bad ledger", n, e)
	}
	if e := os.WriteFile(filepath.Join(dir, "001_first.sql"), []byte(`SELECT 'drift';`), 0600); e != nil {
		t.Fatal(e)
	}
	if e := RunMigrations(dsn, dir); e == nil {
		t.Fatal("checksum drift accepted")
	}
}
func TestMigrationInterruptedDoesNotRecordSuccess(t *testing.T) {
	db := testutil.DBThrough(t, "")
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "001_wait.sql"), []byte(`CREATE TABLE interrupted_write(id int);SELECT pg_sleep(10);`), 0600)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if e := runMigrationsDB(ctx, db, dir); e == nil {
		t.Fatal("interrupted migration accepted")
	}
	var exists bool
	if e := db.QueryRow(`SELECT to_regclass('interrupted_write') IS NOT NULL`).Scan(&exists); e != nil || exists {
		t.Fatal("interrupted writes survived", e)
	}
	var n int
	if e := db.QueryRow(`SELECT count(*) FROM schema_migrations`).Scan(&n); e != nil || n != 0 {
		t.Fatal("interruption marked successful", n, e)
	}
	os.WriteFile(filepath.Join(dir, "001_wait.sql"), []byte(`CREATE TABLE interrupted_write(id int);`), 0600)
	if e := runMigrationsDB(context.Background(), db, dir); e != nil {
		t.Fatal(e)
	}
}
