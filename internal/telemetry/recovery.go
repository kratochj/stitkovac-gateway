package telemetry

import (
	"context"
	"errors"
	"time"
)

var ErrPanic = errors.New("gateway worker panicked")

// Recover must be deferred directly at the goroutine or HTTP boundary. Capture
// runs before the panicking stack unwinds. The panic value is intentionally ignored:
// it may contain credentials, request bodies or a printer document.
func Recover(report func(Diagnostic), failed func()) {
	if recover() == nil {
		return
	}
	diagnostic := Capture("agent_panic")
	if report != nil {
		report(diagnostic)
	}
	if failed != nil {
		failed()
	}
}

// PersistCrash bypasses the asynchronous channel because the service is stopping.
func (s *Spool) PersistCrash(d Diagnostic) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = s.record(ctx, d)
}
