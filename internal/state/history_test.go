package state

import (
	"bytes"
	"fmt"
	"testing"
	"time"
)

func TestHistoryRecordsTransitionsWithoutDocumentsAndPreservesDeduplication(t *testing.T) {
	s, _ := newStore(t)
	a := prepare(t, s)
	page, err := s.History(ctx, 0, "")
	if err != nil || len(page.Attempts) != 1 {
		t.Fatal("missing attempt", err)
	}
	first := page.Attempts[0]
	if first.CreatedAt == 0 || first.State != "CLAIMED" || first.Acknowledged {
		t.Fatal("invalid initial metadata")
	}
	if err := s.BeginSend(ctx, a.JobUID, a.AttemptID, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := s.Finish(ctx, a.JobUID, a.AttemptID, "SENT"); err != nil {
		t.Fatal(err)
	}
	page, _ = s.History(ctx, 0, "SENT")
	if page.Attempts[0].Acknowledged {
		t.Fatal("unconfirmed result reported as acknowledged")
	}
	if err := s.Acknowledge(ctx, a.JobUID, a.AttemptID, "SENT"); err != nil {
		t.Fatal(err)
	}
	if err := s.Prepare(ctx, a); err != nil {
		t.Fatal(err)
	}
	page, _ = s.History(ctx, 0, "")
	row := page.Attempts[0]
	if row.CreatedAt != first.CreatedAt || row.UpdatedAt < row.CreatedAt || row.AcknowledgedAt == 0 || !row.Acknowledged {
		t.Fatal("invalid confirmed history")
	}
	if len(page.Attempts) != 1 {
		t.Fatal("replay created another history row")
	}
	current, _ := s.Attempt(ctx, a.JobUID, a.AttemptID)
	if len(current.Document) != 0 || current.State != "SENT" {
		t.Fatal("history changed journal semantics")
	}
}

func TestHistoryPaginationAndFiltersRemainStableWhileJobsArrive(t *testing.T) {
	s, _ := newStore(t)
	a := prepare(t, s)
	for i := 1; i < 61; i++ {
		a.JobUID = fmt.Sprintf("job-%d", i)
		if err := s.Prepare(ctx, a); err != nil {
			t.Fatal(err)
		}
	}
	page, err := s.History(ctx, 0, "CLAIMED")
	if err != nil || len(page.Attempts) != 50 || page.NextBefore == 0 {
		t.Fatal("unbounded or missing page", err)
	}
	a.JobUID = "arrived-after-first-page"
	if err := s.Prepare(ctx, a); err != nil {
		t.Fatal(err)
	}
	next, err := s.History(ctx, page.NextBefore, "CLAIMED")
	if err != nil || len(next.Attempts) != 11 || next.NextBefore != 0 {
		t.Fatal("unstable pagination", err)
	}
	for _, row := range next.Attempts {
		if row.Sequence >= page.NextBefore {
			t.Fatal("overlapping page")
		}
	}
	if _, err := s.History(ctx, -1, ""); err == nil {
		t.Fatal("negative cursor accepted")
	}
	if _, err := s.History(ctx, 0, "arbitrary SQL"); err == nil {
		t.Fatal("invalid state accepted")
	}
	empty, err := s.History(ctx, 0, "UNKNOWN")
	if err != nil || len(empty.Attempts) != 0 {
		t.Fatal("filter ignored")
	}
}

func TestHistoryUpgradeKeepsLegacyIdentityAndUnknownTimes(t *testing.T) {
	s, dir := newStore(t)
	a := prepare(t, s)
	id, _, _ := s.Identity()
	if _, err := s.db.Exec(`DROP TRIGGER attempt_history_created; DROP TRIGGER attempt_history_changed;
 DROP TRIGGER attempt_history_acknowledged; DROP TABLE attempt_history; DROP INDEX attempts_state;`); err != nil {
		t.Fatal(err)
	}
	s.Close()
	reopened, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	currentID, _, _ := reopened.Identity()
	page, err := reopened.History(ctx, 0, "")
	if err != nil || len(page.Attempts) != 1 || page.Attempts[0].CreatedAt != 0 || currentID != id {
		t.Fatal("migration changed legacy state", err)
	}
	current, err := reopened.Attempt(ctx, a.JobUID, a.AttemptID)
	if err != nil || !bytes.Equal(current.Document, a.Document) {
		t.Fatal("migration changed document")
	}
	if err := reopened.BeginSend(ctx, a.JobUID, a.AttemptID, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := reopened.Recover(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	page, err = reopened.History(ctx, 0, "UNKNOWN")
	if err != nil || len(page.Attempts) != 1 || page.Attempts[0].CreatedAt != 0 || page.Attempts[0].UpdatedAt == 0 {
		t.Fatal("lost recovery history", err)
	}
	count, err := reopened.UncertainCount(ctx)
	if err != nil || count != 1 {
		t.Fatal("missing uncertain warning")
	}
	var version int
	if err := reopened.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 1 {
		t.Fatal("broke older agent compatibility")
	}
}
