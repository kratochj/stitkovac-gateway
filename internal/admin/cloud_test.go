package admin

import (
	"crypto/sha256"
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/kratochj/stitkovac-gateway/internal/cloud"
	"github.com/kratochj/stitkovac-gateway/internal/state"
)

func TestCloudSettingsRequireSessionOriginAndCSRFAndNeverEchoToken(t *testing.T) {
	dir := t.TempDir() + "/state"
	store, err := state.Initialize(dir, "test-password-hash")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	s, err := New(store, "127.0.0.1:8443", "test")
	if err != nil {
		t.Fatal(err)
	}
	manager := cloud.NewManager(dir, cloud.Config{}, nil)
	s.Cloud = manager
	cookie := &http.Cookie{Name: "gateway_session", Value: "test-session"}
	s.sessions[sha256.Sum256([]byte(cookie.Value))] = session{CSRF: "test-csrf", Expires: time.Now().Add(time.Minute)}
	secret := strings.Repeat("very-private-token", 4)
	handler := s.Handler()
	request := func(method, origin, csrf string, authenticated bool) *httptest.ResponseRecorder {
		body := url.Values{"csrf": {csrf}, "server_url": {"https://cloud.example"}, "token": {secret}}.Encode()
		path := "/cloud"
		if method == "GET" {
			path = "/"
		}
		r := httptest.NewRequest(method, "https://127.0.0.1:8443"+path, strings.NewReader(body))
		r.TLS = &tls.ConnectionState{}
		r.Header.Set("Origin", origin)
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if authenticated {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if strings.Contains(w.Body.String(), secret) {
			t.Fatal("token leaked into HTTP response")
		}
		return w
	}
	for _, tc := range []struct {
		origin, csrf  string
		authenticated bool
		code          int
	}{
		{"https://127.0.0.1:8443", "test-csrf", false, 401},
		{"https://evil.example", "test-csrf", true, 403},
		{"null", "test-csrf", true, 403},
		{"https://127.0.0.1:8443", "wrong", true, 403},
	} {
		if w := request("POST", tc.origin, tc.csrf, tc.authenticated); w.Code != tc.code {
			t.Fatalf("status %d, want %d", w.Code, tc.code)
		}
		if manager.Status().TokenSet {
			t.Fatal("unauthorized request changed configuration")
		}
	}
	if w := request("POST", "https://127.0.0.1:8443", "test-csrf", true); w.Code != 303 {
		t.Fatal(w.Code)
	}
	cfg, exists, err := cloud.LoadConfig(dir)
	if err != nil || !exists || cfg.Token != secret {
		t.Fatal("authenticated save failed")
	}
	w := request("GET", "", "", true)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "https://cloud.example") || !strings.Contains(w.Body.String(), "Token je uložený") {
		t.Fatal("missing settings status")
	}
}
