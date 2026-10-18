package handler

import (
	"fmt"
	"testing"
)

func TestProjectEstimateContract(t *testing.T) {
	s, _, h, r, uid := authHTTP(t)
	r.GET("/projects/estimate", h.AuthMiddleware(testSecret), h.EstimateProject)
	token := bearer(t, s, h, uid)
	t.Setenv("VIDEO_RESOLUTION", "720P")
	for _, v := range []struct {
		seconds         int
		credits, budget float64
	}{{5, 230, 5}, {15, 630, 7}, {30, 1230, 13}, {60, 2430, 25}} {
		status, b := doJSON(r, "GET", fmt.Sprintf("/projects/estimate?totalSeconds=%d", v.seconds), "", token)
		if status != 200 {
			t.Fatal(status, b)
		}
		d := b["data"].(map[string]interface{})
		if d["estimatedCredits"] != v.credits || d["suggestedBudgetYuan"] != v.budget {
			t.Fatal(d)
		}
	}
	for _, v := range []string{"0", "7", "181", "x"} {
		status, _ := doJSON(r, "GET", "/projects/estimate?totalSeconds="+v, "", token)
		if status != 400 {
			t.Fatal(status)
		}
	}
	status, _ := doJSON(r, "GET", "/projects/estimate?totalSeconds=5", "", "")
	if status != 401 {
		t.Fatal(status)
	}
}
