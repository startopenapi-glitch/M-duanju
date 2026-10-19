package service

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

var errJobTerminal = errors.New("job already terminal")

// GenerateOneShot keeps the old public entrypoint but can no longer submit upstream.
func (s *Service) GenerateOneShot(project, shot string, seq int) error {
	_, err := s.EnqueueJob(JobShot, project, shot, seq, "")
	return err
}
func (s *Service) PollAllGenerating() { s.adoptLegacyJobs("") }

// ExecuteJob uses an execution-local scope. Renewal is independent of network,
// conversion and filesystem work; a failed renewal prevents publication via fencing.
func (s *Service) ExecuteJob(j *Job) error {
	scoped := s.withJob(j)
	stop, stopped := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				if scoped.RenewJob(j.ID, j.LeaseToken) != nil {
					return
				}
			}
		}
	}()
	defer func() { close(stop); <-stopped }()
	pending, err := scoped.processJob(j)
	if errors.Is(err, errAccountPaused) {
		return scoped.deferJob(j)
	}
	if errors.Is(err, errJobTerminal) {
		return nil
	}
	if errors.Is(err, errJobCancelled) {
		return nil
	}
	if errors.Is(err, ErrLeaseLost) {
		return err
	}
	if err != nil {
		var pending *SettlementPendingError
		if errors.As(err, &pending) {
			return errors.Join(err, scoped.parkSettlement(j, pending))
		}
		_, saveErr := scoped.FailJob(j.ID, j.LeaseToken, err, !errors.Is(err, ErrSubmissionUnknown))
		return errors.Join(err, saveErr)
	}
	if pending {
		return scoped.deferJob(j)
	}
	return scoped.FinishJob(j.ID, j.LeaseToken, j.UpstreamID)
}
func (s *Service) processJob(j *Job) (bool, error) {
	if err := s.checkLease(); err != nil {
		return false, err
	}
	// The provider's terminal failure is already durable. A crash before FailJob
	// releases its reservation must be recoverable even after the run is cancelled;
	// querying or submitting upstream again adds no evidence to this known result.
	if j.Phase == "provider_failed" && j.UpstreamID != "" {
		reason := j.LastError
		if reason == "" {
			reason = "上游生成已确认失败"
		}
		if _, err := s.FailJob(j.ID, j.LeaseToken, errors.New(reason), false); err != nil {
			return false, err
		}
		return false, errJobTerminal
	}
	if j.Kind == JobAssemble {
		return false, s.AssembleFinal(j.ProjectID)
	}
	if j.UpstreamID == "" {
		if j.Phase != "ready" {
			return false, ErrSubmissionUnknown
		}
		p, err := s.getProjectRaw(j.ProjectID)
		if err != nil {
			return false, err
		}
		lm := NewLiMeng()
		var result CreateVideoResp
		var body map[string]interface{}
		path := "/images"
		if j.Kind == JobKeyVisual {
			chars, err := s.listCharacters(j.ProjectID)
			if err != nil {
				return false, err
			}
			scenes, err := s.listScenes(j.ProjectID)
			if err != nil {
				return false, err
			}
			body = map[string]interface{}{"model": "zhimeng-qwen-image", "capability": "t2i", "prompt": buildKeyVisualPrompt(p, chars, scenes), "ratio": p.Aspect, "seed": 0, "steps": 20}
		} else {
			shots, err := s.listShots(j.ProjectID)
			if err != nil {
				return false, err
			}
			found := false
			var payload struct {
				Resolution string `json:"resolution"`
			}
			json.Unmarshal([]byte(j.Payload), &payload)
			if payload.Resolution == "" {
				payload.Resolution = getenvDefault("VIDEO_RESOLUTION", "720P")
			}
			for _, sh := range shots {
				if sh.ID == j.ShotID {
					found = true
					body = map[string]interface{}{"model": "zhimeng-h3-local", "capability": "i2v", "prompt": buildShotPrompt(p, charsForShot(s, sh), scenesForShot(s, sh), &sh, p.Aspect), "image_url": p.KeyvisualURL, "duration": int(sh.Duration), "ratio": p.Aspect, "resolution": payload.Resolution, "steps": 10}
				}
			}
			if !found {
				return false, errors.New("分镜不存在")
			}
			path = "/videos"
		}
		// Commit intent first. Every error after this line is an unknown charged outcome.
		if err = s.jobMutation(j, func(tx *sql.Tx) error {
			if err := s.authorizeSubmissionTx(tx, j); err != nil {
				return err
			}
			if err := ensureGenerationPhase(tx, j.ProjectID, j.Kind); err != nil {
				return err
			}
			return settlementExecOne(tx, "submission intent", `UPDATE jobs SET phase='submitting',updated_at=now() WHERE id=$1 AND phase='ready'`, j.ID)
		}); err != nil {
			if errors.Is(err, errJobCancelled) {
				return false, errJobCancelled
			}
			return false, err
		}
		if err = lm.req("POST", path, body, &result); err != nil {
			return false, fmt.Errorf("%w: %v", ErrSubmissionUnknown, err)
		}
		// The provider returns both an image/video id and a task_id, and they are
		// intentionally different (e.g. img_... vs task_...). The media id is the
		// identity accepted by GET /images|/videos; task_id is auxiliary metadata.
		id := result.ID
		if id == "" {
			id = result.TaskID
		}
		if id == "" {
			return false, ErrSubmissionUnknown
		}
		err = s.jobMutation(j, func(tx *sql.Tx) error {
			if err := checkProviderBinding(tx, j.Kind, j.ProjectID, j.ShotID, j.ID, id); err != nil {
				return err
			}
			if err := settlementExecOne(tx, "bind upstream", `UPDATE jobs SET upstream_id=$1,phase='polling',updated_at=now() WHERE id=$2 AND phase='submitting'`, id, j.ID); err != nil {
				return err
			}
			if j.Kind == JobKeyVisual {
				return settlementExecOne(tx, "bind image", `UPDATE projects SET keyvisual_task_id=$1,keyvisual_started_at=now(),status='KEYVISUAL_GENERATING' WHERE id=$2`, id, j.ProjectID)
			}
			return settlementExecOne(tx, "bind video", `UPDATE shots SET upstream_id=$1,status='QUEUED',error_msg=NULL,updated_at=now() WHERE id=$2 AND project_id=$3`, id, j.ShotID, j.ProjectID)
		})
		if err != nil {
			return false, fmt.Errorf("%w; provider task %s requires recovery: %v", ErrSubmissionUnknown, id, err)
		}
		// Poll on the next claim; claims/pending polls never consume failure attempts.
		j.UpstreamID = id
		s.job.UpstreamID = id
		return true, nil
	}
	if done, err := s.finishDeliveredJob(j); err != nil {
		return false, err
	} else if done {
		return false, errJobTerminal
	}
	if recovered, err := s.recoverPendingJob(j); recovered || err != nil {
		return false, err
	}
	lm := NewLiMeng()
	var task *VideoTaskResp
	var err error
	if j.Kind == JobKeyVisual {
		task, err = lm.GetImage(j.UpstreamID)
	} else {
		task, err = lm.GetVideo(j.UpstreamID)
	}
	if err != nil {
		return false, err
	}
	switch task.Status {
	case "failed", "cancelled":
		err = s.jobMutation(j, func(tx *sql.Tx) error {
			if err := settlementExecOne(tx, "provider terminal", `UPDATE jobs SET phase='provider_failed',last_error=$2 WHERE id=$1`, j.ID, truncate(task.Error, 400)); err != nil {
				return err
			}
			if j.Kind == JobKeyVisual {
				return settlementExecOne(tx, "image failed", `UPDATE projects SET status='FAILED',error_msg=$1,keyvisual_task_id=NULL WHERE id=$2`, truncate(task.Error, 400), j.ProjectID)
			}
			return settlementExecOne(tx, "video failed", `UPDATE shots SET status='FAILED',error_msg=$1 WHERE id=$2 AND project_id=$3`, truncate(task.Error, 400), j.ShotID, j.ProjectID)
		})
		if err != nil {
			return false, err
		}
		// A confirmed provider failure can be retried by a new user-confirmed attempt.
		_, err = s.FailJob(j.ID, j.LeaseToken, fmt.Errorf("上游生成失败: %s", task.Error), false)
		if err != nil {
			return false, err
		}
		return false, errJobTerminal // already terminal, no second mutation
	case "succeeded":
		if j.Kind == JobShot {
			err = s.onShotSucceeded(j.ProjectID, j.ShotID, "", task)
		} else {
			facts := GenerationSettlement{Kind: "image", TaskID: j.UpstreamID, ProjectID: j.ProjectID, ChargeMicros: task.ChargeMicros}
			var legacy bool
			legacy, err = s.checkGenerationDelivery(facts)
			if err == nil && !legacy {
				var path string
				project, projectErr := s.getProjectRaw(j.ProjectID)
				if projectErr != nil {
					err = projectErr
				} else {
					path, err = s.stageGenerationArtifact(task.URL, "image", j.UpstreamID, project.Aspect)
					if err == nil {
						facts.URL = s.publicURL(path)
						_, err = s.SettleGeneration(facts)
					}
				}
			}
		}
		return false, err
	default:
		return true, nil
	}
}

