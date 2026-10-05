package internal

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	mediaevents "github.com/Muxcore-Media/contracts-media/events"
	eventsv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/events/v1"
	"github.com/Muxcore-Media/core/sdk/go/client"
)

// DeleteAccountGuard removes per-user guard rows for an auth account.
// Violations, trust scores, and aliases keyed by that user id are deleted.
// Guard rules and another account's rows stay. A repeated call deletes nothing.
func (m *Module) DeleteAccountGuard(ctx context.Context, userID string) (int64, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return 0, fmt.Errorf("user_id is required")
	}
	db, err := m.dbConn()
	if err != nil {
		return 0, err
	}
	key := trustUserKey(userID, "")
	var n int64
	for _, stmt := range []struct {
		query string
		args  []any
	}{
		{`DELETE FROM violations WHERE user_id = ?`, []any{userID}},
		{`DELETE FROM trust_scores WHERE user_id = ? OR user_key = ?`, []any{userID, key}},
		{`DELETE FROM user_aliases WHERE canonical_user_id = ? OR alias_key = ?`, []any{userID, key}},
	} {
		res, err := db.ExecContext(ctx, stmt.query, stmt.args...)
		if err != nil {
			return n, fmt.Errorf("delete guard rows: %w", err)
		}
		if affected, err := res.RowsAffected(); err == nil {
			n += affected
		}
	}
	return n, nil
}

// ApplyUserDeleted deletes guard rows for the account in an
// identity.user.deleted payload.
func (m *Module) ApplyUserDeleted(ctx context.Context, payload []byte) error {
	var body mediaevents.UserDeletedPayload
	if err := json.Unmarshal(payload, &body); err != nil {
		return fmt.Errorf("decode identity.user.deleted: %w", err)
	}
	if strings.TrimSpace(body.UserID) == "" {
		return fmt.Errorf("identity.user.deleted missing user_id")
	}
	_, err := m.DeleteAccountGuard(ctx, body.UserID)
	return err
}

func (m *Module) subscribeUserDeleted(ctx context.Context, mc *client.Client, wg *sync.WaitGroup, active *int) {
	ch, cancel, err := mc.Events.Subscribe(ctx, mediaevents.EventIdentityUserDeleted)
	if err != nil {
		slog.Debug("playback-guard: subscribe identity.user.deleted failed", "error", err)
		return
	}
	*active++
	wg.Add(1)
	go func(events <-chan *eventsv1.Event, cancel context.CancelFunc) {
		defer wg.Done()
		defer cancel()
		for evt := range events {
			if evt == nil {
				continue
			}
			if err := m.ApplyUserDeleted(ctx, evt.Payload); err != nil {
				slog.Warn("playback-guard: apply identity.user.deleted", "error", err)
			}
		}
	}(ch, cancel)
}
