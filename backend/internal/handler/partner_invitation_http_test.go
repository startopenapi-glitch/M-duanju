package handler

import (
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"drama-platform/internal/service"
	"drama-platform/internal/testutil"
)

func TestPartnerInvitationHTTPMailLookupAcceptAndAdmin(t *testing.T) {
	t.Setenv("PUBLIC_BASE_URL", "https://drama.example.test")
	gin.SetMode(gin.TestMode)
	db := testutil.DB(t)
	var sentTo, sentLink string
	s := service.New(db, testSecret, service.WithPartnerInviteSender(func(to, name, link string) error {
		sentTo, sentLink = to, link
		return nil
	}))
	h := New(s, testSecret)
	if _, err := db.Exec(`INSERT INTO users(id,email,password_hash,nickname,role,status)
		VALUES('admin','admin@example.test','unused','Admin','admin','active'),
		       ('invitee','invitee@example.test','unused','Invitee','user','active'),
		       ('other','other@example.test','unused','Other','user','active')`); err != nil {
		t.Fatal(err)
	}
	r := gin.New()
	r.GET("/partner/invitations/lookup", h.PartnerInvitationLookup)
	auth := r.Group("")
	auth.Use(h.AuthMiddleware(testSecret))
	auth.POST("/partner/invitations/accept", h.PartnerInvitationAccept)
	admin := auth.Group("/admin")
	admin.Use(AdminMiddleware())
	admin.POST("/partner-invitations", h.AdminCreatePartnerInvitation)
	admin.GET("/partner-invitations", h.AdminListPartnerInvitations)
	admin.POST("/partner-invitations/:id/resend", h.AdminResendPartnerInvitation)
	admin.POST("/partner-invitations/:id/revoke", h.AdminRevokePartnerInvitation)

	adminToken, inviteeToken, otherToken := bearer(t, s, h, "admin"), bearer(t, s, h, "invitee"), bearer(t, s, h, "other")
	code, body := doJSON(r, "POST", "/admin/partner-invitations", `{"email":"Invitee@Example.Test","wechat":"wx"}`, adminToken)
	if code != 200 {
		t.Fatalf("create invite = %d %v", code, body)
	}
	data := body["data"].(map[string]interface{})
	if data["email"] != "invitee@example.test" || data["name"] != "invitee" || data["status"] != "pending" || data["id"] == "" {
		t.Fatalf("invite response: %v", data)
	}
	if sentTo != "invitee@example.test" || !strings.Contains(sentLink, "/partner/invite?token=") {
		t.Fatalf("mail transport: to=%q link=%q", sentTo, sentLink)
	}
	u, err := url.Parse(sentLink)
	if err != nil {
		t.Fatal(err)
	}
	token := u.Query().Get("token")
	if token == "" || strings.ContainsAny(token, " \r\n") {
		t.Fatalf("mail token invalid: %q", token)
	}
	if raw, _ := json.Marshal(body); strings.Contains(string(raw), token) {
		t.Fatal("admin response leaked raw invite token")
	}

	code, body = doJSON(r, "GET", "/partner/invitations/lookup?token="+url.QueryEscape(token), "", "")
	if code != 200 {
		t.Fatalf("lookup = %d %v", code, body)
	}
	lookup := body["data"].(map[string]interface{})
	if lookup["email"] != "invitee@example.test" || lookup["name"] != "invitee" {
		t.Fatalf("lookup data: %v", lookup)
	}

	code, body = doJSON(r, "POST", "/partner/invitations/accept", `{"token":"`+token+`"}`, otherToken)
	if code != 403 || body["code"] != "email_mismatch" {
		t.Fatalf("wrong-email accept = %d %v", code, body)
	}
	code, body = doJSON(r, "POST", "/partner/invitations/accept", `{"token":"`+token+`"}`, inviteeToken)
	if code != 200 {
		t.Fatalf("accept = %d %v", code, body)
	}
	partner := body["data"].(map[string]interface{})
	if partner["userId"] != "invitee" || partner["status"] != "active" || partner["code"] == "" {
		t.Fatalf("accepted partner: %v", partner)
	}
	if code, body = doJSON(r, "GET", "/partner/invitations/lookup?token="+url.QueryEscape(token), "", ""); code != 410 || body["code"] != "invite_used" {
		t.Fatalf("used lookup = %d %v", code, body)
	}

	// Admin list shows the accepted invitation; resend/revoke is no longer possible.
	code, body = doJSON(r, "GET", "/admin/partner-invitations?status=accepted", "", adminToken)
	if code != 200 {
		t.Fatalf("list = %d %v", code, body)
	}
	page := body["data"].(map[string]interface{})
	if page["total"] != float64(1) {
		t.Fatalf("accepted list: %v", page)
	}
	id := data["id"].(string)
	if code, body = doJSON(r, "POST", "/admin/partner-invitations/"+id+"/resend", `{}`, adminToken); code != 410 {
		t.Fatalf("resend accepted = %d %v", code, body)
	}
	if code, body = doJSON(r, "POST", "/admin/partner-invitations/"+id+"/revoke", `{}`, adminToken); code != 410 {
		t.Fatalf("revoke accepted = %d %v", code, body)
	}
}

func TestPartnerInvitationHTTPMailFailureKeepsInvitationAndRevoke(t *testing.T) {
	t.Setenv("PUBLIC_BASE_URL", "https://drama.example.test")
	db := testutil.DB(t)
	s := service.New(db, testSecret, service.WithPartnerInviteSender(func(to, name, link string) error {
		return errors.New("synthetic mail failure")
	}))
	h := New(s, testSecret)
	if _, err := db.Exec(`INSERT INTO users(id,email,password_hash,nickname,role,status)
		VALUES('admin','admin@example.test','unused','Admin','admin','active')`); err != nil {
		t.Fatal(err)
	}
	r := gin.New()
	auth := r.Group("")
	auth.Use(h.AuthMiddleware(testSecret))
	admin := auth.Group("/admin")
	admin.Use(AdminMiddleware())
	admin.POST("/partner-invitations", h.AdminCreatePartnerInvitation)
	admin.GET("/partner-invitations", h.AdminListPartnerInvitations)
	admin.POST("/partner-invitations/:id/revoke", h.AdminRevokePartnerInvitation)
	token := bearer(t, s, h, "admin")
	code, body := doJSON(r, "POST", "/admin/partner-invitations", `{"email":"pending@example.test","wechat":"wx-only"}`, token)
	if code != 503 || body["code"] != "invite_mail_failed" {
		t.Fatalf("mail failure = %d %v", code, body)
	}
	data := body["data"].(map[string]interface{})
	id := data["id"].(string)
	code, body = doJSON(r, "GET", "/admin/partner-invitations?status=pending", "", token)
	if code != 200 || body["data"].(map[string]interface{})["total"] != float64(1) {
		t.Fatalf("pending invitation was not retained: %d %v", code, body)
	}
	if code, body = doJSON(r, "POST", "/admin/partner-invitations/"+id+"/revoke", `{}`, token); code != 200 {
		t.Fatalf("revoke = %d %v", code, body)
	}
}
