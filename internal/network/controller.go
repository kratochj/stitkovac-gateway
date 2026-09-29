package network

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/kratochj/stitkovac-gateway/internal/platform"
)

type Backend interface {
	Available(context.Context) bool
	Scan(context.Context) ([]AccessPoint, error)
	Activate(context.Context, Profile) error
	StartAP(context.Context, string) error
	StopAP(context.Context) error
	Link(context.Context) (Link, error)
	Check(context.Context, Profile, string) string
	Cleanup(context.Context, []string) error
}
type journal struct {
	Known    *Profile `json:"known,omitempty"`
	Trial    *Profile `json:"trial,omitempty"`
	TrialURL string   `json:"trialURL,omitempty"`
	ReturnAP bool     `json:"returnAP"`
	AP       bool     `json:"ap"`
	APUntil  int64    `json:"apUntil"`
}
type Controller struct {
	cfg            Config
	backend        Backend
	mu             sync.Mutex
	state          journal
	status         Status
	busy, scanBusy bool
	fatal          bool
	lastScan       time.Time
	changed        chan struct{}
	// The trial and restoration budgets together stay below 90 seconds.
	trialTimeout, rollbackTimeout, apDuration, applyDelay, tick time.Duration
}

func NewController(cfg Config, backend Backend) (*Controller, error) {
	if cfg.Validate() != nil || privateDir(cfg.DataDir) != nil || privateDir(cfg.ProfileDir) != nil {
		return nil, ErrInvalid
	}
	c := &Controller{cfg: cfg, backend: backend, changed: make(chan struct{}, 1), trialTimeout: 55 * time.Second, rollbackTimeout: 30 * time.Second, apDuration: 15 * time.Minute, applyDelay: time.Second, tick: 5 * time.Second}
	c.status = Status{Mode: "starting", Country: cfg.Country, Simulation: cfg.Simulation, APSSID: cfg.APSSID, APAddress: cfg.APCIDR, PrinterCIDR: cfg.PrinterCIDR, WiFiInterface: cfg.WiFiInterface, GPIO: cfg.GPIOChip != ""}
	path := filepath.Join(cfg.DataDir, "selection.json")
	info, err := os.Lstat(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, ErrStorage
	}
	if err == nil {
		if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 8192 {
			return nil, ErrStorage
		}
		b, e := os.ReadFile(path)
		if e != nil || json.Unmarshal(b, &c.state) != nil {
			return nil, ErrStorage
		}
		for _, p := range []*Profile{c.state.Known, c.state.Trial} {
			if p != nil && (!idPattern.MatchString(p.ID) || p.ID == cfg.APID || !validSSID(p.SSID) || !countryPattern.MatchString(p.Country)) {
				return nil, ErrStorage
			}
		}
	}
	return c, nil
}

