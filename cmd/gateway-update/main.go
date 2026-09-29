// gateway-update is an offline service tool. Cloud-triggered activation is not
// enabled until print draining and authenticated fleet commands are integrated.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime"
	"syscall"

	"github.com/kratochj/stitkovac-gateway/internal/update"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: gateway-update download|stage|initialize|request|status [flags]")
	}
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	root := flags.String("releases", "/data/gateway-releases", "Persistent release directory")
	keysPath := flags.String("keys", "/etc/stitkovac-gateway/release-keys.json", "Read-only public trust anchors")
	repository := flags.String("repository", "", "Provisioned HTTPS release origin")
	manifest := flags.String("manifest", "", "Signed release envelope")
	artifact := flags.String("artifact", "", "Signed executable")
	version := flags.String("version", "", "Explicit release version")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected arguments")
	}
	keys, err := update.ReadKeys(*keysPath)
	if err != nil {
		return err
	}
	store, err := update.Open(*root, keys, runtime.GOOS+"-"+runtime.GOARCH)
	if err != nil {
		return err
	}
	switch args[0] {
	case "download":
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		m, err := store.Download(ctx, *repository, *version, nil)
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(m)
	case "stage":
		f, err := os.Open(*manifest)
		if err != nil {
			return err
		}
		defer f.Close()
		b, err := io.ReadAll(io.LimitReader(f, update.MaxManifest+1))
		if err != nil {
			return err
		}
		executable, err := os.Open(*artifact)
		if err != nil {
			return err
		}
		defer executable.Close()
		m, err := store.Stage(b, executable)
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(m)
	case "initialize":
		return store.Initialize(*version)
	case "request":
		return store.Request(*version)
	case "status":
		status, err := store.Status()
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(status)
	default:
		return errors.New("unknown update command")
	}
}
