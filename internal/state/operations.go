package state

import (
	"context"
	"database/sql"
	"errors"
	"net/netip"
	"time"
)

const operationsSchema = `
CREATE TABLE IF NOT EXISTS inventory_version(singleton INTEGER PRIMARY KEY, revision INTEGER NOT NULL, acknowledged INTEGER NOT NULL DEFAULT 0);
INSERT OR IGNORE INTO inventory_version(singleton,revision) VALUES(1,1);
CREATE TRIGGER IF NOT EXISTS reservation_inserted AFTER INSERT ON reservations BEGIN UPDATE inventory_version SET revision=revision+1; END;
CREATE TRIGGER IF NOT EXISTS reservation_updated AFTER UPDATE ON reservations BEGIN UPDATE inventory_version SET revision=revision+1; END;
CREATE TABLE IF NOT EXISTS retired_addresses(ip TEXT PRIMARY KEY);
CREATE TABLE IF NOT EXISTS service_audit(id INTEGER PRIMARY KEY, action TEXT NOT NULL, subject TEXT NOT NULL, detail TEXT NOT NULL, created_at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS attempt_delivery(job_uid TEXT NOT NULL, attempt_id TEXT NOT NULL, acknowledged INTEGER NOT NULL DEFAULT 0, PRIMARY KEY(job_uid,attempt_id));
CREATE TABLE IF NOT EXISTS resolutions(job_uid TEXT NOT NULL, attempt_id TEXT NOT NULL, decision TEXT NOT NULL, acknowledged INTEGER NOT NULL DEFAULT 0, created_at INTEGER NOT NULL DEFAULT (unixepoch()), PRIMARY KEY(job_uid,attempt_id));
CREATE TABLE IF NOT EXISTS tombstones(job_uid TEXT NOT NULL, attempt_id TEXT NOT NULL, PRIMARY KEY(job_uid,attempt_id));
`

func (s *Store) Changes() <-chan struct{} { return s.changes }
func (s *Store) Notify() {
	select {
	case s.changes <- struct{}{}:
	default:
	}
}

type Inventory struct {
	Revision     int64         `json:"revision"`
	Reservations []Reservation `json:"reservations"`
}

func (s *Store) Inventory(ctx context.Context) (Inventory, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Inventory{}, err
	}
	defer tx.Rollback()
	var v Inventory
	if err = tx.QueryRowContext(ctx, "SELECT revision FROM inventory_version").Scan(&v.Revision); err != nil {
		return v, err
	}
	rows, err := tx.QueryContext(ctx, "SELECT mac,ip,created_at,last_seen,lease_until,declined FROM reservations ORDER BY mac")
	if err != nil {
		return v, err
	}
	v.Reservations = []Reservation{}
	for rows.Next() {
		var r Reservation
		if err = rows.Scan(&r.MAC, &r.IP, &r.CreatedAt, &r.LastSeen, &r.LeaseUntil, &r.Declined); err != nil {
			rows.Close()
			return v, err
		}
		v.Reservations = append(v.Reservations, r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return v, err
	}
	return v, tx.Commit()
}
func (s *Store) InventoryAcknowledged(ctx context.Context, revision int64) error {
	_, err := s.db.ExecContext(ctx, "UPDATE inventory_version SET acknowledged=MAX(acknowledged,?) WHERE revision>=?", revision, revision)
	return err
}
func (s *Store) InventoryStatus(ctx context.Context) (revision, acknowledged int64, err error) {
	err = s.db.QueryRowContext(ctx, "SELECT revision,acknowledged FROM inventory_version").Scan(&revision, &acknowledged)
	return
}

