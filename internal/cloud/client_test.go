package cloud

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/kratochj/stitkovac-gateway/internal/printing"
	"github.com/kratochj/stitkovac-gateway/internal/state"
)

type printer struct {
	mu   sync.Mutex
	data bytes.Buffer
}

func (p *printer) Write(b []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.data.Write(b)
}
func (*printer) Read([]byte) (int, error)         { return 0, io.EOF }
func (*printer) Close() error                     { return nil }
func (*printer) LocalAddr() net.Addr              { return nil }
func (*printer) RemoteAddr() net.Addr             { return nil }
func (*printer) SetDeadline(time.Time) error      { return nil }
func (*printer) SetReadDeadline(time.Time) error  { return nil }
func (*printer) SetWriteDeadline(time.Time) error { return nil }

func TestWSSDispatchAndDuplicateNotification(t *testing.T) {
	s, err := state.Initialize(t.TempDir(), "test-hash")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	pool := state.Pool{Network: netip.MustParsePrefix("192.168.77.0/24"), Server: netip.MustParseAddr("192.168.77.1"), First: netip.MustParseAddr("192.168.77.50"), Last: netip.MustParseAddr("192.168.77.199")}
	r, err := s.Reserve(context.Background(), "02:00:00:00:00:01", pool, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	b := []byte("%PDF original")
	hash := sha256.Sum256(b)
	a := state.Attempt{JobUID: "job", AttemptID: "attempt", MAC: r.MAC, IP: r.IP, Port: 9100, Digest: hex.EncodeToString(hash[:]), ExpiresAt: time.Now().Add(time.Minute).Unix()}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	results := make(chan struct{}, 4)
	token := strings.Repeat("t", 32)
	mux := http.NewServeMux()
	mux.HandleFunc("/api/gateway/v1/connect", func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		_, b, err := conn.Read(r.Context())
		if err != nil {
			return
		}
		var hello envelope
		if json.Unmarshal(b, &hello) != nil || hello.Type != "hello" {
			return
		}
		ready, _ := json.Marshal(envelope{Version: 1, Type: "ready", SessionID: "session", MessageID: "ready"})
		conn.Write(r.Context(), websocket.MessageText, ready)
		select {
		case <-results:
		case <-ctx.Done():
			return
		}
		available, _ := json.Marshal(envelope{Version: 1, Type: "jobs.available", MessageID: "duplicate"})
		conn.Write(r.Context(), websocket.MessageText, available)
		select {
		case <-results:
			cancel()
		case <-ctx.Done():
		}
	})
	mux.HandleFunc("/api/gateway/v1/jobs", func(w http.ResponseWriter, r *http.Request) { json.NewEncoder(w).Encode(page{Jobs: []string{"job"}}) })
	mux.HandleFunc("/api/gateway/v1/jobs/job/claim", func(w http.ResponseWriter, r *http.Request) { json.NewEncoder(w).Encode(a) })
	mux.HandleFunc("/api/gateway/v1/jobs/job/document", func(w http.ResponseWriter, r *http.Request) { w.Write(b) })
	mux.HandleFunc("/api/gateway/v1/jobs/job/start", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	mux.HandleFunc("/api/gateway/v1/jobs/job/result", func(w http.ResponseWriter, r *http.Request) {
		var result map[string]string
		json.NewDecoder(r.Body).Decode(&result)
		if result["state"] != "SENT" {
			t.Error("unexpected result", result)
		}
		w.WriteHeader(204)
		results <- struct{}{}
	})
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			t.Error("missing gateway authentication")
			http.Error(w, "unauthorized", 401)
			return
		}
		if r.URL.Path != "/api/gateway/v1/connect" && r.Header.Get("X-Gateway-Session") != "session" {
			t.Error("missing session fencing")
		}
		mux.ServeHTTP(w, r)
	}))
	defer server.Close()
	p := &printer{}
	worker := &printing.Worker{Store: s, Pool: pool, Dial: func(context.Context, string) (net.Conn, error) { return p, nil }}
	c, err := New(server.URL, token, "gateway", "test", worker, server.Client().Transport, nil)
	if err != nil {
		t.Fatal(err)
	}
	c.Session(ctx)
	if ctx.Err() == context.DeadlineExceeded {
		t.Fatal("WSS round trip timed out")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if !bytes.Equal(p.data.Bytes(), b) {
		t.Fatal("duplicate or missing TCP write", p.data.Len())
	}
}

func TestRejectInsecureOrigins(t *testing.T) {
	for _, origin := range []string{"http://localhost", "https://user:pass@host", "https://host/path", "https://host?token=x"} {
		if _, err := New(origin, strings.Repeat("t", 32), "id", "test", nil, nil, nil); err == nil {
			t.Fatal("accepted unsafe origin")
		}
	}
}
