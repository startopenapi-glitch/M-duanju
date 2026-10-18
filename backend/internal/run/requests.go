package run

import (
	"context"
	"database/sql"
	"errors"
)

// RequestIdentity is immutable even when its execution is cancelled or replaced.
// The original key stays on generation_runs; additional keys bind to that same ID.
type RequestIdentity struct {
	RunID     string
	ProjectID string
	InputHash string
}

type requestReader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func resolveRequest(ctx context.Context, db requestReader, userID, key string) (*RequestIdentity, error) {
	var identity RequestIdentity
	// Prefer the durable mapping. The original-key fallback supports historical
	// runs and callers that inserted runs before request mappings were introduced.
	err := db.QueryRowContext(ctx, `SELECT r.id,r.project_id,r.input_hash FROM generation_runs r
 WHERE r.user_id=$1 AND r.id=COALESCE(
   (SELECT run_id FROM generation_run_requests WHERE user_id=$1 AND request_key=$2),
   (SELECT id FROM generation_runs WHERE user_id=$1 AND idempotency_key=$2))`, userID, key).
		Scan(&identity.RunID, &identity.ProjectID, &identity.InputHash)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &identity, nil
}

// ResolveRequest only reads identity; it never starts or resumes work.
func (s *Store) ResolveRequest(ctx context.Context, userID, key string) (*RequestIdentity, error) {
	return resolveRequest(ctx, s.db, userID, key)
}

func lockRequestProject(ctx context.Context, tx *sql.Tx, userID, projectID, key string) error {
	// Account/key must precede project locks: the same key may race across projects.
	// Every creation and alias binding takes the same locks in the same order.
	for _, lock := range []string{"run-request:" + userID + ":" + key, "run:" + projectID, "run-auth:" + projectID} {
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, lock); err != nil {
			return err
		}
	}
	return nil
}

func requestRun(ctx context.Context, tx *sql.Tx, userID, projectID, key, inputHash string) (*Run, error) {
	identity, err := resolveRequest(ctx, tx, userID, key)
	if err != nil {
		return nil, err
	}
	if identity.ProjectID != projectID || identity.InputHash != inputHash {
		return nil, ErrIdempotencyConflict
	}
	return scan(tx.QueryRowContext(ctx, `SELECT `+cols+` FROM generation_runs WHERE id=$1`, identity.RunID))
}

func insertRequest(ctx context.Context, tx *sql.Tx, userID, key, runID string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO generation_run_requests(user_id,request_key,run_id) VALUES($1,$2,$3)`, userID, key, runID)
	return err
}

// BindRequest records a run already observed by the caller. It may have just
// become terminal; that is still the original request's result, never a reason
// to create or authorize a replacement. Existing bindings are never overwritten.
func (s *Store) BindRequest(ctx context.Context, userID, projectID, key, inputHash, runID string) (*Run, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err = lockRequestProject(ctx, tx, userID, projectID, key); err != nil {
		return nil, err
	}
	existing, err := requestRun(ctx, tx, userID, projectID, key, inputHash)
	if err == nil {
		return existing, tx.Commit()
	}
	if !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	var hash string
	err = tx.QueryRowContext(ctx, `SELECT input_hash FROM generation_runs WHERE id=$1 AND user_id=$2 AND project_id=$3`, runID, userID, projectID).Scan(&hash)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if hash != inputHash {
		return nil, ErrRunActive
	}
	if err = insertRequest(ctx, tx, userID, key, runID); err != nil {
		return nil, err
	}
	r, err := scan(tx.QueryRowContext(ctx, `SELECT `+cols+` FROM generation_runs WHERE id=$1`, runID))
	if err != nil {
		return nil, err
	}
	return r, tx.Commit()
}
