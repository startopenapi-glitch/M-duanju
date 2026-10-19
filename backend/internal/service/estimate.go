package service

import "errors"

type ProjectEstimate struct {
	EstimatedCredits    int64  `json:"estimatedCredits"`
	SuggestedBudgetYuan int64  `json:"suggestedBudgetYuan"`
	Resolution          string `json:"resolution"`
	SegmentCount        int    `json:"segmentCount"`
}

func EstimateProject(seconds int) (ProjectEstimate, error) {
	if seconds < 5 || seconds > 180 || seconds%5 != 0 {
		return ProjectEstimate{}, errors.New("时长必须为5–180秒且为5的倍数")
	}
	resolution := getenvDefault("VIDEO_RESOLUTION", "720P")
	credits := 30 + int64(seconds/5)*EstimateShotCost(5, resolution)
	budget := (credits + 99) / 100
	if budget < 5 {
		budget = 5
	}
	return ProjectEstimate{credits, budget, resolution, seconds / 5}, nil
}
