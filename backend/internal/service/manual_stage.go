package service

import (
	"errors"
	"fmt"
)

// ConfirmScript is the manual gate between a reviewed script and paid key-visual
// generation. The actual job creation remains in EnqueueJob so reservation,
// funds and the KEYVISUAL_GENERATING transition stay in one transaction.
func (s *Service) ConfirmScript(userID, projectID string) (string, error) {
	p, err := s.GetProject(projectID)
	if err != nil {
		return "", err
	}
	if p.UserID != userID {
		return "", ErrRunNotAuthorized
	}
	if p.WorkflowMode != "manual" {
		return "", errors.New("该项目不是手动模式")
	}
	var existing string
	if err = s.db.QueryRow(`SELECT id FROM jobs WHERE project_id=$1 AND kind='keyvisual'
		AND status IN ('queued','running','reconcile') ORDER BY created_at DESC LIMIT 1`, projectID).Scan(&existing); err == nil {
		return existing, nil
	}
	if p.Status != "SCRIPT_REVIEW" {
		return "", fmt.Errorf("剧本尚未进入确认状态（当前 %s）", p.Status)
	}
	if len(p.Characters) == 0 || len(p.Scenes) == 0 || len(p.Shots) == 0 {
		return "", errors.New("剧本内容不完整，请先生成完整剧本")
	}
	id, err := s.EnqueueJob(JobKeyVisual, projectID, "", 0, "")
	if err != nil {
		return "", err
	}
	s.Log(userID, projectID, "", "script_confirmed", "SCRIPT_REVIEW", "KEYVISUAL_GENERATING", "job="+id)
	return id, nil
}

// ConfirmKeyVisual is the manual gate between an approved key visual and paid
// shot generation. GenerateShots owns the project lock, preflight and job insert.
func (s *Service) ConfirmKeyVisual(userID, projectID string) (int, error) {
	p, err := s.GetProject(projectID)
	if err != nil {
		return 0, err
	}
	if p.UserID != userID {
		return 0, ErrRunNotAuthorized
	}
	if p.WorkflowMode != "manual" {
		return 0, errors.New("该项目不是手动模式")
	}
	if p.Status != "KEYVISUAL_REVIEW" {
		return 0, fmt.Errorf("定妆照尚未进入确认状态（当前 %s）", p.Status)
	}
	if p.KeyvisualURL == "" {
		return 0, errors.New("定妆照尚未就绪")
	}
	var active int
	if err = s.db.QueryRow(`SELECT count(*) FROM jobs WHERE project_id=$1 AND kind='shot'
		AND status IN ('queued','running','reconcile')`, projectID).Scan(&active); err != nil {
		return 0, err
	}
	if active > 0 {
		return 0, errors.New("镜头任务已经派发，请刷新后继续")
	}
	n, err := s.GenerateShots(projectID, userID, nil)
	if err != nil {
		return 0, err
	}
	s.Log(userID, projectID, "", "keyvisual_confirmed", "KEYVISUAL_REVIEW", "SHOTS_GENERATING", fmt.Sprintf("shots=%d", n))
	return n, nil
}
