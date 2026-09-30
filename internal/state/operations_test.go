package state

import (
	"errors"
	"testing"
	"time"
)

func TestResolutionNeedsCloudAcknowledgementAndNeverReprints(t *testing.T) {
	s, _ := newStore(t)
	a := prepare(t, s)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(s.BeginSend(ctx, a.JobUID, a.AttemptID, time.Now()))
	must(s.Finish(ctx, a.JobUID, a.AttemptID, "UNKNOWN"))
	b := a
	b.JobUID = "next"
	must(s.Prepare(ctx, b))
	must(s.Resolve(ctx, a.JobUID, a.AttemptID, "output_checked"))
	if !errors.Is(s.BeginSend(ctx, b.JobUID, b.AttemptID, time.Now()), ErrConflict) {
		t.Fatal("unconfirmed resolution unblocked printing")
	}
	must(s.Resolve(ctx, a.JobUID, a.AttemptID, "output_checked"))
	if !errors.Is(s.Resolve(ctx, a.JobUID, a.AttemptID, "discarded"), ErrConflict) {
		t.Fatal("decision changed")
	}
	must(s.Acknowledge(ctx, a.JobUID, a.AttemptID, "UNKNOWN"))
	current, _ := s.Attempt(ctx, a.JobUID, a.AttemptID)
	if len(current.Document) != 0 || current.State != "UNKNOWN" {
		t.Fatal("ack changed outcome")
	}
	must(s.AcknowledgeResolution(ctx, Resolution{a.JobUID, a.AttemptID, "output_checked"}))
	must(s.BeginSend(ctx, b.JobUID, b.AttemptID, time.Now()))
	if !errors.Is(s.BeginSend(ctx, a.JobUID, a.AttemptID, time.Now()), ErrConflict) {
		t.Fatal("original attempt replayed")
	}
}

func TestPrunePreservesUncertaintyAndDeduplication(t *testing.T) {
	s, _ := newStore(t)
	a := prepare(t, s)
	now := time.Now()
	if err := s.BeginSend(ctx, a.JobUID, a.AttemptID, now); err != nil {
		t.Fatal(err)
	}
	if err := s.Finish(ctx, a.JobUID, a.AttemptID, "UNKNOWN"); err != nil {
		t.Fatal(err)
	}
	if err := s.Prune(ctx, now.Add(40*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	current, err := s.Attempt(ctx, a.JobUID, a.AttemptID)
	if err != nil || current.State != "UNKNOWN" || current.Document != nil {
		t.Fatal("unresolved journal lost", err)
	}
	if count, _ := s.UncertainCount(ctx); count != 1 {
		t.Fatal("retention cleared block")
	}
	page, _ := s.History(ctx, 0, "")
	if page.Attempts[0].Acknowledged {
		t.Fatal("document retention falsely became server ACK")
	}
	for _, err := range []error{s.Resolve(ctx, a.JobUID, a.AttemptID, "discarded"), s.Acknowledge(ctx, a.JobUID, a.AttemptID, "UNKNOWN"), s.AcknowledgeResolution(ctx, Resolution{a.JobUID, a.AttemptID, "discarded"}), s.Prune(ctx, now.Add(40*24*time.Hour))} {
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err = s.Attempt(ctx, a.JobUID, a.AttemptID); err == nil {
		t.Fatal("expired detailed history retained")
	}
	if !errors.Is(s.Prepare(ctx, a), ErrConflict) {
		t.Fatal("pruning allowed duplicate attempt")
	}
}

func TestReaddressQuarantinesOldIPAndFencesActiveWork(t *testing.T) {
	s, _ := newStore(t)
	a := prepare(t, s)
	if !errors.Is(s.Readdress(ctx, a.MAC, a.IP, "192.168.77.51", testPool), ErrConflict) {
		t.Fatal("changed busy endpoint")
	}
	if err := s.BeginSend(ctx, a.JobUID, a.AttemptID, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := s.Finish(ctx, a.JobUID, a.AttemptID, "SENT"); err != nil {
		t.Fatal(err)
	}
	if err := s.Readdress(ctx, a.MAC, a.IP, "192.168.77.51", testPool); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Reserve(ctx, "02:00:00:00:00:03", testPool, time.Now()); err == nil {
		t.Fatal("old lease handed to another MAC")
	}
	inv, err := s.Inventory(ctx)
	if err != nil || inv.Reservations[0].IP != "192.168.77.51" {
		t.Fatal(inv, err)
	}
	if err = s.InventoryAcknowledged(ctx, inv.Revision); err != nil {
		t.Fatal(err)
	}
	revision, ack, err := s.InventoryStatus(ctx)
	if err != nil || revision != ack {
		t.Fatal("inventory not acknowledged")
	}
}

func TestRemoteStartWithoutJournalBecomesUncertainWithoutBytes(t *testing.T) {
	s, _ := newStore(t)
	a := Attempt{JobUID: "restored-job", AttemptID: "attempt", MAC: "02:00:00:00:00:01", IP: "192.168.77.50", Port: 9100, Digest: "digest", ExpiresAt: time.Now().Add(time.Minute).Unix()}
	if err := s.RememberRemoteStart(ctx, a); err != nil {
		t.Fatal(err)
	}
	current, err := s.Attempt(ctx, a.JobUID, a.AttemptID)
	if err != nil || current.State != "UNKNOWN" || len(current.Document) != 0 {
		t.Fatal("lost journal enabled replay", err)
	}
	if err = s.BeginSend(ctx, a.JobUID, a.AttemptID, time.Now()); !errors.Is(err, ErrConflict) {
		t.Fatal("restored attempt started")
	}
	a.MAC = "02:00:00:00:00:02"
	if err = s.RememberRemoteStart(ctx, a); !errors.Is(err, ErrConflict) {
		t.Fatal("changed snapshot accepted")
	}
}
