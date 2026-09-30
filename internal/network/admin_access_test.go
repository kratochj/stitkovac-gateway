package network

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAdminAccessPersistenceAndStorageFailure(t *testing.T) {
	cfg := testConfig(t)
	c, err := NewController(cfg, &Simulated{Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	if !c.Status().WiFiAdminSupported || c.Status().WiFiAdminEnabled {
		t.Fatal("must default to disabled")
	}
	if err := c.SetWiFiAdmin(true); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewController(cfg, &Simulated{Config: cfg})
	if err != nil || !reopened.Status().WiFiAdminEnabled {
		t.Fatal("setting lost after restart", err)
	}
	if err := reopened.SetWiFiAdmin(false); err != nil {
		t.Fatal(err)
	}
	reopened, err = NewController(cfg, &Simulated{Config: cfg})
	if err != nil || reopened.Status().WiFiAdminEnabled {
		t.Fatal("disable lost", err)
	}
	if err := os.Rename(filepath.Join(cfg.DataDir, "admin-access.json"), filepath.Join(cfg.DataDir, "saved.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(cfg.DataDir, "admin-access.json"), 0700); err != nil {
		t.Fatal(err)
	}
	if c.SetWiFiAdmin(false) != ErrStorage || !c.Status().WiFiAdminEnabled {
		t.Fatal("failed write changed accepted state")
	}
	if err := os.Remove(filepath.Join(cfg.DataDir, "admin-access.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(cfg.DataDir, "saved.json"), filepath.Join(cfg.DataDir, "admin-access.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(cfg.DataDir, "admin-access.json"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := NewController(cfg, &Simulated{Config: cfg}); err != ErrStorage {
		t.Fatal("insecure file accepted")
	}
}

func TestWiFiAdminAddress(t *testing.T) {
	valid := Status{Available: true, WiFiAdminSupported: true, WiFiAdminEnabled: true, Mode: "uplink", PrinterCIDR: "192.168.77.1/24", APAddress: "192.168.78.1/24", Link: Link{Connected: true, Address: "192.168.1.42/24"}}
	if valid.WiFiAdminAddress() != "192.168.1.42:8443" {
		t.Fatal("valid uplink rejected")
	}
	for name, change := range map[string]func(*Status){
		"disabled":        func(s *Status) { s.WiFiAdminEnabled = false },
		"old helper":      func(s *Status) { s.WiFiAdminSupported = false },
		"offline":         func(s *Status) { s.Link.Connected = false },
		"trial":           func(s *Status) { s.Mode = "applying" },
		"ap":              func(s *Status) { s.Mode = "ap" },
		"simulation":      func(s *Status) { s.Simulation = true },
		"public":          func(s *Status) { s.Link.Address = "8.8.8.8/24" },
		"ipv6":            func(s *Status) { s.Link.Address = "fd00::1/64" },
		"printer overlap": func(s *Status) { s.Link.Address = "192.168.77.5/24" },
		"ap overlap":      func(s *Status) { s.Link.Address = "192.168.78.5/24" },
	} {
		t.Run(name, func(t *testing.T) {
			s := valid
			change(&s)
			if s.WiFiAdminAddress() != "" {
				t.Fatal("unsafe address accepted")
			}
		})
	}
}
