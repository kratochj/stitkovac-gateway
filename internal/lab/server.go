package lab

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"html/template"
	"io"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/kratochj/stitkovac-gateway/internal/platform"
	"github.com/kratochj/stitkovac-gateway/internal/state"
)

//go:embed index.html live.js
var pageFiles embed.FS
var page = template.Must(template.New("index.html").Funcs(template.FuncMap{
	"stamp": func(t int64) string { return time.Unix(t, 0).UTC().Format(time.RFC3339) },
}).ParseFS(pageFiles, "index.html"))
var captureName = regexp.MustCompile(`^[0-9]{8}T[0-9]{6}-[a-f0-9]+\.(pdf|bin)$`)
var identifier = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

type Job struct {
	Attempt   state.Attempt `json:"attempt"`
	Kind      string        `json:"kind"`
	Document  []byte        `json:"document"`
	CreatedAt int64         `json:"createdAt,omitempty"`
	UpdatedAt int64         `json:"updatedAt,omitempty"`
}
type Lease struct {
	IP  string `json:"ip"`
	MAC string `json:"mac"`
}
type Config struct{ Dir, Captures, LeaseFile, Host, Token, Password string }
type Server struct {
	cfg                    Config
	mu                     sync.Mutex
	jobs                   []Job
	conn                   *websocket.Conn
	session, gateway, csrf string
	wake                   chan struct{}
}

