package network

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/kratochj/stitkovac-gateway/internal/platform"
)

// Keyfile escaping is independent of shell quoting. No secret becomes an argv.
func keyValue(s string) string {
	r := strings.NewReplacer("\\", "\\\\", "\n", "\\n", "\r", "\\r", "\t", "\\t", " ", "\\s")
	return r.Replace(s)
}

// A byte array avoids ambiguous SSIDs such as "1;2;" in NM keyfiles.
func ssidValue(s string) string {
	var b strings.Builder
	for _, v := range []byte(s) {
		b.WriteString(strconv.Itoa(int(v)))
		b.WriteByte(';')
	}
	return b.String()
}
func profilePath(dir, id string) string { return filepath.Join(dir, "gateway-"+id+".nmconnection") }
func wifiKeyfile(cfg Config, p Profile, r WiFiRequest) []byte {
	text := fmt.Sprintf("[connection]\nid=gateway-%s\nuuid=%s\ntype=wifi\ninterface-name=%s\nautoconnect=false\n\n[wifi]\nssid=%s\nmode=infrastructure\nhidden=%t\n", p.ID, p.ID, cfg.WiFiInterface, ssidValue(r.SSID), r.Hidden)
	if r.Security != "open" {
		text += fmt.Sprintf("\n[wifi-security]\nkey-mgmt=%s\npsk=%s\npsk-flags=0\nproto=rsn;\n", r.Security, keyValue(r.Password))
	}
	text += "\n[ipv4]\nmethod=auto\nnever-default=false\nroute-metric=600\n\n[ipv6]\nmethod=disabled\n"
	return []byte(text)
}
func apKeyfile(cfg Config) []byte {
	return []byte(fmt.Sprintf("[connection]\nid=gateway-service-ap\nuuid=%s\ntype=wifi\ninterface-name=%s\nautoconnect=false\n\n[wifi]\nssid=%s\nmode=ap\nband=bg\nchannel=6\n\n[wifi-security]\nkey-mgmt=wpa-psk\npsk=%s\npsk-flags=0\nproto=rsn;\npairwise=ccmp;\ngroup=ccmp;\n\n[ipv4]\nmethod=manual\naddress1=%s\nnever-default=true\n\n[ipv6]\nmethod=disabled\n", cfg.APID, cfg.WiFiInterface, ssidValue(cfg.APSSID), keyValue(cfg.APPassword), cfg.APCIDR))
}
func privateDir(path string) error {
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return ErrStorage
	}
	return nil
}
func writeProfile(cfg Config, p Profile, r WiFiRequest) error {
	if r.Validate() != nil || !idPattern.MatchString(p.ID) || privateDir(cfg.ProfileDir) != nil {
		return ErrInvalid
	}
	if platform.AtomicWrite(profilePath(cfg.ProfileDir, p.ID), wifiKeyfile(cfg, p, r), 0600) != nil {
		return ErrStorage
	}
	return nil
}
