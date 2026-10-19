package partner

import (
	"errors"
	"strings"
	"testing"
	"time"
)

const testSigningFixture = "fixture-only-value"

func TestCandidateRoundTrip(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	raw := SignCandidate(testSigningFixture, "abcd1234efgh", now, 30*24*time.Hour)
	got, err := VerifyCandidate(testSigningFixture, raw, now.Add(time.Hour))
	if err != nil || got != "abcd1234efgh" {
		t.Fatalf("round trip = %q, %v", got, err)
	}
}

func TestCandidateExpired(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	raw := SignCandidate(testSigningFixture, "abcd1234efgh", now, time.Hour)
	if _, err := VerifyCandidate(testSigningFixture, raw, now.Add(2*time.Hour)); !errors.Is(err, ErrCandidateExpired) {
		t.Fatalf("want ErrCandidateExpired, got %v", err)
	}
}

func TestCandidateTamperedCodeRejected(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	raw := SignCandidate(testSigningFixture, "aaaa1111bbbb", now, time.Hour)
	// 篡改推广码：签名必须失配
	forged := strings.Replace(raw, "aaaa1111bbbb", "cccc2222dddd", 1)
	if _, err := VerifyCandidate(testSigningFixture, forged, now); !errors.Is(err, ErrCandidateInvalid) {
		t.Fatalf("want ErrCandidateInvalid for forged code, got %v", err)
	}
}

func TestCandidateTamperedExpiryRejected(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	raw := SignCandidate(testSigningFixture, "aaaa1111bbbb", now, time.Hour)
	parts := strings.Split(raw, ".")
	parts[1] = "99999999999" // 延长有效期
	if _, err := VerifyCandidate(testSigningFixture, strings.Join(parts, "."), now); !errors.Is(err, ErrCandidateInvalid) {
		t.Fatalf("want ErrCandidateInvalid for forged expiry, got %v", err)
	}
}

func TestCandidateWrongSecretRejected(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	raw := SignCandidate("other-secret", "aaaa1111bbbb", now, time.Hour)
	if _, err := VerifyCandidate(testSigningFixture, raw, now); !errors.Is(err, ErrCandidateInvalid) {
		t.Fatalf("want ErrCandidateInvalid for wrong secret, got %v", err)
	}
}

func TestCandidateRejectsGarbage(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	for _, raw := range []string{"", "v1", "v2.1.a.b", "v1.x.a.b", "v1.1..b", "not-a-cookie"} {
		if _, err := VerifyCandidate(testSigningFixture, raw, now); err == nil {
			t.Fatalf("garbage %q accepted", raw)
		}
	}
}
