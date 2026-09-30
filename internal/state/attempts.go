package state

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"net/netip"
	"time"
)

type Attempt struct {
	JobUID    string `json:"jobUid"`
	AttemptID string `json:"attemptId"`
	MAC       string `json:"mac"`
	IP        string `json:"ip"`
	Port      int    `json:"port"`
	Digest    string `json:"digest"`
	Document  []byte `json:"-"`
	ExpiresAt int64  `json:"expiresAt"`
	State     string `json:"state"`
	Reason    string `json:"reason"`
}

func (s *Store) Prepare(ctx context.Context, a Attempt) error {
	mac, err := MAC(a.MAC)
	if err != nil {
		return err
	}
	if a.JobUID == "" || a.AttemptID == "" || len(a.JobUID) > 128 || len(a.AttemptID) > 128 || a.Port != 9100 || len(a.Document) == 0 || len(a.Document) > 8<<20 || a.ExpiresAt <= 0 {
		return errors.New("invalid print attempt")
	}
	ip, err := netip.ParseAddr(a.IP)
	if err != nil || !ip.Is4() || !ip.IsPrivate() {
		return errors.New("invalid printer address")
	}
	digest := sha256.Sum256(a.Document)
	if a.Digest != hex.EncodeToString(digest[:]) {
		return errors.New("document digest mismatch")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var n int
	if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM tombstones WHERE job_uid=? AND attempt_id=?", a.JobUID, a.AttemptID).Scan(&n); err != nil {
		return err
	}
	if n != 0 {
		return ErrConflict
	}
	if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM reservations WHERE mac=? AND ip=? AND declined=0", mac, a.IP).Scan(&n); err != nil {
		return err
	}
	if n != 1 {
		return ErrConflict
	}
	var old Attempt
	err = tx.QueryRowContext(ctx, "SELECT mac,ip,port,digest,expires_at FROM attempts WHERE job_uid=? AND attempt_id=?", a.JobUID, a.AttemptID).Scan(&old.MAC, &old.IP, &old.Port, &old.Digest, &old.ExpiresAt)
	if err == nil {
		if old.MAC != mac || old.IP != a.IP || old.Port != a.Port || old.Digest != a.Digest || old.ExpiresAt != a.ExpiresAt {
			return ErrConflict
		}
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO attempts(job_uid,attempt_id,mac,ip,port,digest,document,expires_at,state) VALUES(?,?,?,?,?,?,?,?, 'CLAIMED')", a.JobUID, a.AttemptID, mac, a.IP, a.Port, a.Digest, a.Document, a.ExpiresAt)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) Attempt(ctx context.Context, job, attempt string) (Attempt, error) {
	a := Attempt{JobUID: job, AttemptID: attempt}
	err := s.db.QueryRowContext(ctx, "SELECT mac,ip,port,digest,document,expires_at,state,reason FROM attempts WHERE job_uid=? AND attempt_id=?", job, attempt).Scan(&a.MAC, &a.IP, &a.Port, &a.Digest, &a.Document, &a.ExpiresAt, &a.State, &a.Reason)
	return a, err
}

// BeginSend is deliberately NOT replayable: a second call cannot authorize bytes.
// The caller must first obtain the cloud's start authorization for this attempt.
func (s *Store) BeginSend(ctx context.Context, job, attempt string, now time.Time) error {
	result, err := s.db.ExecContext(ctx, `UPDATE attempts SET state='SENDING' WHERE job_uid=? AND attempt_id=? AND state='CLAIMED' AND expires_at>?
AND EXISTS (SELECT 1 FROM reservations r WHERE r.mac=attempts.mac AND r.ip=attempts.ip AND r.declined=0)
AND NOT EXISTS (SELECT 1 FROM attempts a WHERE a.ip=attempts.ip AND a.port=attempts.port AND (a.state='SENDING' OR (a.state='UNKNOWN' AND NOT EXISTS (SELECT 1 FROM resolutions r WHERE r.job_uid=a.job_uid AND r.attempt_id=a.attempt_id AND r.acknowledged=1))))`, job, attempt, now.Unix())
	return changed(result, err)
}

// Finish uses bounded reason codes, never arbitrary errors containing secrets.
func (s *Store) Finish(ctx context.Context, job, attempt, result string) error {
	if result != "SENT" && result != "UNKNOWN" && result != "FAILED" {
		return errors.New("invalid attempt result")
	}
	r, err := s.db.ExecContext(ctx, "UPDATE attempts SET state=? WHERE job_uid=? AND attempt_id=? AND state='SENDING'", result, job, attempt)
	return changed(r, err)
}

// Recover runs exactly once under the process lock, before processing any work.
func (s *Store) Recover(ctx context.Context, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "UPDATE attempts SET state='UNKNOWN',reason='interrupted_send' WHERE state='SENDING'"); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE attempts SET state='EXPIRED',reason='expired_before_send' WHERE state='CLAIMED' AND expires_at<=?", now.Unix()); err != nil {
		return err
	}
	return tx.Commit()
}

// Acknowledge drops only the document, keeping a durable deduplication tombstone.
func (s *Store) Acknowledge(ctx context.Context, job, attempt, result string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	r, err := tx.ExecContext(ctx, "UPDATE attempts SET document=NULL WHERE job_uid=? AND attempt_id=? AND state=? AND state IN ('SENT','FAILED','EXPIRED','UNKNOWN')", job, attempt, result)
	if err = changed(r, err); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO attempt_delivery VALUES(?,?,1) ON CONFLICT(job_uid,attempt_id) DO UPDATE SET acknowledged=1", job, attempt); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) Expire(ctx context.Context, job, attempt string, now time.Time) error {
	r, err := s.db.ExecContext(ctx, "UPDATE attempts SET state='EXPIRED',reason='expired_before_send' WHERE job_uid=? AND attempt_id=? AND state='CLAIMED' AND expires_at<=?", job, attempt, now.Unix())
	return changed(r, err)
}

func (s *Store) MarkUncertain(ctx context.Context, job, attempt string) error {
	r, err := s.db.ExecContext(ctx, "UPDATE attempts SET state='UNKNOWN',reason='interrupted_send' WHERE job_uid=? AND attempt_id=? AND state='CLAIMED'", job, attempt)
	return changed(r, err)
}
