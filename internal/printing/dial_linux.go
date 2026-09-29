package printing

import (
	"context"
	"errors"
	"net"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

func Dialer(device string) func(context.Context, string) (net.Conn, error) {
	return func(ctx context.Context, address string) (net.Conn, error) {
		if device == "" {
			return nil, errors.New("printer interface is required")
		}
		d := net.Dialer{Timeout: 3 * time.Second, Control: func(_, _ string, c syscall.RawConn) error {
			var inner error
			err := c.Control(func(fd uintptr) {
				inner = unix.SetsockoptString(int(fd), unix.SOL_SOCKET, unix.SO_BINDTODEVICE, device)
			})
			if err != nil {
				return err
			}
			return inner
		}}
		return d.DialContext(ctx, "tcp4", address)
	}
}
