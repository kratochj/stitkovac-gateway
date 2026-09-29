//go:build !linux

package printing

import (
	"context"
	"errors"
	"net"
)

func Dialer(string) func(context.Context, string) (net.Conn, error) {
	return func(context.Context, string) (net.Conn, error) {
		return nil, errors.New("production printing requires Linux interface binding")
	}
}
