package admin

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"errors"
	"html/template"
	"io/fs"
	"net/http"
	"sync"
	"time"

	"github.com/kratochj/stitkovac-gateway/internal/auth"
	"github.com/kratochj/stitkovac-gateway/internal/cloud"
	"github.com/kratochj/stitkovac-gateway/internal/network"
	"github.com/kratochj/stitkovac-gateway/internal/ota"
	"github.com/kratochj/stitkovac-gateway/internal/printing"
	"github.com/kratochj/stitkovac-gateway/internal/state"
	"github.com/kratochj/stitkovac-gateway/internal/telemetry"
)

//go:embed templates/*.html static/*
var assets embed.FS

type session struct {
	CSRF    string
	Expires time.Time
	Probe   *printing.ProbeResult
}
type Server struct {
	Pool    state.Pool
	OnPanic func(telemetry.Diagnostic)
	OTA     interface{ Status() ota.Report }
	Network interface {
		Status(context.Context) (network.Status, error)
		Scan(context.Context) ([]network.AccessPoint, error)
		Apply(context.Context, network.WiFiRequest) error
		ServiceAP(context.Context, bool) error
	}
	AdditionalHost string
	Store          *state.Store
	TestPrinter    func(context.Context, string) printing.ProbeResult
	probeBusy      bool
	nextProbe      time.Time
	Cloud          interface {
		Status() cloud.Status
		Save(string, string) error
	}
	Host, Version           string
	mu                      sync.Mutex
	sessions                map[[32]byte]session
	nextLogin               time.Time
	loginBusy               bool
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
	mux.HandleFunc("POST /cloud", s.configureCloud)
	mux.HandleFunc("POST /access/password", s.changePassword)
	mux.HandleFunc("POST /printers/reservation", s.readdressPrinter)
	mux.HandleFunc("POST /jobs/resolve", s.resolveAttempt)
	mux.HandleFunc("POST /printers/check", s.probePrinter)
	mux.HandleFunc("GET /jobs", s.history)
	mux.HandleFunc("GET /network", s.networkPage)
	mux.HandleFunc("POST /network/{action}", s.configureNetwork)
	mux.HandleFunc("GET /{$}", s.index)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer telemetry.Recover(s.OnPanic, func() { http.Error(w, "Vnitřní chyba brány. Služba se obnovuje.", 500) })
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		// no-referrer makes browser form POSTs send Origin: null, including our own login.
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'self'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
		if r.Host != s.Host && (s.AdditionalHost == "" || r.Host != s.AdditionalHost) {
			http.Error(w, "Neplatná adresa brány.", http.StatusMisdirectedRequest)
			return
		}
		if r.TLS == nil {
			http.Error(w, "Je vyžadováno HTTPS.", http.StatusBadRequest)
			return
		}
		if r.Method == http.MethodPost && r.Header.Get("Origin") != "https://"+r.Host {
			http.Error(w, "Neplatný původ požadavku.", http.StatusForbidden)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
		mux.ServeHTTP(w, r)
	})
}

func (s *Server) render(w http.ResponseWriter, name string, data any) {
	t := template.Must(template.New(name).Funcs(template.FuncMap{"networkMessage": networkMessage, "networkMode": networkMode, "formatTime": formatTime, "attemptLabel": attemptLabel, "attemptMessage": attemptMessage, "probeMessage": probeMessage}).ParseFS(assets, "templates/"+name))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := t.Execute(w, data); err != nil {
		return
	}
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	if s.loginBusy || time.Now().Before(s.nextLogin) {
		s.mu.Unlock()
		w.Header().Set("Retry-After", "2")
		http.Error(w, "Vyčkejte a zkuste přihlášení znovu.", http.StatusTooManyRequests)
		return
	}
	s.nextLogin = time.Now().Add(2 * time.Second)
	s.loginBusy = true
	passwordHash := s.passwordHash
	s.mu.Unlock()
	defer func() { s.mu.Lock(); s.loginBusy = false; s.mu.Unlock() }()
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Neplatný formulář.", http.StatusBadRequest)
		return
	}
	if !auth.Verify(passwordHash, r.Form.Get("password")) {
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
	s.dashboard(w, r, "")
}

func (s *Server) configureCloud(w http.ResponseWriter, r *http.Request) {
	v, ok := s.authenticate(r)
	if !ok {
		http.Error(w, "Přihlaste se znovu.", http.StatusUnauthorized)
		return
	}
	if err := r.ParseForm(); err != nil || subtle.ConstantTimeCompare([]byte(r.Form.Get("csrf")), []byte(v.CSRF)) != 1 {
		http.Error(w, "Neplatný formulář.", http.StatusForbidden)
		return
	}
	if s.Cloud == nil {
		http.Error(w, "Není nastavené síťové rozhraní tiskáren.", http.StatusConflict)
		return
	}
	if err := s.Cloud.Save(r.Form.Get("server_url"), r.Form.Get("token")); err != nil {
		message := "Nastavení se nepodařilo uložit. Zkontrolujte datové úložiště brány."
		switch {
		case errors.Is(err, cloud.ErrAddress):
			message = "Zadejte HTTPS adresu serveru bez /api, přihlašovacích údajů a další cesty."
		case errors.Is(err, cloud.ErrToken):
			message = "Vložte celý token brány (32–4096 znaků, bez mezer)."
		case errors.Is(err, cloud.ErrNewTokenRequired):
			message = "Pro první připojení nebo změnu serveru vložte token vydaný pro tuto bránu na daném serveru."
		}
		s.dashboard(w, r, message)
		return
	}
	http.Redirect(w, r, "/?cloud=saved#cloud", http.StatusSeeOther)
}

func (s *Server) dashboard(w http.ResponseWriter, r *http.Request, cloudError string) {
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
	var connection *cloud.Status
	if s.Cloud != nil {
		status := s.Cloud.Status()
		if status.URL == "" {
			status.URL = "https://cloud.stitkovac.app"
		}
		connection = &status
	}
	uncertain, err := s.Store.UncertainCount(r.Context())
	if err != nil {
		http.Error(w, "Evidence úloh není dostupná. Zkontrolujte datové úložiště.", http.StatusServiceUnavailable)
		return
	}
	var otaStatus *ota.Report
	if s.OTA != nil {
		status := s.OTA.Status()
		otaStatus = &status
	}
	revision, acknowledged, err := s.Store.InventoryStatus(r.Context())
	if err != nil {
		http.Error(w, "Stav synchronizace není dostupný.", 503)
		return
	}
	s.render(w, "index.html", struct {
		InventoryRevision, InventoryAcknowledged int64
		OTA                                      *ota.Report
		ID, Version, CSRF                        string
		Now                                      int64
		Devices                                  []state.Reservation
		Cloud                                    *cloud.Status
		CloudError                               string
		CloudSaved                               bool
		PrinterChecks                            bool
		Probe                                    *printing.ProbeResult
		Uncertain                                int
	}{
		InventoryRevision: revision, InventoryAcknowledged: acknowledged, OTA: otaStatus, ID: s.gatewayID, Version: s.Version, CSRF: v.CSRF, Now: time.Now().Unix(), Devices: reservations,
		Cloud: connection, CloudError: cloudError, CloudSaved: r.URL.Query().Get("cloud") == "saved",
		PrinterChecks: s.TestPrinter != nil, Probe: v.Probe, Uncertain: uncertain,
	})
}
