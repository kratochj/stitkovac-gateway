//go:build linux

package platform

import (
	"context"
	"golang.org/x/sys/unix"
	"net"
	"syscall"
)

// ListenFreebind binds only the configured service address, even while its
// interface is inactive. It never exposes the admin server on the uplink IP.
func ListenFreebind(ctx context.Context, address string) (net.Listener, error) {
	cfg := net.ListenConfig{Control: func(_, _ string, c syscall.RawConn) error {
		var e error
		err := c.Control(func(fd uintptr) { e = unix.SetsockoptInt(int(fd), unix.SOL_IP, unix.IP_FREEBIND, 1) })
		if err != nil {
			return err
		}
		return e
	}}
	return cfg.Listen(ctx, "tcp4", address)
}
