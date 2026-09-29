// Package launcher supervises signed agents without importing their database layer.
package launcher

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/kratochj/stitkovac-gateway/internal/update"
	"golang.org/x/sys/unix"
)

// RestartExitCode is accepted only with a verified pending selection.
const RestartExitCode = 75

var errRequestedRestart = errors.New("agent requested update activation")

type Options struct {
	Root         string
	Args         []string
	ReadyTimeout time.Duration
	StopTimeout  time.Duration
	Stdout       io.Writer
	Stderr       io.Writer
}

// Run holds a lifetime supervisor lock; individual selection operations have their
// own lock so an installer can stage/request a release while an agent is running.
func Run(ctx context.Context, store *update.Store, o Options) error {
	if o.ReadyTimeout <= 0 || o.StopTimeout <= 0 {
		return errors.New("positive launcher timeouts are required")
	}
	f, err := os.OpenFile(filepath.Join(o.Root, "launcher.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return errors.New("another launcher owns this release directory")
	}
	defer unix.Flock(int(f.Fd()), unix.LOCK_UN)
	for attempt := 0; ; {
		v, err := store.BeginBoot()
		if err != nil {
			return err
		}
		path, err := store.Executable(v.Active)
		if err != nil {
			return err
		}
		confirmed, err := runChild(ctx, path, v.Active, o, func() error {
			if v.Trial {
				return store.Confirm(v.Active)
			}
			return nil
		})
		if ctx.Err() != nil {
			return nil
		}
		if confirmed && errors.Is(err, errRequestedRestart) {
			selection, e := store.Status()
			if e != nil {
				return e
			}
			if selection.Pending == "" {
				return errors.New("update restart without pending release")
			}
			attempt = 0
			continue
		}
		if err == nil || !v.Trial || confirmed {
			return err
		}
		attempt++
		if attempt >= 2 {
			return errors.New("previous release failed to start")
		}
		// A failed trial remains durable until BeginBoot rolls it back. Failure or
		// power loss before this loop resumes gives the same result at the next boot.
	}
}

func runChild(ctx context.Context, path, version string, o Options, confirm func() error) (bool, error) {
	readyRead, readyWrite, err := os.Pipe()
	if err != nil {
		return false, err
	}
	defer readyRead.Close()
	defer readyWrite.Close()
	goRead, goWrite, err := os.Pipe()
	if err != nil {
		return false, err
	}
	defer goRead.Close()
	defer goWrite.Close()
	args := append(append([]string{}, o.Args...), "--launcher-ready-fd=3", "--launcher-continue-fd=4")
	cmd := exec.Command(path, args...)
	cmd.Stdout, cmd.Stderr = o.Stdout, o.Stderr
	cmd.ExtraFiles = []*os.File{readyWrite, goRead}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return false, err
	}
	readyWrite.Close()
	goRead.Close()
	finished := make(chan struct{})
	var waitErr error
	go func() { waitErr = cmd.Wait(); close(finished) }()
	defer func() {
		select {
		case <-finished:
			return
		default:
		}
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		timer := time.NewTimer(o.StopTimeout)
		defer timer.Stop()
		select {
		case <-finished:
		case <-timer.C:
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			<-finished
		}
	}()
	readiness := make(chan error, 1)
	go func() {
		b, err := io.ReadAll(io.LimitReader(readyRead, 128))
		if err == nil && string(b) != "READY "+version+"\n" {
			err = errors.New("invalid agent readiness response")
		}
		readiness <- err
	}()
	timer := time.NewTimer(o.ReadyTimeout)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false, ctx.Err()
	case <-finished:
		return false, errors.New("agent exited before readiness")
	case <-timer.C:
		return false, errors.New("agent readiness timed out")
	case err := <-readiness:
		if err != nil {
			return false, err
		}
	}
	select {
	case <-finished:
		return false, errors.New("agent exited before confirmation")
	case <-ctx.Done():
		return false, ctx.Err()
	default:
	}
	if err := confirm(); err != nil {
		return false, err
	}
	// Printing is gated until the version selection is durably confirmed.
	if _, err := io.WriteString(goWrite, "CONTINUE\n"); err != nil {
		return true, err
	}
	goWrite.Close()
	select {
	case <-ctx.Done():
		return true, ctx.Err()
	case <-finished:
		var exit *exec.ExitError
		if errors.As(waitErr, &exit) && exit.ExitCode() == RestartExitCode {
			return true, errRequestedRestart
		}
		if waitErr == nil {
			return true, errors.New("agent exited unexpectedly")
		}
		return true, fmt.Errorf("agent process failed: %w", waitErr)
	}
}

// Ready is called after local state recovery and successful listener binding,
// before starting any cloud print worker. The inherited pipes are one-shot and
// contain no credentials. With both descriptors zero the agent runs standalone.
func Ready(ctx context.Context, version string, readyFD, continueFD int) error {
	if readyFD == 0 && continueFD == 0 {
		return nil
	}
	if readyFD != 3 || continueFD != 4 {
		return errors.New("invalid launcher pipe descriptors")
	}
	ready := os.NewFile(uintptr(readyFD), "launcher-ready")
	proceed := os.NewFile(uintptr(continueFD), "launcher-continue")
	if ready == nil || proceed == nil {
		return errors.New("missing launcher pipes")
	}
	defer ready.Close()
	defer proceed.Close()
	if _, err := io.WriteString(ready, "READY "+version+"\n"); err != nil {
		return err
	}
	ready.Close()
	result := make(chan error, 1)
	go func() {
		b, err := io.ReadAll(io.LimitReader(proceed, 32))
		if err == nil && string(b) != "CONTINUE\n" {
			err = errors.New("launcher did not authorize printing")
		}
		result <- err
	}()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-result:
		return err
	}
}
