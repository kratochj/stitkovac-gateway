package printing

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"
)

func TestProbeSendsNoBytesAndDoesNotCreatePrintAttempt(t *testing.T) {
	w, a, conn := setup(t)
	if err := w.Store.Lease(context.Background(), a.MAC, a.IP, time.Now(), time.Hour); err != nil {
		t.Fatal(err)
	}
	dialled := ""
	w.Dial = func(ctx context.Context, address string) (net.Conn, error) {
		dialled = address
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 3*time.Second {
			t.Fatal("unbounded probe")
		}
		return conn, nil
	}
	result := w.Probe(context.Background(), a.MAC)
	if result.Code != "reachable" || dialled != a.IP+":9100" || conn.Len() != 0 {
		t.Fatal("unsafe probe")
	}
	page, err := w.Store.History(context.Background(), 0, "")
	if err != nil || len(page.Attempts) != 0 {
		t.Fatal("probe modified print journal")
	}
}

func TestProbeRejectsUnknownConflictExpiredAndForeignAddresses(t *testing.T) {
	w, a, _ := setup(t)
	calls := 0
	w.Dial = func(context.Context, string) (net.Conn, error) {
		calls++
		return nil, errors.New("private network error")
	}
	for _, mac := range []string{"http://other.example", "02:00:00:00:00:99", a.MAC} {
		if w.Probe(context.Background(), mac).Code == "reachable" {
			t.Fatal("invalid probe accepted")
		}
	}
	if calls != 0 {
		t.Fatal("dialled a MAC without an active reservation")
	}
	if err := w.Store.Lease(context.Background(), a.MAC, a.IP, time.Now(), time.Hour); err != nil {
		t.Fatal(err)
	}
	originalPool := w.Pool
	w.Pool.First = w.Pool.Last
	if result := w.Probe(context.Background(), a.MAC); result.Code != "unavailable" || calls != 0 {
		t.Fatal("probe escaped the configured address pool")
	}
	w.Pool = originalPool
	if result := w.Probe(context.Background(), a.MAC); result.Code != "unreachable" {
		t.Fatal("network error was not sanitized")
	}
	if calls != 1 {
		t.Fatal("missing valid probe")
	}
	if err := w.Store.Release(context.Background(), a.MAC, a.IP, true); err != nil {
		t.Fatal(err)
	}
	if result := w.Probe(context.Background(), a.MAC); result.Code != "conflict" {
		t.Fatal("conflict not blocked")
	}
	if calls != 1 {
		t.Fatal("conflicting IP was dialled")
	}
}

func TestProbeDoesNotOverlapPrintingOnSameEndpoint(t *testing.T) {
	w, a, conn := setup(t)
	if err := w.Store.Lease(context.Background(), a.MAC, a.IP, time.Now(), time.Hour); err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	conn.beforeWrite = func() { close(entered); <-release }
	var calls atomic.Int32
	w.Dial = func(context.Context, string) (net.Conn, error) { calls.Add(1); return conn, nil }
	finished := make(chan error, 1)
	go func() { finished <- w.Process(context.Background(), a, &fakeCloud{}) }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("print did not start")
	}
	result := w.Probe(context.Background(), a.MAC)
	close(release)
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	if result.Code != "busy" || calls.Load() != 1 || conn.String() != string(a.Document) {
		t.Fatal("probe interfered with print")
	}
}
