package service

import (
	"bufio"
	"crypto/sha256"
	"drama-platform/internal/testutil"
	"encoding/hex"
	"fmt"
	"github.com/google/uuid"
	"net"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func smtpStub(t *testing.T) *int32 {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var count int32
	t.Cleanup(func() { listener.Close() })
	t.Setenv("RESEND_API_KEY", "")
	t.Setenv("RESEND_API_KEY_FILE", "")
	t.Setenv("SMTP_HOST", "127.0.0.1")
	t.Setenv("SMTP_PORT", strconv.Itoa(listener.Addr().(*net.TCPAddr).Port))
	t.Setenv("SMTP_FROM", "synthetic@example.com")
	t.Setenv("SMTP_USER", "")
	t.Setenv("SMTP_TLS", "0")
	go func() {
		for {
			conn, e := listener.Accept()
			if e != nil {
				return
			}
			go func() {
				defer conn.Close()
				reader := bufio.NewReader(conn)
				fmt.Fprint(conn, "220 synthetic ESMTP\r\n")
				for {
					line, e := reader.ReadString('\n')
					if e != nil {
						return
					}
					switch {
					case strings.HasPrefix(line, "EHLO"), strings.HasPrefix(line, "HELO"):
						fmt.Fprint(conn, "250 synthetic\r\n")
					case strings.HasPrefix(line, "MAIL"), strings.HasPrefix(line, "RCPT"):
						fmt.Fprint(conn, "250 OK\r\n")
					case strings.HasPrefix(line, "DATA"):
						fmt.Fprint(conn, "354 Send data\r\n")
						for {
							line, e = reader.ReadString('\n')
							if e != nil {
								return
							}
							if line == ".\r\n" {
								break
							}
						}
						atomic.AddInt32(&count, 1)
						fmt.Fprint(conn, "250 Accepted\r\n")
					case strings.HasPrefix(line, "QUIT"):
						fmt.Fprint(conn, "221 Bye\r\n")
						return
					default:
						fmt.Fprint(conn, "250 OK\r\n")
					}
				}
			}()
		}
	}()
	return &count
}
func TestCodeSendAtomicCooldown(t *testing.T) {
	s, db, _ := authFixture(t)
	count := smtpStub(t)
	concurrent(20, func() { s.RequestLoginCode(" PERSON@EXAMPLE.COM ") })
	var rows int
	db.QueryRow("SELECT count(*) FROM login_codes").Scan(&rows)
	if atomic.LoadInt32(count) != 1 || rows != 1 {
		t.Fatalf("concurrent send created rows=%d mails=%d, want 1/1", rows, atomic.LoadInt32(count))
	}
}
func TestMailFailureDoesNotConsumeCooldown(t *testing.T) {
	s, db, _ := authFixture(t)
	t.Setenv("RESEND_API_KEY", "")
	t.Setenv("RESEND_API_KEY_FILE", "")
	t.Setenv("SMTP_HOST", "")
	t.Setenv("SMTP_FROM", "")
	if err := s.RequestLoginCode("person@example.com"); err == nil {
		t.Fatal("unconfigured mail succeeded")
	}
	count := smtpStub(t)
	if err := s.RequestLoginCode("person@example.com"); err != nil {
		t.Fatalf("mail recovery blocked by failed send: %v", err)
	}
	var active int
	db.QueryRow("SELECT count(*) FROM login_codes WHERE used_at IS NULL AND expires_at>now()").Scan(&active)
	if active != 1 || atomic.LoadInt32(count) != 1 {
		t.Fatalf("unexpected active=%d mail=%d", active, atomic.LoadInt32(count))
	}
}
func TestLoginCodePepperSeparation(t *testing.T) {
	s, db, _ := authFixture(t)
	seedCode(t, s, db, 0)
	other := New(db, "different-synthetic-secret")
	if _, err := other.LoginUserByCode("person@example.com", "123456"); err == nil {
		t.Fatal("OTP authenticated with different server secret")
	}
	raw := sha256.Sum256([]byte("person@example.com:123456"))
	var stored string
	db.QueryRow("SELECT code_hash FROM login_codes LIMIT 1").Scan(&stored)
	if stored == hex.EncodeToString(raw[:]) {
		t.Fatal("OTP digest still supports unkeyed offline guessing")
	}
}
func TestTOTPConcurrentSingleUse(t *testing.T) {
	s, db, uid := authFixture(t)
	secret := "JBSWY3DPEHPK3PXP"
	code, _ := totpCodeAt(secret, time.Now())
	db.Exec("UPDATE users SET totp_enabled=true,totp_secret=$1 WHERE id=$2", secret, uid)
	var successes int32
	concurrent(20, func() {
		if s.VerifyUserTOTP(uid, code) {
			atomic.AddInt32(&successes, 1)
		}
	})
	if successes != 1 {
		t.Fatalf("same TOTP step accepted %d times", successes)
	}
}

func TestCodeFailureBudgetSurvivesNewChallenge(t *testing.T) {
	s, db, _ := authFixture(t)
	first := seedCode(t, s, db, 0)
	for i := 0; i < 4; i++ {
		if _, err := s.LoginUserByCode("person@example.com", "000000"); err == nil {
			t.Fatal("wrong code succeeded")
		}
	}
	if _, err := db.Exec("UPDATE login_codes SET expires_at=now()-interval '1 second' WHERE id=$1", first); err != nil {
		t.Fatal(err)
	}
	seedCode(t, s, db, 0)
	s.LoginUserByCode("person@example.com", "000000")
	if _, err := s.LoginUserByCode("person@example.com", "123456"); err == nil {
		t.Fatal("new challenge reset the cross-challenge failure allowance")
	}
	var failures int
	db.QueryRow("SELECT sum(attempts) FROM login_codes").Scan(&failures)
	if failures != 5 {
		t.Fatalf("cross-challenge total wrong attempts=%d want 5", failures)
	}
}
func TestSendIPAndGlobalBudgetsAndRetention(t *testing.T) {
	for _, kind := range []string{"ip", "global"} {
		t.Run(kind, func(t *testing.T) {
			db := testutil.DB(t)
			var sends int32
			s := New(db, "synthetic-auth-test-secret", WithLoginCodeSender(func(email, code string) error { atomic.AddInt32(&sends, 1); return nil }))
			key := "send-ip:198.51.100.8"
			limit := 20
			if kind == "global" {
				key = "send-global:all"
				limit = 200
			}
			if _, err := db.Exec("INSERT INTO auth_rate_limits(key,attempts) VALUES($1,$2)", s.authDigest("rate-limit", key), limit-1); err != nil {
				t.Fatal(err)
			}
			var accepted int32
			concurrent(20, func() {
				email := "synthetic-" + uuid.NewString() + "@example.com"
				if s.RequestLoginCode(email, "198.51.100.8") == nil {
					atomic.AddInt32(&accepted, 1)
				}
			})
			if accepted != 1 || sends != 1 {
				t.Fatalf("%s send allowance exceeded: accepted=%d sends=%d", kind, accepted, sends)
			}
		})
	}
	t.Run("retention", func(t *testing.T) {
		s, db, _ := authFixture(t)
		s.loginCodeSender = func(string, string) error { return nil }
		db.Exec("INSERT INTO login_codes(email,code_hash,expires_at,used_at) VALUES('old@example.com','v1:synthetic',now()-interval '2 days',NULL),('used@example.com','v1:synthetic',now()+interval '1 hour',now()-interval '2 days'),('active@example.com','v1:synthetic',now()+interval '1 hour',NULL)")
		if err := s.RequestLoginCode("person@example.com", "198.51.100.9"); err != nil {
			t.Fatal(err)
		}
		var old, active int
		db.QueryRow("SELECT count(*) FROM login_codes WHERE email IN ('old@example.com','used@example.com')").Scan(&old)
		db.QueryRow("SELECT count(*) FROM login_codes WHERE email='active@example.com'").Scan(&active)
		if old != 0 || active != 1 {
			t.Fatalf("retention removed wrong challenges: old=%d active=%d", old, active)
		}
	})
}

func TestTransportFailureReleasesOnlyItsReservation(t *testing.T) {
	s, db, _ := authFixture(t)
	attempt := 0
	s.loginCodeSender = func(email, code string) error {
		attempt++
		if attempt == 1 {
			return fmt.Errorf("synthetic transport failure")
		}
		return nil
	}
	if err := s.RequestLoginCode("person@example.com", "198.51.100.10"); err == nil {
		t.Fatal("failed transport succeeded")
	}
	var active, reservations int
	db.QueryRow("SELECT count(*) FROM login_codes WHERE used_at IS NULL AND expires_at>now()").Scan(&active)
	db.QueryRow("SELECT count(*) FROM auth_code_sends").Scan(&reservations)
	if active != 0 || reservations != 0 {
		t.Fatalf("failed send left active=%d reservations=%d", active, reservations)
	}
	if err := s.RequestLoginCode("person@example.com", "198.51.100.10"); err != nil {
		t.Fatalf("transport recovery blocked: %v", err)
	}
}
