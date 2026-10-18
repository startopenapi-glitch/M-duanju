package service

import (
	"database/sql"
	"errors"
	"time"
)

// A heartbeat reports the worker loop, not provider health or successful delivery.
func (s *Service) RecordWorkerHeartbeat() error {
	_, e := s.db.Exec(`INSERT INTO worker_heartbeats(name,last_seen) VALUES('generation',now()) ON CONFLICT(name) DO UPDATE SET last_seen=excluded.last_seen`)
	return e
}
func (s *Service) AdminSystem() (map[string]interface{}, error) {
	if err := s.db.Ping(); err != nil {
		return nil, err
	}
	status := "unknown"
	var beat time.Time
	var last interface{}
	err := s.db.QueryRow(`SELECT last_seen FROM worker_heartbeats WHERE name='generation'`).Scan(&beat)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if err == nil {
		status = "healthy"
		last = beat
		if time.Since(beat) > 90*time.Second {
			status = "stale"
		}
	}
	wp := NewWechatPay()
	mock := false
	if wp != nil {
		mock = wp.Mock
	}
	return map[string]interface{}{"database": "ok", "worker": map[string]interface{}{"status": status, "lastHeartbeat": last}, "payments": map[string]bool{"enabled": wp.Enabled(), "mock": mock}, "mailConfigured": NewMailer().Enabled(), "asOf": time.Now().UTC()}, nil
}
