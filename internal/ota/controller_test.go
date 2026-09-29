package ota

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kratochj/stitkovac-gateway/internal/update"
)

type remoteFake struct {
	command     *Command
	reports     []Report
	allowed     bool
	state       string
	onActivate  func()
	reportErr   error
	activateErr error
}

func (r *remoteFake) CredentialID() string                          { return "credential-test" }
func (r *remoteFake) Source() string                                { return "https://cloud.example#test" }
func (r *remoteFake) Command(context.Context) (*Command, error)     { return r.command, nil }
func (r *remoteFake) State(context.Context, string) (string, error) { return r.state, nil }
func (r *remoteFake) Report(_ context.Context, _ string, report Report) error {
	r.reports = append(r.reports, report)
	return r.reportErr
}
func (r *remoteFake) Activate(context.Context, string, int64) (bool, error) {
	if r.onActivate != nil {
		r.onActivate()
	}
	return r.allowed, r.activateErr
}

type drainerFake struct {
	paused bool
	err    error
	source string
}

func (d *drainerFake) Pause(context.Context) error { d.paused = true; return d.err }
func (d *drainerFake) Resume()                     { d.paused = false }
func (d *drainerFake) CredentialID() string        { return "credential-test" }
func (d *drainerFake) Source() string {
	if d.source != "" {
		return d.source
	}
	return "https://cloud.example#test"
}
func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
func fixture(t *testing.T) (*Controller, *remoteFake, *drainerFake, *update.Store, string) {
	t.Helper()
	dir, root := t.TempDir(), t.TempDir()
	must(t, os.Chmod(root, 0700))
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	must(t, err)
	store, err := update.Open(root, map[string]ed25519.PublicKey{"test": pub}, "linux-arm64")
	must(t, err)
	for _, v := range []string{"1.0.0", "2.0.0"} {
		b := []byte("signed executable " + v)
		hash := sha256.Sum256(b)
		payload, _ := json.Marshal(update.Manifest{Schema: 1, Version: v, Platform: "linux-arm64", Size: int64(len(b)), SHA256: hex.EncodeToString(hash[:]), LauncherProtocol: 2})
		envelope, _ := json.Marshal(update.Envelope{KeyID: "test", Payload: payload, Signature: ed25519.Sign(key, append([]byte("stitkovac-gateway-release-v1\x00"), payload...))})
		_, err = store.Stage(envelope, bytes.NewReader(b))
		must(t, err)
	}
	must(t, store.Initialize("1.0.0"))
	d := &drainerFake{}
	c, err := New(dir, "https://releases.example", "1.0.0", store, d)
	must(t, err)
	r := &remoteFake{command: &Command{ID: "command-1", Version: "2.0.0", From: "1.0.0", Kind: "UPDATE", Expires: time.Now().Add(time.Hour).Unix()}, allowed: true}
	return c, r, d, store, dir
}
func activate(t *testing.T, c *Controller, r *remoteFake) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); c.handle(ctx, r) }()
	select {
	case <-c.Restart():
	case <-time.After(3 * time.Second):
		cancel()
		<-done
		t.Fatal("activation did not request restart")
	}
	cancel()
	<-done
}
func TestActivationDrainsBeforeGrantAndReportsOnlyAfterConfirmedBoot(t *testing.T) {
	c, r, d, store, dir := fixture(t)
	r.onActivate = func() {
		if !d.paused {
			t.Error("activation before draining")
		}
		s, _ := store.Status()
		if s.Pending != "" {
			t.Error("selection changed before authorization")
		}
	}
	activate(t, c, r)
	s, err := store.Status()
	must(t, err)
	if s.Pending != "2.0.0" || !d.paused {
		t.Fatal("activation was not durable")
	}
	for _, report := range r.reports {
		if report.State == "SUCCEEDED" {
			t.Fatal("reported success before boot")
		}
	}
	s, err = store.BeginBoot()
	must(t, err)
	must(t, store.Confirm(s.Active))
	next, err := New(dir, "https://releases.example", "2.0.0", store, d)
	must(t, err)
	r.command = nil
	next.handle(context.Background(), r)
	if next.Status().State != "SUCCEEDED" || r.reports[len(r.reports)-1].ActualVersion != "2.0.0" {
		t.Fatal("confirmed result missing")
	}
}
func TestPowerCutDuringTrialReportsRollbackWithoutTouchingPrintData(t *testing.T) {
	c, r, d, store, dir := fixture(t)
	sentinel := filepath.Join(dir, "gateway.db")
	must(t, os.WriteFile(sentinel, []byte("durable print journal"), 0600))
	activate(t, c, r)
	_, err := store.BeginBoot()
	must(t, err)
	_, err = store.BeginBoot()
	must(t, err)
	restored, err := New(dir, "https://releases.example", "1.0.0", store, d)
	must(t, err)
	r.command = nil
	restored.handle(context.Background(), r)
	if restored.Status().State != "ROLLED_BACK" {
		t.Fatal(restored.Status())
	}
	b, err := os.ReadFile(sentinel)
	must(t, err)
	if string(b) != "durable print journal" {
		t.Fatal("print journal changed")
	}
}
func TestRejectedActivationAndDrainFailureResumePrinting(t *testing.T) {
	for _, mode := range []string{"paused", "drain failure", "ambiguous grant", "foreign registration", "expired"} {
		t.Run(mode, func(t *testing.T) {
			c, r, d, store, _ := fixture(t)
			switch mode {
			case "paused":
				r.allowed = false
			case "drain failure":
				d.err = errors.New("unfinished write")
			case "ambiguous grant":
				r.activateErr = errors.New("connection lost")
			case "foreign registration":
				d.source = "https://other.example#new"
			case "expired":
				r.command.Expires = time.Now().Add(-time.Minute).Unix()
			}
			c.handle(context.Background(), r)
			s, err := store.Status()
			must(t, err)
			if s.Pending != "" || s.Active != "1.0.0" || d.paused {
				t.Fatal("failed activation affected printing or selection")
			}
			select {
			case <-c.Restart():
				t.Fatal("unexpected restart")
			default:
			}
		})
	}
}
func TestTerminalResultRetrySurvivesRestartAndCancellationIsVisible(t *testing.T) {
	c, r, d, store, dir := fixture(t)
	r.allowed = false
	c.handle(context.Background(), r)
	if c.Status().State != "DRAINING" {
		t.Fatal(c.Status())
	}
	r.command = nil
	r.state = "CANCELLED"
	c.handle(context.Background(), r)
	if c.Status().State != "CANCELLED" {
		t.Fatal(c.Status())
	}
	r.command = &Command{ID: "command-2", Version: "2.0.0", From: "1.0.0", Kind: "UPDATE", Expires: time.Now().Add(time.Hour).Unix()}
	r.allowed = true
	activate(t, c, r)
	_, err := store.BeginBoot()
	must(t, err)
	must(t, store.Confirm("2.0.0"))
	next, err := New(dir, "https://releases.example", "2.0.0", store, d)
	must(t, err)
	r.command = nil
	r.reportErr = errors.New("offline")
	next.handle(context.Background(), r)
	afterCut, err := New(dir, "https://releases.example", "2.0.0", store, d)
	must(t, err)
	r.reportErr = nil
	afterCut.handle(context.Background(), r)
	if !afterCut.state.Acknowledged || afterCut.Status().State != "SUCCEEDED" {
		t.Fatal("terminal report not retried")
	}
}
func TestDurableJournalRejectsUnsafeFilesAndInvalidState(t *testing.T) {
	c, _, _, _, dir := fixture(t)
	must(t, c.save(journal{Command: Command{ID: "x", Version: "2.0.0", From: "1.0.0", Kind: "UPDATE", Expires: 1}, Report: Report{Sequence: 1, State: "INVALID", ActualVersion: "1.0.0"}}))
	if _, err := New(dir, "https://releases.example", "1.0.0", c.store, c.drainer); err == nil {
		t.Fatal("invalid phase accepted")
	}
	must(t, os.Chmod(c.path, 0644))
	if _, err := New(dir, "https://releases.example", "1.0.0", c.store, c.drainer); err == nil {
		t.Fatal("public journal accepted")
	}
}

func TestShutdownCannotRewritePendingActivationAsInterrupted(t *testing.T) {
	c, r, _, store, _ := fixture(t)
	activate(t, c, r)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// A queued WSS notification can race with the process shutdown signal.
	c.handle(ctx, r)
	if c.Status().State != "ACTIVATING" {
		t.Fatal("shutdown overwrote activation intent", c.Status())
	}
	selection, err := store.Status()
	must(t, err)
	if selection.Pending != "2.0.0" {
		t.Fatal(selection)
	}
}
