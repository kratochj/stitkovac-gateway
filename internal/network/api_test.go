package network

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHelperPeerAuthorizationAndStrictRequests(t *testing.T) {
	cfg := testConfig(t)
	c, err := NewController(cfg, &Simulated{Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		uid                uint32
		method, path, body string
		want               int
	}{
		{999, "GET", "/status", "", 403}, {1000, "GET", "/status", "", 200}, {0, "GET", "/status", "", 200},
		{1000, "POST", "/wifi", `{"ssid":"x","command":"rm"}`, 400}, {1000, "POST", "/wifi", `{} {}`, 400},
		{1000, "POST", "/arbitrary", "", 404},
		{999, "POST", "/wifi-admin", `{"enabled":true}`, 403},
		{1000, "POST", "/wifi-admin", `{}`, 400},
		{1000, "POST", "/wifi-admin", `{"enabled":true,"command":"x"}`, 400},
		{1000, "POST", "/wifi-admin", `{"enabled":true} {}`, 400},
		{1000, "POST", "/wifi-admin", `{"enabled":true}`, 200},
		{1000, "POST", "/wifi-admin", `{"enabled":false}`, 200},
	}
	for _, tt := range cases {
		r := httptest.NewRequest(tt.method, tt.path, strings.NewReader(tt.body))
		r = r.WithContext(context.WithValue(r.Context(), peerKey{}, tt.uid))
		w := httptest.NewRecorder()
		c.handler().ServeHTTP(w, r)
		if w.Code != tt.want {
			t.Fatalf("%+v: %d", tt, w.Code)
		}
		if strings.Contains(w.Body.String(), cfg.APPassword) {
			t.Fatal("AP password exposed")
		}
	}
	r := httptest.NewRequest("GET", "/status", nil)
	w := httptest.NewRecorder()
	c.handler().ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("missing peer allowed")
	}
}
