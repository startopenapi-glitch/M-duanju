package run

import (
	"context"
	"os"
	"testing"

	"drama-platform/internal/testutil"
)

func TestRequestMigrationBackfillsOnlyKnownOriginalKeys(t *testing.T) {
	db := testutil.DBThrough(t, "022z")
	if _, err := db.Exec(`INSERT INTO users(id,email,password_hash,credits) VALUES('req-u','request@example.com','unused',1000);
 INSERT INTO projects(id,user_id,idea,budget_credits,status) VALUES('req-p','req-u','synthetic',1000,'DRAFT');
 INSERT INTO generation_runs(id,user_id,project_id,mode,status,idempotency_key,input_hash) VALUES('11111111111111111111111111111111','req-u','req-p','first','failed','known-original','hash')`); err != nil {
		t.Fatal(err)
	}
	migration, err := os.ReadFile("../../migrations/023_run_request_aliases.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(string(migration)); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = db.QueryRow(`SELECT count(*) FROM generation_run_requests`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("backfill invented request identities: count=%d err=%v", count, err)
	}
	identity, err := New(db).ResolveRequest(context.Background(), "req-u", "known-original")
	if err != nil || identity.RunID != "11111111111111111111111111111111" {
		t.Fatalf("original request not recovered: %v", err)
	}
	if _, err = New(db).ResolveRequest(context.Background(), "req-u", "unrecorded-historical-alias"); err != ErrNotFound {
		t.Fatalf("unknown historical request must remain unknown: %v", err)
	}
}
