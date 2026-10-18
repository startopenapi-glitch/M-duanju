package service

import (
	"database/sql"
	"os"
	"testing"
)

func reloadSettlementMigration(t *testing.T, db *sql.DB) {
	t.Helper()
	migration, e := os.ReadFile("../../migrations/009_generation_settlements.sql")
	if e != nil {
		t.Fatal(e)
	}
	mustExec(t, db, string(migration))
}

func TestLegacyDeliveryIdentityMigrationKeepsAmbiguousVideoUnknown(t *testing.T) {
	for _, state := range []string{"QUEUED", "PENDING", "FAILED"} {
		for _, known := range []bool{false, true} {
			label := "unknown"
			if known {
				label = "explicit_old"
			}
			t.Run(state+"_"+label, func(t *testing.T) {
				s, db, in := settlementFixture(t)
				mustExec(t, db, `UPDATE shots SET status='`+state+`',video_url='old.mp4',delivered_upstream_id=NULL WHERE id='s'`)
				if known {
					mustExec(t, db, `UPDATE shots SET delivered_upstream_id='old-task' WHERE id='s'`)
				}
				reloadSettlementMigration(t, db)
				var currentLegacy, receipts int
				var delivered, status, url, task string
				if e := db.QueryRow(`SELECT (SELECT count(*) FROM generation_settlements WHERE upstream_id='task-1'),(SELECT count(*) FROM generation_settlements),COALESCE(delivered_upstream_id,''),status,video_url,upstream_id FROM shots WHERE id='s'`).Scan(&currentLegacy, &receipts, &delivered, &status, &url, &task); e != nil {
					t.Fatal(e)
				}
				expected, identity := 0, ""
				if known {
					expected, identity = 1, "old-task"
				}
				if currentLegacy != 0 || receipts != expected || delivered != identity || status != state || url != "old.mp4" || task != in.TaskID {
					t.Fatalf("migration inferred identity or changed attempt: current_receipts=%d receipts=%d delivered=%q status=%s url=%s task=%s", currentLegacy, receipts, delivered, status, url, task)
				}
				assertSettlementTotals(t, db, 1000, 0, 0)
				// The existing settlement interface requires QUEUED. This is an explicit
				// same-ID resume precondition, not a migration or Task3 recovery assertion.
				mustExec(t, db, `UPDATE shots SET status='QUEUED' WHERE id='s'`)
				if ok, e := s.SettleGeneration(in); e != nil || !ok {
					t.Fatalf("new task not settled: %v %v", ok, e)
				}
				if ok, e := s.SettleGeneration(in); e != nil || ok {
					t.Fatalf("new task replay: %v %v", ok, e)
				}
				assertSettlementTotals(t, db, 800, 200, 1)
				if e := db.QueryRow(`SELECT video_url,delivered_upstream_id,status FROM shots WHERE id='s'`).Scan(&url, &delivered, &status); e != nil {
					t.Fatal(e)
				}
				if url != in.URL || delivered != in.TaskID || status != "GENERATED" {
					t.Fatal("new result not delivered")
				}
				if known {
					old := in
					old.TaskID = "old-task"
					old.URL = "old.mp4"
					if ok, e := s.SettleGeneration(old); e != nil || ok {
						t.Fatalf("explicit old task charged: %v %v", ok, e)
					}
					assertSettlementTotals(t, db, 800, 200, 1)
				}
			})
		}
	}
}

func TestLegacyProtectionHelperUsesExplicitDeliveryIdentity(t *testing.T) {
	for _, state := range []string{"QUEUED", "PENDING", "FAILED", "GENERATED", "APPROVED"} {
		for _, known := range []bool{false, true} {
			label := "unknown"
			if known {
				label = "explicit_old"
			}
			t.Run(state+"_"+label, func(t *testing.T) {
				_, db, _ := settlementFixture(t)
				delivered := ""
				if known {
					delivered = "old-task"
				}
				mustExec(t, db, `UPDATE shots SET status='`+state+`',video_url='old.mp4' WHERE id='s'`)
				if known {
					mustExec(t, db, `UPDATE shots SET delivered_upstream_id='old-task' WHERE id='s'`)
				}
				tx, e := db.Begin()
				if e != nil {
					t.Fatal(e)
				}
				defer tx.Rollback()
				if e = protectLegacyShot(tx, "u", "p", "s", "task-1", state, "old.mp4", delivered); e != nil {
					t.Fatal(e)
				}
				if e = tx.Commit(); e != nil {
					t.Fatal(e)
				}
				expected := ""
				if known {
					expected = "old-task"
				} else if state == "GENERATED" || state == "APPROVED" {
					expected = "task-1"
				}
				var receiptTask, stored string
				var count int
				if e = db.QueryRow(`SELECT (SELECT count(*) FROM generation_settlements),(SELECT COALESCE(max(upstream_id),'') FROM generation_settlements),COALESCE(delivered_upstream_id,'') FROM shots WHERE id='s'`).Scan(&count, &receiptTask, &stored); e != nil {
					t.Fatal(e)
				}
				want := 0
				if expected != "" {
					want = 1
				}
				if count != want || receiptTask != expected || stored != expected {
					t.Fatalf("helper inferred wrong identity: count=%d receipt=%q delivered=%q want=%q", count, receiptTask, stored, expected)
				}
				assertSettlementTotals(t, db, 1000, 0, 0)
			})
		}
	}
}

