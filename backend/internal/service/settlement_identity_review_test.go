package service

import (
	"os"
	"testing"
)

func TestLegacyMigrationDoesNotExemptReplacement(t *testing.T) {
	for _, known := range []bool{false, true} {
		name := "ambiguous_delivery"
		if known {
			name = "known_old_delivery"
		}
		t.Run(name, func(t *testing.T) {
			s, db, in := settlementFixture(t)
			if _, e := db.Exec("UPDATE shots SET status='QUEUED',upstream_id='task-1',video_url='old-delivered.mp4',delivered_upstream_id=NULL WHERE id='s'; DROP TABLE generation_settlements"); e != nil {
				t.Fatal(e)
			}
			if known {
				if _, e := db.Exec("UPDATE shots SET delivered_upstream_id='old-task' WHERE id='s'"); e != nil {
					t.Fatal(e)
				}
			}
			migration, e := os.ReadFile("../../migrations/009_generation_settlements.sql")
			if e != nil {
				t.Fatal(e)
			}
			if _, e = db.Exec(string(migration)); e != nil {
				t.Fatal(e)
			}
			var receipts int
			var delivery string
			if e = db.QueryRow("SELECT (SELECT count(*) FROM generation_settlements WHERE upstream_id='task-1' AND status='legacy_unverified'),COALESCE(delivered_upstream_id,'') FROM shots WHERE id='s'").Scan(&receipts, &delivery); e != nil {
				t.Fatal(e)
			}
			ok, e := s.SettleGeneration(in)
			var bal, spent int64
			var ledgers int
			var st string
			if q := db.QueryRow("SELECT (SELECT credits FROM users WHERE id='u'),(SELECT spent_credits FROM projects WHERE id='p'),(SELECT count(*) FROM credit_transactions WHERE kind='spend'),status FROM shots WHERE id='s'").Scan(&bal, &spent, &ledgers, &st); q != nil {
				t.Fatal(q)
			}
			t.Logf("known_old_identity=%v legacy_receipts_for_current=%d delivered_identity=%q settled=%v err=%v balance=%d spent=%d ledgers=%d shot=%s", known, receipts, delivery, ok, e, bal, spent, ledgers, st)
			if receipts != 0 || !ok || e != nil || bal != 800 || spent != 200 || ledgers != 1 || st != "GENERATED" {
				t.Fatal("migration inferred current replacement as historic delivered task")
			}
		})
	}
}
