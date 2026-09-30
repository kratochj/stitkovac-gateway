package cloud

import (
	"context"
	"errors"
)

// Reconcile results before listing work: an accepted result with a lost HTTP ACK
// no longer appears in the server's open queue, but must still clear local data.
func (c *Client) syncOperations(ctx context.Context, session string) error {
	if c.worker == nil || c.worker.Store == nil {
		return errors.New("missing local journal")
	}
	store := c.worker.Store
	for {
		pending, err := store.PendingResults(ctx)
		if err != nil {
			return err
		}
		for _, a := range pending {
			if err = (sessionCloud{c, session}).Result(ctx, a); err != nil {
				return err
			}
			if err = store.Acknowledge(ctx, a.JobUID, a.AttemptID, a.State); err != nil {
				return err
			}
		}
		if len(pending) < 100 {
			break
		}
	}
	for {
		resolutions, err := store.PendingResolutions(ctx)
		if err != nil {
			return err
		}
		for _, r := range resolutions {
			if !safeID.MatchString(r.JobUID) || !safeID.MatchString(r.AttemptID) {
				return errors.New("invalid resolution identity")
			}
			if err = c.request(ctx, "POST", "/jobs/"+r.JobUID+"/resolve", session, r, nil); err != nil {
				return err
			}
			if err = store.AcknowledgeResolution(ctx, r); err != nil {
				return err
			}
		}
		if len(resolutions) < 100 {
			break
		}
	}
	inventory, err := store.Inventory(ctx)
	if err != nil {
		return err
	}
	var ack struct {
		Revision int64 `json:"revision"`
	}
	if err = c.request(ctx, "PUT", "/inventory", session, inventory, &ack); err != nil {
		return err
	}
	if ack.Revision != inventory.Revision {
		return errors.New("inventory acknowledgement mismatch")
	}
	return store.InventoryAcknowledged(ctx, ack.Revision)
}
