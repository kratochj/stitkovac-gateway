package admin

import (
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"html/template"
	"io/fs"
	"net/http"
	"sync"
	"time"

	"github.com/kratochj/stitkovac-gateway/internal/auth"
	"github.com/kratochj/stitkovac-gateway/internal/state"
)

//go:embed templates/*.html static/*
var assets embed.FS

type session struct {
	CSRF    string
	Expires time.Time
}
type Server struct {
	Store                   *state.Store
	Host, Version           string
	mu                      sync.Mutex
	sessions                map[[32]byte]session
	nextLogin               time.Time
	passwordHash, gatewayID string
}

func New(store *state.Store, host, version string) (*Server, error) {
	id, hash, err := store.Identity()
	if err != nil {
		return nil, err
	}
	return &Server{Store: store, Host: host, Version: version, sessions: map[[32]byte]session{}, passwordHash: hash, gatewayID: id}, nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	static, _ := fs.Sub(assets, "static")
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(static))))
	mux.HandleFunc("GET /login", func(w http.ResponseWriter, r *http.Request) { s.render(w, "login.html", nil) })
	mux.HandleFunc("POST /login", s.login)
	mux.HandleFunc("POST /logout", s.logout)
	mux.HandleFunc("GET /{$}", s.index)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'self'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
		if r.Host != s.Host {
			http.Error(w, "Neplatná adresa brány.", http.StatusMisdirectedRequest)
			return
		}
		if r.TLS == nil {
			http.Error(w, "Je vyžadováno HTTPS.", http.StatusBadRequest)
			return
		}
		if r.Method == http.MethodPost && r.Header.Get("Origin") != "https://"+s.Host {
			http.Error(w, "Neplatný původ požadavku.", http.StatusForbidden)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		mux.ServeHTTP(w, r)
	})
}

func (s *Server) render(w http.ResponseWriter, name string, data any) {
	t := template.Must(template.ParseFS(assets, "templates/"+name))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := t.Execute(w, data); err != nil {
		return
	}
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	if time.Now().Before(s.nextLogin) {
		s.mu.Unlock()
		w.Header().Set("Retry-After", "2")
		http.Error(w, "Vyčkejte a zkuste přihlášení znovu.", http.StatusTooManyRequests)
		return
	}
	s.nextLogin = time.Now().Add(2 * time.Second)
	s.mu.Unlock()
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Neplatný formulář.", http.StatusBadRequest)
		return
	}
	if !auth.Verify(s.passwordHash, r.Form.Get("password")) {
		w.WriteHeader(http.StatusUnauthorized)
		s.render(w, "login.html", map[string]string{"Error": "Nesprávné heslo."})
		return
	}
	token, csrf := state.ID()+state.ID(), state.ID()
	s.mu.Lock()
	for key, v := range s.sessions {
		if time.Now().After(v.Expires) {
			delete(s.sessions, key)
		}
	}
	if len(s.sessions) >= 32 {
		s.mu.Unlock()
		http.Error(w, "Příliš mnoho přihlášení.", http.StatusTooManyRequests)
		return
	}
	s.sessions[sha256.Sum256([]byte(token))] = session{CSRF: csrf, Expires: time.Now().Add(30 * time.Minute)}
	s.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: "gateway_session", Value: token, Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: 1800})
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) authenticate(r *http.Request) (session, bool) {
	c, err := r.Cookie("gateway_session")
	if err != nil {
		return session{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := sha256.Sum256([]byte(c.Value))
	v, ok := s.sessions[key]
	if ok && time.Now().Before(v.Expires) {
		return v, true
	}
	delete(s.sessions, key)
	return session{}, false
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	v, ok := s.authenticate(r)
	if !ok {
		http.Error(w, "Přihlaste se znovu.", http.StatusUnauthorized)
		return
	}
	if err := r.ParseForm(); err != nil || subtle.ConstantTimeCompare([]byte(r.Form.Get("csrf")), []byte(v.CSRF)) != 1 {
		http.Error(w, "Neplatný formulář.", http.StatusForbidden)
		return
	}
	c, _ := r.Cookie("gateway_session")
	s.mu.Lock()
	delete(s.sessions, sha256.Sum256([]byte(c.Value)))
	s.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: "gateway_session", Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: -1})
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (s *Server) index(w http.ResponseWriter, r *http.Request) {
	v, ok := s.authenticate(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	reservations, err := s.Store.Reservations(r.Context())
	if err != nil {
		http.Error(w, "Evidence zařízení není dostupná. Tisk pozastavte a zkontrolujte úložiště.", http.StatusServiceUnavailable)
		return
	}
	s.render(w, "index.html", struct {
		ID, Version, CSRF string
		Now               int64
		Devices           []state.Reservation
	}{s.gatewayID, s.Version, v.CSRF, time.Now().Unix(), reservations})
}
