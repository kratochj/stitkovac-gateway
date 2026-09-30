package admin

import (
	"crypto/subtle"
	"errors"
	"github.com/kratochj/stitkovac-gateway/internal/network"
	"net/http"
	"net/netip"
)

func networkMessage(code string) string {
	switch code {
	case "":
		return ""
	case "saved":
		return "Změna probíhá. Při přepnutí Wi-Fi se toto spojení může přerušit. Za 90 sekund obnovte stránku na původní síti nebo servisním AP."
	case "admin_saved":
		return "Nastavení přístupu přes Wi-Fi bylo uloženo. Vypnutí ukončí přístup přes Wi-Fi; přes kabel zůstává administrace dostupná."
	case "done":
		return "Požadavek byl zpracován. Výsledek najdete ve stavu připojení níže."
	case "busy":
		return "Právě probíhá změna sítě nebo jiné hledání. Počkejte několik sekund a obnovte stav."
	case "invalid":
		return "Zkontrolujte SSID, zabezpečení, heslo a dvoupísmenný kód země. WPA2 vyžaduje 8–63 znaků nebo 64 hexadecimálních číslic; WPA3 vyžaduje 8–63 znaků."
	case "scan":
		return "Hledání sítí není dostupné. Některé adaptéry v režimu servisního AP hledání nepodporují. Název sítě můžete zadat ručně."
	case "interrupted":
		return "Pokus přerušil restart nebo výpadek napájení. Brána obnovila předchozí nastavení."
	case "subnet_conflict":
		return "Wi-Fi používá rozsah tiskové nebo servisní sítě. Změna nebyla přijata; správce musí při instalaci zvolit jiné oddělené rozsahy."
	case "association_failed":
		return "Připojení k Wi-Fi se nezdařilo. Ověřte název sítě a heslo. Původní nastavení zůstalo uložené."
	case "ip_failed":
		return "Wi-Fi nepřidělila použitelnou IPv4 adresu a výchozí bránu. Původní nastavení zůstalo uložené."
	case "dns_failed":
		return "Přes novou Wi-Fi nefunguje DNS. Původní nastavení zůstalo uložené."
	case "tls_failed":
		return "Přes novou Wi-Fi se nepodařilo ověřit TLS spojení se serverem. Ověřte internet, čas brány a adresu serveru. Původní nastavení zůstalo uložené."
	case "activation_failed":
		return "Síťový profil se nepodařilo aktivovat. Brána jej zkusí znovu; servisní AP lze vyvolat tlačítkem."
	case "no_wifi":
		return "Wi-Fi adaptér není dostupný nebo jej nespravuje NetworkManager."
	case "storage":
		return "Nastavení nelze bezpečně uložit. Je nutná servisní kontrola úložiště."
	default:
		return "Síťová správa není dostupná. Ověřte instalaci síťového pomocníka."
	}
}
func networkMode(mode string) string {
	switch mode {
	case "ap":
		return "Servisní Wi-Fi"
	case "uplink":
		return "Wi-Fi zákazníka"
	case "applying":
		return "Ověřuji nové připojení"
	case "switching":
		return "Přepínám síť"
	case "starting":
		return "Spouštím síť"
	default:
		return "Vyžaduje kontrolu"
	}
}
func (s *Server) networkPage(w http.ResponseWriter, r *http.Request) {
	v, ok := s.authenticate(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	s.renderNetwork(w, r, v, nil, "")
}
func (s *Server) renderNetwork(w http.ResponseWriter, r *http.Request, v session, scan []network.AccessPoint, message string) {
	data := map[string]any{"CSRF": v.CSRF, "Networks": scan, "Message": message}
	if s.Network != nil {
		status, err := s.Network.Status(r.Context())
		if err == nil {
			data["Network"] = status
			if address := status.WiFiAdminAddress(); address != "" {
				data["WiFiURL"] = "https://" + address
			}
			if prefix, e := netip.ParsePrefix(status.APAddress); e == nil {
				data["APURL"] = "https://" + netip.AddrPortFrom(prefix.Addr(), 8443).String()
			}
		} else {
			data["Unavailable"] = true
		}
	} else {
		data["Unavailable"] = true
	}
	if message == "" && r.URL.Query().Get("admin_saved") == "1" {
		data["Message"] = "admin_saved"
	}
	if message == "" && r.URL.Query().Get("saved") == "1" {
		data["Message"] = "saved"
		if status, ok := data["Network"].(network.Status); ok && status.Mode != "applying" && status.Mode != "switching" {
			data["Message"] = "done"
		}
	}
	s.render(w, "network.html", data)
}
func (s *Server) configureNetwork(w http.ResponseWriter, r *http.Request) {
	v, ok := s.authenticate(r)
	if !ok {
		http.Error(w, "Přihlaste se znovu.", 401)
		return
	}
	if r.ParseForm() != nil || subtle.ConstantTimeCompare([]byte(r.Form.Get("csrf")), []byte(v.CSRF)) != 1 {
		http.Error(w, "Neplatný formulář.", 403)
		return
	}
	if s.Network == nil {
		s.renderNetwork(w, r, v, nil, "unavailable")
		return
	}
	var err error
	switch r.PathValue("action") {
	case "scan":
		var found []network.AccessPoint
		found, err = s.Network.Scan(r.Context())
		if err == nil {
			s.renderNetwork(w, r, v, found, "")
			return
		}
		if !errors.Is(err, network.ErrBusy) {
			s.renderNetwork(w, r, v, nil, "scan")
			return
		}
	case "wifi":
		target := "https://cloud.stitkovac.app"
		if s.Cloud != nil && s.Cloud.Status().URL != "" {
			target = s.Cloud.Status().URL
		}
		err = s.Network.Apply(r.Context(), network.WiFiRequest{SSID: r.Form.Get("ssid"), Password: r.Form.Get("password"), Security: r.Form.Get("security"), Hidden: r.Form.Get("hidden") == "1", Country: r.Form.Get("country"), ServerURL: target})
	case "wifi-admin":
		if r.Form.Get("enabled") != "0" && r.Form.Get("enabled") != "1" {
			err = network.ErrInvalid
		} else {
			err = s.Network.SetWiFiAdmin(r.Context(), r.Form.Get("enabled") == "1")
		}
	case "ap":
		err = s.Network.ServiceAP(r.Context(), true)
	case "uplink":
		err = s.Network.ServiceAP(r.Context(), false)
	default:
		http.NotFound(w, r)
		return
	}
	if err == nil {
		target := "/network?saved=1"
		if r.PathValue("action") == "wifi-admin" {
			target = "/network?admin_saved=1"
		}
		http.Redirect(w, r, target, 303)
		return
	}
	code := "unavailable"
	if errors.Is(err, network.ErrInvalid) {
		code = "invalid"
	} else if errors.Is(err, network.ErrBusy) {
		code = "busy"
	}
	// Never redisplay a submitted password, including after validation failure.
	s.renderNetwork(w, r, v, nil, code)
}
