package dhcp

import (
	"context"
	"errors"
	"net/netip"
	"testing"
	"time"

	"github.com/kratochj/stitkovac-gateway/internal/state"
)

var pool = state.Pool{Network: netip.MustParsePrefix("192.168.77.0/24"), Server: netip.MustParseAddr("192.168.77.1"), First: netip.MustParseAddr("192.168.77.50"), Last: netip.MustParseAddr("192.168.77.199")}

func packet(kind byte, extra ...byte) []byte {
	b := make([]byte, 240)
	b[0], b[1], b[2] = 1, 1, 6
	b[28] = 2
	b[33] = 1
	copy(b[236:], cookie)
	b = append(b, 53, 1, kind)
	b = append(b, extra...)
	return append(b, 255)
}

func TestDHCPRoundTripIsDurableBeforeReply(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := state.Initialize(dir, "test-hash")
	if err != nil {
		t.Fatal(err)
	}
	h := Handler{Store: s, Pool: pool}
	offer, err := h.Handle(ctx, packet(1, 61, 2, 1, 2))
	if err != nil || offer == nil {
		t.Fatal(err)
	}
	ip := netip.AddrFrom4([4]byte(offer.Packet[16:20]))
	s.Close()
	s, err = state.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	h.Store = s
	request := []byte{50, 4}
	request = append(request, ip.AsSlice()...)
	request = append(request, 54, 4)
	request = append(request, pool.Server.AsSlice()...)
	ack, err := h.Handle(ctx, packet(3, request...))
	if err != nil || ack == nil {
		t.Fatal(err)
	}
	opts, _ := options(ack.Packet[240:])
	if opts[53][0] != 5 || opts[3] != nil || opts[6] != nil {
		t.Fatal("unexpected ACK options")
	}
	r, err := s.Reservations(ctx)
	if err != nil || len(r) != 1 || r[0].LeaseUntil <= time.Now().Unix() {
		t.Fatal(r, err)
	}
	offer2, err := h.Handle(ctx, packet(1, 61, 2, 9, 9))
	if err != nil || string(offer2.Packet[16:20]) != string(ip.AsSlice()) {
		t.Fatal("client ID changed reservation")
	}
}

type failedStore struct{}

func (failedStore) Reserve(context.Context, string, state.Pool, time.Time) (state.Reservation, error) {
	return state.Reservation{}, errors.New("disk failed")
}
func (failedStore) Lease(context.Context, string, string, time.Time, time.Duration) error {
	return errors.New("disk failed")
}
func (failedStore) Release(context.Context, string, string, bool) error { return nil }

func TestNoReplyWhenPersistenceFails(t *testing.T) {
	h := Handler{Store: failedStore{}, Pool: pool}
	if r, err := h.Handle(context.Background(), packet(1)); r != nil || err == nil {
		t.Fatal("replied without durable storage")
	}
}

func TestMalformedAndForeignRequests(t *testing.T) {
	h := Handler{Store: failedStore{}, Pool: pool}
	for _, b := range [][]byte{nil, packet(1, 53, 1, 1), packet(1, 50, 9, 1), packet(1, 52, 1, 1)} {
		if r, err := h.Handle(context.Background(), b); r != nil || !errors.Is(err, ErrPacket) {
			t.Fatal("accepted malformed packet", err)
		}
	}
	if r, err := h.Handle(context.Background(), packet(3, 54, 4, 192, 168, 77, 2)); r != nil || err != nil {
		t.Fatal("answered another server's request")
	}
}

func TestReleaseDoesNotRecycleAndDeclineQuarantines(t *testing.T) {
	s, err := state.Initialize(t.TempDir(), "test-hash")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	h := Handler{Store: s, Pool: pool}
	ctx := context.Background()
	offer, _ := h.Handle(ctx, packet(1))
	ip := offer.Packet[16:20]
	release := packet(7, 54, 4, 192, 168, 77, 1)
	copy(release[12:16], ip)
	if _, err := h.Handle(ctx, release); err != nil {
		t.Fatal(err)
	}
	offer2, err := h.Handle(ctx, packet(1))
	if err != nil || string(offer2.Packet[16:20]) != string(ip) {
		t.Fatal("release lost reservation")
	}
	decline := []byte{54, 4, 192, 168, 77, 1, 50, 4}
	decline = append(decline, ip...)
	if _, err := h.Handle(ctx, packet(4, decline...)); err != nil {
		t.Fatal(err)
	}
	if r, err := h.Handle(ctx, packet(1)); r != nil || err == nil {
		t.Fatal("reused declined address")
	}
}

func FuzzMalformedPacket(f *testing.F) {
	f.Add(packet(1))
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, b []byte) {
		h := Handler{Store: failedStore{}, Pool: pool}
		h.Handle(context.Background(), b)
	})
}
