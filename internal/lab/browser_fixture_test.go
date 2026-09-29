package lab

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestLabBrowserFixture exposes only disposable test data to the opt-in browser smoke.
func TestLabBrowserFixture(t *testing.T) {
	dir := os.Getenv("GATEWAY_LAB_BROWSER_DIR")
	if dir == "" {
		t.Skip("opt-in browser fixture")
	}
	s := testServer(t)
	s.jobs = []Job{{Kind: "receipt"}}
	s.jobs[0].Attempt.JobUID = "historical-local-job"
	s.jobs[0].Attempt.State = "SENT"
	handler := s.Handler()
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The production lab fixes its Host to the SSH tunnel port; the test uses a random port.
		r.Host = s.cfg.Host
		handler.ServeHTTP(w, r)
	}))
	server.Config.WriteTimeout = 15 * time.Second
	server.StartTLS()
	defer server.Close()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	printerDone := make(chan error, 1)
	go func() { printerDone <- Printer(ctx, listener, s.cfg.Captures) }()
	defer func() {
		cancel()
		if err := <-printerDone; err != nil {
			t.Error(err)
		}
	}()
	fixture, _ := json.Marshal(map[string]string{"url": server.URL, "printer": listener.Addr().String(), "password": s.cfg.Password})
	if err := os.WriteFile(filepath.Join(dir, "fixture.json"), fixture, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "document.pdf"), Document("label", "browser-test"), 0600); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(filepath.Join(dir, "done")); err == nil {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("browser smoke did not complete")
}
