// Package dhcp implements the gateway's bounded Ethernet-only DHCPv4 service.
// Durable reservation and lease commits precede every successful wire reply.
package dhcp

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"net"
	"net/netip"
	"time"

	"github.com/kratochj/stitkovac-gateway/internal/state"
)

var ErrPacket = errors.New("unsupported or malformed DHCP packet")
var cookie = []byte{99, 130, 83, 99}

type Repository interface {
	Reserve(context.Context, string, state.Pool, time.Time) (state.Reservation, error)
	Lease(context.Context, string, string, time.Time, time.Duration) error
	Release(context.Context, string, string, bool) error
}

type Handler struct {
	Store Repository
	Pool  state.Pool
	Now   func() time.Time
}
type Reply struct {
	Packet      []byte
	Destination netip.Addr
}

func options(b []byte) (map[byte][]byte, error) {
	m := map[byte][]byte{}
	for i := 0; i < len(b); {
		code := b[i]
		i++
		if code == 255 {
			return m, nil
		}
		if code == 0 {
			continue
		}
		if i >= len(b) {
			return nil, ErrPacket
		}
		n := int(b[i])
		i++
		if n == 0 || i+n > len(b) {
			return nil, ErrPacket
		}
		if _, exists := m[code]; exists {
			return nil, ErrPacket
		}
		m[code] = b[i : i+n]
		i += n
	}
	return nil, ErrPacket
}

func ipOption(v []byte) (netip.Addr, error) {
	if len(v) != 4 {
		return netip.Addr{}, ErrPacket
	}
	return netip.AddrFrom4([4]byte(v)), nil
}

func (h Handler) Handle(ctx context.Context, b []byte) (*Reply, error) {
	if err := h.Pool.Validate(); err != nil {
		return nil, err
	}
	if len(b) < 241 || len(b) > 1500 || b[0] != 1 || b[1] != 1 || b[2] != 6 || b[3] != 0 || !bytes.Equal(b[236:240], cookie) || !bytes.Equal(b[24:28], []byte{0, 0, 0, 0}) {
		return nil, ErrPacket
	}
	opts, err := options(b[240:])
	if err != nil {
		return nil, err
	}
	if len(opts[53]) != 1 || opts[52] != nil {
		return nil, ErrPacket
	}
	mac, err := state.MAC(net.HardwareAddr(b[28:34]).String())
	if err != nil {
		return nil, ErrPacket
	}
	ciaddr := netip.AddrFrom4([4]byte(b[12:16]))
	if v, ok := opts[54]; ok {
		server, err := ipOption(v)
		if err != nil {
			return nil, err
		}
		if server != h.Pool.Server {
			return nil, nil
		}
	}
	var requested netip.Addr
	if v, ok := opts[50]; ok {
		requested, err = ipOption(v)
		if err != nil {
			return nil, err
		}
	}
	now := time.Now()
	if h.Now != nil {
		now = h.Now()
	}
	message := opts[53][0]
	switch message {
	case 4, 7:
		if opts[54] == nil {
			return nil, ErrPacket
		}
		ip := ciaddr
		if message == 4 {
			ip = requested
		}
		if !ip.IsValid() || !h.Pool.Network.Contains(ip) {
			return nil, ErrPacket
		}
		return nil, h.Store.Release(ctx, mac, ip.String(), message == 4)
	case 1:
		if !ciaddr.IsUnspecified() {
			return nil, ErrPacket
		}
	case 3:
		if ciaddr.IsUnspecified() && !requested.IsValid() {
			return nil, ErrPacket
		}
		if !ciaddr.IsUnspecified() && requested.IsValid() {
			return nil, ErrPacket
		}
	default:
		return nil, nil
	}
	r, err := h.Store.Reserve(ctx, mac, h.Pool, now)
	if err != nil {
		return nil, err
	}
	ip, err := netip.ParseAddr(r.IP)
	if err != nil {
		return nil, err
	}
	typeCode := byte(2)
	if message == 3 {
		want := requested
		if !ciaddr.IsUnspecified() {
			want = ciaddr
		}
		if want != ip {
			return h.reply(b, opts, 6, netip.IPv4Unspecified()), nil
		}
		if err := h.Store.Lease(ctx, mac, r.IP, now, 24*time.Hour); err != nil {
			return nil, err
		}
		typeCode = 5
	}
	return h.reply(b, opts, typeCode, ip), nil
}

func (h Handler) reply(request []byte, opts map[byte][]byte, kind byte, ip netip.Addr) *Reply {
	b := make([]byte, 240, 576)
	b[0], b[1], b[2] = 2, 1, 6
	copy(b[4:8], request[4:8])
	copy(b[10:12], request[10:12])
	copy(b[28:44], request[28:44])
	copy(b[236:], cookie)
	if kind != 6 {
		copy(b[12:16], request[12:16])
		copy(b[16:20], ip.AsSlice())
	}
	appendOption := func(code byte, v []byte) { b = append(b, code, byte(len(v))); b = append(b, v...) }
	appendOption(53, []byte{kind})
	appendOption(54, h.Pool.Server.AsSlice())
	if id := opts[61]; len(id) > 0 {
		appendOption(61, id)
	}
	if kind != 6 {
		appendOption(1, net.CIDRMask(h.Pool.Network.Bits(), 32))
		for _, v := range []struct {
			code    byte
			seconds uint32
		}{{51, 86400}, {58, 43200}, {59, 75600}} {
			var x [4]byte
			binary.BigEndian.PutUint32(x[:], v.seconds)
			appendOption(v.code, x[:])
		}
	}
	b = append(b, 255)
	for len(b) < 300 {
		b = append(b, 0)
	}
	dest := netip.MustParseAddr("255.255.255.255")
	ciaddr := netip.AddrFrom4([4]byte(request[12:16]))
	if kind != 6 && !ciaddr.IsUnspecified() {
		dest = ciaddr
	}
	return &Reply{Packet: b, Destination: dest}
}
