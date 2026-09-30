package admin

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kratochj/stitkovac-gateway/internal/network"
	"github.com/kratochj/stitkovac-gateway/internal/state"
)

type wifiStub struct {
	networkStub
	mu     sync.Mutex
	status network.Status
	err    error
}

func (n *wifiStub) Status(context.Context) (network.Status, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.status, n.err
}
func (n *wifiStub) SetWiFiAdmin(_ context.Context, enabled bool) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.status.WiFiAdminEnabled = enabled
	return nil
}
func wifiServer(t *testing.T) (*Server, *wifiStub) {
	t.Helper()
	store, err := state.Initialize(t.TempDir()+"/state", "hash")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	s, err := New(store, "192.168.77.1:8443", "test")
	if err != nil {
		t.Fatal(err)
	}
	n := &wifiStub{status: network.Status{Available: true, WiFiAdminSupported: true, WiFiAdminEnabled: true, Mode: "uplink", PrinterCIDR: "192.168.77.1/24", APAddress: "192.168.78.1/24", Link: network.Link{Connected: true, Address: "192.168.1.42/24"}}}
	s.Network = n
	return s, n
}
func TestWiFiHostOriginAndDisable(t *testing.T) {
	s, n := wifiServer(t)
	addr := "192.168.1.42:8443"
	handler := s.wifiHandler(addr)
	request := func(host, origin, method string, local bool) int {
		r := httptest.NewRequest(method, "https://"+host+"/login", strings.NewReader("password=wrong"))
		r.Header.Set("Origin", origin)
		if local {
			r = r.WithContext(context.WithValue(r.Context(), http.LocalAddrContextKey, &net.TCPAddr{IP: net.ParseIP("192.168.1.42"), Port: 8443}))
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w.Code
	}
	if code := request(addr, "", "GET", true); code != 200 {
		t.Fatal(code)
	}
	if code := request(addr, "https://evil.invalid", "POST", true); code != 403 {
		t.Fatal(code)
	}
	if code := request(s.Host, "", "GET", true); code != 403 {
		t.Fatal("static host bypass", code)
	}
	if code := request(addr, "", "GET", false); code != 403 {
		t.Fatal("missing local address accepted", code)
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest("GET", "https://"+addr+"/login", nil))
	if w.Code != 421 {
		t.Fatal("uplink host accepted outside Wi-Fi listener")
	}
	n.SetWiFiAdmin(context.Background(), false)
	if code := request(addr, "", "GET", true); code != 403 {
		t.Fatal("disabled existing connection accepted", code)
	}
	n.SetWiFiAdmin(context.Background(), true)
	n.err = errors.New("helper unavailable")
	if code := request(addr, "", "GET", true); code != 403 {
		t.Fatal("helper failure did not close access", code)
	}
}
func TestWiFiListenerFollowsDHCPAndCloses(t *testing.T) {
	s, n := wifiServer(t)
	bound := make(chan string, 10)
	listen := func(ctx context.Context, address string) (net.Listener, error) {
		var lc net.ListenConfig
		l, err := lc.Listen(ctx, "tcp4", "127.0.0.1:0")
		if err == nil {
			bound <- address
		}
		return l, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); s.serveWiFi(ctx, &tls.Config{}, time.Millisecond, listen) }()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("listener did not stop")
		}
	}()
	expect := func(address string) {
		t.Helper()
		select {
		case got := <-bound:
			if got != address {
				t.Fatal(got)
			}
		case <-time.After(time.Second):
			t.Fatal("listener missing", address)
		}
	}
	expect("192.168.1.42:8443")
	n.mu.Lock()
	n.status.Link.Address = "192.168.1.43/24"
	n.mu.Unlock()
	expect("192.168.1.43:8443")
	n.SetWiFiAdmin(ctx, false)
	time.Sleep(10 * time.Millisecond)
	n.SetWiFiAdmin(ctx, true)
	expect("192.168.1.43:8443")
}

func TestWiFiAdminToggleRequiresSessionOriginAndCSRF(t *testing.T) {
	s, n := wifiServer(t)
	s.sessions[sha256.Sum256([]byte("token"))] = session{CSRF: "csrf", Expires: time.Now().Add(time.Minute)}
	for _, tc := range []struct {
		cookie, origin, csrf string
		want                 int
	}{
		{"", "https://" + s.Host, "csrf", 401},
		{"token", "https://evil.invalid", "csrf", 403},
		{"token", "https://" + s.Host, "wrong", 403},
		{"token", "https://" + s.Host, "csrf", 303},
	} {
		r := httptest.NewRequest("POST", "https://"+s.Host+"/network/wifi-admin", strings.NewReader(url.Values{"csrf": {tc.csrf}, "enabled": {"0"}}.Encode()))
		r.Header.Set("Origin", tc.origin)
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if tc.cookie != "" {
			r.AddCookie(&http.Cookie{Name: "gateway_session", Value: tc.cookie})
		}
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Fatal(w.Code, tc.want)
		}
		status, _ := n.Status(context.Background())
		if status.WiFiAdminEnabled != (tc.want != 303) {
			t.Fatal("unexpected access mutation")
		}
	}
}
