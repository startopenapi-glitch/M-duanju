package service

import "testing"

func TestRechargeBonusTiers(t *testing.T) {
	cases := []struct {
		amountCents int64
		credits     int64
	}{
		{10000, 10000},
		{50000, 66667},
		{300000, 461538},
		{12345, 12345}, // exact tier map only
	}
	for _, tc := range cases {
		if got := rechargeCredits(tc.amountCents); got != tc.credits {
			t.Fatalf("rechargeCredits(%d)=%d want %d", tc.amountCents, got, tc.credits)
		}
	}
}

func TestRefundCreditsProportionalToBonus(t *testing.T) {
	if got := refundCredits(50000, 50000, 66667); got != 66667 {
		t.Fatalf("full bonus refund=%d", got)
	}
	if got := refundCredits(25000, 50000, 66667); got != 33334 {
		t.Fatalf("half bonus refund=%d", got)
	}
	if got := refundCredits(10000, 10000, 10000); got != 10000 {
		t.Fatalf("plain refund=%d", got)
	}
}
