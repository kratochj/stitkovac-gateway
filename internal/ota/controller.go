// Package ota coordinates authenticated commands, durable progress and print draining.
package ota

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/kratochj/stitkovac-gateway/internal/platform"
	"github.com/kratochj/stitkovac-gateway/internal/update"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"
)

var ErrRestart = errors.New("activate staged release")
var identifier = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

type Command struct {
	ID      string `json:"commandId"`
	Version string `json:"version"`
	From    string `json:"fromVersion"`
	Kind    string `json:"kind"`
	Expires int64  `json:"expiresAt"`
}
type Report struct {
	Sequence      int64  `json:"sequence"`
	State         string `json:"state"`
	ActualVersion string `json:"actualVersion"`
	Reason        string `json:"reason,omitempty"`
}
type Remote interface {
	Source() string
	Command(context.Context) (*Command, error)
	Report(context.Context, string, Report) error
	Activate(context.Context, string, int64) (bool, error)
	State(context.Context, string) (string, error)
}
type Drainer interface {
	Pause(context.Context) error
	Resume()
	Source() string
}
type journal struct {
	Command      Command `json:"command"`
	Source       string  `json:"source"`
	Report       Report  `json:"report"`
	Acknowledged bool    `json:"acknowledged"`
}
type Controller struct {
	OnDiagnostic              func(string)
	DownloadTransport         http.RoundTripper
	mu                        sync.Mutex
	store                     *update.Store
	path, repository, version string
	drainer                   Drainer
	state                     journal
	remote                    Remote
	busy                      bool
	wake                      chan struct{}
	restart                   chan struct{}
}

