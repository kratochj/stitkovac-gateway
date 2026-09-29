package lab

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testServer(t *testing.T) *Server {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	captures := filepath.Join(dir, "prints")
	if err := os.Mkdir(captures, 0700); err != nil {
		t.Fatal(err)
	}
	lease := filepath.Join(dir, "lease.json")
	if err := os.WriteFile(lease, []byte(`{"ip":"192.168.77.50","mac":"02:77:00:00:00:01"}`), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := New(Config{Dir: dir, Captures: captures, LeaseFile: lease, Host: "127.0.0.1:9443", Token: strings.Repeat("t", 40), Password: strings.Repeat("p", 20)})
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func request(s *Server, method, path, body string, ui, origin bool) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "https://127.0.0.1:9443"+path, strings.NewReader(body))
	if ui {
		r.SetBasicAuth("technik", s.cfg.Password)
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	} else {
		r.Header.Set("Authorization", "Bearer "+s.cfg.Token)
		r.Header.Set("X-Gateway-Session", s.session)
	}
	if origin {
		r.Header.Set("Origin", "https://127.0.0.1:9443")
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}
func TestLabRequiresAuthenticationOriginAndCSRF(t *testing.T) {
	s := testServer(t)
	unauth := httptest.NewRequest("GET", "https://127.0.0.1:9443/", nil)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, unauth)
	if w.Code != 401 {
		t.Fatal("unprotected lab console")
	}
	if w := request(s, "POST", "/print", "kind=label&csrf="+s.csrf, true, false); w.Code != 403 {
		t.Fatal("accepted cross-origin request")
	}
	if w := request(s, "POST", "/print", "kind=label&csrf=wrong", true, true); w.Code != 403 {
		t.Fatal("accepted invalid CSRF")
	}
	if w := request(s, "GET", "/api/gateway/v1/jobs", "", false, false); w.Code != 409 {
		t.Fatal("accepted missing session")
	}
}
func TestLabPrintIsDurableAndTerminalResultIsImmutable(t *testing.T) {
	s := testServer(t)
	form := url.Values{"kind": {"receipt"}, "csrf": {s.csrf}}
	if w := request(s, "POST", "/print", form.Encode(), true, true); w.Code != 303 {
		t.Fatalf("print failed: %d %s", w.Code, w.Body.String())
	}
	reopened, err := New(s.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(reopened.jobs) != 1 || reopened.jobs[0].Kind != "receipt" {
		t.Fatal("job was not committed")
	}
	job := reopened.jobs[0]
	if !bytes.HasPrefix(job.Document, []byte("%PDF-1.4")) || !bytes.HasSuffix(job.Document, []byte("%%EOF\n")) {
		t.Fatal("invalid PDF envelope")
	}
	s.session = "session"
	result, _ := json.Marshal(map[string]string{"attemptId": job.Attempt.AttemptID, "state": "SENT"})
	path := "/api/gateway/v1/jobs/" + job.Attempt.JobUID + "/result"
	for i := 0; i < 2; i++ {
		if w := request(s, "POST", path, string(result), false, false); w.Code != 204 {
			t.Fatal("result not replayable")
		}
	}
	result, _ = json.Marshal(map[string]string{"attemptId": job.Attempt.AttemptID, "state": "FAILED"})
	if w := request(s, "POST", path, string(result), false, false); w.Code != 409 {
		t.Fatal("overwrote terminal result")
	}
	if w := request(s, "GET", "/api/gateway/v1/jobs", "", false, false); strings.Contains(w.Body.String(), job.Attempt.JobUID) {
		t.Fatal("requeued completed job")
	}
}
func TestPrinterPreservesActualTCPBytes(t *testing.T) {
	dir := t.TempDir()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- Printer(ctx, listener, dir) }()
	connection, err := net.Dial("tcp4", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	doc := Document("label", "test-job")
	if _, err := connection.Write(doc); err != nil {
		t.Fatal(err)
	}
	connection.Close()
	deadline := time.Now().Add(3 * time.Second)
	var files []string
	for time.Now().Before(deadline) {
		files, _ = Captures(dir)
		if len(files) == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(files) != 1 {
		t.Fatal("printer did not capture stream")
	}
	f, err := os.Open(filepath.Join(dir, files[0]))
	if err != nil {
		t.Fatal(err)
	}
	received, err := io.ReadAll(f)
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(doc, received) {
		t.Fatal("printer changed received bytes")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
