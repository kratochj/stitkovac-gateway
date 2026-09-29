// Package network defines the bounded protocol between the unprivileged web and
// the root network controller. Passwords exist only in requests and NM keyfiles.
package network

import (
	"errors"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

var ErrInvalid = errors.New("invalid")
var ErrBusy = errors.New("busy")
var ErrUnavailable = errors.New("unavailable")
var ErrStorage = errors.New("storage")
var ifacePattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,15}$`)
var countryPattern = regexp.MustCompile(`^[A-Z]{2}$`)
var idPattern = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)

type Config struct {
	DataDir          string `json:"dataDir"`
	ProfileDir       string `json:"profileDir"`
	WiFiInterface    string `json:"wifiInterface"`
	PrinterInterface string `json:"printerInterface"`
	PrinterCIDR      string `json:"printerCIDR"`
	APCIDR           string `json:"apCIDR"`
	APSSID           string `json:"apSSID"`
	APPassword       string `json:"apPassword"`
	Country          string `json:"country"`
	APID             string `json:"apID"`
	AgentUID         uint32 `json:"agentUID"`
	AgentGID         int    `json:"agentGID"`
	GPIOChip         string `json:"gpioChip"`
	GPIOLine         uint32 `json:"gpioLine"`
	Simulation       bool   `json:"simulation"`
}

func (c Config) Validate() error {
	if !ifacePattern.MatchString(c.WiFiInterface) || !ifacePattern.MatchString(c.PrinterInterface) || c.WiFiInterface == c.PrinterInterface || !idPattern.MatchString(c.APID) || !countryPattern.MatchString(c.Country) || !validSSID(c.APSSID) || !validPassword(c.APPassword, "wpa-psk") || c.AgentUID == 0 || c.AgentGID < 0 {
		return ErrInvalid
	}
	for _, path := range []string{c.DataDir, c.ProfileDir} {
		if !strings.HasPrefix(path, "/") || strings.ContainsAny(path, "\r\n\x00") {
			return ErrInvalid
		}
	}
	printer, e1 := netip.ParsePrefix(c.PrinterCIDR)
	ap, e2 := netip.ParsePrefix(c.APCIDR)
	if e1 != nil || e2 != nil || !printer.Addr().Is4() || !ap.Addr().Is4() || !printer.Addr().IsPrivate() || !ap.Addr().IsPrivate() || printer.Bits() < 16 || printer.Bits() > 29 || ap.Bits() != 24 || printer.Overlaps(ap) {
		return ErrInvalid
	}
	for _, p := range []netip.Prefix{printer, ap} {
		host := p.Addr().As4()
		base := p.Masked().Addr().As4()
		size := uint32(1) << uint32(32-p.Bits())
		value := uint32(host[0])<<24 | uint32(host[1])<<16 | uint32(host[2])<<8 | uint32(host[3])
		start := uint32(base[0])<<24 | uint32(base[1])<<16 | uint32(base[2])<<8 | uint32(base[3])
		if value == start || value == start+size-1 {
			return ErrInvalid
		}
	}
	if c.GPIOChip != "" && (!regexp.MustCompile(`^/dev/gpiochip[0-9]+$`).MatchString(c.GPIOChip) || c.GPIOLine > 1023) {
		return ErrInvalid
	}
	return nil
}

type WiFiRequest struct {
	SSID      string `json:"ssid"`
	Password  string `json:"password"`
	Security  string `json:"security"`
	Hidden    bool   `json:"hidden"`
	Country   string `json:"country"`
	ServerURL string `json:"serverURL"`
}

func validSSID(s string) bool {
	if len(s) < 1 || len(s) > 32 || !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
func validPassword(s, security string) bool {
	if security == "open" {
		return s == ""
	}
	if len(s) == 64 && security == "wpa-psk" {
		for _, c := range s {
			if !strings.ContainsRune("0123456789abcdefABCDEF", c) {
				return false
			}
		}
		return true
	}
	if len(s) < 8 || len(s) > 63 {
		return false
	}
	for _, c := range s {
		if c < 32 || c > 126 {
			return false
		}
	}
	return true
}
func (r WiFiRequest) Validate() error {
	if !validSSID(r.SSID) || !countryPattern.MatchString(r.Country) || (r.Security != "wpa-psk" && r.Security != "sae" && r.Security != "open") || !validPassword(r.Password, r.Security) {
		return ErrInvalid
	}
	u, err := url.Parse(r.ServerURL)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || (u.Path != "" && u.Path != "/") || len(r.ServerURL) > 2048 {
		return ErrInvalid
	}
	return nil
}

type Profile struct {
	ID      string `json:"id"`
	SSID    string `json:"ssid"`
	Country string `json:"country"`
}
type AccessPoint struct {
	SSID     string `json:"ssid"`
	Signal   int    `json:"signal"`
	Security string `json:"security"`
}
type Link struct {
	ActiveID  string   `json:"activeID"`
	Address   string   `json:"address"`
	Gateway   string   `json:"gateway"`
	DNS       []string `json:"dns"`
	Connected bool     `json:"connected"`
}
type Status struct {
	Available     bool   `json:"available"`
	Simulation    bool   `json:"simulation"`
	Mode          string `json:"mode"`
	Phase         string `json:"phase"`
	Error         string `json:"error"`
	SSID          string `json:"ssid"`
	Country       string `json:"country"`
	APSSID        string `json:"apSSID"`
	APAddress     string `json:"apAddress"`
	APUntil       int64  `json:"apUntil"`
	PrinterCIDR   string `json:"printerCIDR"`
	WiFiInterface string `json:"wifiInterface"`
	GPIO          bool   `json:"gpio"`
	Link          Link   `json:"link"`
}