func New(dir, repository, version string, store *update.Store, drainer Drainer) (*Controller, error) {
	if !update.ValidVersion(version) {
		return nil, errors.New("OTA requires a stable build version")
	}
	origin, err := url.Parse(repository)
	if err != nil || origin.Scheme != "https" || origin.Host == "" || origin.User != nil || origin.RawQuery != "" || origin.Fragment != "" || origin.Path != "" {
		return nil, errors.New("OTA requires a provisioned HTTPS origin")
	}
	c := &Controller{store: store, path: filepath.Join(dir, "ota-command.json"), repository: repository, version: version, drainer: drainer, wake: make(chan struct{}, 1), restart: make(chan struct{}, 1)}
	if info, err := os.Lstat(c.path); err == nil {
		if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 8192 {
			return nil, errors.New("invalid OTA journal")
		}
		b, err := os.ReadFile(c.path)
		if err != nil || json.Unmarshal(b, &c.state) != nil || !valid(c.state.Command) || c.state.Report.Sequence < 1 || c.state.Report.Sequence > 1_000_000 || !update.ValidVersion(c.state.Report.ActualVersion) || !validPhase(c.state.Report.State) {
			return nil, errors.New("invalid OTA journal")
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	return c, nil
}
func valid(c Command) bool {
	return identifier.MatchString(c.ID) && update.ValidVersion(c.Version) && update.ValidVersion(c.From) && (c.Kind == "UPDATE" || c.Kind == "ROLLBACK") && c.Expires > 0
}
func terminal(s string) bool {
	return s == "SUCCEEDED" || s == "ROLLED_BACK" || s == "FAILED" || s == "CANCELLED" || s == "EXPIRED"
}
func validPhase(s string) bool {
	return terminal(s) || s == "DOWNLOADING" || s == "VERIFIED" || s == "DRAINING" || s == "ACTIVATING"
}
func (c *Controller) Notify(remote Remote) {
	c.mu.Lock()
	c.remote = remote
	c.mu.Unlock()
	select {
	case c.wake <- struct{}{}:
	default:
	}
}
func (c *Controller) Restart() <-chan struct{} { return c.restart }
func (c *Controller) Status() Report           { c.mu.Lock(); defer c.mu.Unlock(); return c.state.Report }
func (c *Controller) save(s journal) error {
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	if err := platform.AtomicWrite(c.path, b, 0600); err != nil {
		return err
	}
	c.mu.Lock()
	c.state = s
	c.mu.Unlock()
	return nil
}
func (c *Controller) phase(s *journal, state, reason string) error {
	s.Report = Report{Sequence: s.Report.Sequence + 1, State: state, ActualVersion: c.version, Reason: reason}
	s.Acknowledged = false
	err := c.save(*s)
	if err == nil && (state == "FAILED" || state == "ROLLED_BACK") && c.OnDiagnostic != nil {
		c.OnDiagnostic("ota_update_failed")
	}
	return err
}
func (c *Controller) send(ctx context.Context, remote Remote, s *journal) error {
	if err := remote.Report(ctx, s.Command.ID, s.Report); err != nil {
		return err
	}
	s.Acknowledged = true
	return c.save(*s)
}
func (c *Controller) Run(ctx context.Context) {
	// The timer only retries unsent terminal results. Command delivery stays event-driven.
	retry := time.NewTicker(30 * time.Second)
	defer retry.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-c.wake:
		case <-retry.C:
			c.mu.Lock()
			pending := terminal(c.state.Report.State) && !c.state.Acknowledged && c.remote != nil && c.state.Source == c.remote.Source()
			c.mu.Unlock()
			if !pending {
				continue
			}
		}
		if ctx.Err() != nil {
			return
		}
		c.mu.Lock()
		remote := c.remote
		c.busy = true
		c.mu.Unlock()
		if remote != nil {
			c.handle(ctx, remote)
		}
		c.mu.Lock()
		c.busy = false
		c.mu.Unlock()
	}
}
func (c *Controller) handle(parent context.Context, remote Remote) {
	if parent.Err() != nil {
		return
	}
	ctx, cancel := context.WithTimeout(parent, 5*time.Minute)
	defer cancel()
	c.mu.Lock()
	s := c.state
	c.mu.Unlock()
	if s.Command.ID != "" && s.Source == remote.Source() {
		if s.Report.State == "ACTIVATING" {
			selection, err := c.store.Status()
			if err != nil {
				return
			}
			phase, reason := "FAILED", "interrupted"
			if c.version == s.Command.Version && selection.Active == c.version && !selection.Trial {
				phase, reason = "SUCCEEDED", ""
			} else if selection.Failed == s.Command.Version && c.version == s.Command.From {
				phase, reason = "ROLLED_BACK", "health_failed"
			}
			if c.phase(&s, phase, reason) != nil {
				return
			}
		}
		if terminal(s.Report.State) && !s.Acknowledged {
			if c.send(ctx, remote, &s) != nil {
				return
			}
		}
	}
	command, err := remote.Command(ctx)
	if err != nil {
		return
	}
	if command == nil {
		if s.Command.ID != "" && s.Source == remote.Source() && !terminal(s.Report.State) {
			if state, err := remote.State(ctx, s.Command.ID); err == nil && (state == "CANCELLED" || state == "EXPIRED") {
				if c.phase(&s, state, "") == nil {
					s.Acknowledged = true
					_ = c.save(s)
				}
			}
		}
		return
	}
	if !valid(*command) {
		return
	}
	if s.Command.ID == command.ID && s.Source == remote.Source() && terminal(s.Report.State) {
		return
	}
	if s.Command.ID != command.ID || s.Source != remote.Source() {
		s = journal{Command: *command, Source: remote.Source()}
	}
	fail := func(reason string) {
		if c.phase(&s, "FAILED", reason) == nil {
			_ = c.send(ctx, remote, &s)
		}
	}
	if remote.Source() != c.drainer.Source() {
		return
	}
	if command.From != c.version {
		fail("precondition_failed")
		return
	}
	if remote.Source() != c.drainer.Source() {
		return
	}
	if time.Now().Unix() >= command.Expires {
		fail("expired")
		return
	}
	selection, err := c.store.Status()
	if err != nil {
		fail("storage_failed")
		return
	}
	if selection.Trial || selection.Pending != "" || selection.Active != c.version {
		fail("precondition_failed")
		return
	}
	if command.Kind == "ROLLBACK" && selection.Previous != command.Version {
		fail("rollback_unavailable")
		return
	}
	if s.Report.State != "VERIFIED" && s.Report.State != "DRAINING" {
		if c.phase(&s, "DOWNLOADING", "") != nil {
			return
		}
		if c.send(ctx, remote, &s) != nil {
			return
		}
		if err := c.store.Cleanup(command.Version); err != nil {
			fail("storage_failed")
			return
		}
		if _, err := c.store.Executable(command.Version); err != nil {
			if command.Kind == "ROLLBACK" {
				fail("rollback_unavailable")
				return
			}
			if _, err := c.store.Download(ctx, c.repository, command.Version, c.DownloadTransport); err != nil {
				if parent.Err() == nil {
					fail("download_failed")
				}
				return
			}
		}
		if c.phase(&s, "VERIFIED", "") != nil {
			return
		}
		if c.send(ctx, remote, &s) != nil {
			return
		}
	}
	if c.phase(&s, "DRAINING", "") != nil {
		return
	}
	if c.send(ctx, remote, &s) != nil {
		return
	}
	drain, stop := context.WithTimeout(ctx, 45*time.Second)
	err = c.drainer.Pause(drain)
	stop()
	if err != nil {
		c.drainer.Resume()
		fail("drain_failed")
		return
	}
	activating := false
	defer func() {
		if !activating {
			c.drainer.Resume()
		}
	}()
	if remote.Source() != c.drainer.Source() {
		return
	}
	if time.Now().Unix() >= command.Expires {
		fail("expired")
		return
	}
	// Persist intent before the server grant. A cut here reports interruption, not success.
	if c.phase(&s, "ACTIVATING", "") != nil {
		return
	}
	allowed, err := remote.Activate(ctx, command.ID, s.Report.Sequence)
	if err != nil {
		return
	}
	if !allowed {
		// A paused rollout is retried on a later server notification; printing resumes.
		_ = c.phase(&s, "DRAINING", "")
		return
	}
	if command.Kind == "ROLLBACK" {
		err = c.store.RequestRollback(command.Version)
	} else {
		err = c.store.Request(command.Version)
	}
	if err != nil {
		fail("storage_failed")
		return
	}
	activating = true
	select {
	case c.restart <- struct{}{}:
	default:
	}
	// Stop processing notifications until the supervising launcher replaces us.
	<-parent.Done()
}