// Enforces the global lock order and fences all business writes, not just jobs.
func (s *Service) jobMutation(j *Job, fn func(*sql.Tx) error) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// 与 CancelRun 共用同一把项目级授权锁，锁顺序固定为 advisory -> user -> project -> shot -> job。
	if _, err = tx.Exec(`SELECT pg_advisory_xact_lock(hashtext($1))`, "run-auth:"+j.ProjectID); err != nil {
		return err
	}
	if _, _, _, _, err = lockQueueProject(tx, j.ProjectID); err != nil {
		return err
	}
	if j.ShotID != "" {
		var id string
		if err = tx.QueryRow(`SELECT id FROM shots WHERE id=$1 AND project_id=$2 FOR UPDATE`, j.ShotID, j.ProjectID).Scan(&id); err != nil {
			return err
		}
	}
	if j.Kind == JobAssemble {
		rows, e := tx.Query(`SELECT id FROM shots WHERE project_id=$1 ORDER BY seq FOR UPDATE`, j.ProjectID)
		if e != nil {
			return e
		}
		for rows.Next() {
			var id string
			if e = rows.Scan(&id); e != nil {
				rows.Close()
				return e
			}
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
	}
	if err = s.fenceTx(tx, j.ProjectID, j.ShotID, ""); err != nil {
		return err
	}
	if err = fn(tx); err != nil {
		// 取消清理是必须持久化的事实，不能随着 sentinel 一起回滚。
		if errors.Is(err, errJobCancelled) {
			if ce := tx.Commit(); ce != nil {
				return ce
			}
		}
		return err
	}
	return tx.Commit()
}

