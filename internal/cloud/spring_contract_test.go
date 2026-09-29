package cloud

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/netip"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kratochj/stitkovac-gateway/internal/ota"
	"github.com/kratochj/stitkovac-gateway/internal/printing"
	"github.com/kratochj/stitkovac-gateway/internal/state"
	"github.com/kratochj/stitkovac-gateway/internal/update"
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
	pending, err := s.PendingResults(context.Background())
	if err != nil || len(pending) != 0 {
		t.Fatal("lost acknowledgement left local data pending", err)
	}
	revision, acknowledged, err := s.InventoryStatus(context.Background())
	if err != nil || revision != acknowledged {
		t.Fatal("real server did not acknowledge inventory", err)
	}
	if !bytes.Equal(p.data.Bytes(), []byte("%PDF-test")) {
		t.Fatal("missing or duplicate printer write")
	}
}

// The real Spring/MariaDB server signs and hosts the release. This test exercises
// WSS delivery, download, print draining and durable confirmation reporting. The
// separate launcher process tests exercise the actual startup health handshake.
func TestSpringOTA(t *testing.T) {
	origin := os.Getenv("GATEWAY_CONTRACT_ORIGIN")
	if origin == "" {
		t.Skip("run through GatewayOtaTests")
	}
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "http" || (u.Hostname() != "localhost" && u.Hostname() != "127.0.0.1") {
		t.Fatal("contract server must be local")
	}
	target := os.Getenv("GATEWAY_CONTRACT_TARGET")
	dir, root := t.TempDir(), t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	keys, err := update.ReadKeys(os.Getenv("GATEWAY_CONTRACT_KEYS"))
	if err != nil {
		t.Fatal(err)
	}
	releases, err := update.Open(root, keys, "linux-arm64")
	if err != nil {
		t.Fatal(err)
	}
	signed, err := os.ReadFile(os.Getenv("GATEWAY_CONTRACT_INITIAL"))
	if err != nil {
		t.Fatal(err)
	}
	initial := bytes.Repeat([]byte{42}, 64)
	copy(initial, []byte{0x7f, 0x45, 0x4c, 0x46, 2, 1})
	initial[18] = 0xb7
	initial[19] = 0
	if _, err = releases.Stage(signed, bytes.NewReader(initial)); err != nil {
		t.Fatal(err)
	}
	if err = releases.Initialize("1.0.0"); err != nil {
		t.Fatal(err)
	}
	s, err := state.Initialize(dir+"/state", "test-hash")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	pool := state.Pool{Network: netip.MustParsePrefix("192.168.77.0/24"), Server: netip.MustParseAddr("192.168.77.1"), First: netip.MustParseAddr("192.168.77.50"), Last: netip.MustParseAddr("192.168.77.199")}
	if _, err = s.Reserve(context.Background(), "02:00:00:00:00:01", pool, time.Now()); err != nil {
		t.Fatal(err)
	}
	p := &otaPrinter{started: make(chan struct{}), release: make(chan struct{})}
	defer p.unblock()
	draining, complete := make(chan struct{}, 1), make(chan struct{}, 1)
	acknowledged := make(chan struct{}, 1)
	var earlyActivation atomic.Bool
	var rebooted atomic.Bool
	proxy := httputil.NewSingleHostReverseProxy(u)
	proxy.ModifyResponse = func(response *http.Response) error {
		if rebooted.Load() && strings.HasSuffix(response.Request.URL.Path, "/jobs") && response.StatusCode == 200 {
			b, err := io.ReadAll(response.Body)
			response.Body.Close()
			if err != nil {
				return err
			}
			response.Body = io.NopCloser(bytes.NewReader(b))
			var queue page
			if json.Unmarshal(b, &queue) == nil && len(queue.Jobs) == 0 {
				// The server may have committed SENT before cancellation lost its response.
				select {
				case acknowledged <- struct{}{}:
				default:
				}
			}
		}
		if strings.HasSuffix(response.Request.URL.Path, "/result") && response.StatusCode == 204 {
			select {
			case acknowledged <- struct{}{}:
			default:
			}
		}
		return nil
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/activate") && !p.finished.Load() {
			earlyActivation.Store(true)
		}
		if strings.HasSuffix(r.URL.Path, "/report") {
			b, _ := io.ReadAll(r.Body)
			r.Body.Close()
			r.Body = io.NopCloser(bytes.NewReader(b))
			var report ota.Report
			_ = json.Unmarshal(b, &report)
			proxy.ServeHTTP(w, r)
			if report.State == "DRAINING" {
				select {
				case draining <- struct{}{}:
				default:
				}
			}
			if report.State == "SUCCEEDED" {
				select {
				case complete <- struct{}{}:
				default:
				}
			}
			return
		}
		proxy.ServeHTTP(w, r)
	}))
	defer server.Close()
	cfg := Config{URL: server.URL, Token: os.Getenv("GATEWAY_CONTRACT_TOKEN")}
	worker := &printing.Worker{Store: s, Pool: pool, Dial: func(context.Context, string) (net.Conn, error) { return p, nil }}
	start := func(version string, waitForPrint bool) (*ota.Controller, context.CancelFunc, <-chan struct{}) {
		ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
		var controller *ota.Controller
		manager := NewManager(dir, os.Getenv("GATEWAY_CONTRACT_ID"), cfg, func(cfg Config, observe func(string)) (Connection, error) {
			client, err := New(cfg.URL, cfg.Token, os.Getenv("GATEWAY_CONTRACT_ID"), version, worker, server.Client().Transport, nil)
			if err != nil {
				return nil, err
			}
			client.OnState = observe
			client.OnOTA = func(remote ota.Remote) {
				if !waitForPrint {
					controller.Notify(remote)
					return
				}
				go func() {
					select {
					case <-p.started:
						controller.Notify(remote)
					case <-ctx.Done():
					}
				}()
			}
			return client, nil
		})
		controller, err = ota.New(dir, server.URL, version, releases, manager)
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		controller.DownloadTransport = server.Client().Transport
		done := make(chan struct{})
		go func() {
			defer close(done)
			child := make(chan struct{})
			go func() { defer close(child); controller.Run(ctx) }()
			manager.Run(ctx)
			<-child
		}()
		return controller, cancel, done
	}
	controller, cancel, done := start("1.0.0", true)
	defer func() { p.unblock(); cancel(); <-done }()
	select {
	case <-draining:
	case <-time.After(15 * time.Second):
		t.Fatal("OTA did not download and reach draining")
	}
	selection, err := releases.Status()
	if err != nil || selection.Pending != "" {
		t.Fatal("activated while print was blocked")
	}
	p.unblock()
	select {
	case <-controller.Restart():
	case <-time.After(10 * time.Second):
		t.Fatal("drained update did not request restart")
	}
	cancel()
	<-done
	selection, err = releases.BeginBoot()
	if err != nil || selection.Active != target || !selection.Trial {
		t.Fatal(selection, err)
	}
	if err = releases.Confirm(target); err != nil {
		t.Fatal(err)
	}
	rebooted.Store(true)
	nextController, stopNext, nextDone := start(target, false)
	defer func() { stopNext(); <-nextDone }()
	select {
	case <-complete:
	case <-time.After(15 * time.Second):
		t.Fatal("confirmed version was not reported", nextController.Status())
	}
	select {
	case <-acknowledged:
	case <-time.After(10 * time.Second):
		t.Fatal("print acknowledgement was not reconciled after update")
	}
	if earlyActivation.Load() {
		t.Fatal("server granted activation before print completion")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if string(p.data.Bytes()) != "%PDF-OTA" {
		t.Fatal("missing or duplicate print")
	}
}

type otaPrinter struct {
	printer
	started, release  chan struct{}
	once, releaseOnce sync.Once
	finished          atomic.Bool
}

func (p *otaPrinter) unblock() { p.releaseOnce.Do(func() { close(p.release) }) }
func (p *otaPrinter) Write(b []byte) (int, error) {
	p.once.Do(func() { close(p.started) })
	<-p.release
	n, err := p.printer.Write(b)
	p.finished.Store(true)
	return n, err
}