// Readdress requires an explicit physical inspection. Old addresses remain quarantined
// permanently so a stale printer lease can never be handed to another device.
func (s *Store) Readdress(ctx context.Context, raw, oldIP, newIP string, pool Pool) error {
	mac, err := MAC(raw)
	if err != nil {
		return err
	}
	if err = pool.Validate(); err != nil {
		return err
	}
	ip, err := netip.ParseAddr(newIP)
	if err != nil || ip.Compare(pool.First) < 0 || ip.Compare(pool.Last) > 0 {
		return errors.New("address outside DHCP pool")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var n int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM attempts a WHERE (mac=? OR ip=?) AND (state='SENDING' OR (state='CLAIMED' AND expires_at>unixepoch()) OR (state='UNKNOWN' AND NOT EXISTS (SELECT 1 FROM resolutions r WHERE r.job_uid=a.job_uid AND r.attempt_id=a.attempt_id AND r.acknowledged=1)))`, mac, oldIP).Scan(&n); err != nil {
		return err
	}
	if n != 0 {
		return ErrConflict
	}
	if newIP != oldIP {
		if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM retired_addresses WHERE ip=?", newIP).Scan(&n); err != nil {
			return err
		}
		if n != 0 {
			return ErrConflict
		}
		if _, err = tx.ExecContext(ctx, "INSERT OR IGNORE INTO retired_addresses VALUES(?)", oldIP); err != nil {
			return err
		}
	}
	res, err := tx.ExecContext(ctx, "UPDATE reservations SET ip=?,declined=0,lease_until=0 WHERE mac=? AND ip=?", newIP, mac, oldIP)
	if err = changed(res, err); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO service_audit(action,subject,detail,created_at) VALUES('reservation',?,?,unixepoch())", mac, oldIP+" -> "+newIP); err != nil {
		return err
	}
	if err = tx.Commit(); err == nil {
		s.Notify()
	}
	return err
}

type Resolution struct {
	JobUID    string `json:"jobUid"`
	AttemptID string `json:"attemptId"`
	Decision  string `json:"decision"`
}

// Decisions never authorize a reprint. A new print must be requested explicitly.
func (s *Store) Resolve(ctx context.Context, job, attempt, decision string) error {
	if decision != "output_checked" && decision != "discarded" {
		return errors.New("invalid decision")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var status string
	if err = tx.QueryRowContext(ctx, "SELECT state FROM attempts WHERE job_uid=? AND attempt_id=?", job, attempt).Scan(&status); err != nil {
		return err
	}
	if status != "UNKNOWN" {
		return ErrConflict
	}
	var previous string
	err = tx.QueryRowContext(ctx, "SELECT decision FROM resolutions WHERE job_uid=? AND attempt_id=?", job, attempt).Scan(&previous)
	if err == nil {
		if previous != decision {
			return ErrConflict
		}
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO resolutions(job_uid,attempt_id,decision) VALUES(?,?,?)", job, attempt, decision); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO service_audit(action,subject,detail,created_at) VALUES('resolve',?,?,unixepoch())", job+"/"+attempt, decision); err != nil {
		return err
	}
	if err = tx.Commit(); err == nil {
		s.Notify()
	}
	return err
}
func (s *Store) PendingResolutions(ctx context.Context) ([]Resolution, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT job_uid,attempt_id,decision FROM resolutions WHERE acknowledged=0 LIMIT 100")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Resolution{}
	for rows.Next() {
		var r Resolution
		if err = rows.Scan(&r.JobUID, &r.AttemptID, &r.Decision); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
func (s *Store) AcknowledgeResolution(ctx context.Context, r Resolution) error {
	res, err := s.db.ExecContext(ctx, "UPDATE resolutions SET acknowledged=1 WHERE job_uid=? AND attempt_id=? AND decision=?", r.JobUID, r.AttemptID, r.Decision)
	return changed(res, err)
}
func (s *Store) PendingResults(ctx context.Context) ([]Attempt, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT a.job_uid,a.attempt_id,a.state,a.reason FROM attempts a WHERE state IN ('SENT','FAILED','EXPIRED','UNKNOWN') AND NOT EXISTS(SELECT 1 FROM attempt_delivery d WHERE d.job_uid=a.job_uid AND d.attempt_id=a.attempt_id AND d.acknowledged=1) LIMIT 100`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Attempt{}
	for rows.Next() {
		var a Attempt
		if err = rows.Scan(&a.JobUID, &a.AttemptID, &a.State, &a.Reason); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// Documents have a seven-day upper bound. Unresolved state and deduplication
// tombstones are retained even after the thirty-day detailed history expires.
func (s *Store) Prune(ctx context.Context, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "UPDATE attempts SET state='EXPIRED',reason='expired_before_send' WHERE state='CLAIMED' AND expires_at<=?", now.Unix()); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE attempts SET document=NULL WHERE state!='SENDING' AND expires_at<?", now.Add(-7*24*time.Hour).Unix()); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO tombstones SELECT a.job_uid,a.attempt_id FROM attempts a JOIN attempt_history h USING(job_uid,attempt_id) JOIN attempt_delivery d USING(job_uid,attempt_id) WHERE d.acknowledged=1 AND COALESCE(h.updated_at,h.created_at,0)<? AND a.expires_at<? AND (a.state IN ('SENT','FAILED','EXPIRED') OR (a.state='UNKNOWN' AND EXISTS(SELECT 1 FROM resolutions r WHERE r.job_uid=a.job_uid AND r.attempt_id=a.attempt_id AND r.acknowledged=1 AND r.created_at<?)))`, now.Add(-30*24*time.Hour).Unix(), now.Unix(), now.Add(-30*24*time.Hour).Unix())
	if err != nil {
		return err
	}
	for _, table := range []string{"attempt_history", "attempt_delivery", "resolutions", "attempts"} {
		if _, err = tx.ExecContext(ctx, "DELETE FROM "+table+" WHERE EXISTS (SELECT 1 FROM tombstones t WHERE t.job_uid="+table+".job_uid AND t.attempt_id="+table+".attempt_id)"); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM service_audit WHERE created_at<?", now.Add(-365*24*time.Hour).Unix()); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) ChangePassword(ctx context.Context, oldHash, newHash string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, "UPDATE identity SET password_hash=? WHERE singleton=1 AND password_hash=?", newHash, oldHash)
	if err = changed(res, err); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO service_audit(action,subject,detail,created_at) VALUES('password_changed','local-admin','',unixepoch())"); err != nil {
		return err
	}
	return tx.Commit()
}

// ServerExpired is an authoritative rejection before start. It cannot overwrite
// a locally started or completed write, even if a stale server response is replayed.
func (s *Store) ServerExpired(ctx context.Context, job, attempt string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var status string
	err = tx.QueryRowContext(ctx, "SELECT state FROM attempts WHERE job_uid=? AND attempt_id=?", job, attempt).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if status != "CLAIMED" && status != "EXPIRED" {
		return ErrConflict
	}
	if _, err = tx.ExecContext(ctx, "UPDATE attempts SET state='EXPIRED',reason='expired_before_send',document=NULL WHERE job_uid=? AND attempt_id=?", job, attempt); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO attempt_delivery VALUES(?,?,1) ON CONFLICT(job_uid,attempt_id) DO UPDATE SET acknowledged=1", job, attempt); err != nil {
		return err
	}
	return tx.Commit()
}

// RememberRemoteStart handles a missing/older journal after restore. A server
// start authorization is not permission to reconstruct and replay the bytes.
func (s *Store) RememberRemoteStart(ctx context.Context, a Attempt) error {
	mac, err := MAC(a.MAC)
	if err != nil {
		return err
	}
	ip, err := netip.ParseAddr(a.IP)
	if err != nil || !ip.Is4() || !ip.IsPrivate() || a.Port != 9100 {
		return ErrConflict
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var existing Attempt
	err = tx.QueryRowContext(ctx, "SELECT mac,ip,port,digest,expires_at,state FROM attempts WHERE job_uid=? AND attempt_id=?", a.JobUID, a.AttemptID).Scan(&existing.MAC, &existing.IP, &existing.Port, &existing.Digest, &existing.ExpiresAt, &existing.State)
	if err == nil {
		if existing.MAC != mac || existing.IP != a.IP || existing.Port != a.Port || existing.Digest != a.Digest || existing.ExpiresAt != a.ExpiresAt {
			return ErrConflict
		}
		if existing.State == "CLAIMED" {
			_, err = tx.ExecContext(ctx, "UPDATE attempts SET state='UNKNOWN',reason='interrupted_send' WHERE job_uid=? AND attempt_id=?", a.JobUID, a.AttemptID)
		} else if existing.State == "SENDING" {
			return ErrConflict
		}
	} else if errors.Is(err, sql.ErrNoRows) {
		var n int
		if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM tombstones WHERE job_uid=? AND attempt_id=?", a.JobUID, a.AttemptID).Scan(&n); err != nil {
			return err
		}
		if n != 0 {
			return ErrConflict
		}
		_, err = tx.ExecContext(ctx, "INSERT INTO attempts(job_uid,attempt_id,mac,ip,port,digest,expires_at,state,reason) VALUES(?,?,?,?,?,?,?,'UNKNOWN','interrupted_send')", a.JobUID, a.AttemptID, mac, a.IP, a.Port, a.Digest, a.ExpiresAt)
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}
