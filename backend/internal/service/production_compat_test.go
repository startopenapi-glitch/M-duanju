package service

import (
	"fmt"
	"testing"
)

func TestProductionCommercialRatesPreserved(t *testing.T) {
	for _, c := range []struct {
		resolution string
		want       int64
	}{{"standard", 100}, {"720P", 200}, {"1080P", 400}, {"unknown", 200}} {
		if got := EstimateShotCost(5, c.resolution); got != c.want {
			t.Errorf("%s got %d want %d", c.resolution, got, c.want)
		}
	}
	if got := EstimateShotCost(1, "standard"); got != 80 {
		t.Fatal(got)
	}
	t.Setenv("VIDEO_RESOLUTION", "720P")
	e, err := EstimateProject(15)
	if err != nil || e.EstimatedCredits != 630 || e.SuggestedBudgetYuan != 7 {
		t.Fatalf("estimate %+v %v", e, err)
	}
}

func TestProductionVideoRoundingAndReplayPreserved(t *testing.T) {
	for _, c := range []struct{ micros, credits int64 }{{0, 0}, {4999, 0}, {5000, 1}, {9999, 1}, {10000, 1}, {14999, 1}, {15000, 2}, {2005000, 201}} {
		t.Run(fmt.Sprint(c.micros), func(t *testing.T) {
			s, db, in := settlementFixture(t)
			in.ChargeMicros = c.micros
			for i := 0; i < 2; i++ {
				if _, err := s.SettleGeneration(in); err != nil {
					t.Fatal(err)
				}
			}
			rows := 0
			if c.credits > 0 {
				rows = 1
			}
			assertSettlementTotals(t, db, 1000-c.credits, c.credits, rows)
			if _, err := s.checkGenerationDelivery(in); err != nil {
				t.Fatal("replay metadata rejected", err)
			}
		})
	}
}
