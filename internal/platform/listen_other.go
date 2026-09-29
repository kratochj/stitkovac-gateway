//go:build !linux

package platform

import (
	"context"
	"net"
)

func ListenFreebind(ctx context.Context, address string) (net.Listener, error) {
	return (&net.ListenConfig{}).Listen(ctx, "tcp4", address)
}
