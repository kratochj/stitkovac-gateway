package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/kratochj/stitkovac-gateway/internal/network"
	"github.com/kratochj/stitkovac-gateway/internal/platform"
)

func main() {
	if run() != nil {
		slog.Error("Network helper stopped; inspect configuration and service permissions")
		os.Exit(1)
	}
}
func run() error {
	config := flag.String("config", "/etc/stitkovac-gateway/network.json", "Root-owned network configuration")
	socket := flag.String("socket", network.SocketPath, "Local control socket")
	check := flag.Bool("check", false, "Validate configuration without changing networking")
	flag.Parse()
	if flag.NArg() != 0 {
		return network.ErrInvalid
	}
	info, err := os.Lstat(*config)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 16384 {
		return network.ErrInvalid
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); !ok || stat.Uid != 0 {
		return network.ErrInvalid
	}
	f, err := os.Open(*config)
	if err != nil {
		return err
	}
	defer f.Close()
	var cfg network.Config
	decoder := json.NewDecoder(io.LimitReader(f, 16385))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&cfg) != nil || decoder.Decode(&struct{}{}) != io.EOF || cfg.Validate() != nil {
		return network.ErrInvalid
	}
	if *check {
		return nil
	}
	if os.Geteuid() != 0 {
		return errors.New("root required")
	}
	unlock, err := platform.Lock(cfg.DataDir)
	if err != nil {
		return err
	}
	defer unlock()
	var backend network.Backend = &network.NM{Config: cfg}
	if cfg.Simulation {
		backend = &network.Simulated{Config: cfg}
	}
	controller, err := network.NewController(cfg, backend)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	done := make(chan struct{})
	go func() { defer close(done); controller.Run(ctx) }()
	defer func() { stop(); <-done }()
	if cfg.GPIOChip != "" {
		go func() {
			if network.WatchButton(ctx, cfg, func() { _ = controller.ServiceAP(true) }) != nil {
				controller.DisableButton()
				slog.Error("Service button unavailable")
			}
		}()
	}
	return controller.Serve(ctx, *socket)
}
