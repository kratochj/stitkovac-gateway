package printing

import (
	"context"
	"net"
	"net/netip"
	"time"

	"github.com/kratochj/stitkovac-gateway/internal/state"
)

type ProbeResult struct {
	MAC, IP, Code string
	CheckedAt     int64
}

// Probe opens and closes port 9100 without sending bytes. It cannot choose a
// remote IP/port and uses the same bound dialer and endpoint lock as print jobs.
func (w *Worker) Probe(parent context.Context, rawMAC string) ProbeResult {
	result := ProbeResult{Code: "unavailable", CheckedAt: time.Now().Unix()}
	mac, err := state.MAC(rawMAC)
	if err != nil {
		result.Code = "invalid"
		return result
	}
	result.MAC = mac
	ctx, cancel := context.WithTimeout(parent, 3*time.Second)
	defer cancel()
	reservation, err := w.Store.Reservation(ctx, mac)
	if err != nil {
		return result
	}
	ip, err := netip.ParseAddr(reservation.IP)
	if err != nil || !ip.Is4() || !ip.IsPrivate() || !w.Pool.Network.Contains(ip) || ip == w.Pool.Server || ip.Compare(w.Pool.First) < 0 || ip.Compare(w.Pool.Last) > 0 {
		return result
	}
	result.IP = ip.String()
	if reservation.Declined {
		result.Code = "conflict"
		return result
	}
	if reservation.LeaseUntil <= time.Now().Unix() {
		result.Code = "no_lease"
		return result
	}
	address := net.JoinHostPort(result.IP, "9100")
	gate := w.endpointGate(address)
	select {
	case gate <- struct{}{}:
		defer func() { <-gate }()
	default:
		result.Code = "busy"
		return result
	}
	conn, err := w.Dial(ctx, address)
	if err != nil {
		result.Code = "unreachable"
		return result
	}
	conn.Close()
	result.Code = "reachable"
	return result
}