// persist must be called with mu held. An accepted profile is never changed in memory before fsync.
func (c *Controller) persist(s journal) error {
	b, err := json.Marshal(s)
	if err != nil || platform.AtomicWrite(filepath.Join(c.cfg.DataDir, "selection.json"), b, 0600) != nil {
		return ErrStorage
	}
	c.state = s
	return nil
}
func (c *Controller) Status() Status {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := c.status
	s.Link.DNS = append([]string(nil), s.Link.DNS...)
	return s
}
func (c *Controller) Apply(r WiFiRequest) error {
	if r.Validate() != nil {
		return ErrInvalid
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.status.Available {
		return ErrUnavailable
	}
	if c.busy || c.scanBusy {
		return ErrBusy
	}
	p := Profile{ID: uuid.NewString(), SSID: r.SSID, Country: r.Country}
	if err := writeProfile(c.cfg, p, r); err != nil {
		return err
	}
	next := c.state
	next.Trial = &p
	next.TrialURL = r.ServerURL
	next.ReturnAP = next.AP
	if err := c.persist(next); err != nil {
		_ = os.Remove(profilePath(c.cfg.ProfileDir, p.ID))
		return err
	}
	c.busy = true
	c.status.Mode = "applying"
	c.status.Phase = "pending"
	c.status.Error = ""
	c.wake()
	return nil
}
func (c *Controller) ServiceAP(enable bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.status.Available {
		return ErrUnavailable
	}
	if c.busy || c.scanBusy {
		return ErrBusy
	}
	if !enable && c.state.Known == nil {
		return ErrInvalid
	}
	next := c.state
	next.AP = enable
	next.APUntil = 0
	if enable && next.Known != nil {
		next.APUntil = time.Now().Add(c.apDuration).Unix()
	}
	if err := c.persist(next); err != nil {
		return err
	}
	c.busy = true
	c.status.Mode = "switching"
	c.status.Error = ""
	c.wake()
	return nil
}
func (c *Controller) wake() {
	select {
	case c.changed <- struct{}{}:
	default:
	}
}
func (c *Controller) Scan(ctx context.Context) ([]AccessPoint, error) {
	c.mu.Lock()
	if !c.status.Available {
		c.mu.Unlock()
		return nil, ErrUnavailable
	}
	if c.busy || c.scanBusy || time.Since(c.lastScan) < 10*time.Second {
		c.mu.Unlock()
		return nil, ErrBusy
	}
	c.scanBusy = true
	c.lastScan = time.Now()
	c.mu.Unlock()
	defer func() { c.mu.Lock(); c.scanBusy = false; c.mu.Unlock() }()
	bounded, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	// Some single-radio drivers cannot scan in AP mode. Return a bounded error;
	// never tear down the technician's only connection just to discover SSIDs.
	return c.backend.Scan(bounded)
}
func (c *Controller) phase(value string) { c.mu.Lock(); c.status.Phase = value; c.mu.Unlock() }
func (c *Controller) Run(ctx context.Context) {
	c.mu.Lock()
	next := c.state
	// Never resume a trial after power loss; the previously accepted profile wins.
	if next.Trial != nil {
		next.Trial = nil
		next.TrialURL = ""
		next.AP = next.ReturnAP
		c.status.Error = "interrupted"
	}
	if next.Known == nil {
		next.AP = true
		next.APUntil = 0
	}
	if next.AP && next.Known != nil && next.APUntil <= time.Now().Unix() {
		next.AP = false
	}
	err := c.persist(next)
	c.busy = true
	c.mu.Unlock()
	if err != nil {
		c.fail("storage")
		return
	}
	c.restore(ctx, c.Status().Error == "interrupted")
	ticker := time.NewTicker(c.tick)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-c.changed:
		case <-ticker.C:
		}
		if ctx.Err() != nil {
			return
		}
		c.mu.Lock()
		if c.fatal {
			c.mu.Unlock()
			return
		}
		if c.scanBusy {
			c.mu.Unlock()
			continue
		}
		if c.state.Trial != nil {
			c.mu.Unlock()
			c.tryWiFi(ctx)
			continue
		}
		if c.busy || c.status.Mode == "error" {
			c.busy = true
			c.mu.Unlock()
			c.restore(ctx, false)
			continue
		}
		if c.state.AP && c.state.Known != nil && c.state.APUntil > 0 && c.state.APUntil <= time.Now().Unix() {
			next := c.state
			next.AP = false
			err := c.persist(next)
			c.busy = true
			c.mu.Unlock()
			if err != nil {
				c.fail("storage")
				return
			}
			c.restore(ctx, false)
			continue
		}
		c.mu.Unlock()
		probe, cancel := context.WithTimeout(ctx, 3*time.Second)
		link, err := c.backend.Link(probe)
		cancel()
		c.mu.Lock()
		c.status.Link = link
		// Recheck under lock: Apply may have staged a trial during the link query.
		retry := !c.busy && !c.scanBusy && !c.state.AP && c.state.Known != nil && (err != nil || !link.Connected || link.ActiveID != c.state.Known.ID)
		if retry {
			c.busy = true
		}
		c.mu.Unlock()
		// Ordinary radio loss retries the accepted profile, never enables a service AP.
		if retry {
			c.restore(ctx, false)
		}
	}
}
func (c *Controller) tryWiFi(parent context.Context) {
	ctx, cancel := context.WithTimeout(parent, c.trialTimeout)
	defer cancel()
	timer := time.NewTimer(c.applyDelay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return
	case <-timer.C:
	}
	c.mu.Lock()
	p := *c.state.Trial
	target := c.state.TrialURL
	c.mu.Unlock()
	c.phase("association")
	code := "association_failed"
	if c.backend.StopAP(ctx) == nil && c.backend.Activate(ctx, p) == nil {
		c.phase("ip_dns_tls")
		code = c.backend.Check(ctx, p, target)
	}
	if parent.Err() != nil {
		return
	}
	c.mu.Lock()
	next := c.state
	if code == "" {
		next.Known = &p
		next.AP = false
		next.APUntil = 0
	} else {
		next.AP = next.ReturnAP || next.Known == nil
		if next.AP && next.Known != nil {
			next.APUntil = time.Now().Add(c.apDuration).Unix()
		}
	}
	next.Trial = nil
	next.TrialURL = ""
	err := c.persist(next)
	c.status.Error = code
	c.mu.Unlock()
	if err != nil {
		// Keep the durable pending trial for next boot, but restore the old selection
		// immediately. Disable new mutations until the helper is restarted.
		c.restore(parent, code != "")
		c.fail("storage")
		return
	}
	c.restore(parent, code != "")
}
func (c *Controller) restore(parent context.Context, fallback bool) {
	ctx, cancel := context.WithTimeout(parent, c.rollbackTimeout)
	defer cancel()
	available := c.backend.Available(ctx)
	c.mu.Lock()
	s := c.state
	c.status.Available = available
	c.mu.Unlock()
	if !available {
		c.fail("no_wifi")
		return
	}
	country := c.cfg.Country
	if s.Known != nil {
		country = s.Known.Country
	}
	var err error
	if s.AP || s.Known == nil {
		err = c.backend.StartAP(ctx, country)
	} else {
		// Leave a separate part of the restoration budget for a rescue AP.
		activateCtx, activateCancel := context.WithTimeout(ctx, 15*time.Second)
		err = c.backend.StopAP(activateCtx)
		if err == nil {
			err = c.backend.Activate(activateCtx, *s.Known)
		}
		activateCancel()
		if err != nil && fallback && parent.Err() == nil {
			s.AP = true
			s.APUntil = time.Now().Add(c.apDuration).Unix()
			c.mu.Lock()
			saveErr := c.persist(s)
			c.mu.Unlock()
			if saveErr != nil {
				c.fail("storage")
				return
			}
			err = c.backend.StartAP(ctx, country)
		}
	}
	if err != nil {
		c.fail("activation_failed")
		return
	}
	link, _ := c.backend.Link(ctx)
	keep := []string{c.cfg.APID}
	if s.Known != nil {
		keep = append(keep, s.Known.ID)
	}
	if s.Trial != nil {
		keep = append(keep, s.Trial.ID)
	}
	// Remain busy until cleanup is done so a concurrently staged profile is safe.
	_ = c.backend.Cleanup(ctx, keep)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.busy = false
	c.status.Link = link
	c.status.Phase = ""
	c.status.APUntil = s.APUntil
	c.status.SSID = ""
	if s.AP || s.Known == nil {
		c.status.Mode = "ap"
		c.status.Country = country
	} else {
		c.status.Mode = "uplink"
		c.status.SSID = s.Known.SSID
		c.status.Country = s.Known.Country
	}
}
func (c *Controller) fail(code string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.status.Error = code
	c.status.Mode = "error"
	c.busy = false
	if code == "storage" {
		c.status.Available = false
		c.busy = true
		c.fatal = true
	}
}

func (c *Controller) DisableButton() { c.mu.Lock(); defer c.mu.Unlock(); c.status.GPIO = false }
