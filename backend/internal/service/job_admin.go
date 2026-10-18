package service

import (
	"database/sql"
	"errors"
)

// AbandonJob 释放一个永远无法核对的任务（没有上游身份，无法查询也无法结算）。
// 只允许对 reconcile/failed 且 upstream_id 为空的任务执行：一旦有上游 ID 就必须走
// ReconcileSubmission 绑定，禁止用放弃来掩盖重复提交。
// 释放占用是安全的：没有上游 ID 就不会产生结算，也就不会扣用户的钱。
func (s *Service) AbandonJob(project, id, reason string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, _, _, _, err = lockQueueProject(tx, project); err != nil {
		return err
	}
	var status, phase, task, shot string
	if err = tx.QueryRow(`SELECT status,phase,COALESCE(upstream_id,''),COALESCE(shot_id,'')
	 FROM jobs WHERE id=$1 AND project_id=$2 FOR UPDATE`, id, project).Scan(&status, &phase, &task, &shot); err != nil {
		return err
	}
	if task != "" {
		return errors.New("任务已有上游身份，请核对并绑定，不能放弃")
	}
	if status != JobReconcile && status != JobFailed {
		return errors.New("任务不在可放弃状态")
	}
	if reason == "" {
		reason = "运营确认上游无对应任务，释放占用"
	}
	if err = settlementExecOne(tx, "abandon job", `UPDATE jobs SET status='failed',phase='abandoned',
	 reserved_credits=0,error_code='abandoned',last_error=$2,lease_token=lease_token+1,updated_at=now()
	 WHERE id=$1`, id, reason); err != nil {
		return err
	}
	if shot != "" {
		if err = settlementExecOne(tx, "abandon shot", `UPDATE shots SET status='PENDING',updated_at=now()
		 WHERE id=$1 AND project_id=$2`, shot, project); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(`UPDATE projects SET status=CASE WHEN status='SHOTS_GENERATING' THEN 'SHOTS_REVIEW' ELSE status END,
	 updated_at=now() WHERE id=$1`, project); err != nil {
		return err
	}
	return tx.Commit()
}

var _ = sql.ErrNoRows
