package admin

import (
	"crypto/sha256"
	"crypto/tls"
	"github.com/kratochj/stitkovac-gateway/internal/auth"
	"github.com/kratochj/stitkovac-gateway/internal/state"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestPasswordRotationRequiresCurrentSecretAndRevokesAllSessions(t *testing.T) {
	old := "the original private password"
	newPassword := "the replacement private password"
	hash, _ := auth.Hash(old)
	store, err := state.Initialize(t.TempDir()+"/state", hash)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	server, _ := New(store, "127.0.0.1:8443", "test")
	server.sessions[sha256.Sum256([]byte("session"))] = session{CSRF: "csrf", Expires: time.Now().Add(time.Hour)}
	post := func(password, csrf string) int {
		req := httptest.NewRequest("POST", "https://127.0.0.1:8443/access/password", strings.NewReader(url.Values{"csrf": {csrf}, "current_password": {password}, "new_password": {newPassword}, "confirm_password": {newPassword}}.Encode()))
		req.TLS = &tls.ConnectionState{}
		req.Header.Set("Origin", "https://127.0.0.1:8443")
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: "gateway_session", Value: "session"})
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, req)
		return response.Code
	}
	if post(old, "wrong") != 403 {
		t.Fatal("CSRF bypass")
	}
	if post("wrong", "csrf") != 403 {
		t.Fatal("old secret not checked")
	}
	server.nextLogin = time.Time{}
	if post(old, "csrf") != 303 {
		t.Fatal("rotation failed")
	}
	_, current, _ := store.Identity()
	if auth.Verify(current, old) || !auth.Verify(current, newPassword) || len(server.sessions) != 0 {
		t.Fatal("credentials or sessions not rotated")
	}
	if post(newPassword, "csrf") != 401 {
		t.Fatal("revoked session accepted")
	}
}
