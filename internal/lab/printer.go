package lab

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/kratochj/stitkovac-gateway/internal/platform"
	"github.com/kratochj/stitkovac-gateway/internal/state"
)

// Printer receives the exact TCP stream and retains at most 50 bounded captures.
// Run it only inside the isolated printer network namespace.
func Printer(ctx context.Context, listener net.Listener, dir string) error {
	go func() { <-ctx.Done(); listener.Close() }()
	for {
		conn, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		conn.SetReadDeadline(time.Now().Add(20 * time.Second))
		b, err := io.ReadAll(io.LimitReader(conn, (8<<20)+1))
		conn.Close()
		if err != nil || len(b) == 0 || len(b) > 8<<20 {
			continue
		}
		ext := "bin"
		if bytes.HasPrefix(b, []byte("%PDF-")) {
			ext = "pdf"
		}
		name := fmt.Sprintf("%s-%s.%s", time.Now().UTC().Format("20060102T150405"), state.ID(), ext)
		if err := platform.AtomicWrite(filepath.Join(dir, name), b, 0600); err != nil {
			return err
		}
		files, err := Captures(dir)
		if err != nil {
			return err
		}
		for len(files) > 50 {
			if err := os.Remove(filepath.Join(dir, files[len(files)-1])); err != nil {
				return err
			}
			files = files[:len(files)-1]
		}
	}
}

func Captures(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, entry := range entries {
		if !entry.IsDir() && captureName.MatchString(entry.Name()) {
			files = append(files, entry.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(files)))
	return files, nil
}

func privateDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return errors.New("lab storage must be a private real directory")
	}
	return nil
}
