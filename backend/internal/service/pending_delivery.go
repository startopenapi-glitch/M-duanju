package service

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Resume verified durable facts without requiring an expired provider result/URL.
// Financial writes still go exclusively through the normal fenced settlement.
func (s *Service) recoverPendingJob(j *Job) (bool, error) {
	kind := "video"
	if j.Kind == JobKeyVisual {
		kind = "image"
	}
	var in GenerationSettlement
	var owner, receiptOwner string
	err := s.db.QueryRow(`SELECT g.kind,g.upstream_id,g.project_id,COALESCE(g.shot_id,''),g.user_id,p.user_id,g.result_url,g.charge_micros,g.result_duration,g.result_width,g.result_height
 FROM generation_settlements g JOIN projects p ON p.id=g.project_id
 WHERE g.provider='limeng' AND g.kind=$1 AND g.upstream_id=$2 AND g.status='pending'`, kind, j.UpstreamID).Scan(&in.Kind, &in.TaskID, &in.ProjectID, &in.ShotID, &receiptOwner, &owner, &in.URL, &in.ChargeMicros, &in.Duration, &in.Width, &in.Height)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return true, err
	}
	if owner != receiptOwner || in.ProjectID != j.ProjectID || in.ShotID != j.ShotID {
		return true, errors.New("待结算结果归属不匹配，已保留待核对")
	}
	path, err := s.verifiedPendingMedia(in)
	if err != nil {
		return true, err
	}
	settled, err := s.SettleGeneration(in)
	if err == nil && settled && kind == "video" {
		s.extractFrames(j.ProjectID, j.ShotID, path)
	}
	return true, err
}

func (s *Service) verifiedPendingMedia(in GenerationSettlement) (string, error) {
	bad := func() (string, error) {
		return "", errors.New("本地生成结果缺失或校验失败，已保留待结算记录，请恢复原文件后继续")
	}
	sub := "videos"
	extension := "mp4"
	if in.Kind == "image" {
		sub = "keyvisuals"
		extension = "(?:png|jpg|jpeg)"
	}
	dir := s.storageDir(sub)
	prefix := s.publicURL(filepath.Join(dir, "artifact"))
	prefix = strings.TrimSuffix(prefix, "artifact")
	if prefix == "" || !strings.HasPrefix(in.URL, prefix) {
		return bad()
	}
	name := strings.TrimPrefix(in.URL, prefix)
	match := regexp.MustCompile("^" + generationArtifactKey(in.TaskID) + "-([0-9a-f]{64})\\." + extension + "$").FindStringSubmatch(name)
	if len(match) != 2 {
		return bad()
	}
	path := filepath.Join(dir, name)
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
		return bad()
	}
	// Also reject a symlinked subdirectory; a configured storage-root symlink is allowed.
	root, err := filepath.EvalSymlinks(getenvDefault("STORAGE_DIR", "/opt/ai-drama-studio/storage"))
	if err != nil {
		return bad()
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return bad()
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return bad()
	}
	real, err = filepath.Abs(real)
	if err != nil || real != filepath.Join(root, sub, name) {
		return bad()
	}
	digest, err := mediaDigest(path)
	if err != nil || digest != match[1] {
		return bad()
	}
	if err = validateGenerationMedia(path, in.Kind); err != nil {
		return "", fmt.Errorf("本地生成结果媒体校验失败，保留待结算")
	}
	return path, nil
}
