// Package telemetry stores only allowlisted diagnostics. Raw errors, network
// configuration, HTTP bodies and credentials are never accepted by this API.
package telemetry

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"time"

	"github.com/kratochj/stitkovac-gateway/internal/state"
)

var messages = map[string]string{
	"dhcp_request_failed":     "DHCP request could not be committed or sent",
	"cloud_unavailable":       "Cloud synchronization repeatedly failed",
	"storage_recovery_failed": "Persistent state recovery failed",
	"agent_panic":             "Gateway worker panicked",
}
var safeValue = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)

type Frame struct {
	File     string `json:"file"`
	Function string `json:"function"`
	Line     int    `json:"line"`
}
type Event struct {
	EventID   string  `json:"eventId"`
	GatewayID string  `json:"gatewayId"`
	BootID    string  `json:"bootId"`
	Code      string  `json:"code"`
	Message   string  `json:"message"`
	Version   string  `json:"version"`
	Timestamp int64   `json:"timestamp"`
	Frames    []Frame `json:"frames"`
}
type Spool struct {
	db                *sql.DB
	id, version, boot string
}

func Open(dir, id, version string) (*Spool, error) {
	if !safeValue.MatchString(id) || !safeValue.MatchString(version) {
		return nil, errors.New("invalid diagnostic identity")
	}
	path, err := filepath.Abs(filepath.Join(dir, "events.db"))
	if err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	f.Close()
	if err != nil {
		return nil, err
	}
	if info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("diagnostic database must be private")
	}
	u := url.URL{Scheme: "file", Path: path}
	q := url.Values{"_pragma": {"busy_timeout(500)", "journal_mode(WAL)", "synchronous(NORMAL)"}}
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("CREATE TABLE IF NOT EXISTS events(id TEXT PRIMARY KEY, created_at INTEGER NOT NULL, payload TEXT NOT NULL)"); err != nil {
		db.Close()
		return nil, err
	}
	return &Spool{db: db, id: id, version: version, boot: state.ID()}, nil
}

func (s *Spool) Close() error { return s.db.Close() }

func (s *Spool) Record(ctx context.Context, code string) error {
	message, ok := messages[code]
	if !ok {
		return errors.New("diagnostic code is not allowlisted")
	}
	e := Event{EventID: state.ID(), GatewayID: s.id, BootID: s.boot, Code: code, Message: message, Version: s.version, Timestamp: time.Now().Unix()}
	pcs := make([]uintptr, 12)
	n := runtime.Callers(2, pcs)
	frames := runtime.CallersFrames(pcs[:n])
	for {
		f, more := frames.Next()
		e.Frames = append(e.Frames, Frame{File: filepath.Base(f.File), Function: f.Function, Line: f.Line})
		if !more {
			break
		}
	}
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	if len(b) > 8192 {
		return errors.New("diagnostic exceeds size limit")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "DELETE FROM events WHERE created_at<?", time.Now().Add(-7*24*time.Hour).Unix()); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO events VALUES(?,?,?)", e.EventID, e.Timestamp, string(b)); err != nil {
		return err
	}
	// At most 1000 events of 8 KiB: payload size remains below the 10 MiB budget.
	if _, err := tx.ExecContext(ctx, "DELETE FROM events WHERE id IN (SELECT id FROM events ORDER BY rowid DESC LIMIT -1 OFFSET 1000)"); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Spool) Pending(ctx context.Context) ([]Event, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT payload FROM events WHERE created_at>=? ORDER BY rowid LIMIT 20", time.Now().Add(-7*24*time.Hour).Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []Event
	for rows.Next() {
		var b string
		if err := rows.Scan(&b); err != nil {
			return nil, err
		}
		var e Event
		if err := json.Unmarshal([]byte(b), &e); err != nil {
			return nil, err
		}
		result = append(result, e)
	}
	return result, rows.Err()
}

// Run is independent of print transactions; it retries diagnostic delivery only.
func (s *Spool) Run(ctx context.Context, in <-chan string, send func(context.Context, Event) error) {
	last := map[string]time.Time{}
	flush := func() {
		if send == nil {
			return
		}
		events, err := s.Pending(ctx)
		if err != nil {
			return
		}
		for _, e := range events {
			attempt, cancel := context.WithTimeout(ctx, 5*time.Second)
			err := send(attempt, e)
			cancel()
			if err != nil {
				return
			}
			if _, err := s.db.ExecContext(ctx, "DELETE FROM events WHERE id=?", e.EventID); err != nil {
				return
			}
		}
	}
	flush()
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case code := <-in:
			if time.Since(last[code]) < time.Minute {
				continue
			}
			if _, ok := messages[code]; !ok {
				continue
			}
			last[code] = time.Now()
			_ = s.Record(ctx, code)
		case <-ticker.C:
			flush()
		}
	}
}
