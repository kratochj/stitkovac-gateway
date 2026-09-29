package admin

import (
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/kratochj/stitkovac-gateway/internal/printing"
	"github.com/kratochj/stitkovac-gateway/internal/state"
)

func formatTime(seconds int64) string {
	if seconds == 0 {
		return "Nezaznamenáno"
	}
	return time.Unix(seconds, 0).UTC().Format("02.01.2006 15:04:05")
}

func attemptLabel(value string) string {
	switch value {
	case "CLAIMED":
		return "Převzato"
	case "SENDING":
		return "Probíhá přenos"
	case "SENT":
		return "Data odeslána"
	case "FAILED":
		return "Neodesláno"
	case "UNKNOWN":
		return "Nejistý výsledek"
	case "EXPIRED":
		return "Vypršela platnost"
	default:
		return "Neznámý stav"
	}
}

func attemptMessage(value string) string {
	switch value {
	case "CLAIMED":
		return "Dokument čeká na povolení a zahájení přenosu."
	case "SENDING":
		return "Brána zahájila odesílání dokumentu tiskárně."
	case "SENT":
		return "Všechny bajty byly předány přes TCP. Stav papíru ani fyzický výtisk tím nejsou potvrzené."
	case "FAILED":
		return "Přenos selhal před odesláním dat. Brána úlohu sama neopakuje."
	case "UNKNOWN":
		return "Část nebo celý dokument mohl být odeslán. Zkontrolujte výtisk. Další tisk na tuto tiskárnu je blokovaný; brána úlohu sama neopakuje."
	case "EXPIRED":
		return "Platnost pokusu vypršela před odesláním. Dokument se neodeslal."
	default:
		return "Stav vyžaduje servisní kontrolu."
	}
}

func probeMessage(code string) string {
	switch code {
	case "reachable":
		return "TCP port 9100 je dostupný. Test neodeslal žádná tisková data; papír ani připravenost k tisku neověřuje."
	case "unreachable":
		return "TCP spojení se nepodařilo navázat do 3 sekund. Zkontrolujte napájení tiskárny, ethernetový kabel a nastavení RAW tisku."
	case "busy":
		return "Tiskárna právě zpracovává úlohu. Test zkuste po dokončení přenosu."
	case "rate_limited":
		return "Jiný test právě běží nebo byl spuštěn před chvílí. Za několik sekund to zkuste znovu."
	case "conflict":
		return "Adresa je v konfliktu. Nejdříve vyřešte DHCP rezervaci."
	case "no_lease":
		return "Tiskárna nemá aktivní DHCP lease. Zapněte ji a ověřte ethernetové připojení."
	default:
		return "Zařízení není dostupné pro test. Obnovte přehled rezervací."
	}
}

func (s *Server) probePrinter(w http.ResponseWriter, r *http.Request) {
	v, ok := s.authenticate(r)
	if !ok {
		http.Error(w, "Přihlaste se znovu.", http.StatusUnauthorized)
		return
	}
	if err := r.ParseForm(); err != nil || subtle.ConstantTimeCompare([]byte(r.Form.Get("csrf")), []byte(v.CSRF)) != 1 {
		http.Error(w, "Neplatný formulář.", http.StatusForbidden)
		return
	}
	mac, err := state.MAC(r.Form.Get("mac"))
	if err != nil {
		http.Error(w, "Neplatná MAC adresa.", http.StatusBadRequest)
		return
	}
	if s.TestPrinter == nil {
		http.Error(w, "Není nastavené síťové rozhraní tiskáren.", http.StatusConflict)
		return
	}
	s.mu.Lock()
	blocked := s.probeBusy || time.Now().Before(s.nextProbe)
	if !blocked {
		s.probeBusy = true
		s.nextProbe = time.Now().Add(2 * time.Second)
	}
	s.mu.Unlock()
	result := printing.ProbeResult{MAC: mac, Code: "rate_limited", CheckedAt: time.Now().Unix()}
	if !blocked {
		func() {
			defer func() { s.mu.Lock(); s.probeBusy = false; s.mu.Unlock() }()
			result = s.TestPrinter(r.Context(), mac)
		}()
	}
	cookie, _ := r.Cookie("gateway_session")
	key := sha256.Sum256([]byte(cookie.Value))
	s.mu.Lock()
	if current, exists := s.sessions[key]; exists {
		current.Probe = &result
		s.sessions[key] = current
	}
	s.mu.Unlock()
	http.Redirect(w, r, "/#printers", http.StatusSeeOther)
}

func (s *Server) history(w http.ResponseWriter, r *http.Request) {
	v, ok := s.authenticate(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	before := int64(0)
	if raw := r.URL.Query().Get("before"); raw != "" {
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || value <= 0 {
			http.Error(w, "Neplatná stránka historie.", http.StatusBadRequest)
			return
		}
		before = value
	}
	filter := r.URL.Query().Get("state")
	if !state.ValidAttemptState(filter) {
		http.Error(w, "Neplatný filtr úloh.", http.StatusBadRequest)
		return
	}
	page, err := s.Store.History(r.Context(), before, filter)
	if err != nil {
		http.Error(w, "Historie tisku není dostupná. Zkontrolujte datové úložiště.", http.StatusServiceUnavailable)
		return
	}
	uncertain, err := s.Store.UncertainCount(r.Context())
	if err != nil {
		http.Error(w, "Historie tisku není dostupná.", http.StatusServiceUnavailable)
		return
	}
	next := ""
	if page.NextBefore != 0 {
		next = "/jobs?" + url.Values{"before": {strconv.FormatInt(page.NextBefore, 10)}, "state": {filter}}.Encode()
	}
	s.render(w, "jobs.html", struct {
		CSRF, Filter, NextURL string
		Attempts              []state.AttemptSummary
		States                []string
		Uncertain             int
	}{v.CSRF, filter, next, page.Attempts, []string{"CLAIMED", "SENDING", "SENT", "FAILED", "UNKNOWN", "EXPIRED"}, uncertain})
}
