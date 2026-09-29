package dhcp

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// Serve binds the socket to the configured printer interface, never the uplink.
func Serve(ctx context.Context, device string, h Handler, report func(error)) error {
	iface, err := net.InterfaceByName(device)
	if err != nil {
		return err
	}
	if iface.Flags&net.FlagLoopback != 0 {
		return errors.New("DHCP requires a dedicated Ethernet interface")
	}
	addrs, err := iface.Addrs()
	if err != nil {
		return err
	}
	found := false
	for _, a := range addrs {
		p, err := netip.ParsePrefix(a.String())
		if err == nil && p.Addr() == h.Pool.Server && p.Bits() == h.Pool.Network.Bits() {
			found = true
		}
	}
	if !found {
		return errors.New("printer interface does not have configured server address")
	}
	lc := net.ListenConfig{Control: func(_, _ string, c syscall.RawConn) error {
		var inner error
		err := c.Control(func(fd uintptr) {
			inner = unix.SetsockoptString(int(fd), unix.SOL_SOCKET, unix.SO_BINDTODEVICE, device)
			if inner == nil {
				inner = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_BROADCAST, 1)
			}
		})
		if err != nil {
			return err
		}
		return inner
	}}
	conn, err := lc.ListenPacket(ctx, "udp4", "0.0.0.0:67")
	if err != nil {
		return err
	}
	defer conn.Close()
	go func() { <-ctx.Done(); conn.Close() }()
	b := make([]byte, 1501)
	for {
		n, addr, err := conn.ReadFrom(b)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		peer, ok := addr.(*net.UDPAddr)
		if !ok || peer.Port != 68 {
			continue
		}
		reply, err := h.Handle(ctx, b[:n])
		if err != nil {
			if !errors.Is(err, ErrPacket) && report != nil {
				report(err)
			}
			continue
		}
		if reply == nil {
			continue
		}
		if err := conn.SetWriteDeadline(time.Now().Add(time.Second)); err != nil {
			return err
		}
		if _, err := conn.WriteTo(reply.Packet, &net.UDPAddr{IP: net.IP(reply.Destination.AsSlice()), Port: 68}); err != nil && report != nil {
			report(err)
		}
	}
}
