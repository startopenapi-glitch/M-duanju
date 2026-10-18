package handler

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"drama-platform/internal/service"
	"drama-platform/internal/testutil"
	"encoding/hex"
	"encoding/json"
	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
	"net/http/httptest"
	"strings"
	"testing"
)

const testSecret = "synthetic-auth-test-secret"

func authHTTP(t *testing.T) (*service.Service, *sql.DB, *Handler, *gin.Engine, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db := testutil.DB(t)
	s := service.New(db, testSecret)
	h := New(s, testSecret)
	hash, _ := bcrypt.GenerateFromPassword([]byte("SyntheticPassword12"), bcrypt.MinCost)
	var uid string
	if err := db.QueryRow(`INSERT INTO users(email,password_hash,nickname,role) VALUES('person@example.test',$1,'Synthetic','admin') RETURNING id`, string(hash)).Scan(&uid); err != nil {
		t.Fatal(err)
	}
	r := gin.New()
	r.POST("/auth/login", h.Login)
	r.POST("/auth/login-by-code", h.LoginByEmailCode)
	a := r.Group("")
	a.Use(h.AuthMiddleware(testSecret))
	a.GET("/auth/me", h.Me)
	a.POST("/auth/totp/setup", h.TOTPSetup)
	admin := a.Group("/admin")
	admin.Use(AdminMiddleware())
	admin.GET("/test", func(c *gin.Context) { ok(c, gin.H{"accepted": true}) })
	p := a.Group("/projects/:id")
	p.Use(h.OwnProject())
	p.PATCH("/shots/:sid", h.UpdateShot)
	p.PATCH("/characters/:cid", h.UpdateCharacter)
	p.PATCH("/scenes/:sid", h.UpdateScene)
	return s, db, h, r, uid
}
func doJSON(r *gin.Engine, method, path, body, token string) (int, map[string]interface{}) {
	req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var b map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &b)
	return w.Code, b
}
func putOTP(t *testing.T, db *sql.DB) {
	t.Helper()
	derive := hmac.New(sha256.New, []byte(testSecret))
	derive.Write([]byte("drama-platform/auth/login-code/v1"))
	mac := hmac.New(sha256.New, derive.Sum(nil))
	mac.Write([]byte("person@example.test\x00" + "123456"))
	sum := mac.Sum(nil)
	if _, err := db.Exec(`INSERT INTO login_codes(email,code_hash,expires_at) VALUES('person@example.test',$1,now()+interval '10 minutes')`, "v1:"+hex.EncodeToString(sum)); err != nil {
		t.Fatal(err)
	}
}
func bearer(t *testing.T, s *service.Service, h *Handler, uid string) string {
	t.Helper()
	u, err := s.GetUser(uid)
	if err != nil {
		t.Fatal(err)
	}
	return h.issueToken(u)
}
func TestEmailLoginRequiresTOTPWithoutConsumingChallenge(t *testing.T) {
	_, db, _, r, uid := authHTTP(t)
	putOTP(t, db)
	if _, err := db.Exec(`UPDATE users SET totp_enabled=true,totp_secret='JBSWY3DPEHPK3PXP' WHERE id=$1`, uid); err != nil {
		t.Fatal(err)
	}
	status, b := doJSON(r, "POST", "/auth/login-by-code", `{"email":"person@example.test","code":"123456"}`, "")
	if status != 401 || b["needTotp"] != true || b["data"] != nil {
		t.Fatalf("got status=%d response=%v; want needTotp without token", status, b)
	}
	var used bool
	db.QueryRow(`SELECT used_at IS NOT NULL FROM login_codes`).Scan(&used)
	if used {
		t.Fatal("missing second factor consumed OTP")
	}
}
func TestEmailLoginClearsFailuresAndAudits(t *testing.T) {
	_, db, _, r, uid := authHTTP(t)
	putOTP(t, db)
	db.Exec(`UPDATE users SET failed_login_count=4 WHERE id=$1`, uid)
	status, _ := doJSON(r, "POST", "/auth/login-by-code", `{"email":" PERSON@EXAMPLE.TEST ","code":"123456"}`, "")
	if status != 200 {
		t.Fatalf("normalized login returned %d", status)
	}
	var failures, audits int
	db.QueryRow(`SELECT failed_login_count FROM users WHERE id=$1`, uid).Scan(&failures)
	db.QueryRow(`SELECT count(*) FROM audit_logs WHERE user_id=$1 AND action='login'`, uid).Scan(&audits)
	if failures != 0 || audits != 1 {
		t.Fatalf("success left failures=%d audits=%d", failures, audits)
	}
}
func TestPasswordEmailNormalization(t *testing.T) {
	_, _, _, r, _ := authHTTP(t)
	status, b := doJSON(r, "POST", "/auth/login", `{"email":" PERSON@EXAMPLE.TEST ","password":"SyntheticPassword12"}`, "")
	if status != 200 {
		t.Fatalf("normalized password login status=%d response=%v", status, b)
	}
}
func TestUnownedChallengeDoesNotLockVictim(t *testing.T) {
	_, db, _, r, uid := authHTTP(t)
	for i := 0; i < 8; i++ {
		doJSON(r, "POST", "/auth/login-by-code", `{"email":"person@example.test","code":"999999"}`, "")
	}
	var count int
	db.QueryRow(`SELECT failed_login_count FROM users WHERE id=$1`, uid).Scan(&count)
	if count != 0 {
		t.Fatalf("no valid challenge caused %d victim account failures", count)
	}
}
func TestCurrentIdentityInvalidatesOldSessions(t *testing.T) {
	for _, mutation := range []string{"status='disabled'", "role='user'", "password_hash='changed'"} {
		t.Run(mutation, func(t *testing.T) {
			s, db, h, r, uid := authHTTP(t)
			token := bearer(t, s, h, uid)
			if _, err := db.Exec(`UPDATE users SET `+mutation+` WHERE id=$1`, uid); err != nil {
				t.Fatal(err)
			}
			status, _ := doJSON(r, "GET", "/admin/test", "", token)
			if status != 401 && status != 403 {
				t.Fatalf("old privileged token remained valid after %s: %d", mutation, status)
			}
		})
	}
}
func TestTOTPSetupReturnsLocalPNG(t *testing.T) {
	s, _, h, r, uid := authHTTP(t)
	status, b := doJSON(r, "POST", "/auth/totp/setup", `{}`, bearer(t, s, h, uid))
	if status != 200 {
		t.Fatalf("setup failed %v", b)
	}
	data := b["data"].(map[string]interface{})
	qr, _ := data["qrDataUrl"].(string)
	if !strings.HasPrefix(qr, "data:image/png;base64,") {
		t.Fatal("setup lacks local PNG data URL")
	}
}
func TestParentChildOwnershipForAllResources(t *testing.T) {
	for _, role := range []string{"user", "admin"} {
		t.Run(role, func(t *testing.T) {
			s, db, h, r, uid := authHTTP(t)
			db.Exec(`UPDATE users SET role=$1 WHERE id=$2`, role, uid)
			var other, own, foreign string
			if err := db.QueryRow(`INSERT INTO users(email,password_hash,nickname) VALUES('other@example.test','unused','Other') RETURNING id`).Scan(&other); err != nil {
				t.Fatal(err)
			}
			if err := db.QueryRow(`INSERT INTO projects(user_id,title,idea) VALUES($1,'Own','synthetic') RETURNING id`, uid).Scan(&own); err != nil {
				t.Fatal(err)
			}
			if err := db.QueryRow(`INSERT INTO projects(user_id,title,idea) VALUES($1,'Foreign','synthetic') RETURNING id`, other).Scan(&foreign); err != nil {
				t.Fatal(err)
			}
			token := bearer(t, s, h, uid)
			for _, resource := range []struct{ table, insert, body, column string }{
				{"shots", `INSERT INTO shots(project_id,shot_code,seq,visual_prompt) VALUES($1,'S01',1,'original') RETURNING id`, `{"visualPrompt":"changed"}`, "visual_prompt"},
				{"characters", `INSERT INTO characters(project_id,code,name) VALUES($1,'C01','original') RETURNING id`, `{"name":"changed"}`, "name"},
				{"scenes", `INSERT INTO scenes(project_id,code,name) VALUES($1,'L01','original') RETURNING id`, `{"name":"changed"}`, "name"},
			} {
				var id string
				if err := db.QueryRow(resource.insert, foreign).Scan(&id); err != nil {
					t.Fatal(err)
				}
				status, _ := doJSON(r, "PATCH", "/projects/"+own+"/"+resource.table+"/"+id, resource.body, token)
				if status < 400 {
					t.Errorf("%s foreign child accepted under own parent: %d", resource.table, status)
				}
				var value string
				db.QueryRow("SELECT "+resource.column+" FROM "+resource.table+" WHERE id=$1", id).Scan(&value)
				if value != "original" {
					t.Errorf("%s foreign data changed", resource.table)
				}
				var validID string
				if err := db.QueryRow(resource.insert, own).Scan(&validID); err != nil {
					t.Fatal(err)
				}
				status, response := doJSON(r, "PATCH", "/projects/"+own+"/"+resource.table+"/"+validID, resource.body, token)
				if status != 200 {
					t.Errorf("%s matching child rejected: %d %v", resource.table, status, response)
				}
				db.QueryRow("SELECT "+resource.column+" FROM "+resource.table+" WHERE id=$1", validID).Scan(&value)
				if value != "changed" {
					t.Errorf("%s matching update had no effect", resource.table)
				}
			}
		})
	}
}

