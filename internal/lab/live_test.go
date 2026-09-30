package lab

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDisconnectedLabRejectsPrintingAndSeparatesHistory(t *testing.T) {
	s := testServer(t)
	form := url.Values{"kind": {"receipt"}, "csrf": {s.csrf}}
	if w := request(s, "POST", "/print", form.Encode(), true, true); w.Code != 409 || len(s.jobs) != 0 {
		t.Fatal("disconnected lab queued a misleading print")
	}
	// Existing journals have no timestamps; do not invent a recent creation time.
	s.jobs = []Job{{Kind: "receipt"}}
	s.jobs[0].Attempt.State = "SENT"
	w := request(s, "GET", "/", "", true, false)
	for _, text := range []string{"Historie úloh místního", "Čas nebyl zaznamenán", "Úlohy z cloudového serveru", `value="label" disabled`, "SENT"} {
		if !strings.Contains(w.Body.String(), text) {
			t.Fatalf("missing %q", text)
		}
	}
	if !strings.Contains(w.Header().Get("Content-Security-Policy"), "connect-src 'self'") {
		t.Fatal("SSE origin not restricted")
	}
}

func TestCaptureFailureIsNotPresentedAsAnEmptyPrinter(t *testing.T) {
	s := testServer(t)
	if err := os.Remove(s.cfg.Captures); err != nil {
		t.Fatal(err)
	}
	w := request(s, "GET", "/", "", true, false)
	if !strings.Contains(w.Body.String(), "nyní nelze ověřit") || strings.Contains(w.Body.String(), "Zatím nebyl přijat žádný dokument") {
		t.Fatal("storage failure was presented as no printing")
	}
}

func TestLiveFeedRequiresConsoleAuthentication(t *testing.T) {
	s := testServer(t)
	for _, path := range []string{"/events", "/static/live.js"} {
		for _, gatewayCredential := range []bool{false, true} {
			r := httptest.NewRequest("GET", "https://127.0.0.1:9443"+path, nil)
			if gatewayCredential {
				r.Header.Set("Authorization", "Bearer "+s.cfg.Token)
			}
			w := httptest.NewRecorder()
			s.Handler().ServeHTTP(w, r)
			if w.Code != 401 {
				t.Fatalf("unprotected console resource %s: %d", path, w.Code)
			}
		}
	}
}

func TestLiveFeedObservesPrinterFilesWithoutLocalJobsAndReconnects(t *testing.T) {
	s := testServer(t)
	h := s.Handler()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Host = s.cfg.Host
		h.ServeHTTP(w, r)
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	open := func() (*http.Response, *bufio.Reader) {
		t.Helper()
		r, _ := http.NewRequestWithContext(ctx, "GET", server.URL+"/events", nil)
		r.SetBasicAuth("technik", s.cfg.Password)
		response, err := server.Client().Do(r)
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != 200 || response.Header.Get("Content-Type") != "text/event-stream" || response.Header.Get("Cache-Control") != "no-store" {
			t.Fatal("invalid stream response")
		}
		return response, bufio.NewReader(response.Body)
	}
	read := func(reader *bufio.Reader) string {
		t.Helper()
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				t.Fatal(err)
			}
			if strings.HasPrefix(line, "data: ") {
				var event struct{ HTML, CheckedAt string }
				if err := json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, "data: "))), &event); err != nil {
					t.Fatal(err)
				}
				if event.CheckedAt == "" {
					t.Fatal("missing freshness timestamp")
				}
				return event.HTML
			}
		}
	}
	response, reader := open()
	defer response.Body.Close()
	if !strings.Contains(read(reader), "Zatím nebyl přijat žádný dokument") {
		t.Fatal("missing initial snapshot")
	}
	name := "20260929T123456-abcdef.pdf"
	if err := os.WriteFile(filepath.Join(s.cfg.Captures, name), []byte("%PDF-capture"), 0600); err != nil {
		t.Fatal(err)
	}
	if html := read(reader); !strings.Contains(html, name) || !strings.Contains(html, "12 B") || !strings.Contains(html, "Zachyceno v simulátoru") {
		t.Fatal("external printer capture did not reach the live feed")
	}
	if len(s.jobs) != 0 {
		t.Fatal("capture created a fake cloud job")
	}
	response.Body.Close()
	reconnected, next := open()
	defer reconnected.Body.Close()
	if !strings.Contains(read(next), name) {
		t.Fatal("reconnect did not receive the current state")
	}
	if err := os.Remove(filepath.Join(s.cfg.Captures, name)); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(read(next), name) {
		t.Fatal("retention change was not streamed")
	}
}
