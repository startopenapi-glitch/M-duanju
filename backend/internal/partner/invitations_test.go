package partner

import (
	"errors"
	"testing"
)

func TestInvitationCreateLookupAccept(t *testing.T) {
	s, ctx, mkUser := fixture(t)
	mkUser("admin")
	mkUser("invitee")
	inv, token, err := s.CreateInvitation(ctx, "admin", " Invitee@Example.com ", "合作一号", "简介", "wx1")
	if err != nil {
		t.Fatal(err)
	}
	if inv.Status != InvitePending || token == "" || inv.Email != "invitee@example.com" {
		t.Fatalf("unexpected invitation: %+v token=%q", inv, token)
	}
	lookup, err := s.LookupInvitation(ctx, token)
	if err != nil || !lookup.Valid || lookup.Email != inv.Email || lookup.Name != "合作一号" {
		t.Fatalf("lookup: %+v %v", lookup, err)
	}
	if _, err = s.ByUser(ctx, "invitee"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("invite must not open partner before accept: %v", err)
	}
	p, err := s.AcceptInvitation(ctx, token, "invitee")
	if err != nil {
		t.Fatal(err)
	}
	if p.UserID != "invitee" || p.Name != "合作一号" || p.Status != StatusActive {
		t.Fatalf("accepted partner: %+v", p)
	}
	if len(p.Code) < 12 {
		t.Fatalf("code too short: %q", p.Code)
	}
	accepted, err := s.InvitationByID(ctx, inv.ID)
	if err != nil || accepted.Status != InviteAccepted || accepted.AcceptedUserID != "invitee" || accepted.AcceptedPartnerID != p.ID {
		t.Fatalf("invitation not closed: %+v %v", accepted, err)
	}
	if _, err = s.LookupInvitation(ctx, token); !errors.Is(err, ErrInviteUsed) {
		t.Fatalf("used token lookup: %v", err)
	}
	// A retry after a lost accept response is idempotent for the same account.
	again, err := s.AcceptInvitation(ctx, token, "invitee")
	if err != nil || again.ID != p.ID {
		t.Fatalf("idempotent accept: %+v %v", again, err)
	}
	var audits int
	if err = s.db.QueryRowContext(ctx, `SELECT count(*) FROM partner_audit_events WHERE partner_id=$1 AND action='invite_accept'`, p.ID).Scan(&audits); err != nil || audits != 1 {
		t.Fatalf("invite audit: %d %v", audits, err)
	}
}

func TestInvitationEmailMismatchAndRevoke(t *testing.T) {
	s, ctx, mkUser := fixture(t)
	mkUser("admin")
	mkUser("invitee")
	mkUser("other")
	inv, token, err := s.CreateInvitation(ctx, "admin", "invitee@example.com", "合作", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.AcceptInvitation(ctx, token, "other"); !errors.Is(err, ErrInviteEmail) {
		t.Fatalf("wrong email accepted: %v", err)
	}
	if _, err = s.RevokeInvitation(ctx, "admin", inv.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.LookupInvitation(ctx, token); !errors.Is(err, ErrInviteInvalid) {
		t.Fatalf("revoked lookup: %v", err)
	}
	if _, err = s.AcceptInvitation(ctx, token, "invitee"); !errors.Is(err, ErrInviteInvalid) {
		t.Fatalf("revoked accept: %v", err)
	}
}

func TestInvitationResendRotatesTokenAndCreateReusesPending(t *testing.T) {
	s, ctx, mkUser := fixture(t)
	mkUser("admin")
	mkUser("invitee")
	inv, oldToken, err := s.CreateInvitation(ctx, "admin", "invitee@example.com", "合作", "", "")
	if err != nil {
		t.Fatal(err)
	}
	again, newToken, err := s.CreateInvitation(ctx, "admin", "invitee@example.com", "合作二号", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if again.ID != inv.ID || newToken == oldToken || again.SendCount != 2 {
		t.Fatalf("pending invitation should be reused: %+v old=%q new=%q", again, oldToken, newToken)
	}
	if _, err = s.LookupInvitation(ctx, oldToken); !errors.Is(err, ErrInviteInvalid) {
		t.Fatalf("old token still valid: %v", err)
	}
	if _, err = s.LookupInvitation(ctx, newToken); err != nil {
		t.Fatalf("new token invalid: %v", err)
	}
	resend, thirdToken, err := s.ResendInvitation(ctx, "admin", inv.ID)
	if err != nil || thirdToken == newToken || resend.SendCount != 3 {
		t.Fatalf("resend: %+v %v", resend, err)
	}
}

func TestInvitationExpiry(t *testing.T) {
	s, ctx, mkUser := fixture(t)
	mkUser("admin")
	mkUser("invitee")
	inv, token, err := s.CreateInvitation(ctx, "admin", "invitee@example.com", "合作", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.ExecContext(ctx, `UPDATE partner_invitations SET expires_at=now()-interval '1 minute' WHERE id=$1`, inv.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.LookupInvitation(ctx, token); !errors.Is(err, ErrInviteExpired) {
		t.Fatalf("expired lookup: %v", err)
	}
	if _, err = s.AcceptInvitation(ctx, token, "invitee"); !errors.Is(err, ErrInviteExpired) {
		t.Fatalf("expired accept: %v", err)
	}
}

func TestInvitationRejectsExistingPartnerAndLists(t *testing.T) {
	s, ctx, mkUser := fixture(t)
	mkUser("admin")
	mkUser("invitee")
	if _, err := s.Create(ctx, "admin", "invitee", "已开通", "", ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.CreateInvitation(ctx, "admin", "invitee@example.com", "重复", "", ""); !errors.Is(err, ErrInviteMailUser) {
		t.Fatalf("existing partner invited: %v", err)
	}
	// A separate mailbox can be listed and filtered.
	if _, _, err := s.CreateInvitation(ctx, "admin", "new@example.com", "新代理", "", ""); err != nil {
		t.Fatal(err)
	}
	page, err := s.InvitationsPage(ctx, "new", InvitePending, 1, 20)
	if err != nil || page.Total != 1 {
		t.Fatalf("pending list: %+v %v", page, err)
	}
	items, ok := page.Items.([]Invitation)
	if !ok || len(items) != 1 || items[0].Email != "new@example.com" {
		t.Fatalf("list items: %#v", page.Items)
	}
}

func TestInvitationConcurrentAcceptCreatesOnePartner(t *testing.T) {
	s, ctx, mkUser := fixture(t)
	mkUser("admin")
	mkUser("invitee")
	_, token, err := s.CreateInvitation(ctx, "admin", "invitee@example.com", "并发合作", "", "")
	if err != nil {
		t.Fatal(err)
	}
	const n = 6
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		go func() {
			_, e := s.AcceptInvitation(ctx, token, "invitee")
			errs <- e
		}()
	}
	for i := 0; i < n; i++ {
		if e := <-errs; e != nil {
			t.Fatalf("concurrent accept %d: %v", i, e)
		}
	}
	var partners int
	if err = s.db.QueryRowContext(ctx, `SELECT count(*) FROM partners WHERE user_id='invitee'`).Scan(&partners); err != nil {
		t.Fatal(err)
	}
	if partners != 1 {
		t.Fatalf("concurrent accept created %d partners", partners)
	}
}
