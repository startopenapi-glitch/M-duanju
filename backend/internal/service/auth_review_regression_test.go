package service

import (
	"context"
	"database/sql"
	"errors"
	"golang.org/x/crypto/bcrypt"
	"sync/atomic"
	"testing"
	"time"
)

// Missing second factors must spend IP allowance without consuming credentials or locking an account.
func TestReviewMissingTOTPStillConsumesIPBudget(t *testing.T) {
	for _, mode := range []string{"password", "code"} {
		for _, challenge := range []string{"valid", "wrong", "absent"} {
			t.Run(mode+"/"+challenge, func(t *testing.T) {
				s, db, uid := authFixture(t)
				hash, e := bcrypt.GenerateFromPassword([]byte("SyntheticPassword12"), bcrypt.MinCost)
				if e != nil {
					t.Fatal(e)
				}
				if _, e = db.Exec(`UPDATE users SET password_hash=$1,totp_enabled=true,totp_secret='JBSWY3DPEHPK3PXP',recovery_codes=$2 WHERE id=$3`, string(hash), `["`+hashOne("AB12CD34EF")+`"]`, uid); e != nil {
					t.Fatal(e)
				}
				if challenge != "absent" {
					seedCode(t, s, db, 0)
				}
				ip := "198.51.100.21"
				key := s.authDigest("rate-limit", mode+"-ip:"+ip)
				if _, e = db.Exec(`INSERT INTO auth_rate_limits(key,attempts) VALUES($1,99)`, key); e != nil {
					t.Fatal(e)
				}
				login := func() error {
					if mode == "password" {
						password := "SyntheticPassword12"
						if challenge == "wrong" {
							password = "wrong-password"
						}
						_, e := s.AuthenticatePassword("person@example.com", password, "", ip)
						return e
					}
					code := "123456"
					if challenge == "wrong" {
						code = "000000"
					}
					_, e := s.AuthenticateCode("person@example.com", code, "", ip)
					return e
				}
				want := ErrNeedTOTP
				if challenge == "wrong" || (mode == "code" && challenge == "absent") {
					want = ErrInvalidCredentials
				}
				if e := login(); !errors.Is(e, want) {
					t.Errorf("100th request=%v want %v", e, want)
				}
				var attempts int
				if e = db.QueryRow(`SELECT attempts FROM auth_rate_limits WHERE key=$1`, key).Scan(&attempts); e != nil {
					t.Fatal(e)
				}
				if attempts != 100 {
					t.Errorf("100th request persisted attempts=%d want 100", attempts)
				}
				for i := 0; i < 2; i++ {
					if e := login(); !errors.Is(e, ErrAuthRateLimited) {
						t.Errorf("request beyond 100=%v want rate limit", e)
					}
				}
				var failed int
				var locked bool
				var recovery string
				var step int64
				if e = db.QueryRow(`SELECT failed_login_count,locked_until IS NOT NULL,COALESCE(recovery_codes,''),totp_last_step FROM users WHERE id=$1`, uid).Scan(&failed, &locked, &recovery, &step); e != nil {
					t.Fatal(e)
				}
				wantFailed := 0
				if mode == "password" && challenge == "wrong" {
					wantFailed = 1
				}
				if failed != wantFailed || locked || step != -1 || recovery != `["`+hashOne("AB12CD34EF")+`"]` {
					t.Errorf("rejection changed unrelated credentials: failed=%d locked=%v step=%d", failed, locked, step)
				}
				var used, codeAttempts int
				if e = db.QueryRow(`SELECT count(*) FILTER(WHERE used_at IS NOT NULL),COALESCE(sum(attempts),0) FROM login_codes`).Scan(&used, &codeAttempts); e != nil {
					t.Fatal(e)
				}
				wantCodeAttempts := 0
				if mode == "code" && challenge == "wrong" {
					wantCodeAttempts = 1
				}
				if used != 0 || codeAttempts != wantCodeAttempts {
					t.Errorf("used=%d challenge attempts=%d", used, codeAttempts)
				}
				var audits int
				if e = db.QueryRow(`SELECT count(*) FROM audit_logs WHERE action IN ('login','use_recovery_code')`).Scan(&audits); e != nil {
					t.Fatal(e)
				}
				if audits != 0 {
					t.Errorf("rejected login produced %d success audits", audits)
				}
			})
		}
	}
}
func TestReviewEnableCodeCannotBeReplayed(t *testing.T) {
	for _, mode := range []string{"password", "code"} {
		t.Run(mode, func(t *testing.T) {
			s, db, uid := authFixture(t)
			hash, e := bcrypt.GenerateFromPassword([]byte("SyntheticPassword12"), bcrypt.MinCost)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = db.Exec(`UPDATE users SET password_hash=$1 WHERE id=$2`, string(hash), uid); e != nil {
				t.Fatal(e)
			}
			secret, _, e := s.SetupTOTP(uid)
			if e != nil {
				t.Fatal(e)
			}
			now := time.Now()
			step := now.Unix() / totpPeriod
			code, e := totpCodeAt(secret, now)
			if e != nil {
				t.Fatal(e)
			}
			recovery, e := s.EnableTOTP(uid, code)
			if e != nil {
				t.Fatal(e)
			}
			var stored int64
			if e = db.QueryRow(`SELECT totp_last_step FROM users WHERE id=$1`, uid).Scan(&stored); e != nil {
				t.Fatal(e)
			}
			if stored != step {
				t.Errorf("enabled step=%d want %d", stored, step)
			}
			seedCode(t, s, db, 0)
			login := func(factor string) error {
				if mode == "password" {
					_, e := s.AuthenticatePassword("person@example.com", "SyntheticPassword12", factor, "198.51.100.28")
					return e
				}
				_, e := s.AuthenticateCode("person@example.com", "123456", factor, "198.51.100.28")
				return e
			}
			if e = login(code); !errors.Is(e, ErrInvalidTOTP) {
				t.Errorf("enable step replay=%v want invalid TOTP", e)
			}
			if s.VerifyUserTOTP(uid, code) {
				t.Error("enable step replay accepted by direct verification")
			}
			factor := recovery[0]
			if mode == "password" {
				factor, e = totpCodeAt(secret, time.Unix((step+1)*totpPeriod, 0))
				if e != nil {
					t.Fatal(e)
				}
			}
			if e = login(factor); e != nil {
				t.Fatalf("next step or recovery login failed: %v", e)
			}
		})
	}
}
func TestEnableTOTPCommitFailurePreservesPendingSeed(t *testing.T) {
	s, db, uid := authFixture(t)
	secret, _, e := s.SetupTOTP(uid)
	if e != nil {
		t.Fatal(e)
	}
	code, e := totpCodeAt(secret, time.Now())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(`CREATE FUNCTION reject_enable_commit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.totp_enabled AND NOT OLD.totp_enabled THEN RAISE EXCEPTION 'synthetic enable failure'; END IF; RETURN NEW; END $$; CREATE CONSTRAINT TRIGGER reject_enable_commit AFTER UPDATE ON users DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE PROCEDURE reject_enable_commit()`); e != nil {
		t.Fatal(e)
	}
	if codes, e := s.EnableTOTP(uid, code); !errors.Is(e, ErrAuthUnavailable) || codes != nil {
		t.Fatalf("failed commit returned credentials=%v error=%v", codes != nil, e)
	}
	var pending string
	var enabled bool
	var recovery, active sql.NullString
	var step int64
	if e = db.QueryRow(`SELECT COALESCE(totp_pending_secret,''),totp_enabled,totp_secret,recovery_codes,totp_last_step FROM users WHERE id=$1`, uid).Scan(&pending, &enabled, &active, &recovery, &step); e != nil {
		t.Fatal(e)
	}
	if pending != secret || enabled || active.Valid || recovery.Valid || step != -1 {
		t.Fatal("failed enable transaction changed pending or active credentials")
	}
	var audits int
	if e = db.QueryRow(`SELECT count(*) FROM audit_logs WHERE action='enable_totp'`).Scan(&audits); e != nil {
		t.Fatal(e)
	}
	if audits != 0 {
		t.Fatal("failed enable transaction leaked audit")
	}
	if _, e = db.Exec(`DROP TRIGGER reject_enable_commit ON users`); e != nil {
		t.Fatal(e)
	}
	codes, e := s.EnableTOTP(uid, code)
	if e != nil || len(codes) != 8 {
		t.Fatalf("retry same pending seed failed: %v", e)
	}
	if !s.VerifyUserTOTP(uid, codes[0]) {
		t.Fatal("recovery code from retried enable rejected")
	}
}
func reviewWait(t *testing.T, db *sql.DB, q string, args ...interface{}) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var yes bool
		if e := db.QueryRow(q, args...).Scan(&yes); e != nil {
			t.Fatal(e)
		}
		if yes {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("synchronization condition not reached")
}
func TestReviewSendCleanupAndLoginLockOrder(t *testing.T) {
	s, db, _ := authFixture(t)
	s.loginCodeSender = func(string, string) error { return nil }
	seedCode(t, s, db, 0)
	ip := "198.51.100.22"
	key := s.authDigest("rate-limit", "code-ip:"+ip)
	if _, e := db.Exec(`INSERT INTO auth_rate_limits(key,attempts,window_started_at) VALUES($1,1,now()-interval '2 days')`, key); e != nil {
		t.Fatal(e)
	}
	blocker, e := db.Conn(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	defer blocker.Close()
	const lock = 76101137
	if _, e = blocker.ExecContext(context.Background(), `SELECT pg_advisory_lock($1)`, lock); e != nil {
		t.Fatal(e)
	}
	defer blocker.ExecContext(context.Background(), `SELECT pg_advisory_unlock($1)`, lock)
	if _, e = db.Exec(`CREATE FUNCTION review_pause_resend() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.expires_at < OLD.expires_at THEN PERFORM pg_advisory_xact_lock(76101137); END IF; RETURN NEW; END $$; CREATE TRIGGER review_pause_resend BEFORE UPDATE ON login_codes FOR EACH ROW EXECUTE PROCEDURE review_pause_resend()`); e != nil {
		t.Fatal(e)
	}
	send := make(chan error, 1)
	go func() { send <- s.RequestLoginCode("person@example.com", "198.51.100.23") }()
	reviewWait(t, db, `SELECT EXISTS(SELECT 1 FROM pg_locks WHERE locktype='advisory' AND objid=$1 AND NOT granted)`, lock)
	var senderPID int
	if e = db.QueryRow(`SELECT pid FROM pg_locks WHERE locktype='advisory' AND objid=$1 AND NOT granted`, lock).Scan(&senderPID); e != nil {
		t.Fatal(e)
	}
	login := make(chan error, 1)
	go func() { _, e := s.AuthenticateCode("person@example.com", "123456", "", ip); login <- e }()
	reviewWait(t, db, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid)))`, senderPID)
	if _, e = blocker.ExecContext(context.Background(), `SELECT pg_advisory_unlock($1)`, lock); e != nil {
		t.Fatal(e)
	}
	// Observe the actual PostgreSQL wait graph while both operations complete.
	cycle := false
	sendDone, loginDone := false, false
	var sendErr, loginErr error
	deadline := time.After(5 * time.Second)
	for !sendDone || !loginDone {
		var mutual bool
		if e = db.QueryRow(`SELECT EXISTS(SELECT 1 FROM pg_stat_activity a JOIN pg_stat_activity b ON b.pid=ANY(pg_blocking_pids(a.pid)) WHERE a.pid=ANY(pg_blocking_pids(b.pid)) AND (a.pid=$1 OR b.pid=$1))`, senderPID).Scan(&mutual); e != nil {
			t.Fatal(e)
		}
		cycle = cycle || mutual
		select {
		case sendErr = <-send:
			sendDone = true
		case loginErr = <-login:
			loginDone = true
		case <-deadline:
			t.Fatal("ordinary resend/login did not finish within 5 seconds")
		case <-time.After(5 * time.Millisecond):
		}
	}
	t.Logf("PostgreSQL mutual wait=%v send=%v login=%v", cycle, sendErr, loginErr)
	if cycle {
		t.Error("observed PostgreSQL sender/login wait-for cycle")
	}
	if sendErr != nil {
		t.Errorf("resend=%v", sendErr)
	}
	if !errors.Is(loginErr, ErrInvalidCredentials) {
		t.Errorf("login after resend=%v want invalidated challenge", loginErr)
	}
}

func TestReviewCrossEntrySecondFactorSingleUse(t *testing.T) {
	for _, kind := range []string{"totp", "recovery"} {
		t.Run(kind, func(t *testing.T) {
			s, db, uid := authFixture(t)
			hash, _ := bcrypt.GenerateFromPassword([]byte("SyntheticPassword12"), bcrypt.MinCost)
			secret := "JBSWY3DPEHPK3PXP"
			factor, _ := totpCodeAt(secret, time.Now())
			recovery := "[]"
			if kind == "recovery" {
				factor = "AB12CD34EF"
				recovery = `["` + hashOne(factor) + `"]`
			}
			if _, e := db.Exec(`UPDATE users SET password_hash=$1,totp_enabled=true,totp_secret=$2,recovery_codes=$3 WHERE id=$4`, string(hash), secret, recovery, uid); e != nil {
				t.Fatal(e)
			}
			seedCode(t, s, db, 0)
			start := make(chan struct{})
			results := make(chan error, 20)
			for i := 0; i < 20; i++ {
				go func(i int) {
					<-start
					if i%2 == 0 {
						_, e := s.AuthenticatePassword("person@example.com", "SyntheticPassword12", factor, "198.51.100.24")
						results <- e
					} else {
						_, e := s.AuthenticateCode("person@example.com", "123456", factor, "198.51.100.25")
						results <- e
					}
				}(i)
			}
			close(start)
			success := 0
			for i := 0; i < 20; i++ {
				if e := <-results; e == nil {
					success++
				} else if errors.Is(e, ErrAuthUnavailable) {
					t.Errorf("unexpected unavailable: %v", e)
				}
			}
			if success != 1 {
				t.Fatalf("cross-entry successes=%d want 1", success)
			}
			t.Logf("20 mixed password/code requests: %d accepted", success)
		})
	}
}
func TestReviewTOTPDeferredFailureRollsBackStep(t *testing.T) {
	s, db, uid := authFixture(t)
	secret := "JBSWY3DPEHPK3PXP"
	factor, _ := totpCodeAt(secret, time.Now())
	if _, e := db.Exec(`UPDATE users SET totp_enabled=true,totp_secret=$1 WHERE id=$2`, secret, uid); e != nil {
		t.Fatal(e)
	}
	seedCode(t, s, db, 0)
	if _, e := db.Exec(`CREATE FUNCTION review_reject_commit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.used_at IS NOT NULL THEN RAISE EXCEPTION 'synthetic'; END IF; RETURN NEW; END $$; CREATE CONSTRAINT TRIGGER review_reject_commit AFTER UPDATE ON login_codes DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE PROCEDURE review_reject_commit()`); e != nil {
		t.Fatal(e)
	}
	if _, e := s.AuthenticateCode("person@example.com", "123456", factor, "198.51.100.26"); !errors.Is(e, ErrAuthUnavailable) {
		t.Fatalf("commit failure returned %v", e)
	}
	var step int64
	var used bool
	db.QueryRow(`SELECT totp_last_step FROM users WHERE id=$1`, uid).Scan(&step)
	db.QueryRow(`SELECT used_at IS NOT NULL FROM login_codes`).Scan(&used)
	if step != -1 || used {
		t.Fatalf("rollback leaked state step=%d used=%v", step, used)
	}
	if _, e := db.Exec(`DROP TRIGGER review_reject_commit ON login_codes`); e != nil {
		t.Fatal(e)
	}
	if _, e := s.AuthenticateCode("person@example.com", "123456", factor, "198.51.100.26"); e != nil {
		t.Fatalf("same factors failed retry: %v", e)
	}
}
func TestReviewLegacyEmailCollisionRejectsBothEntrypoints(t *testing.T) {
	s, db, uid := authFixture(t)
	hash, _ := bcrypt.GenerateFromPassword([]byte("SyntheticPassword12"), bcrypt.MinCost)
	if _, e := db.Exec(`UPDATE users SET password_hash=$1 WHERE id=$2`, string(hash), uid); e != nil {
		t.Fatal(e)
	}
	if _, e := db.Exec(`INSERT INTO users(email,password_hash,nickname) VALUES('PERSON@EXAMPLE.COM',$1,'Duplicate')`, string(hash)); e != nil {
		t.Fatal(e)
	}
	seedCode(t, s, db, 0)
	if _, e := s.AuthenticatePassword("person@example.com", "SyntheticPassword12", "", "198.51.100.27"); !errors.Is(e, ErrInvalidCredentials) {
		t.Fatalf("password collision err=%v", e)
	}
	if _, e := s.AuthenticateCode("person@example.com", "123456", "", "198.51.100.27"); !errors.Is(e, ErrInvalidCredentials) {
		t.Fatalf("code collision err=%v", e)
	}
}

func TestEnableTOTPRecordsFutureSkewedStep(t *testing.T) {
	s, db, uid := authFixture(t)
	secret, _, err := s.SetupTOTP(uid)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(time.Duration(totpPeriod) * time.Second)
	step := at.Unix() / totpPeriod
	code, err := totpCodeAt(secret, at)
	if err != nil {
		t.Fatal(err)
	}
	codes, err := s.EnableTOTP(uid, code)
	if err != nil {
		t.Fatal(err)
	}
	var stored int64
	if err = db.QueryRow(`SELECT totp_last_step FROM users WHERE id=$1`, uid).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != step {
		t.Errorf("enable matched skewed step=%d but persisted %d", step, stored)
	}
	if s.VerifyUserTOTP(uid, code) {
		t.Error("skewed enable code replayed")
	}
	if !s.VerifyUserTOTP(uid, codes[0]) {
		t.Error("recovery rejected after skewed enable")
	}
}

func TestAuthHistoryCleanupSkipsLockedRows(t *testing.T) {
	for _, table := range []string{"login_codes", "auth_rate_limits", "auth_code_sends"} {
		t.Run(table, func(t *testing.T) {
			s, db, _ := authFixture(t)
			s.loginCodeSender = func(string, string) error { return nil }
			var query, count string
			switch table {
			case "login_codes":
				query = `INSERT INTO login_codes(id,email,code_hash,expires_at) VALUES('stale','old@example.com','v1:synthetic',now()-interval '2 days'),('active','active@example.com','v1:synthetic',now()+interval '1 hour')`
				count = `SELECT count(*) FROM login_codes WHERE id='stale'`
			case "auth_rate_limits":
				query = `INSERT INTO auth_rate_limits(key,attempts,window_started_at) VALUES('stale',1,now()-interval '2 days'),('active',1,now())`
				count = `SELECT count(*) FROM auth_rate_limits WHERE key='stale'`
			case "auth_code_sends":
				query = `INSERT INTO auth_code_sends(email,challenge_id,last_started_at) VALUES('stale','stale',now()-interval '2 days'),('active','active',now())`
				count = `SELECT count(*) FROM auth_code_sends WHERE email='stale'`
			}
			if _, err := db.Exec(query); err != nil {
				t.Fatal(err)
			}
			locked, err := db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			defer locked.Rollback()
			lockQuery := map[string]string{"login_codes": `SELECT id FROM login_codes WHERE id='stale' FOR UPDATE`, "auth_rate_limits": `SELECT key FROM auth_rate_limits WHERE key='stale' FOR UPDATE`, "auth_code_sends": `SELECT email FROM auth_code_sends WHERE email='stale' FOR UPDATE`}[table]
			if _, err = locked.Exec(lockQuery); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- s.RequestLoginCode("person@example.com", "198.51.100.30") }()
			select {
			case err = <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(2 * time.Second):
				locked.Rollback()
				<-done
				t.Fatal("history cleanup waited on a locked stale row")
			}
			var remaining int
			if err = db.QueryRow(count).Scan(&remaining); err != nil {
				t.Fatal(err)
			}
			if remaining != 1 {
				t.Fatal("locked history row was removed")
			}
			activeQuery := map[string]string{"login_codes": `SELECT count(*) FROM login_codes WHERE id='active'`, "auth_rate_limits": `SELECT count(*) FROM auth_rate_limits WHERE key='active'`, "auth_code_sends": `SELECT count(*) FROM auth_code_sends WHERE email='active'`}[table]
			if err = db.QueryRow(activeQuery).Scan(&remaining); err != nil {
				t.Fatal(err)
			}
			if remaining != 1 {
				t.Fatal("active auth record was removed")
			}
			if err = locked.Commit(); err != nil {
				t.Fatal(err)
			}
			if err = s.RequestLoginCode("next@example.com", "198.51.100.31"); err != nil {
				t.Fatal(err)
			}
			if err = db.QueryRow(count).Scan(&remaining); err != nil {
				t.Fatal(err)
			}
			if remaining != 0 {
				t.Fatal("unlocked stale row was not pruned by next request")
			}
		})
	}
}

func TestAuthHistoryCleanupUsesBoundedBatches(t *testing.T) {
	s, db, _ := authFixture(t)
	s.loginCodeSender = func(string, string) error { return nil }
	for _, query := range []string{
		`INSERT INTO login_codes(id,email,code_hash,expires_at) SELECT md5(n::text),'old@example.com','v1:synthetic',now()-interval '2 days' FROM generate_series(1,101) n`,
		`INSERT INTO auth_rate_limits(key,window_started_at) SELECT n::text,now()-interval '2 days' FROM generate_series(1,101) n`,
		`INSERT INTO auth_code_sends(email,challenge_id,last_started_at) SELECT n::text,md5(n::text),now()-interval '2 days' FROM generate_series(1,101) n`,
	} {
		if _, err := db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.RequestLoginCode("person@example.com", "198.51.100.32"); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{`SELECT count(*) FROM login_codes WHERE expires_at<now()-interval '24 hours'`, `SELECT count(*) FROM auth_rate_limits WHERE window_started_at<now()-interval '24 hours'`, `SELECT count(*) FROM auth_code_sends WHERE last_started_at<now()-interval '24 hours'`} {
		var left int
		if err := db.QueryRow(query).Scan(&left); err != nil {
			t.Fatal(err)
		}
		if left != 1 {
			t.Errorf("cleanup stale rows left=%d want 1 after 100-row batch", left)
		}
	}
}

func TestTransportFailureCleanupAndResendHaveNoLockCycle(t *testing.T) {
	s, db, _ := authFixture(t)
	var calls int32
	firstSent := make(chan struct{})
	failTransport := make(chan struct{})
	s.loginCodeSender = func(string, string) error {
		if atomic.AddInt32(&calls, 1) == 1 {
			close(firstSent)
			<-failTransport
			return errors.New("synthetic transport failure")
		}
		return nil
	}
	blocker, e := db.Conn(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	defer blocker.Close()
	const gate = 76101138
	if _, e = blocker.ExecContext(context.Background(), `SELECT pg_advisory_lock($1)`, gate); e != nil {
		t.Fatal(e)
	}
	defer blocker.ExecContext(context.Background(), `SELECT pg_advisory_unlock($1)`, gate)
	if _, e = db.Exec(`CREATE FUNCTION pause_failed_send_cleanup() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN PERFORM pg_advisory_xact_lock(76101138); RETURN OLD; END $$; CREATE TRIGGER pause_failed_send_cleanup BEFORE DELETE ON login_codes FOR EACH ROW EXECUTE PROCEDURE pause_failed_send_cleanup()`); e != nil {
		t.Fatal(e)
	}
	first := make(chan error, 1)
	go func() { first <- s.RequestLoginCode("person@example.com", "198.51.100.33") }()
	select {
	case <-firstSent:
	case <-time.After(5 * time.Second):
		t.Fatal("first transport did not start")
	}
	if _, e = db.Exec(`UPDATE auth_code_sends SET last_started_at=now()-interval '2 minutes' WHERE email='person@example.com'`); e != nil {
		t.Fatal(e)
	}
	close(failTransport)
	reviewWait(t, db, `SELECT EXISTS(SELECT 1 FROM pg_locks WHERE locktype='advisory' AND objid=$1 AND NOT granted)`, gate)
	var cleanupPID int
	if e = db.QueryRow(`SELECT pid FROM pg_locks WHERE locktype='advisory' AND objid=$1 AND NOT granted`, gate).Scan(&cleanupPID); e != nil {
		t.Fatal(e)
	}
	second := make(chan error, 1)
	go func() { second <- s.RequestLoginCode("person@example.com", "198.51.100.34") }()
	reviewWait(t, db, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid)))`, cleanupPID)
	if _, e = blocker.ExecContext(context.Background(), `SELECT pg_advisory_unlock($1)`, gate); e != nil {
		t.Fatal(e)
	}
	cycle := false
	firstDone, secondDone := false, false
	var firstErr, secondErr error
	deadline := time.After(5 * time.Second)
	for !firstDone || !secondDone {
		var mutual bool
		if e = db.QueryRow(`SELECT EXISTS(SELECT 1 FROM pg_stat_activity a JOIN pg_stat_activity b ON b.pid=ANY(pg_blocking_pids(a.pid)) WHERE a.pid=ANY(pg_blocking_pids(b.pid)) AND (a.pid=$1 OR b.pid=$1))`, cleanupPID).Scan(&mutual); e != nil {
			t.Fatal(e)
		}
		cycle = cycle || mutual
		select {
		case firstErr = <-first:
			firstDone = true
		case secondErr = <-second:
			secondDone = true
		case <-deadline:
			t.Fatal("failed cleanup and resend did not complete")
		case <-time.After(5 * time.Millisecond):
		}
	}
	t.Logf("PostgreSQL transport-cleanup/resend mutual wait=%v failed-send=%v resend=%v", cycle, firstErr, secondErr)
	if cycle {
		t.Error("transport cleanup and resend formed PostgreSQL wait-for cycle")
	}
	if firstErr == nil || errors.Is(firstErr, ErrAuthUnavailable) {
		t.Errorf("failed transport result=%v", firstErr)
	}
	if secondErr != nil {
		t.Errorf("resend=%v", secondErr)
	}
	var active, reserved int
	if e = db.QueryRow(`SELECT count(*) FROM login_codes WHERE used_at IS NULL AND expires_at>now()`).Scan(&active); e != nil {
		t.Fatal(e)
	}
	if e = db.QueryRow(`SELECT count(*) FROM auth_code_sends a JOIN login_codes c ON c.id=a.challenge_id WHERE c.used_at IS NULL AND c.expires_at>now()`).Scan(&reserved); e != nil {
		t.Fatal(e)
	}
	if active != 1 || reserved != 1 {
		t.Errorf("new challenge not preserved: active=%d reservations=%d", active, reserved)
	}
}

func TestTOTPStepMatcherUsesActualSkewedStep(t *testing.T) {
	now := time.Unix(1700000010, 0)
	for _, tc := range []struct {
		offset time.Duration
		want   int64
		valid  bool
	}{
		{-30 * time.Second, 56666666, true}, {0, 56666667, true}, {30 * time.Second, 56666668, true}, {60 * time.Second, 0, false},
	} {
		code, err := totpCodeAt("JBSWY3DPEHPK3PXP", now.Add(tc.offset))
		if err != nil {
			t.Fatal(err)
		}
		step, valid := verifiedTOTPStep("JBSWY3DPEHPK3PXP", code, now)
		if valid != tc.valid || step != tc.want {
			t.Errorf("offset=%v matched=(%d,%v) want=(%d,%v)", tc.offset, step, valid, tc.want, tc.valid)
		}
	}
	for _, code := range []string{"", "12345", "1234567"} {
		if _, ok := verifiedTOTPStep("JBSWY3DPEHPK3PXP", code, now); ok {
			t.Errorf("malformed code %q accepted", code)
		}
	}
}
