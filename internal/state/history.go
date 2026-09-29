package state

import (
	"context"
	"errors"
)

// History metadata is additive to journal v1. Older agents can still read and
// update attempts; triggers record their transitions without changing the protocol.
const historySchema = `
CREATE TABLE IF NOT EXISTS attempt_history (
 job_uid TEXT NOT NULL, attempt_id TEXT NOT NULL,
 created_at INTEGER, updated_at INTEGER, acknowledged_at INTEGER,
 PRIMARY KEY(job_uid, attempt_id),
 FOREIGN KEY(job_uid, attempt_id) REFERENCES attempts(job_uid, attempt_id)
);
CREATE INDEX IF NOT EXISTS attempts_state ON attempts(state);
CREATE TRIGGER IF NOT EXISTS attempt_history_created AFTER INSERT ON attempts BEGIN
 INSERT INTO attempt_history(job_uid,attempt_id,created_at,updated_at)
 VALUES(NEW.job_uid,NEW.attempt_id,unixepoch(),unixepoch());
END;
CREATE TRIGGER IF NOT EXISTS attempt_history_changed AFTER UPDATE OF state ON attempts
WHEN OLD.state != NEW.state BEGIN
 INSERT INTO attempt_history(job_uid,attempt_id,updated_at)
 VALUES(NEW.job_uid,NEW.attempt_id,unixepoch())
 ON CONFLICT(job_uid,attempt_id) DO UPDATE SET updated_at=excluded.updated_at;
END;
CREATE TRIGGER IF NOT EXISTS attempt_history_acknowledged AFTER UPDATE OF document ON attempts
WHEN OLD.document IS NOT NULL AND NEW.document IS NULL AND NEW.state IN ('SENT','FAILED','EXPIRED') BEGIN
 INSERT INTO attempt_history(job_uid,attempt_id,acknowledged_at)
 VALUES(NEW.job_uid,NEW.attempt_id,unixepoch())
 ON CONFLICT(job_uid,attempt_id) DO UPDATE SET acknowledged_at=excluded.acknowledged_at;
END;
`

// AttemptSummary deliberately has no document, credentials or arbitrary error text.
type AttemptSummary struct {
	Sequence                                  int64
	JobUID, AttemptID, MAC, IP, State, Reason string
	Port                                      int
	CreatedAt, UpdatedAt, AcknowledgedAt      int64
	Acknowledged                              bool
}

type HistoryPage struct {
	Attempts   []AttemptSummary
	NextBefore int64
}

func ValidAttemptState(value string) bool {
	switch value {
	case "", "CLAIMED", "SENDING", "SENT", "FAILED", "UNKNOWN", "EXPIRED":
		return true
	default:
		return false
	}
}

// History uses a bounded keyset page, not an offset over concurrently arriving jobs.
func (s *Store) History(ctx context.Context, before int64, filter string) (HistoryPage, error) {
	if before < 0 || !ValidAttemptState(filter) {
		return HistoryPage{}, errors.New("invalid history query")
	}
	query := `SELECT a.rowid,a.job_uid,a.attempt_id,a.mac,a.ip,a.port,a.state,a.reason,
 COALESCE(h.created_at,0),COALESCE(h.updated_at,0),COALESCE(h.acknowledged_at,0),
 (a.document IS NULL AND a.state IN ('SENT','FAILED','EXPIRED'))
 FROM attempts a LEFT JOIN attempt_history h ON h.job_uid=a.job_uid AND h.attempt_id=a.attempt_id WHERE 1=1`
	args := []any{}
	if before != 0 {
		query += " AND a.rowid<?"
		args = append(args, before)
	}
	if filter != "" {
		query += " AND a.state=?"
		args = append(args, filter)
	}
	query += " ORDER BY a.rowid DESC LIMIT 51"
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return HistoryPage{}, err
	}
	defer rows.Close()
	page := HistoryPage{Attempts: []AttemptSummary{}}
	for rows.Next() {
		var a AttemptSummary
		if err := rows.Scan(&a.Sequence, &a.JobUID, &a.AttemptID, &a.MAC, &a.IP, &a.Port, &a.State, &a.Reason, &a.CreatedAt, &a.UpdatedAt, &a.AcknowledgedAt, &a.Acknowledged); err != nil {
			return HistoryPage{}, err
		}
		page.Attempts = append(page.Attempts, a)
	}
	if len(page.Attempts) > 50 {
		page.Attempts = page.Attempts[:50]
		page.NextBefore = page.Attempts[49].Sequence
	}
	return page, rows.Err()
}

func (s *Store) UncertainCount(ctx context.Context) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM attempts WHERE state='UNKNOWN'").Scan(&count)
	return count, err
}
