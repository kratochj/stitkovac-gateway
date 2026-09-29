package admin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/kratochj/stitkovac-gateway/internal/printing"
	"github.com/kratochj/stitkovac-gateway/internal/state"
)

func TestDiagnosticsRequireAuthenticationAndNeverExposeDocuments(t *testing.T) {
	store, err := state.Initialize(t.TempDir()+"/state", "test-hash")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	server, err := New(store, "127.0.0.1:8443", "test")
	if err != nil {
		t.Fatal(err)
	}
	cookie := &http.Cookie{Name: "gateway_session", Value: "diagnostics-session"}
	server.sessions[sha256.Sum256([]byte(cookie.Value))] = session{CSRF: "csrf", Expires: time.Now().Add(time.Minute)}
	handler := server.Handler()
	calls := 0
	server.TestPrinter = func(_ context.Context, mac string) printing.ProbeResult {
		calls++
		return printing.ProbeResult{MAC: mac, IP: "192.168.77.50", Code: "reachable", CheckedAt: time.Now().Unix()}
	}
	request := func(method, path, origin, csrf string, authenticated bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "https://127.0.0.1:8443"+path, strings.NewReader(url.Values{"csrf": {csrf}, "mac": {"02:00:00:00:00:01"}}.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("Origin", origin)
		if authenticated {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	for _, tc := range []struct {
		origin, csrf string
		auth         bool
		code         int
	}{
		{"https://127.0.0.1:8443", "csrf", false, 401},
		{"https://evil.invalid", "csrf", true, 403},
		{"https://127.0.0.1:8443", "wrong", true, 403},
	} {
		if w := request("POST", "/printers/check", tc.origin, tc.csrf, tc.auth); w.Code != tc.code {
			t.Fatal(w.Code, tc.code)
		}
	}
	if calls != 0 {
		t.Fatal("unauthorized connection probe")
	}
	if w := request("GET", "/jobs", "", "", false); w.Code != 303 {
		t.Fatal("history leaked without authentication")
	}
	for _, path := range []string{"/jobs?before=-1", "/jobs?before=invalid", "/jobs?state=invalid"} {
		if w := request("GET", path, "", "", true); w.Code != 400 {
			t.Fatal("accepted invalid history query")
		}
	}
	if w := request("POST", "/printers/check", "https://127.0.0.1:8443", "csrf", true); w.Code != 303 {
		t.Fatal(w.Code)
	}
	w := request("GET", "/", "", "", true)
	if !strings.Contains(w.Body.String(), "TCP port 9100 je dostupný") || calls != 1 {
		t.Fatal("missing probe result")
	}
	request("POST", "/printers/check", "https://127.0.0.1:8443", "csrf", true)
	if calls != 1 {
		t.Fatal("probe rate limit missing")
	}
	// An actual journal row verifies escaping and the absence of PDF content.
	pool := state.Pool{Network: netip.MustParsePrefix("192.168.77.0/24"), Server: netip.MustParseAddr("192.168.77.1"), First: netip.MustParseAddr("192.168.77.50"), Last: netip.MustParseAddr("192.168.77.51")}
	reservation, err := store.Reserve(context.Background(), "02:00:00:00:00:01", pool, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	document := []byte("%PDF private document body MUST NOT appear in HTML")
	digest := sha256.Sum256(document)
	a := state.Attempt{JobUID: "<script>alert(1)</script>", AttemptID: "attempt", MAC: reservation.MAC, IP: reservation.IP, Port: 9100, Digest: hex.EncodeToString(digest[:]), Document: document, ExpiresAt: time.Now().Add(time.Minute).Unix()}
	if err := store.Prepare(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	if err := store.BeginSend(context.Background(), a.JobUID, a.AttemptID, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := store.Recover(context.Background(), time.Now()); err != nil {
		t.Fatal(err)
	}
	w = request("GET", "/jobs?state=UNKNOWN", "", "", true)
	body := w.Body.String()
	if w.Code != 200 || !strings.Contains(body, "Nejistý výsledek") || !strings.Contains(body, "blokovaný") || !strings.Contains(body, "&lt;script&gt;") {
		t.Fatal("missing or unsafe history view")
	}
	if strings.Contains(body, string(document)) || strings.Contains(body, "<script>") {
		t.Fatal("history leaked or failed to escape journal contents")
	}
	w = request("GET", "/", "", "", true)
	if !strings.Contains(w.Body.String(), "Nejistý výsledek tisku: 1") {
		t.Fatal("missing persistent warning on dashboard")
	}
}
