//go:build !linux

package network

import "net"

func peerUID(net.Conn) (uint32, error) { return 0, ErrUnavailable }
