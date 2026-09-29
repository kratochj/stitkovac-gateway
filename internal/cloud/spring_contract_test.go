package cloud

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/netip"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kratochj/stitkovac-gateway/internal/printing"
	"github.com/kratochj/stitkovac-gateway/internal/state"
)

// Spring's GatewayTransportTests supplies an isolated MariaDB-backed HTTP server.
// The test proxy provides trusted TLS, just as the production ingress does.
func TestSpringBackend(t *testing.T) {
	origin := os.Getenv("GATEWAY_CONTRACT_ORIGIN")
	if origin == "" {
		t.Skip("run through the server GatewayTransportTests with GATEWAY_GO_COMMAND and GATEWAY_SOURCE")
	}
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "http" || u.Hostname() != "localhost" && u.Hostname() != "127.0.0.1" {
		t.Fatal("contract server must be local")
	}
	s, err := state.Initialize(t.TempDir()+"/state", "test-hash")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	pool := state.Pool{Network: netip.MustParsePrefix("192.168.77.0/24"), Server: netip.MustParseAddr("192.168.77.1"), First: netip.MustParseAddr("192.168.77.50"), Last: netip.MustParseAddr("192.168.77.199")}
	if _, err := s.Reserve(context.Background(), "02:00:00:00:00:01", pool, time.Now()); err != nil {
		t.Fatal(err)
	}
	var lost atomic.Bool
	empty := make(chan struct{}, 1)
	proxy := httputil.NewSingleHostReverseProxy(u)
	proxy.ModifyResponse = func(response *http.Response) error {
		path := response.Request.URL.Path
		if strings.HasSuffix(path, "/result") && response.StatusCode == 204 && lost.CompareAndSwap(false, true) {
			// The database accepted SENT, but the gateway loses the acknowledgement.
			response.StatusCode = 502
			response.Status = "502 Bad Gateway"
		}
		if strings.HasSuffix(path, "/jobs") && lost.Load() && response.StatusCode == 200 {
			b, err := io.ReadAll(response.Body)
			response.Body.Close()
			if err != nil {
				return err
			}
			response.Body = io.NopCloser(bytes.NewReader(b))
			if bytes.Contains(b, []byte(`"jobs":[]`)) {
				select {
				case empty <- struct{}{}:
				default:
				}
			}
		}
		return nil
	}
	server := httptest.NewTLSServer(proxy)
	defer server.Close()
	p := &printer{}
	worker := &printing.Worker{Store: s, Pool: pool, Dial: func(context.Context, string) (net.Conn, error) { return p, nil }}
	client, err := New(server.URL, os.Getenv("GATEWAY_CONTRACT_TOKEN"), os.Getenv("GATEWAY_CONTRACT_ID"), "contract-test", worker, server.Client().Transport, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := client.Session(ctx); err == nil || !lost.Load() || ctx.Err() != nil {
		t.Fatal("first session did not reach the durable result")
	}
	done := make(chan struct{})
	go func() { defer close(done); client.Session(ctx) }()
	select {
	case <-empty:
	case <-ctx.Done():
		t.Fatal("reconnect did not reconcile the completed job")
	}
	cancel()
	<-done
	p.mu.Lock()
	defer p.mu.Unlock()
	if !bytes.Equal(p.data.Bytes(), []byte("%PDF-test")) {
		t.Fatal("missing or duplicate printer write")
	}
}
