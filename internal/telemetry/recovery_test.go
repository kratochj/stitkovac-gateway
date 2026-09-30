package telemetry

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

//go:noinline
func panicAtOriginalSite() { panic("secret-token-and-document") }

func TestRecoveryKeepsOriginalFrameWithoutPanicValue(t *testing.T) {
	spool, err := Open(t.TempDir(), "gateway", "test")
	if err != nil {
		t.Fatal(err)
	}
	defer spool.Close()
	failed := false
	func() { defer Recover(spool.PersistCrash, func() { failed = true }); panicAtOriginalSite() }()
	events, err := spool.Pending(context.Background())
	if err != nil || !failed || len(events) != 1 {
		t.Fatal("panic was not persisted", err)
	}
	found := false
	for _, f := range events[0].Frames {
		if strings.HasSuffix(f.Function, "panicAtOriginalSite") && f.File == "recovery_test.go" && f.Line > 0 {
			found = true
		}
	}
	if !found {
		t.Fatal("original panic site missing", events[0].Frames)
	}
	data, _ := json.Marshal(events)
	if strings.Contains(string(data), "secret-token-and-document") {
		t.Fatal("panic payload leaked")
	}
}