func TestEmailLoginCommitFailureRollsBackBothFactors(t *testing.T) {
	_, db, _, r, uid := authHTTP(t)
	putOTP(t, db)
	sum := sha256.Sum256([]byte("AB12CD34EF"))
	codes, _ := json.Marshal([]string{hex.EncodeToString(sum[:])})
	if _, err := db.Exec(`UPDATE users SET totp_enabled=true,totp_secret='JBSWY3DPEHPK3PXP',recovery_codes=$1 WHERE id=$2`, string(codes), uid); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE FUNCTION reject_login_commit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.used_at IS NOT NULL THEN RAISE EXCEPTION 'synthetic commit failure'; END IF; RETURN NEW; END $$;
 CREATE CONSTRAINT TRIGGER reject_login_commit AFTER UPDATE ON login_codes DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE PROCEDURE reject_login_commit()`); err != nil {
		t.Fatal(err)
	}
	status, b := doJSON(r, "POST", "/auth/login-by-code", `{"email":"person@example.test","code":"123456","totpCode":"AB12CD34EF"}`, "")
	if status != 503 || b["data"] != nil {
		t.Fatalf("commit failure issued authentication: %d %v", status, b)
	}
	var used bool
	var recovery string
	var audits int
	db.QueryRow("SELECT used_at IS NOT NULL FROM login_codes").Scan(&used)
	db.QueryRow("SELECT recovery_codes FROM users WHERE id=$1", uid).Scan(&recovery)
	db.QueryRow("SELECT count(*) FROM audit_logs").Scan(&audits)
	if used || recovery != string(codes) || audits != 0 {
		t.Fatalf("failed commit leaked state: used=%v recovery=%s audits=%d", used, recovery, audits)
	}
	if _, err := db.Exec("DROP TRIGGER reject_login_commit ON login_codes"); err != nil {
		t.Fatal(err)
	}
	status, b = doJSON(r, "POST", "/auth/login-by-code", `{"email":"person@example.test","code":"123456","totpCode":"ab12cd34ef"}`, "")
	if status != 200 {
		t.Fatalf("same two credentials did not recover after failed commit: %d %v", status, b)
	}
	status, _ = doJSON(r, "POST", "/auth/login-by-code", `{"email":"person@example.test","code":"123456","totpCode":"AB12CD34EF"}`, "")
	if status == 200 {
		t.Fatal("consumed credentials reused")
	}
}

func TestLockedAndDisabledPoliciesMatchAcrossLoginMethods(t *testing.T) {
	for _, state := range []string{"disabled", "locked"} {
		t.Run(state, func(t *testing.T) {
			_, db, _, r, uid := authHTTP(t)
			putOTP(t, db)
			mutation := "status='disabled'"
			want := 401
			if state == "locked" {
				mutation = "locked_until=now()+interval '15 minutes'"
				want = 429
			}
			db.Exec("UPDATE users SET "+mutation+" WHERE id=$1", uid)
			for _, body := range []struct{ path, body string }{
				{"/auth/login", `{"email":" PERSON@EXAMPLE.TEST ","password":"SyntheticPassword12"}`},
				{"/auth/login-by-code", `{"email":" PERSON@EXAMPLE.TEST ","code":"123456"}`},
			} {
				status, response := doJSON(r, "POST", body.path, body.body, "")
				if status != want || response["data"] != nil {
					t.Errorf("%s %s status=%d want %d", state, body.path, status, want)
				}
			}
		})
	}
}
