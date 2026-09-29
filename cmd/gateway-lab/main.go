package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/kratochj/stitkovac-gateway/internal/lab"
	"github.com/kratochj/stitkovac-gateway/internal/platform"
)

func main() {
	if err := run(); err != nil {
		slog.Error("Lab stopped", "error", err)
		os.Exit(1)
	}
}
func readSecret(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 4096 {
		return "", errors.New("lab secret must be private")
	}
	b, err := os.ReadFile(path)
	return strings.TrimSpace(string(b)), err
}
func run() error {
	mode := flag.String("mode", "cloud", "cloud or printer")
	dir := flag.String("dir", "/data/lab", "Private lab storage")
	address := flag.String("address", "127.0.0.1:9443", "Listener address")
	lease := flag.String("lease-file", "/run/gateway-lab/lease.json", "Simulated printer DHCP lease")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	captures := filepath.Join(*dir, "prints")
	if *mode == "printer" {
		listener, err := net.Listen("tcp4", *address)
		if err != nil {
			return err
		}
		defer listener.Close()
		return lab.Printer(ctx, listener, captures)
	}
	if *mode != "cloud" {
		return errors.New("unknown lab mode")
	}
	unlock, err := platform.Lock(*dir)
	if err != nil {
		return err
	}
	defer unlock()
	token, err := readSecret(filepath.Join(*dir, "token"))
	if err != nil {
		return err
	}
	password, err := readSecret(filepath.Join(*dir, "password"))
	if err != nil {
		return err
	}
	if _, err := os.Lstat(filepath.Join(*dir, "tls.crt")); errors.Is(err, os.ErrNotExist) {
		if _, err := platform.Certificate(*dir, []net.IP{net.IPv4(127, 0, 0, 1)}); err != nil {
			return err
		}
	}
	labServer, err := lab.New(lab.Config{Dir: *dir, Captures: captures, LeaseFile: *lease, Host: *address, Token: token, Password: password})
	if err != nil {
		return err
	}
	server := &http.Server{Addr: *address, Handler: labServer.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: time.Minute}
	go func() { <-ctx.Done(); server.Close() }()
	err = server.ListenAndServeTLS(filepath.Join(*dir, "tls.crt"), filepath.Join(*dir, "tls.key"))
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
