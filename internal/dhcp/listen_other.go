//go:build !linux

package dhcp

import (
	"context"
	"errors"
)

func Serve(context.Context, string, Handler, func(error), func()) error {
	return errors.New("DHCP socket binding is supported only on Linux")
}
