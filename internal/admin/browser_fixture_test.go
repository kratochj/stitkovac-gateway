package admin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/kratochj/stitkovac-gateway/internal/auth"
	"github.com/kratochj/stitkovac-gateway/internal/state"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Opt-in disposable browser fixture. Never reads VM or production configuration.
func TestOperationsBrowserFixture(t *testing.T) {
	dir := os.Getenv("GATEWAY_ADMIN_BROWSER_FIXTURE")
	if dir == "" {
		t.Skip("opt-in browser fixture")
	}
	hash, _ := auth.Hash("browser-fixture-password")
	store, err := state.Initialize(t.TempDir()+"/state", hash)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	pool := state.Pool{Network: netip.MustParsePrefix("192.168.77.0/24"), Server: netip.MustParseAddr("192.168.77.1"), First: netip.MustParseAddr("192.168.77.50"), Last: netip.MustParseAddr("192.168.77.199")}
	ctx := context.Background()
	r, err := store.Reserve(ctx, "02:00:00:00:00:01", pool, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	doc := []byte("%PDF fixture")
	digest := sha256.Sum256(doc)
	a := state.Attempt{JobUID: "fixture-job", AttemptID: "fixture-attempt", MAC: r.MAC, IP: r.IP, Port: 9100, Document: doc, Digest: hex.EncodeToString(digest[:]), ExpiresAt: time.Now().Add(time.Hour).Unix()}
	for _, err := range []error{store.Prepare(ctx, a), store.BeginSend(ctx, a.JobUID, a.AttemptID, time.Now()), store.Finish(ctx, a.JobUID, a.AttemptID, "UNKNOWN")} {
		if err != nil {
			t.Fatal(err)
		}
	}
	admin, _ := New(store, "unused", "test")
	admin.Pool = pool
	server := httptest.NewUnstartedServer(admin.Handler())
	admin.Host = server.Listener.Addr().String()
	server.StartTLS()
	defer server.Close()
	parsed, _ := url.Parse(server.URL)
	if admin.Host != parsed.Host {
		t.Fatal("fixture listener mismatch")
	}
	if err = os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(map[string]string{"url": server.URL})
	if err = os.WriteFile(filepath.Join(dir, "ready.json"), b, 0600); err != nil {
		t.Fatal(err)
	}
	deadline := time.NewTimer(2 * time.Minute)
	defer deadline.Stop()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-deadline.C:
			t.Fatal("browser fixture timed out")
		case <-ticker.C:
			if _, err := os.Stat(filepath.Join(dir, "stop")); err == nil {
				return
			}
		}
	}
}