// Legacy adoption is the only compatibility bridge. It binds an existing ID or
// records reconciliation; it never guesses that an unrecorded submission failed.
func (s *Service) adoptLegacyJobs(project string) {
	rows, err := s.db.Query(`SELECT id FROM projects WHERE ($1='' OR id=$1) AND (status='KEYVISUAL_GENERATING' OR EXISTS(SELECT 1 FROM shots WHERE project_id=projects.id AND status='QUEUED'))`, project)
	if err != nil {
		return
	}
	var ids []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	for _, id := range ids {
		_ = s.adoptLegacyProject(id)
	}
}
func (s *Service) adoptLegacyProject(project string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, _, _, _, err = lockQueueProject(tx, project); err != nil {
		return err
	}
	type old struct {
		kind, shot, task string
		seq              int
		cost             int64
	}
	var status, task string
	if err = tx.QueryRow(`SELECT status,COALESCE(keyvisual_task_id,'') FROM projects WHERE id=$1`, project).Scan(&status, &task); err != nil {
		return err
	}
	list := []old{}
	if status == "KEYVISUAL_GENERATING" {
		list = append(list, old{JobKeyVisual, "", task, 0, 30})
	}
	rows, err := tx.Query(`SELECT id,seq,COALESCE(upstream_id,''),duration FROM shots WHERE project_id=$1 AND status='QUEUED' ORDER BY seq FOR UPDATE`, project)
	if err != nil {
		return err
	}
	for rows.Next() {
		var o old
		var dur float64
		o.kind = JobShot
		if err = rows.Scan(&o.shot, &o.seq, &o.task, &dur); err != nil {
			rows.Close()
			return err
		}
		o.cost = EstimateShotCost(dur, getenvDefault("VIDEO_RESOLUTION", "720P"))
		list = append(list, o)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, o := range list {
		phase, st, code := "polling", JobQueued, ""
		if o.task == "" {
			phase = "submitting"
			st = JobReconcile
			code = "submission_unknown"
		}
		_, err = tx.Exec(`INSERT INTO jobs(kind,project_id,shot_id,seq,upstream_id,phase,status,reserved_credits,error_code) SELECT $1::varchar,$2::varchar,$3::varchar,$4::int,$5::varchar,$6::varchar,$7::varchar,$8::bigint,$9::varchar WHERE NOT EXISTS(SELECT 1 FROM jobs WHERE project_id=$2 AND kind=$1 AND COALESCE(shot_id,'')=COALESCE($3,'') AND status IN ('queued','running','reconcile')) ON CONFLICT DO NOTHING`, o.kind, project, nullIfEmpty(o.shot), o.seq, nullIfEmpty(o.task), phase, st, o.cost, code)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}
