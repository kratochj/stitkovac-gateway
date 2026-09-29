package network

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testConfig(t *testing.T) Config {
	t.Helper()
	dir := t.TempDir()
	profiles := filepath.Join(dir, "profiles")
	if err := os.Mkdir(profiles, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	return Config{DataDir: dir, ProfileDir: profiles, WiFiInterface: "wlan0", PrinterInterface: "eth0", PrinterCIDR: "192.168.77.1/24", APCIDR: "192.168.78.1/24", APSSID: "Stitkovac-GW-test", APPassword: "private-ap-password", APID: "11111111-1111-4111-8111-111111111111", Country: "CZ", AgentUID: 1000, AgentGID: 1000, Simulation: true}
}
func testController(t *testing.T, cfg Config) *Controller {
	t.Helper()
	c, err := NewController(cfg, &Simulated{Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	c.applyDelay = time.Millisecond
	c.tick = 5 * time.Millisecond
	c.apDuration = time.Second
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); c.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	return c
}
func waitStatus(t *testing.T, c *Controller, predicate func(Status) bool) Status {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		s := c.Status()
		if predicate(s) {
			return s
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("unexpected status: %+v", c.Status())
	return Status{}
}
func request(ssid string) WiFiRequest {
	return WiFiRequest{SSID: ssid, Password: "customer-secret", Security: "wpa-psk", Country: "CZ", ServerURL: "https://cloud.stitkovac.app"}
}
func TestTrialCommitRollbackAndAPExpiry(t *testing.T) {
	cfg := testConfig(t)
	c := testController(t, cfg)
	waitStatus(t, c, func(s Status) bool { return s.Mode == "ap" })
	if c.ServiceAP(false) != ErrInvalid {
		t.Fatal("return without accepted profile")
	}
	if err := c.Apply(request("Customer")); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, c, func(s Status) bool { return s.Mode == "uplink" && s.SSID == "Customer" })
	if err := c.Apply(request("Simulated failure")); err != nil {
		t.Fatal(err)
	}
	s := waitStatus(t, c, func(s Status) bool { return s.Mode == "uplink" && s.Error == "tls_failed" })
	if s.SSID != "Customer" {
		t.Fatal(s)
	}
	if err := c.ServiceAP(true); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, c, func(s Status) bool { return s.Mode == "ap" })
	waitStatus(t, c, func(s Status) bool { return s.Mode == "uplink" })
	b, err := os.ReadFile(filepath.Join(cfg.DataDir, "selection.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "secret") || strings.Contains(string(b), "Simulated failure") {
		t.Fatal("journal retained credentials or rejected profile")
	}
	files, _ := filepath.Glob(filepath.Join(cfg.ProfileDir, "gateway-*.nmconnection"))
	if len(files) != 2 {
		t.Fatalf("stale candidate: %v", files)
	}
	info, _ := os.Stat(files[0])
	if info.Mode().Perm() != 0600 {
		t.Fatal("profile is not private")
	}
}
func TestPowerLossDiscardsTrial(t *testing.T) {
	for _, known := range []bool{false, true} {
		t.Run(map[bool]string{false: "first_setup", true: "accepted_profile"}[known], func(t *testing.T) {
			cfg := testConfig(t)
			candidate := Profile{ID: "22222222-2222-4222-8222-222222222222", SSID: "Candidate", Country: "CZ"}
			s := journal{Trial: &candidate, TrialURL: "https://cloud.stitkovac.app"}
			if known {
				s.Known = &Profile{ID: "33333333-3333-4333-8333-333333333333", SSID: "Accepted", Country: "CZ"}
			}
			b, _ := json.Marshal(s)
			if err := os.WriteFile(filepath.Join(cfg.DataDir, "selection.json"), b, 0600); err != nil {
				t.Fatal(err)
			}
			c := testController(t, cfg)
			v := waitStatus(t, c, func(v Status) bool { return v.Mode == "uplink" || v.Mode == "ap" })
			if v.Error != "interrupted" || known && v.SSID != "Accepted" || !known && v.Mode != "ap" {
				t.Fatal(v)
			}
		})
	}
}
func TestOrdinaryDisconnectNeverEnablesAP(t *testing.T) {
	cfg := testConfig(t)
	c := testController(t, cfg)
	waitStatus(t, c, func(s Status) bool { return s.Mode == "ap" })
	if err := c.Apply(request("Customer")); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, c, func(s Status) bool { return s.Mode == "uplink" })
	sim := c.backend.(*Simulated)
	sim.mu.Lock()
	sim.link.Connected = false
	sim.mu.Unlock()
	waitStatus(t, c, func(s Status) bool { return s.Mode == "uplink" && s.Link.Connected })
	c.mu.Lock()
	ap := c.state.AP
	c.mu.Unlock()
	if ap {
		t.Fatal("ordinary outage enabled AP")
	}
}
func TestConfigurationValidationAndEscaping(t *testing.T) {
	cfg := testConfig(t)
	for _, ssid := range []string{"shop\n[ipv4]", "", strings.Repeat("x", 33)} {
		r := request(ssid)
		if r.Validate() == nil {
			t.Fatal("accepted invalid SSID")
		}
	}
	r := request("Shop ; \\ 1;2;")
	r.Password = "pass \\; word"
	if r.Validate() != nil {
		t.Fatal("valid escaped values rejected")
	}
	text := string(wifiKeyfile(cfg, Profile{ID: cfg.APID}, r))
	if !strings.Contains(text, "psk=pass\\s\\\\;\\sword") {
		t.Fatal(text)
	}
	if strings.Contains(text, "ssid=Shop") {
		t.Fatal("SSID must be unambiguous byte array")
	}
	r.ServerURL = "https://user:secret@example.com"
	if r.Validate() == nil {
		t.Fatal("credentials in health target")
	}
	cfg.APCIDR = cfg.PrinterCIDR
	if cfg.Validate() == nil {
		t.Fatal("overlapping private networks accepted")
	}
}
func TestMalformedOrPublicJournalFailsClosed(t *testing.T) {
	for _, mode := range []os.FileMode{0600, 0644} {
		cfg := testConfig(t)
		data := []byte(`{"known":{"id":"../../escape","ssid":"x","country":"CZ"}}`)
		if mode == 0644 {
			data = []byte(`{}`)
		}
		if err := os.WriteFile(filepath.Join(cfg.DataDir, "selection.json"), data, mode); err != nil {
			t.Fatal(err)
		}
		if _, err := NewController(cfg, &Simulated{}); err != ErrStorage {
			t.Fatal(err)
		}
	}
}
func TestScanParserAndLink(t *testing.T) {
	got := parseScan("Shop\\:one:91:WPA2\nShop\\:one:30:WPA2\nBack\\\\slash:20:WPA3\ninvalid:999:--")
	if len(got) != 2 || got[0].SSID != "Shop:one" || got[1].SSID != "Back\\slash" {
		t.Fatal(got)
	}
	link := parseLink("GENERAL.STATE:100 (connected)\nGENERAL.CON-UUID:id\nIP4.ADDRESS[1]:192.168.1.2/24\nIP4.GATEWAY:192.168.1.1\nIP4.DNS[1]:192.168.1.1")
	if !link.Connected || link.Address != "192.168.1.2/24" || len(link.DNS) != 1 {
		t.Fatal(link)
	}
	n := NM{Config: testConfig(t)}
	link.Address = "192.168.77.20/24"
	if n.checkLink(context.Background(), link, "https://example.com") != "subnet_conflict" {
		t.Fatal("collision accepted")
	}
}
func TestButtonRequiresContinuousHoldAndRelease(t *testing.T) {
	h := Hold{}
	start := time.Now()
	if h.Update(true, start) || h.Update(true, start.Add(4*time.Second)) {
		t.Fatal("early trigger")
	}
	if !h.Update(true, start.Add(5*time.Second)) || h.Update(true, start.Add(20*time.Second)) {
		t.Fatal("not exactly once")
	}
	h.Update(false, start.Add(21*time.Second))
	if h.Update(true, start.Add(22*time.Second)) {
		t.Fatal("missing release")
	}
	if !h.Update(true, start.Add(27*time.Second)) {
		t.Fatal("cannot trigger again")
	}
}

type brokenLink struct {
	*Simulated
	rejectKnown bool
	stallTrial  bool
}

func (b *brokenLink) Activate(ctx context.Context, p Profile) error {
	if b.rejectKnown && p.SSID == "Known" {
		return ErrUnavailable
	}
	if b.stallTrial && p.SSID == "Candidate" {
		<-ctx.Done()
		return ctx.Err()
	}
	return b.Simulated.Activate(ctx, p)
}
func TestFailedRollbackFallsBackToServiceAP(t *testing.T) {
	cfg := testConfig(t)
	backend := &brokenLink{Simulated: &Simulated{Config: cfg}, rejectKnown: true}
	c, err := NewController(cfg, backend)
	if err != nil {
		t.Fatal(err)
	}
	c.mu.Lock()
	err = c.persist(journal{Known: &Profile{ID: "22222222-2222-4222-8222-222222222222", SSID: "Known", Country: "CZ"}})
	c.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	c.restore(context.Background(), true)
	s := c.Status()
	if s.Mode != "ap" || s.APUntil == 0 {
		t.Fatal(s)
	}
	c.mu.Lock()
	ap := c.state.AP
	c.mu.Unlock()
	if !ap {
		t.Fatal("fallback not durable")
	}
}
func TestTrialDeadlineRestoresKnownProfile(t *testing.T) {
	cfg := testConfig(t)
	backend := &brokenLink{Simulated: &Simulated{Config: cfg}, stallTrial: true}
	c, err := NewController(cfg, backend)
	if err != nil {
		t.Fatal(err)
	}
	c.trialTimeout = 20 * time.Millisecond
	c.applyDelay = time.Millisecond
	c.mu.Lock()
	err = c.persist(journal{Known: &Profile{ID: "22222222-2222-4222-8222-222222222222", SSID: "Known", Country: "CZ"}})
	c.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	c.restore(context.Background(), false)
	if err := c.Apply(request("Candidate")); err != nil {
		t.Fatal(err)
	}
	c.tryWiFi(context.Background())
	s := c.Status()
	if s.Mode != "uplink" || s.SSID != "Known" || s.Error != "association_failed" {
		t.Fatal(s)
	}
}
