package state

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

var testPool = Pool{Network: netip.MustParsePrefix("192.168.77.0/24"), Server: netip.MustParseAddr("192.168.77.1"), First: netip.MustParseAddr("192.168.77.50"), Last: netip.MustParseAddr("192.168.77.51")}
var ctx = context.Background()

func newStore(t *testing.T) (*Store, string) {
	t.Helper()
	dir := t.TempDir()
	s, err := Initialize(dir, "test-hash")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, dir
}

func TestMissingStateFailsClosed(t *testing.T) {
	dir := t.TempDir()
	if _, err := Open(dir); err == nil {
		t.Fatal("opened missing state")
	}
	if _, err := os.Stat(filepath.Join(dir, "gateway.db")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("open created state")
	}
}

func TestIdentityAndDatabaseSettings(t *testing.T) {
	s, dir := newStore(t)
	id, _, err := s.Identity()
	if err != nil {
		t.Fatal(err)
	}
	var sync int
	var journal string
	if err := s.db.QueryRow("PRAGMA synchronous").Scan(&sync); err != nil || sync != 2 {
		t.Fatalf("synchronous=%d: %v", sync, err)
	}
	if err := s.db.QueryRow("PRAGMA journal_mode").Scan(&journal); err != nil || journal != "wal" {
		t.Fatalf("journal=%s: %v", journal, err)
	}
	if _, err := Initialize(dir, "other"); err == nil {
		t.Fatal("reinitialized identity")
	}
	s.Close()
	s2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	id2, _, _ := s2.Identity()
	if id2 != id {
		t.Fatal("identity changed")
	}
}

func TestReservationsSurviveReleaseAndPoolExhaustion(t *testing.T) {
	s, dir := newStore(t)
	now := time.Now()
	r, err := s.Reserve(ctx, "02:00:00:00:00:01", testPool, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Lease(ctx, r.MAC, r.IP, now, time.Hour); err != nil {
		t.Fatal(err)
	}
	if err := s.Release(ctx, r.MAC, r.IP, false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Reserve(ctx, "02:00:00:00:00:02", testPool, now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Reserve(ctx, "02:00:00:00:00:03", testPool, now); err == nil {
		t.Fatal("recycled reserved address")
	}
	s.Close()
	s2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	r2, err := s2.Reserve(ctx, r.MAC, testPool, now.Add(7*24*time.Hour))
	if err != nil || r2.IP != r.IP {
		t.Fatalf("lost reservation: %v", err)
	}
}

func prepare(t *testing.T, s *Store) Attempt {
	t.Helper()
	r, err := s.Reserve(ctx, "02:00:00:00:00:01", testPool, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	b := []byte("%PDF-test")
	d := sha256.Sum256(b)
	a := Attempt{JobUID: "job", AttemptID: "attempt", MAC: r.MAC, IP: r.IP, Port: 9100, Digest: hex.EncodeToString(d[:]), Document: b, ExpiresAt: time.Now().Add(time.Hour).Unix()}
	if err := s.Prepare(ctx, a); err != nil {
		t.Fatal(err)
	}
	return a
}

func TestAttemptRecoveryNeverReplaysPossibleSend(t *testing.T) {
	s, _ := newStore(t)
	a := prepare(t, s)
	if err := s.BeginSend(ctx, a.JobUID, a.AttemptID, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := s.Recover(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	a, err := s.Attempt(ctx, a.JobUID, a.AttemptID)
	if err != nil || a.State != "UNKNOWN" {
		t.Fatalf("%+v %v", a, err)
	}
	if err := s.Prepare(ctx, a); err != nil {
		t.Fatal(err)
	}
	if err := s.BeginSend(ctx, a.JobUID, a.AttemptID, time.Now()); !errors.Is(err, ErrConflict) {
		t.Fatal("replayed uncertain send")
	}
	a.AttemptID = "second"
	if err := s.Prepare(ctx, a); err != nil {
		t.Fatal(err)
	}
	if err := s.BeginSend(ctx, a.JobUID, a.AttemptID, time.Now()); !errors.Is(err, ErrConflict) {
		t.Fatal("ignored blocked printer")
	}
}

func TestSentTombstoneSurvivesRecoveryAndReplay(t *testing.T) {
	s, _ := newStore(t)
	a := prepare(t, s)
	if err := s.BeginSend(ctx, a.JobUID, a.AttemptID, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := s.Finish(ctx, a.JobUID, a.AttemptID, "SENT"); err != nil {
		t.Fatal(err)
	}
	if err := s.Acknowledge(ctx, a.JobUID, a.AttemptID, "SENT"); err != nil {
		t.Fatal(err)
	}
	if err := s.Recover(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := s.Prepare(ctx, a); err != nil {
		t.Fatal(err)
	}
	r, err := s.Attempt(ctx, a.JobUID, a.AttemptID)
	if err != nil || r.State != "SENT" || len(r.Document) != 0 {
		t.Fatal(r, err)
	}
	if err := s.BeginSend(ctx, a.JobUID, a.AttemptID, time.Now()); err == nil {
		t.Fatal("reprinted acknowledged job")
	}
	a.Digest = "changed"
	if err := s.Prepare(ctx, a); err == nil {
		t.Fatal("accepted changed payload")
	}
}

func TestAbruptProcessExit(t *testing.T) {
	if dir := os.Getenv("GATEWAY_CRASH_TEST_DIR"); dir != "" {
		s, err := Open(dir)
		if err != nil {
			os.Exit(21)
		}
		if err := s.BeginSend(ctx, "job", "attempt", time.Now()); err != nil {
			os.Exit(22)
		}
		os.Exit(23)
	}
	s, dir := newStore(t)
	prepare(t, s)
	s.Close()
	cmd := exec.Command(os.Args[0], "-test.run=^TestAbruptProcessExit$")
	cmd.Env = append(os.Environ(), "GATEWAY_CRASH_TEST_DIR="+dir)
	err := cmd.Run()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 23 {
		t.Fatalf("child failed: %v", err)
	}
	s2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if err := s2.Recover(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	a, err := s2.Attempt(ctx, "job", "attempt")
	if err != nil || a.State != "UNKNOWN" {
		t.Fatalf("unsafe recovery: %+v %v", a, err)
	}
}
