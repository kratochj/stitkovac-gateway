package update

import (
	"errors"
	"golang.org/x/sys/unix"
)

const freeSpaceReserve = 64 << 20

func checkSpace(path string, size int64) error {
	var stat unix.Statfs_t
	if err := unix.Statfs(path, &stat); err != nil {
		return err
	}
	if stat.Bsize <= 0 || uint64(stat.Bavail) < (uint64(size)+freeSpaceReserve+uint64(stat.Bsize)-1)/uint64(stat.Bsize) {
		return errors.New("insufficient release storage space")
	}
	return nil
}
