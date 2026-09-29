package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/kratochj/stitkovac-gateway/internal/admin"
	"github.com/kratochj/stitkovac-gateway/internal/auth"
	"github.com/kratochj/stitkovac-gateway/internal/cloud"
	"github.com/kratochj/stitkovac-gateway/internal/dhcp"
	"github.com/kratochj/stitkovac-gateway/internal/platform"
	"github.com/kratochj/stitkovac-gateway/internal/printing"
	"github.com/kratochj/stitkovac-gateway/internal/state"
)

var version = "dev"

func main() {
	if err := run(os.Args[1:]); err != nil {
		slog.Error("Gateway stopped", "error", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: gateway init|serve|version")
	}
	if args[0] == "version" {
		fmt.Println(version)
		return nil
	}
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	dir := flags.String("data-dir", "/data/gateway", "Persistent state directory")
	passwordFile := flags.String("password-file", "", "Read provisioning password from a private file (init only)")
	adminAddress := flags.String("admin-address", "127.0.0.1:8443", "Explicit IPv4 address and port for HTTPS")
	printerAddress := flags.String("printer-address", "192.168.77.1/24", "Gateway address on printer interface")
	first := flags.String("pool-first", "192.168.77.50", "First DHCP address")
	last := flags.String("pool-last", "192.168.77.199", "Last DHCP address")
	device := flags.String("dhcp-interface", "", "Dedicated Linux printer interface; empty disables DHCP")
	cloudURL := flags.String("cloud-url", "", "HTTPS origin implementing gateway API v1; empty disables cloud")
	tokenFile := flags.String("cloud-token-file", "", "Private file containing the gateway token")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	listen, err := netip.ParseAddrPort(*adminAddress)
	if err != nil || !listen.Addr().Is4() || listen.Addr().IsUnspecified() || listen.Port() == 0 {
		return errors.New("admin address must be an explicit IPv4 address and port")
	}
	network, err := netip.ParsePrefix(*printerAddress)
	if err != nil {
		return err
	}
	ipFirst, err := netip.ParseAddr(*first)
	if err != nil {
		return err
	}
	ipLast, err := netip.ParseAddr(*last)
	if err != nil {
		return err
	}
	pool := state.Pool{Network: network.Masked(), Server: network.Addr(), First: ipFirst, Last: ipLast}
	if err := pool.Validate(); err != nil {
		return err
	}
	switch args[0] {
	case "init":
		if *passwordFile == "" {
			return errors.New("--password-file is required")
		}
		if _, err := os.Lstat(filepath.Join(*dir, "gateway.db")); !errors.Is(err, os.ErrNotExist) {
			return errors.New("state already exists or cannot be inspected")
		}
		f, err := os.Open(*passwordFile)
		if err != nil {
			return err
		}
		defer f.Close()
		info, err := f.Stat()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
			return errors.New("password file must be private (0600)")
		}
		b, err := io.ReadAll(io.LimitReader(f, 1026))
		if err != nil {
			return err
		}
		password := strings.TrimRight(string(b), "\r\n")
		hash, err := auth.Hash(password)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(*dir, 0700); err != nil {
			return err
		}
		unlock, err := platform.Lock(*dir)
		if err != nil {
			return err
		}
		defer unlock()
		if _, err := os.Lstat(filepath.Join(*dir, "gateway.db")); !errors.Is(err, os.ErrNotExist) {
			return errors.New("state already exists or cannot be inspected")
		}
		fingerprint, err := platform.Certificate(*dir, []net.IP{net.IP(listen.Addr().AsSlice()), net.IP(pool.Server.AsSlice()), net.IPv4(127, 0, 0, 1)})
		if err != nil {
			return err
		}
		s, err := state.Initialize(*dir, hash)
		if err != nil {
			return err
		}
		defer s.Close()
		id, _, err := s.Identity()
		if err != nil {
			return err
		}
		fmt.Printf("Gateway ID: %s\nTLS SHA-256: %s\n", id, fingerprint)
		return nil
	case "serve":
		if !listen.Addr().IsLoopback() && listen.Addr() != pool.Server {
			return errors.New("admin listener must bind to loopback or the printer interface address")
		}
		unlock, err := platform.Lock(*dir)
		if err != nil {
			return err
		}
		defer unlock()
		s, err := state.Open(*dir)
		if err != nil {
			return err
		}
		defer s.Close()
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		if err := s.Recover(ctx, time.Now()); err != nil {
			return err
		}
		var cloudClient *cloud.Client
		if *cloudURL != "" || *tokenFile != "" {
			if *device == "" || *cloudURL == "" || *tokenFile == "" {
				return errors.New("cloud requires URL, token file and dedicated printer interface")
			}
			token, err := readToken(*tokenFile)
			if err != nil {
				return err
			}
			id, _, err := s.Identity()
			if err != nil {
				return err
			}
			worker := &printing.Worker{Store: s, Pool: pool, Dial: printing.Dialer(*device)}
			cloudClient, err = cloud.New(*cloudURL, token, id, version, worker, nil, nil)
			if err != nil {
				return err
			}
		}
		web, err := admin.New(s, *adminAddress, version)
		if err != nil {
			return err
		}
		server := &http.Server{Addr: *adminAddress, Handler: web.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16384, TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12}}
		result := make(chan error, 2)
		go func() {
			result <- server.ListenAndServeTLS(filepath.Join(*dir, "tls.crt"), filepath.Join(*dir, "tls.key"))
		}()
		if *device != "" {
			go func() {
				result <- dhcp.Serve(ctx, *device, dhcp.Handler{Store: s, Pool: pool}, func(error) { slog.Warn("DHCP request failed; no address acknowledged") })
			}()
		}
		cloudStopped := make(chan struct{})
		if cloudClient != nil {
			go func() { defer close(cloudStopped); cloudClient.Run(ctx) }()
		} else {
			close(cloudStopped)
		}
		slog.Info("Gateway services started", "version", version, "dhcpEnabled", *device != "")
		select {
		case err = <-result:
			stop()
		case <-ctx.Done():
		}
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		server.Shutdown(shutdown)
		<-cloudStopped
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	default:
		return errors.New("unknown command")
	}
}

func readToken(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return "", errors.New("token file must be private (0600)")
	}
	b, err := io.ReadAll(io.LimitReader(f, 4098))
	if err != nil {
		return "", err
	}
	token := strings.TrimSpace(string(b))
	if len(token) < 32 || len(token) > 4096 || strings.ContainsAny(token, "\r\n\t ") {
		return "", errors.New("invalid gateway token")
	}
	return token, nil
}
