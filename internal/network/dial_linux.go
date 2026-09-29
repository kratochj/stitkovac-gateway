//go:build linux

package network

import (
	"golang.org/x/sys/unix"
	"net"
	"syscall"
	"time"
)

func boundDialer(device string) *net.Dialer {
	return &net.Dialer{Timeout: 5 * time.Second, Control: func(_, _ string, c syscall.RawConn) error {
		var sockErr error
		err := c.Control(func(fd uintptr) {
			sockErr = unix.SetsockoptString(int(fd), unix.SOL_SOCKET, unix.SO_BINDTODEVICE, device)
		})
		if err != nil {
			return err
		}
		return sockErr
	}}
}
