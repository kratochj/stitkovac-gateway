// Package state owns the durable gateway journal. Opening a running gateway never
// creates a database: missing storage must not silently create a new identity.
package state

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

var ErrConflict = errors.New("state conflict")

type Store struct{ db *sql.DB }

func ID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

func connect(path string, create bool) (*Store, error) {
	mode := "rw"
	if create {
		mode = "rwc"
	}
	u := url.URL{Scheme: "file", Path: path}
	q := url.Values{"mode": {mode}, "_pragma": {"busy_timeout(5000)", "journal_mode(WAL)", "synchronous(FULL)", "foreign_keys(ON)"}}
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Initialize is an explicit provisioning operation and refuses existing state.
func Initialize(dir, passwordHash string) (*Store, error) {
	if passwordHash == "" {
		return nil, errors.New("password hash is required")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	if err := privateDirectory(dir); err != nil {
		return nil, err
	}
	path, err := filepath.Abs(filepath.Join(dir, "gateway.db"))
	if err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("initialize storage: %w", err)
	}
	if err := f.Close(); err != nil {
		return nil, err
	}
	s, err := connect(path, true)
	if err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		s.Close()
		return nil, err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(schema); err == nil {
		_, err = tx.Exec("INSERT INTO identity(singleton, gateway_id, password_hash) VALUES (1, ?, ?)", ID(), passwordHash)
	}
	if err == nil {
		err = tx.Commit()
	}
	if err == nil {
		err = syncDirectory(dir)
	}
	if err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}

func Open(dir string) (*Store, error) {
	if err := privateDirectory(dir); err != nil {
		return nil, err
	}
	path, err := filepath.Abs(filepath.Join(dir, "gateway.db"))
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("persistent storage unavailable: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("database must be a private regular file")
	}
	s, err := connect(path, false)
	if err != nil {
		return nil, err
	}
	var integrity string
	err = s.db.QueryRow("PRAGMA quick_check").Scan(&integrity)
	if err == nil && integrity != "ok" {
		err = errors.New("database integrity check failed")
	}
	var version int
	if err == nil {
		err = s.db.QueryRow("PRAGMA user_version").Scan(&version)
	}
	if err == nil && version != 1 {
		err = errors.New("unsupported database version")
	}
	if err == nil {
		_, _, err = s.Identity()
	}
	if err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}

func privateDirectory(dir string) error {
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return errors.New("data directory must be private (0700) and not a symlink")
	}
	return nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) Identity() (id, passwordHash string, err error) {
	err = s.db.QueryRow("SELECT gateway_id, password_hash FROM identity WHERE singleton=1").Scan(&id, &passwordHash)
	return
}

func syncDirectory(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

const schema = `
CREATE TABLE identity(singleton INTEGER PRIMARY KEY CHECK(singleton=1), gateway_id TEXT NOT NULL UNIQUE, password_hash TEXT NOT NULL);
CREATE TABLE reservations(mac TEXT PRIMARY KEY, ip TEXT NOT NULL UNIQUE, created_at INTEGER NOT NULL, last_seen INTEGER NOT NULL, lease_until INTEGER NOT NULL DEFAULT 0, declined INTEGER NOT NULL DEFAULT 0);
CREATE TABLE attempts(job_uid TEXT NOT NULL, attempt_id TEXT NOT NULL, mac TEXT NOT NULL, ip TEXT NOT NULL, port INTEGER NOT NULL, digest TEXT NOT NULL, document BLOB, expires_at INTEGER NOT NULL, state TEXT NOT NULL CHECK(state IN ('CLAIMED','SENDING','SENT','FAILED','UNKNOWN','EXPIRED')), reason TEXT NOT NULL DEFAULT '', PRIMARY KEY(job_uid,attempt_id));
PRAGMA user_version=1;
`
