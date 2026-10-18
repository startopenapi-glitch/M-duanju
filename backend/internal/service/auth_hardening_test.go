package service

import (
	"database/sql"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"

	"drama-platform/internal/model"
	"drama-platform/internal/testutil"
)

func authFixture(t *testing.T) (*Service, *sql.DB, string) {
	t.Helper()
	db := testutil.DB(t)
	s := New(db, "synthetic-auth-test-secret")
	var uid string
	if err := db.QueryRow(`INSERT INTO users(email,password_hash,nickname) VALUES('person@example.test','unused','Synthetic') RETURNING id`).Scan(&uid); err != nil {
		t.Fatal(err)
	}
	return s, db, uid
}

func seedCode(t *testing.T, s *Service, db *sql.DB, attempts int) string {
	t.Helper()
	var id string
	if err := db.QueryRow(`INSERT INTO login_codes(email,code_hash,expires_at,attempts) VALUES('person@example.test',$1,now()+interval '10 minutes',$2) RETURNING id`, s.hashLoginCode("person@example.test", "123456"), attempts).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func concurrent(n int, f func()) {
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); <-start; f() }()
	}
	close(start)
	wg.Wait()
}

func TestOTPConcurrentSingleUse(t *testing.T) {
	s, db, _ := authFixture(t)
	seedCode(t, s, db, 0)
	if _, err := db.Exec("CREATE FUNCTION delay_consume() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN PERFORM pg_sleep(0.05); RETURN NEW; END $$; CREATE TRIGGER delay_consume BEFORE UPDATE ON login_codes FOR EACH ROW EXECUTE PROCEDURE delay_consume()"); err != nil {
		t.Fatal(err)
	}
	var successes int32
	concurrent(20, func() {
		if _, err := s.LoginUserByCode("person@example.test", "123456"); err == nil {
			atomic.AddInt32(&successes, 1)
		}
	})
	if successes != 1 {
		t.Fatalf("20 concurrent consumers: got %d successes, want exactly 1", successes)
	}
}

func TestOTPWrongAttemptsBounded(t *testing.T) {
	s, db, _ := authFixture(t)
	id := seedCode(t, s, db, 0)
	concurrent(20, func() { s.LoginUserByCode("person@example.test", "654321") })
	var attempts int
	if err := db.QueryRow(`SELECT attempts FROM login_codes WHERE id=$1`, id).Scan(&attempts); err != nil {
		t.Fatal(err)
	}
	if attempts != 5 {
		t.Fatalf("wrong attempt count=%d, want 5", attempts)
	}
	if _, err := s.LoginUserByCode("person@example.test", "123456"); err == nil {
		t.Fatal("exhausted challenge accepted")
	}
}

func TestOTPConsumptionWriteFailureRejects(t *testing.T) {
	s, db, _ := authFixture(t)
	seedCode(t, s, db, 0)
	_, err := db.Exec(`CREATE FUNCTION fail_consume() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.used_at IS NOT NULL THEN RAISE EXCEPTION 'synthetic consume failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER fail_consume BEFORE UPDATE ON login_codes FOR EACH ROW EXECUTE PROCEDURE fail_consume()`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.LoginUserByCode("person@example.test", "123456"); err == nil {
		t.Fatal("consume write failed but authentication succeeded")
	}
}

func TestDisabledUserCannotUseOTP(t *testing.T) {
	s, db, uid := authFixture(t)
	seedCode(t, s, db, 0)
	if _, err := db.Exec(`UPDATE users SET status='disabled' WHERE id=$1`, uid); err != nil {
		t.Fatal(err)
	}
	if _, err := s.LoginUserByCode("person@example.test", "123456"); err == nil {
		t.Fatal("disabled user authenticated")
	}
}

func TestRecoveryCodeConcurrentSingleUse(t *testing.T) {
	s, db, uid := authFixture(t)
	b, _ := json.Marshal(hashCodes([]string{"AB12CD34EF"}))
	if _, err := db.Exec(`UPDATE users SET totp_enabled=true,totp_secret='JBSWY3DPEHPK3PXP',recovery_codes=$1 WHERE id=$2`, string(b), uid); err != nil {
		t.Fatal(err)
	}
	var successes int32
	concurrent(20, func() {
		if s.VerifyUserTOTP(uid, "AB12CD34EF") {
			atomic.AddInt32(&successes, 1)
		}
	})
	if successes != 1 {
		t.Fatalf("recovery code accepted %d times, want 1", successes)
	}
}

func TestRecoveryWriteFailureRejects(t *testing.T) {
	s, db, uid := authFixture(t)
	b, _ := json.Marshal(hashCodes([]string{"AB12CD34EF"}))
	db.Exec(`UPDATE users SET totp_enabled=true,totp_secret='JBSWY3DPEHPK3PXP',recovery_codes=$1 WHERE id=$2`, string(b), uid)
	if _, err := db.Exec(`CREATE FUNCTION fail_recovery() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic recovery failure'; END $$; CREATE TRIGGER fail_recovery BEFORE UPDATE ON users FOR EACH ROW EXECUTE PROCEDURE fail_recovery()`); err != nil {
		t.Fatal(err)
	}
	if s.VerifyUserTOTP(uid, "AB12CD34EF") {
		t.Fatal("recovery write failure authenticated")
	}
}

func TestLockedEmailAliases(t *testing.T) {
	s, db, uid := authFixture(t)
	db.Exec(`UPDATE users SET locked_until=now()+interval '15 minutes' WHERE id=$1`, uid)
	if locked, _ := s.IsLocked("  PERSON@example.test  "); !locked {
		t.Fatal("email normalization bypassed lock")
	}
}

func TestRegistrationRejectsNormalizedExistingIdentity(t *testing.T) {
	s, db, uid := authFixture(t)
	if _, err := db.Exec("UPDATE users SET email='PERSON@EXAMPLE.TEST' WHERE id=$1", uid); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RESEND_API_KEY", "")
	t.Setenv("RESEND_API_KEY_FILE", "")
	t.Setenv("SMTP_HOST", "")
	t.Setenv("SMTP_FROM", "")
	if _, err := s.CreateUser(&model.RegisterReq{Email: "person@example.test", Password: "SyntheticPassword12", Nickname: "Duplicate"}); err == nil {
		t.Fatal("case-variant registration created an ambiguous identity")
	}
	var count int
	db.QueryRow("SELECT count(*) FROM users").Scan(&count)
	if count != 1 {
		t.Fatalf("unexpected identity count=%d", count)
	}
}
