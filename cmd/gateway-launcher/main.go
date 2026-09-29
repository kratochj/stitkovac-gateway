package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/kratochj/stitkovac-gateway/internal/launcher"
	"github.com/kratochj/stitkovac-gateway/internal/update"
)

func main() {
	root := flag.String("releases", "/data/gateway-releases", "Persistent release directory")
	keysPath := flag.String("keys", "/etc/stitkovac-gateway/release-keys.json", "Read-only public trust anchors")
	timeout := flag.Duration("ready-timeout", 30*time.Second, "Local startup health deadline")
	flag.Parse()
	keys, err := update.ReadKeys(*keysPath)
	if err == nil {
		var store *update.Store
		store, err = update.Open(*root, keys, runtime.GOOS+"-"+runtime.GOARCH)
		if err == nil {
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			err = launcher.Run(ctx, store, launcher.Options{Root: *root, Args: append([]string{"serve"}, flag.Args()...), ReadyTimeout: *timeout, StopTimeout: 20 * time.Second, Stdout: os.Stdout, Stderr: os.Stderr})
		}
	}
	if err != nil {
		slog.Error("Gateway launcher stopped", "error", err)
		os.Exit(1)
	}
}
