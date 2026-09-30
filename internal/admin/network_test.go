package admin

import (
	"context"
	"crypto/sha256"
	"github.com/kratochj/stitkovac-gateway/internal/network"
	"github.com/kratochj/stitkovac-gateway/internal/state"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

type networkStub struct {
	applied int
	request network.WiFiRequest
	reject  bool
}

func (n *networkStub) Status(context.Context) (network.Status, error) {
	return network.Status{Available: true, Simulation: true, Mode: "ap", Country: "CZ", APSSID: "Service", APAddress: "192.168.78.1/24"}, nil
}
func (n *networkStub) Scan(context.Context) ([]network.AccessPoint, error) {
	return []network.AccessPoint{{SSID: "<script>untrusted</script>", Signal: 99, Security: "WPA2"}}, nil
}
func (n *networkStub) Apply(_ context.Context, r network.WiFiRequest) error {
	n.applied++
	n.request = r
	if n.reject {
		return network.ErrInvalid
	}
	return nil
}
func (n *networkStub) SetWiFiAdmin(context.Context, bool) error { return nil }
func (n *networkStub) ServiceAP(context.Context, bool) error    { return nil }
func TestNetworkFormsAuthOriginCSRFAndSecrets(t *testing.T) {
	store, err := state.Initialize(t.TempDir()+"/state", "hash")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	s, _ := New(store, "127.0.0.1:8443", "test")
	s.AdditionalHost = "192.168.78.1:8443"
	backend := &networkStub{}
	s.Network = backend
	cookie := &http.Cookie{Name: "gateway_session", Value: "session"}
	s.sessions[sha256.Sum256([]byte(cookie.Value))] = session{CSRF: "csrf", Expires: time.Now().Add(time.Minute)}
	secret := "private-wifi-password"
	send := func(method, path, host, origin, csrf string, auth bool) *httptest.ResponseRecorder {
		values := url.Values{"csrf": {csrf}, "ssid": {"Customer"}, "password": {secret}, "country": {"CZ"}, "security": {"wpa-psk"}}
		r := httptest.NewRequest(method, "https://"+host+path, strings.NewReader(values.Encode()))
		r.Header.Set("Origin", origin)
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if auth {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if strings.Contains(w.Body.String(), secret) {
			t.Fatal("password exposed")
		}
		return w
	}
	if w := send("GET", "/network", s.Host, "", "", false); w.Code != 303 {
		t.Fatal(w.Code)
	}
	for _, tc := range []struct {
		auth         bool
		origin, csrf string
		code         int
	}{{false, "https://" + s.Host, "csrf", 401}, {true, "https://evil.invalid", "csrf", 403}, {true, "https://" + s.Host, "wrong", 403}} {
		if w := send("POST", "/network/wifi", s.Host, tc.origin, tc.csrf, tc.auth); w.Code != tc.code {
			t.Fatal(w.Code)
		}
	}
	if backend.applied != 0 {
		t.Fatal("unauthorized mutation")
	}
	if w := send("POST", "/network/wifi", s.AdditionalHost, "https://"+s.Host, "csrf", true); w.Code != 403 {
		t.Fatal("cross-host origin accepted")
	}
	if w := send("POST", "/network/wifi", s.AdditionalHost, "https://"+s.AdditionalHost, "csrf", true); w.Code != 303 {
		t.Fatal(w.Code)
	}
	if backend.request.Password != secret || backend.request.ServerURL != "https://cloud.stitkovac.app" {
		t.Fatal("incorrect helper request")
	}
	backend.reject = true
	if w := send("POST", "/network/wifi", s.Host, "https://"+s.Host, "csrf", true); w.Code != 200 || !strings.Contains(w.Body.String(), "Zkontrolujte SSID") {
		t.Fatal(w.Code, w.Body.String())
	}
	w := send("POST", "/network/scan", s.Host, "https://"+s.Host, "csrf", true)
	if w.Code != 200 || strings.Contains(w.Body.String(), "<script>untrusted") || !strings.Contains(w.Body.String(), "&lt;script&gt;") {
		t.Fatal("SSID escaping failed")
	}
	s.Network = nil
	w = send("GET", "/network", s.Host, "", "", true)
	if !strings.Contains(w.Body.String(), "není nainstalovaná") {
		t.Fatal("missing offline status")
	}
}
