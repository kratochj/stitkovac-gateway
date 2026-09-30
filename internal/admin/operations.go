package admin

import (
	"crypto/subtle"
	"github.com/kratochj/stitkovac-gateway/internal/auth"
	"net/http"
	"time"
)

func (s *Server) serviceForm(w http.ResponseWriter, r *http.Request) bool {
	v, ok := s.authenticate(r)
	if !ok {
		http.Error(w, "Přihlaste se znovu.", http.StatusUnauthorized)
		return false
	}
	if r.ParseForm() != nil || subtle.ConstantTimeCompare([]byte(r.Form.Get("csrf")), []byte(v.CSRF)) != 1 {
		http.Error(w, "Neplatný formulář.", http.StatusForbidden)
		return false
	}
	return true
}
func (s *Server) resolveAttempt(w http.ResponseWriter, r *http.Request) {
	if !s.serviceForm(w, r) {
		return
	}
	if r.Form.Get("checked") != "yes" {
		http.Error(w, "Nejprve zkontrolujte výtisk a vyprázdněte tiskovou frontu tiskárny.", http.StatusBadRequest)
		return
	}
	if err := s.Store.Resolve(r.Context(), r.Form.Get("job"), r.Form.Get("attempt"), r.Form.Get("decision")); err != nil {
		http.Error(w, "Úlohu nelze uzavřít. Obnovte historii a ověřte její stav.", http.StatusConflict)
		return
	}
	http.Redirect(w, r, "/jobs", http.StatusSeeOther)
}
func (s *Server) readdressPrinter(w http.ResponseWriter, r *http.Request) {
	if !s.serviceForm(w, r) {
		return
	}
	if r.Form.Get("checked") != "yes" {
		http.Error(w, "Potvrďte kontrolu zařízení na tiskové síti.", http.StatusBadRequest)
		return
	}
	if err := s.Store.Readdress(r.Context(), r.Form.Get("mac"), r.Form.Get("old_ip"), r.Form.Get("ip"), s.Pool); err != nil {
		http.Error(w, "Rezervaci nelze změnit. Adresa musí být volná v DHCP poolu a úlohy tiskárny vyřešené.", http.StatusConflict)
		return
	}
	http.Redirect(w, r, "/#printers", http.StatusSeeOther)
}
func (s *Server) changePassword(w http.ResponseWriter, r *http.Request) {
	if !s.serviceForm(w, r) {
		return
	}
	// Serialize costly password hashing with login and prevent an in-flight login
	// authenticated against the old hash from issuing a new session after rotation.
	s.mu.Lock()
	if s.loginBusy || time.Now().Before(s.nextLogin) {
		s.mu.Unlock()
		http.Error(w, "Vyčkejte a zkuste změnu znovu.", 429)
		return
	}
	s.loginBusy = true
	s.nextLogin = time.Now().Add(2 * time.Second)
	old := s.passwordHash
	s.mu.Unlock()
	defer func() { s.mu.Lock(); s.loginBusy = false; s.mu.Unlock() }()
	if !auth.Verify(old, r.Form.Get("current_password")) {
		http.Error(w, "Současné heslo není správné.", http.StatusForbidden)
		return
	}
	if r.Form.Get("new_password") != r.Form.Get("confirm_password") {
		http.Error(w, "Nová hesla se neshodují.", http.StatusBadRequest)
		return
	}
	hash, err := auth.Hash(r.Form.Get("new_password"))
	if err != nil {
		http.Error(w, "Nové heslo musí mít 16–1024 bajtů.", http.StatusBadRequest)
		return
	}
	if err = s.Store.ChangePassword(r.Context(), old, hash); err != nil {
		http.Error(w, "Heslo se nepodařilo uložit.", http.StatusServiceUnavailable)
		return
	}
	s.mu.Lock()
	s.passwordHash = hash
	s.sessions = map[[32]byte]session{}
	s.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: "gateway_session", Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: -1})
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}
