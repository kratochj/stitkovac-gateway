//go:build !linux

package network

import "context"

func WatchButton(context.Context, Config, func()) error { return ErrUnavailable }