func TestLegacyDeliveryIdentityMigrationProtectsCompletedVideo(t *testing.T) {
	for _, state := range []string{"GENERATED", "APPROVED"} {
		t.Run(state, func(t *testing.T) {
			s, db, in := settlementFixture(t)
			mustExec(t, db, `UPDATE shots SET status='`+state+`',video_url='old.mp4' WHERE id='s'`)
			reloadSettlementMigration(t, db)
			if ok, e := s.SettleGeneration(in); e != nil || ok {
				t.Fatalf("completed legacy charged: %v %v", ok, e)
			}
			var delivered, status, url string
			var receipts int
			if e := db.QueryRow(`SELECT delivered_upstream_id,status,video_url,(SELECT count(*) FROM generation_settlements WHERE upstream_id='task-1' AND status='legacy_unverified') FROM shots WHERE id='s'`).Scan(&delivered, &status, &url, &receipts); e != nil {
				t.Fatal(e)
			}
			if delivered != in.TaskID || status != state || url != "old.mp4" || receipts != 1 {
				t.Fatal("confirmed history lost protection")
			}
			assertSettlementTotals(t, db, 1000, 0, 0)
		})
	}
	t.Run("no_task_identity", func(t *testing.T) {
		_, db, _ := settlementFixture(t)
		mustExec(t, db, `UPDATE shots SET status='GENERATED',video_url='unknown.mp4',upstream_id=NULL WHERE id='s'`)
		reloadSettlementMigration(t, db)
		var count int
		var delivered string
		if e := db.QueryRow(`SELECT (SELECT count(*) FROM generation_settlements),COALESCE(delivered_upstream_id,'') FROM shots WHERE id='s'`).Scan(&count, &delivered); e != nil {
			t.Fatal(e)
		}
		if count != 0 || delivered != "" {
			t.Fatal("unknown identity fabricated")
		}
	})
}

func TestLegacyImageMigrationDoesNotInferCurrentTaskFromUIState(t *testing.T) {
	for _, state := range []string{"KEYVISUAL_GENERATING", "KEYVISUAL_REVIEW", "SHOTS_GENERATING", "SHOTS_REVIEW", "ASSEMBLING", "COMPLETED", "FAILED"} {
		t.Run(state, func(t *testing.T) {
			s, db, _ := settlementFixture(t)
			mustExec(t, db, `UPDATE projects SET status='`+state+`',keyvisual_task_id='image-new',keyvisual_url='old.jpg' WHERE id='p'`)
			reloadSettlementMigration(t, db)
			var count int
			var current, url, status string
			if e := db.QueryRow(`SELECT (SELECT count(*) FROM generation_settlements WHERE kind='image'),keyvisual_task_id,keyvisual_url,status FROM projects WHERE id='p'`).Scan(&count, &current, &url, &status); e != nil {
				t.Fatal(e)
			}
			if count != 0 || current != "image-new" || url != "old.jpg" || status != state {
				t.Fatalf("UI state inferred delivered image: receipts=%d task=%s url=%s status=%s", count, current, url, status)
			}
			assertSettlementTotals(t, db, 1000, 0, 0)
			// Same-ID resume is the settlement interface precondition; migration itself
			// intentionally leaves UI status and task unchanged. Task3 owns actual recovery.
			mustExec(t, db, `UPDATE projects SET status='KEYVISUAL_GENERATING' WHERE id='p'`)
			in := GenerationSettlement{Kind: "image", TaskID: "image-new", ProjectID: "p", URL: "new.jpg", ChargeMicros: 999999}
			if ok, e := s.SettleGeneration(in); e != nil || !ok {
				t.Fatalf("new image not settled: %v %v", ok, e)
			}
			if ok, e := s.SettleGeneration(in); e != nil || ok {
				t.Fatalf("new image replay: %v %v", ok, e)
			}
			assertSettlementTotals(t, db, 970, 30, 1)
		})
	}
}

func TestLegacyImageMigrationPreservesVerifiedReceiptIdentity(t *testing.T) {
	s, db, _ := settlementFixture(t)
	mustExec(t, db, `UPDATE projects SET status='KEYVISUAL_GENERATING',keyvisual_task_id='image-old' WHERE id='p'`)
	old := GenerationSettlement{Kind: "image", TaskID: "image-old", ProjectID: "p", URL: "old.jpg", ChargeMicros: 10000}
	if ok, e := s.SettleGeneration(old); e != nil || !ok {
		t.Fatalf("old image fixture: %v %v", ok, e)
	}
	mustExec(t, db, `UPDATE projects SET status='KEYVISUAL_REVIEW',keyvisual_task_id='image-new' WHERE id='p'`)
	reloadSettlementMigration(t, db)
	var currentReceipts, oldSettled int
	if e := db.QueryRow(`SELECT (SELECT count(*) FROM generation_settlements WHERE upstream_id='image-new'),(SELECT count(*) FROM generation_settlements WHERE upstream_id='image-old' AND status='settled')`).Scan(&currentReceipts, &oldSettled); e != nil {
		t.Fatal(e)
	}
	if currentReceipts != 0 || oldSettled != 1 {
		t.Fatal("trusted old receipt or unknown current identity changed")
	}
	if ok, e := s.SettleGeneration(old); e != nil || ok {
		t.Fatalf("verified old image charged twice: %v %v", ok, e)
	}
	assertSettlementTotals(t, db, 970, 30, 1)
	mustExec(t, db, `UPDATE projects SET status='KEYVISUAL_GENERATING' WHERE id='p'`)
	next := old
	next.TaskID = "image-new"
	next.URL = "new.jpg"
	if ok, e := s.SettleGeneration(next); e != nil || !ok {
		t.Fatalf("new image exemption: %v %v", ok, e)
	}
	assertSettlementTotals(t, db, 940, 60, 2)
}
