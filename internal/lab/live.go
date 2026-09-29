package lab

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

type captureView struct {
	Name       string
	Size       int64
	ReceivedAt int64
}

type dashboard struct {
	Lease         Lease
	Files         []captureView
	Jobs          []Job
	Online        bool
	CaptureError  bool
	Gateway, CSRF string
}

func (s *Server) snapshot() dashboard {
	lease, _ := s.lease()
	names, err := Captures(s.cfg.Captures)
	view := dashboard{Lease: lease, CaptureError: err != nil, CSRF: s.csrf}
	for _, name := range names {
		info, err := os.Lstat(filepath.Join(s.cfg.Captures, name))
		if os.IsNotExist(err) {
			continue // Retention can remove a capture between listing and stat.
		}
		if err != nil {
			view.CaptureError = true
			continue
		}
		if info.Mode().IsRegular() {
			view.Files = append(view.Files, captureView{Name: name, Size: info.Size(), ReceivedAt: info.ModTime().Unix()})
		}
	}
	s.mu.Lock()
	view.Jobs = append([]Job(nil), s.jobs...)
	view.Online = s.conn != nil && s.session != ""
	view.Gateway = s.gateway
	s.mu.Unlock()
	for left, right := 0, len(view.Jobs)-1; left < right; left, right = left+1, right-1 {
		view.Jobs[left], view.Jobs[right] = view.Jobs[right], view.Jobs[left]
	}
	return view
}

// events streams authenticated snapshots. The printer is a separate process;
// inspecting its bounded capture directory also observes cloud-originated prints.
func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	control := http.NewResponseController(w)
	if err := control.SetWriteDeadline(time.Time{}); err != nil {
		http.Error(w, "Živé připojení není dostupné.", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("X-Accel-Buffering", "no")
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	var previous []byte
	lastSent := time.Time{}
	for {
		var rendered bytes.Buffer
		if err := page.ExecuteTemplate(&rendered, "dashboard", s.snapshot()); err != nil {
			return
		}
		changed := !bytes.Equal(previous, rendered.Bytes())
		if changed || time.Since(lastSent) >= 10*time.Second {
			name := "heartbeat"
			payload := struct {
				HTML      string `json:"html,omitempty"`
				CheckedAt string `json:"checkedAt"`
			}{CheckedAt: time.Now().UTC().Format(time.RFC3339)}
			if changed {
				name = "snapshot"
				payload.HTML = rendered.String()
			}
			body, err := json.Marshal(payload)
			if err != nil {
				return
			}
			if err := control.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
				return
			}
			if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, body); err != nil {
				return
			}
			if err := control.Flush(); err != nil {
				return
			}
			if err := control.SetWriteDeadline(time.Time{}); err != nil {
				return
			}
			previous = append(previous[:0], rendered.Bytes()...)
			lastSent = time.Now()
		}
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
		}
	}
}
