//go:build linux

package network

import (
	"context"
	"golang.org/x/sys/unix"
	"time"
	"unsafe"
)

func gpioIoctl(fd int, request uintptr, data unsafe.Pointer) error {
	_, _, err := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), request, uintptr(data))
	if err != 0 {
		return err
	}
	return nil
}
func WatchButton(ctx context.Context, cfg Config, press func()) error {
	if cfg.GPIOChip == "" {
		return nil
	}
	if cfg.Validate() != nil {
		return ErrInvalid
	}
	fd, err := unix.Open(cfg.GPIOChip, unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		return ErrUnavailable
	}
	defer unix.Close(fd)
	req := unix.GPIOV2LineRequest{Num_lines: 1}
	req.Offsets[0] = cfg.GPIOLine
	copy(req.Consumer[:], "stitkovac-service-ap")
	// Stable GPIO v2 UAPI enum values (x/sys exports the structs, not these enums).
	const input, activeLow, pullUp = uint64(1 << 2), uint64(1 << 1), uint64(1 << 8)
	req.Config.Flags = input | activeLow | pullUp
	if gpioIoctl(fd, unix.GPIO_V2_GET_LINE_IOCTL, unsafe.Pointer(&req)) != nil {
		return ErrUnavailable
	}
	defer unix.Close(int(req.Fd))
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	hold := Hold{}
	for {
		select {
		case <-ctx.Done():
			return nil
		case now := <-ticker.C:
			value := unix.GPIOV2LineValues{Mask: 1}
			if gpioIoctl(int(req.Fd), unix.GPIO_V2_LINE_GET_VALUES_IOCTL, unsafe.Pointer(&value)) != nil {
				return ErrUnavailable
			}
			if hold.Update(value.Bits&1 != 0, now) {
				press()
			}
		}
	}
}
