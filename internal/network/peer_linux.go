//go:build linux

package network

import (
	"golang.org/x/sys/unix"
	"net"
)

func peerUID(conn net.Conn) (uint32, error) {
	socket, ok := conn.(*net.UnixConn)
	if !ok {
		return 0, ErrUnavailable
	}
	raw, err := socket.SyscallConn()
	if err != nil {
		return 0, err
	}
	var uid uint32
	var peerErr error
	err = raw.Control(func(fd uintptr) {
		cred, e := unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
		peerErr = e
		if e == nil {
			uid = cred.Uid
		}
	})
	if err != nil {
		return 0, err
	}
	return uid, peerErr
}
