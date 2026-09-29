package telemetry

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestSpoolSurvivesRestartWithoutRawErrors(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, "gateway", "test")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := s.Record(ctx, "password=very-secret SSID=customer"); err == nil {
		t.Fatal("accepted raw error")
	}
	if err := s.Record(ctx, "dhcp_request_failed"); err != nil {
		t.Fatal(err)
	}
	before, err := s.Pending(ctx)
	if err != nil || len(before) != 1 {
		t.Fatal(before, err)
	}
	s.Close()
	s, err = Open(dir, "gateway", "test")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	after, err := s.Pending(ctx)
	if err != nil || len(after) != 1 || after[0].EventID != before[0].EventID {
		t.Fatal("lost offline event")
	}
	b, _ := json.Marshal(after)
	if strings.Contains(string(b), "very-secret") || strings.Contains(string(b), "/Users/") {
		t.Fatal("sensitive diagnostics")
	}
}

func TestSpoolLimit(t *testing.T) {
	s, err := Open(t.TempDir(), "gateway", "test")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for i := 0; i < 1005; i++ {
		if err := s.Record(context.Background(), "dhcp_request_failed"); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err := s.db.QueryRow("SELECT count(*) FROM events").Scan(&count); err != nil || count != 1000 {
		t.Fatal(count, err)
	}
}