func New(c Config) (*Server, error) {
	if err := privateDirectory(c.Dir); err != nil {
		return nil, err
	}
	if err := privateDirectory(c.Captures); err != nil {
		return nil, err
	}
	if c.Host != "127.0.0.1:9443" || len(c.Token) < 32 || len(c.Password) < 16 {
		return nil, errors.New("invalid isolated lab configuration")
	}
	s := &Server{cfg: c, csrf: state.ID(), wake: make(chan struct{}, 1)}
	b, err := os.ReadFile(filepath.Join(c.Dir, "jobs.json"))
	if err == nil {
		if len(b) > 2<<20 || json.Unmarshal(b, &s.jobs) != nil || len(s.jobs) > 100 {
			return nil, errors.New("invalid lab journal")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return s, nil
}
func (s *Server) save() error {
	b, err := json.Marshal(s.jobs)
	if err != nil {
		return err
	}
	return platform.AtomicWrite(filepath.Join(s.cfg.Dir, "jobs.json"), b, 0600)
}
func equal(a, b string) bool {
	x, y := sha256.Sum256([]byte(a)), sha256.Sum256([]byte(b))
	return subtle.ConstantTimeCompare(x[:], y[:]) == 1
}
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.index)
	mux.HandleFunc("GET /events", s.events)
	mux.HandleFunc("GET /static/live.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		b, _ := pageFiles.ReadFile("live.js")
		w.Write(b)
	})
	mux.HandleFunc("POST /print", s.print)
	mux.HandleFunc("GET /captures/{name}", s.capture)
	mux.HandleFunc("GET /api/gateway/v1/connect", s.connect)
	mux.HandleFunc("GET /api/gateway/v1/jobs", s.list)
	mux.HandleFunc("POST /api/gateway/v1/jobs/{uid}/claim", s.claim)
	mux.HandleFunc("GET /api/gateway/v1/jobs/{uid}/document", s.document)
	mux.HandleFunc("POST /api/gateway/v1/jobs/{uid}/start", s.start)
	mux.HandleFunc("POST /api/gateway/v1/jobs/{uid}/result", s.result)
	mux.HandleFunc("POST /api/gateway/v1/events", func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.Copy(io.Discard, r.Body); err != nil {
			http.Error(w, "event too large", 413)
			return
		}
		w.WriteHeader(204)
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; connect-src 'self'; style-src 'unsafe-inline'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
		if r.TLS == nil || r.Host != s.cfg.Host {
			http.Error(w, "Neplatná adresa laboratoře.", 421)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
		if strings.HasPrefix(r.URL.Path, "/api/") {
			if !equal(r.Header.Get("Authorization"), "Bearer "+s.cfg.Token) {
				http.Error(w, "unauthorized", 401)
				return
			}
			if r.URL.Path != "/api/gateway/v1/connect" && r.URL.Path != "/api/gateway/v1/events" {
				s.mu.Lock()
				valid := s.session != "" && equal(r.Header.Get("X-Gateway-Session"), s.session)
				s.mu.Unlock()
				if !valid {
					http.Error(w, "stale session", 409)
					return
				}
			}
		} else {
			user, password, ok := r.BasicAuth()
			if !ok || user != "technik" || !equal(password, s.cfg.Password) {
				w.Header().Set("WWW-Authenticate", `Basic realm="Gateway Lab", charset="UTF-8"`)
				http.Error(w, "Přihlaste se účtem technik.", 401)
				return
			}
			if r.Method == http.MethodPost && r.Header.Get("Origin") != "https://"+s.cfg.Host {
				http.Error(w, "Neplatný původ požadavku.", 403)
				return
			}
		}
		mux.ServeHTTP(w, r)
	})
}
func (s *Server) lease() (Lease, error) {
	var lease Lease
	b, err := os.ReadFile(s.cfg.LeaseFile)
	if err != nil {
		return lease, err
	}
	if len(b) > 1024 || json.Unmarshal(b, &lease) != nil {
		return lease, errors.New("invalid lease")
	}
	ip, err := netip.ParseAddr(lease.IP)
	if err != nil || !netip.MustParsePrefix("192.168.77.0/24").Contains(ip) || lease.MAC != "02:77:00:00:00:01" {
		return lease, errors.New("unexpected simulated printer")
	}
	return lease, nil
}
func (s *Server) index(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	page.Execute(w, s.snapshot())
}
func (s *Server) print(w http.ResponseWriter, r *http.Request) {
	if r.ParseForm() != nil || !equal(r.Form.Get("csrf"), s.csrf) {
		http.Error(w, "Neplatný formulář.", 403)
		return
	}
	kind := r.Form.Get("kind")
	if kind != "label" && kind != "receipt" {
		http.Error(w, "Neplatný typ dokumentu.", 400)
		return
	}
	lease, err := s.lease()
	if err != nil {
		http.Error(w, "Tiskárna ještě nemá DHCP adresu.", 409)
		return
	}
	uid := state.ID()
	doc := Document(kind, uid)
	hash := sha256.Sum256(doc)
	job := Job{Attempt: state.Attempt{JobUID: uid, AttemptID: state.ID(), MAC: lease.MAC, IP: lease.IP, Port: 9100, Digest: hex.EncodeToString(hash[:]), ExpiresAt: time.Now().Add(5 * time.Minute).Unix(), State: "PENDING"}, Kind: kind, Document: doc, CreatedAt: time.Now().Unix(), UpdatedAt: time.Now().Unix()}
	s.mu.Lock()
	if s.conn == nil || s.session == "" {
		s.mu.Unlock()
		http.Error(w, "Brána není připojená k místnímu testovacímu serveru. Tisk zadejte na serveru, ke kterému je připojená.", 409)
		return
	}
	if len(s.jobs) >= 100 {
		s.mu.Unlock()
		http.Error(w, "Laboratoř dosáhla limitu 100 úloh.", 409)
		return
	}
	s.jobs = append(s.jobs, job)
	err = s.save()
	if err != nil {
		s.jobs = s.jobs[:len(s.jobs)-1]
	}
	s.mu.Unlock()
	if err != nil {
		http.Error(w, "Nelze uložit úlohu.", 500)
		return
	}
	select {
	case s.wake <- struct{}{}:
	default:
	}
	http.Redirect(w, r, "/", 303)
}
func (s *Server) capture(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !captureName.MatchString(name) {
		http.NotFound(w, r)
		return
	}
	path := filepath.Join(s.cfg.Captures, name)
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	if strings.HasSuffix(name, ".pdf") {
		w.Header().Set("Content-Type", "application/pdf")
	} else {
		w.Header().Set("Content-Type", "application/octet-stream")
	}
	http.ServeFile(w, r, path)
}
func (s *Server) connect(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer conn.CloseNow()
	conn.SetReadLimit(16 << 10)
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	_, b, err := conn.Read(ctx)
	if err != nil {
		return
	}
	var hello struct {
		Version int    `json:"version"`
		Type    string `json:"type"`
		Gateway string `json:"gatewayId"`
	}
	if json.Unmarshal(b, &hello) != nil || hello.Version != 1 || hello.Type != "hello" || !identifier.MatchString(hello.Gateway) {
		return
	}
	session := state.ID()
	s.mu.Lock()
	previous := s.conn
	s.conn, s.session, s.gateway = conn, session, hello.Gateway
	s.mu.Unlock()
	if previous != nil {
		previous.CloseNow()
	}
	defer func() {
		s.mu.Lock()
		if s.conn == conn {
			s.conn = nil
			s.session = ""
		}
		s.mu.Unlock()
	}()
	ready, _ := json.Marshal(map[string]any{"version": 1, "type": "ready", "sessionId": session, "messageId": state.ID()})
	if err := conn.Write(ctx, websocket.MessageText, ready); err != nil {
		return
	}
	read := conn.CloseRead(r.Context())
	for {
		select {
		case <-read.Done():
			return
		case <-s.wake:
			b, _ := json.Marshal(map[string]any{"version": 1, "type": "jobs.available", "messageId": state.ID()})
			if err := conn.Write(read, websocket.MessageText, b); err != nil {
				return
			}
		}
	}
}
func (s *Server) list(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	jobs := []string{}
	for _, job := range s.jobs {
		if job.Attempt.State == "PENDING" || job.Attempt.State == "STARTED" {
			jobs = append(jobs, job.Attempt.JobUID)
		}
	}
	json.NewEncoder(w).Encode(map[string]any{"jobs": jobs, "nextCursor": ""})
}
func (s *Server) find(uid string) *Job {
	for i := range s.jobs {
		if s.jobs[i].Attempt.JobUID == uid {
			return &s.jobs[i]
		}
	}
	return nil
}
func (s *Server) claim(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	job := s.find(r.PathValue("uid"))
	if job == nil {
		http.NotFound(w, r)
		return
	}
	json.NewEncoder(w).Encode(job.Attempt)
}
func (s *Server) document(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	job := s.find(r.PathValue("uid"))
	if job == nil || job.Attempt.AttemptID != r.URL.Query().Get("attemptId") {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/pdf")
	w.Write(job.Document)
}
func (s *Server) transition(w http.ResponseWriter, r *http.Request, start bool) {
	var body struct {
		AttemptID string `json:"attemptId"`
		State     string `json:"state"`
		Reason    string `json:"reason"`
	}
	if json.NewDecoder(r.Body).Decode(&body) != nil {
		http.Error(w, "invalid result", 400)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	job := s.find(r.PathValue("uid"))
	if job == nil || body.AttemptID != job.Attempt.AttemptID {
		http.NotFound(w, r)
		return
	}
	previousState := job.Attempt.State
	previousUpdated := job.UpdatedAt
	if start {
		for _, other := range s.jobs {
			if other.Attempt.State == "UNKNOWN" {
				http.Error(w, "uncertain printer outcome", 409)
				return
			}
		}
		if job.Attempt.State != "PENDING" && job.Attempt.State != "STARTED" {
			http.Error(w, "terminal job", 409)
			return
		}
		job.Attempt.State = "STARTED"
	} else {
		if body.State != "SENT" && body.State != "FAILED" && body.State != "UNKNOWN" && body.State != "EXPIRED" {
			http.Error(w, "invalid state", 400)
			return
		}
		if job.Attempt.State != "PENDING" && job.Attempt.State != "STARTED" && job.Attempt.State != body.State {
			http.Error(w, "immutable result", 409)
			return
		}
		job.Attempt.State = body.State
	}
	if job.Attempt.State != previousState {
		job.UpdatedAt = time.Now().Unix()
	}
	if err := s.save(); err != nil {
		job.UpdatedAt = previousUpdated
		job.Attempt.State = previousState
		http.Error(w, "journal unavailable", 500)
		return
	}
	w.WriteHeader(204)
}
func (s *Server) start(w http.ResponseWriter, r *http.Request)  { s.transition(w, r, true) }
func (s *Server) result(w http.ResponseWriter, r *http.Request) { s.transition(w, r, false) }
