//go:build !linux

package network

import (
	"net"
	"syscall"
)

func boundDialer(_ string) *net.Dialer {
	return &net.Dialer{Control: func(string, string, syscall.RawConn) error { return ErrUnavailable }}
}
