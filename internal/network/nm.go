package network

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/kratochj/stitkovac-gateway/internal/platform"
)

// NM only executes fixed programs. Neither passwords nor keyfile contents are
// passed through argv, command output, error messages or logs.
type NM struct{ Config Config }
type cappedOutput struct{ b []byte }

func (w *cappedOutput) Write(p []byte) (int, error) {
	n := len(p)
	if len(w.b)+n > 65536 {
		return 0, errors.New("output limit")
	}
	w.b = append(w.b, p...)
	return n, nil
}
func command(ctx context.Context, program string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, program, args...)
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LC_ALL=C"}
	cmd.WaitDelay = time.Second
	out := &cappedOutput{}
	cmd.Stdout = out
	if cmd.Run() != nil {
		return "", ErrUnavailable
	}
	return strings.TrimSpace(string(out.b)), nil
}
func (n *NM) nm(ctx context.Context, args ...string) (string, error) {
	return command(ctx, "/usr/bin/nmcli", append([]string{"--wait", "20"}, args...)...)
}
func (n *NM) Available(ctx context.Context) bool {
	value, err := n.nm(ctx, "-g", "GENERAL.TYPE", "device", "show", n.Config.WiFiInterface)
	return err == nil && value == "wifi"
}
func fields(s string) []string {
	var result []string
	var field strings.Builder
	escape := false
	for _, r := range s {
		if escape {
			field.WriteRune(r)
			escape = false
		} else if r == '\\' {
			escape = true
		} else if r == ':' {
			result = append(result, field.String())
			field.Reset()
		} else {
			field.WriteRune(r)
		}
	}
	if escape {
		field.WriteRune('\\')
	}
	return append(result, field.String())
}
func parseScan(s string) []AccessPoint {
	result := []AccessPoint{}
	seen := map[string]bool{}
	for _, line := range strings.Split(s, "\n") {
		p := fields(line)
		if len(p) != 3 || !validSSID(p[0]) || seen[p[0]] {
			continue
		}
		signal, err := strconv.Atoi(p[1])
		if err != nil || signal < 0 || signal > 100 {
			continue
		}
		seen[p[0]] = true
		result = append(result, AccessPoint{SSID: p[0], Signal: signal, Security: p[2]})
		if len(result) == 100 {
			break
		}
	}
	return result
}
func (n *NM) Scan(ctx context.Context) ([]AccessPoint, error) {
	s, err := n.nm(ctx, "--terse", "--escape", "yes", "--fields", "SSID,SIGNAL,SECURITY", "device", "wifi", "list", "ifname", n.Config.WiFiInterface, "--rescan", "yes")
	if err != nil {
		return nil, err
	}
	return parseScan(s), nil
}
func (n *NM) Activate(ctx context.Context, p Profile) error {
	if !idPattern.MatchString(p.ID) || !countryPattern.MatchString(p.Country) {
		return ErrInvalid
	}
	if _, err := command(ctx, "/usr/sbin/iw", "reg", "set", p.Country); err != nil {
		return err
	}
	if _, err := n.nm(ctx, "connection", "load", profilePath(n.Config.ProfileDir, p.ID)); err != nil {
		return err
	}
	_, err := n.nm(ctx, "connection", "up", "uuid", p.ID, "ifname", n.Config.WiFiInterface)
	return err
}
func (n *NM) StartAP(ctx context.Context, country string) error {
	if platform.AtomicWrite(profilePath(n.Config.ProfileDir, n.Config.APID), apKeyfile(n.Config), 0600) != nil {
		return ErrStorage
	}
	if err := n.Activate(ctx, Profile{ID: n.Config.APID, Country: country}); err != nil {
		return err
	}
	if _, err := command(ctx, "/usr/bin/systemctl", "start", "stitkovac-gateway-ap-dhcp.service"); err != nil {
		return err
	}
	// Enable inbound AP traffic only after the radio is actually in AP mode.
	// Address matching alone would expose the web during an overlapping DHCP trial.
	_, err := command(ctx, "/usr/sbin/nft", "add", "element", "inet", "stitkovac_gateway", "service_ap_ifaces", "{", strconv.Quote(n.Config.WiFiInterface), "}")
	return err
}
func (n *NM) StopAP(ctx context.Context) error {
	if _, err := command(ctx, "/usr/sbin/nft", "flush", "set", "inet", "stitkovac_gateway", "service_ap_ifaces"); err != nil {
		return err
	}
	_, err := command(ctx, "/usr/bin/systemctl", "stop", "stitkovac-gateway-ap-dhcp.service")
	return err
}
func parseLink(s string) Link {
	l := Link{}
	for _, line := range strings.Split(s, "\n") {
		p := fields(line)
		if len(p) != 2 {
			continue
		}
		key, value := p[0], p[1]
		switch {
		case key == "GENERAL.STATE":
			l.Connected = strings.HasPrefix(value, "100")
		case key == "GENERAL.CON-UUID":
			l.ActiveID = value
		case strings.HasPrefix(key, "IP4.ADDRESS[") && l.Address == "":
			l.Address = value
		case key == "IP4.GATEWAY":
			l.Gateway = value
		case strings.HasPrefix(key, "IP4.DNS["):
			if ip, err := netip.ParseAddr(value); err == nil && ip.Is4() {
				l.DNS = append(l.DNS, value)
			}
		}
	}
	return l
}
func (n *NM) Link(ctx context.Context) (Link, error) {
	s, err := n.nm(ctx, "--terse", "--escape", "yes", "--fields", "GENERAL.STATE,GENERAL.CON-UUID,IP4.ADDRESS,IP4.GATEWAY,IP4.DNS", "device", "show", n.Config.WiFiInterface)
	if err != nil {
		return Link{}, err
	}
	return parseLink(s), nil
}
func (n *NM) Check(ctx context.Context, p Profile, target string) string {
	code := "ip_failed"
	for ctx.Err() == nil {
		attempt, cancel := context.WithTimeout(ctx, 6*time.Second)
		l, err := n.Link(attempt)
		if err == nil && l.Connected && l.ActiveID == p.ID {
			code = n.checkLink(attempt, l, target)
		}
		cancel()
		if code == "" || code == "subnet_conflict" {
			return code
		}
		select {
		case <-ctx.Done():
			return code
		case <-time.After(time.Second):
		}
	}
	return code
}
func (n *NM) checkLink(ctx context.Context, l Link, target string) string {
	prefix, err := netip.ParsePrefix(l.Address)
	if err != nil || !prefix.Addr().Is4() || l.Gateway == "" {
		return "ip_failed"
	}
	for _, private := range []string{n.Config.PrinterCIDR, n.Config.APCIDR} {
		p, _ := netip.ParsePrefix(private)
		if prefix.Overlaps(p) {
			return "subnet_conflict"
		}
	}
	if len(l.DNS) == 0 {
		return "dns_failed"
	}
	u, err := url.Parse(target)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" {
		return "tls_failed"
	}
	dial := boundDialer(n.Config.WiFiInterface)
	resolver := &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
		return dial.DialContext(ctx, network, net.JoinHostPort(l.DNS[0], "53"))
	}}
	addresses, err := resolver.LookupIP(ctx, "ip4", u.Hostname())
	if err != nil || len(addresses) == 0 {
		return "dns_failed"
	}
	port := u.Port()
	if port == "" {
		port = "443"
	}
	for _, ip := range addresses {
		conn, e := dial.DialContext(ctx, "tcp4", net.JoinHostPort(ip.String(), port))
		if e != nil {
			continue
		}
		secured := tls.Client(conn, &tls.Config{ServerName: u.Hostname(), MinVersion: tls.VersionTLS12})
		e = secured.HandshakeContext(ctx)
		_ = secured.Close()
		if e == nil {
			return ""
		}
	}
	return "tls_failed"
}
func (n *NM) Cleanup(ctx context.Context, keep []string) error {
	files, err := filepath.Glob(filepath.Join(n.Config.ProfileDir, "gateway-*.nmconnection"))
	if err != nil {
		return err
	}
	allowed := map[string]bool{}
	for _, id := range keep {
		allowed[profilePath(n.Config.ProfileDir, id)] = true
	}
	changed := false
	for _, path := range files {
		if allowed[path] {
			continue
		}
		id := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(path), "gateway-"), ".nmconnection")
		if !idPattern.MatchString(id) {
			continue
		}
		if _, err := n.nm(ctx, "connection", "delete", "uuid", id); err != nil {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return ErrStorage
			}
		}
		changed = true
	}
	if changed {
		dir, err := os.Open(n.Config.ProfileDir)
		if err != nil {
			return ErrStorage
		}
		defer dir.Close()
		if dir.Sync() != nil {
			return ErrStorage
		}
	}
	return nil
}

// Summary contains no credentials and is safe for an installation self-check.
func (n *NM) Summary() string { return fmt.Sprintf("NetworkManager on %s", n.Config.WiFiInterface) }
