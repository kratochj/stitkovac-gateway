package admin

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/kratochj/stitkovac-gateway/internal/auth"
	"github.com/kratochj/stitkovac-gateway/internal/state"
)

func TestAuthenticationAndOrigin(t *testing.T) {
	hash, err := auth.Hash("a long test password")
	if err != nil {
		t.Fatal(err)
	}
	store, err := state.Initialize(t.TempDir(), hash)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	s, err := New(store, "127.0.0.1:8443", "test")
	if err != nil {
		t.Fatal(err)
	}
	h := s.Handler()
	request := func(method, path, origin, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "https://127.0.0.1:8443"+path, strings.NewReader(body))
		r.TLS = &tls.ConnectionState{}
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("Origin", origin)
		if cookie != nil {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := request("GET", "/", "", "", nil); w.Code != 303 || strings.Contains(w.Body.String(), "MAC") {
		t.Fatal("unauthenticated data leak")
	}
	if w := request("POST", "/login", "https://evil.invalid", "password=x", nil); w.Code != 403 {
		t.Fatal("cross origin login allowed")
	}
	w := request("POST", "/login", "https://127.0.0.1:8443", "password="+url.QueryEscape("a long test password"), nil)
	if w.Code != 303 {
		t.Fatal(w.Code, w.Body.String())
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].Secure || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatal("unsafe session cookie")
	}
	w = request("GET", "/", "", "", cookies[0])
	if w.Code != 200 || !strings.Contains(w.Body.String(), "DHCP rezervace") {
		t.Fatal("missing dashboard")
	}
	if w := request("POST", "/logout", "https://127.0.0.1:8443", "csrf=wrong", cookies[0]); w.Code != 403 {
		t.Fatal("missing CSRF validation")
	}
	r := httptest.NewRequest("GET", "https://evil.invalid/", nil)
	r.TLS = &tls.ConnectionState{}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 421 {
		t.Fatal("host not restricted")
	}
}
