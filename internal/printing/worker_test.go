package printing

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/kratochj/stitkovac-gateway/internal/state"
)

type fakeCloud struct {
	starts  int
	results []string
	loseACK bool
}

func (c *fakeCloud) Start(context.Context, state.Attempt) error { c.starts++; return nil }
func (c *fakeCloud) Result(_ context.Context, a state.Attempt) error {
	c.results = append(c.results, a.State)
	if c.loseACK {
		return errors.New("connection lost")
	}
	return nil
}

type fakeConn struct {
	bytes.Buffer
	partial     bool
	beforeWrite func()
}

func (c *fakeConn) Write(b []byte) (int, error) {
	if c.beforeWrite != nil {
		c.beforeWrite()
	}
	if c.partial {
		c.Buffer.Write(b[:1])
		return 1, io.ErrUnexpectedEOF
	}
	return c.Buffer.Write(b)
}
func (*fakeConn) Close() error                     { return nil }
func (*fakeConn) LocalAddr() net.Addr              { return nil }
func (*fakeConn) RemoteAddr() net.Addr             { return nil }
func (*fakeConn) SetDeadline(time.Time) error      { return nil }
func (*fakeConn) SetReadDeadline(time.Time) error  { return nil }
func (*fakeConn) SetWriteDeadline(time.Time) error { return nil }

func setup(t *testing.T) (*Worker, state.Attempt, *fakeConn) {
	t.Helper()
	s, err := state.Initialize(t.TempDir()+"/state", "test-hash")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	pool := state.Pool{Network: netip.MustParsePrefix("192.168.77.0/24"), Server: netip.MustParseAddr("192.168.77.1"), First: netip.MustParseAddr("192.168.77.50"), Last: netip.MustParseAddr("192.168.77.199")}
	r, err := s.Reserve(context.Background(), "02:00:00:00:00:01", pool, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	b := []byte("%PDF-exact original bytes")
	d := sha256.Sum256(b)
	a := state.Attempt{JobUID: "job", AttemptID: "attempt", MAC: r.MAC, IP: r.IP, Port: 9100, Digest: hex.EncodeToString(d[:]), Document: b, ExpiresAt: time.Now().Add(time.Minute).Unix()}
	conn := &fakeConn{}
	w := &Worker{Store: s, Pool: pool, Dial: func(context.Context, string) (net.Conn, error) { return conn, nil }}
	conn.beforeWrite = func() {
		current, err := s.Attempt(context.Background(), a.JobUID, a.AttemptID)
		if err != nil || current.State != "SENDING" {
			t.Fatal("write preceded durable SENDING")
		}
	}
	return w, a, conn
}

func TestLostResultNeverRepeatsTCPWrite(t *testing.T) {
	w, a, conn := setup(t)
	cloud := &fakeCloud{loseACK: true}
	ctx := context.Background()
	if err := w.Process(ctx, a, cloud); err == nil {
		t.Fatal("expected lost ACK")
	}
	cloud.loseACK = false
	if err := w.Process(ctx, a, cloud); err != nil {
		t.Fatal(err)
	}
	if conn.String() != string(a.Document) || cloud.starts != 1 {
		t.Fatal("document was resent")
	}
	current, _ := w.Store.Attempt(ctx, a.JobUID, a.AttemptID)
	if current.State != "SENT" || len(current.Document) != 0 {
		t.Fatal(current)
	}
}

func TestPartialWriteBecomesUnknown(t *testing.T) {
	w, a, conn := setup(t)
	conn.partial = true
	cloud := &fakeCloud{}
	if err := w.Process(context.Background(), a, cloud); err != nil {
		t.Fatal(err)
	}
	if err := w.Process(context.Background(), a, cloud); err != nil {
		t.Fatal(err)
	}
	if conn.Len() != 1 || cloud.starts != 1 || cloud.results[0] != "UNKNOWN" {
		t.Fatal("unsafe partial write recovery")
	}
}

func TestExpiredAndForeignTargetsNeverConnect(t *testing.T) {
	w, a, conn := setup(t)
	a.ExpiresAt = time.Now().Add(-time.Minute).Unix()
	cloud := &fakeCloud{}
	if err := w.Process(context.Background(), a, cloud); err != nil {
		t.Fatal(err)
	}
	if cloud.starts != 0 || conn.Len() != 0 || cloud.results[0] != "EXPIRED" {
		t.Fatal("sent expired job")
	}
	a.IP = "10.1.2.3"
	if err := w.Process(context.Background(), a, cloud); err == nil {
		t.Fatal("accepted foreign target")
	}
}
