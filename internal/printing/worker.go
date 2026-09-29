// Package printing transfers the original document only after cloud authorization
// and a durable local SENDING commit. It never infers physical paper delivery.
package printing

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"strconv"
	"sync"
	"time"

	"github.com/kratochj/stitkovac-gateway/internal/state"
)

type Cloud interface {
	Start(context.Context, state.Attempt) error
	Result(context.Context, state.Attempt) error
}

type Worker struct {
	Store *state.Store
	Pool  state.Pool
	Dial  func(context.Context, string) (net.Conn, error)
	locks sync.Map
}

func (w *Worker) Process(ctx context.Context, a state.Attempt, cloud Cloud) error {
	ip, err := netip.ParseAddr(a.IP)
	if err != nil || !w.Pool.Network.Contains(ip) || ip == w.Pool.Server || a.Port != 9100 {
		return errors.New("printer target outside configured network")
	}
	address := net.JoinHostPort(a.IP, strconv.Itoa(a.Port))
	gate := w.endpointGate(address)
	select {
	case gate <- struct{}{}:
		defer func() { <-gate }()
	case <-ctx.Done():
		return ctx.Err()
	}
	if err := w.Store.Prepare(ctx, a); err != nil {
		return err
	}
	current, err := w.Store.Attempt(ctx, a.JobUID, a.AttemptID)
	if err != nil {
		return err
	}
	if current.State == "CLAIMED" {
		if current.ExpiresAt <= time.Now().Unix() {
			if err := w.Store.Expire(ctx, current.JobUID, current.AttemptID, time.Now()); err != nil {
				return err
			}
		} else {
			if err := cloud.Start(ctx, current); err != nil {
				return err
			}
			if err := w.Store.BeginSend(ctx, current.JobUID, current.AttemptID, time.Now()); err != nil {
				return err
			}
			// Once authorized, an internet disconnect must not interrupt the local write.
			sendCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
			result := w.send(sendCtx, address, current.Document)
			cancel()
			if err := w.Store.Finish(context.WithoutCancel(ctx), current.JobUID, current.AttemptID, result); err != nil {
				return err
			}
		}
		current, err = w.Store.Attempt(context.WithoutCancel(ctx), a.JobUID, a.AttemptID)
		if err != nil {
			return err
		}
	}
	if current.State == "SENDING" {
		return state.ErrConflict
	}
	if err := cloud.Result(ctx, current); err != nil {
		return err
	}
	if current.State == "UNKNOWN" {
		return nil
	}
	return w.Store.Acknowledge(ctx, current.JobUID, current.AttemptID, current.State)
}

func (w *Worker) send(ctx context.Context, address string, document []byte) string {
	conn, err := w.Dial(ctx, address)
	if err != nil {
		return "FAILED"
	}
	defer conn.Close()
	deadline, _ := ctx.Deadline()
	if err := conn.SetWriteDeadline(deadline); err != nil {
		return "FAILED"
	}
	for len(document) > 0 {
		n, err := conn.Write(document)
		if err != nil || n <= 0 || n > len(document) {
			return "UNKNOWN"
		}
		document = document[n:]
	}
	return "SENT"
}

func (w *Worker) endpointGate(address string) chan struct{} {
	lock, _ := w.locks.LoadOrStore(address, make(chan struct{}, 1))
	return lock.(chan struct{})
}
