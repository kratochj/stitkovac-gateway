package network

import (
	"context"
	"github.com/kratochj/stitkovac-gateway/internal/platform"
	"sync"
)

// Simulated never invokes NetworkManager or changes host networking. The SSID
// "Simulated failure" deterministically exercises the real controller rollback.
type Simulated struct {
	Config Config
	mu     sync.Mutex
	link   Link
	failed bool
}

func (s *Simulated) Available(context.Context) bool { return true }
func (s *Simulated) Scan(context.Context) ([]AccessPoint, error) {
	return []AccessPoint{{SSID: "Simulated Wi-Fi", Signal: 90, Security: "WPA2"}, {SSID: "Simulated failure", Signal: 50, Security: "WPA2"}}, nil
}
func (s *Simulated) Activate(_ context.Context, p Profile) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failed = p.SSID == "Simulated failure"
	s.link = Link{ActiveID: p.ID, Address: "192.168.1.20/24", Gateway: "192.168.1.1", DNS: []string{"192.168.1.1"}, Connected: true}
	return nil
}
func (s *Simulated) StartAP(context.Context, string) error {
	if err := platform.AtomicWrite(profilePath(s.Config.ProfileDir, s.Config.APID), apKeyfile(s.Config), 0600); err != nil {
		return ErrStorage
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.link = Link{ActiveID: s.Config.APID, Address: s.Config.APCIDR, Connected: true}
	return nil
}
func (s *Simulated) StopAP(context.Context) error { return nil }
func (s *Simulated) Link(context.Context) (Link, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	l := s.link
	l.DNS = append([]string(nil), l.DNS...)
	return l, nil
}
func (s *Simulated) Check(context.Context, Profile, string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failed {
		return "tls_failed"
	}
	return ""
}
func (s *Simulated) Cleanup(ctx context.Context, keep []string) error {
	// Only remove private keyfiles; simulation must never execute nmcli.
	return cleanupSimulation(s.Config.ProfileDir, keep)
}
