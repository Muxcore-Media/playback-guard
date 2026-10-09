package internal

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"strings"

	"github.com/Muxcore-Media/core/sdk/go/module/erasure"

	discoveryv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/discovery/v1"
	"google.golang.org/grpc"
)

// User erasure (ADR-0035). The identity provider's erasure ledger is the only
// authority: nothing in this file reacts to an event, a header or an RPC
// payload. The reconciler pulls tombstones from the provider and calls
// EraseUser; everything else here only refuses to re-create what was erased.
//
// Disposition of this module's personal data (ADR-0035 §3):
//
//   - violations, trust_scores: rows keyed by the erased user id are deleted.
//   - user_aliases: aliases whose canonical user is the id, and the alias keyed
//     by the id itself, are deleted. An alias is an operator-asserted merge
//     ("these are the same person"), so rows reachable through such an alias
//     (an `id:` alias key, or a `name:` alias key) are deleted with it.
//   - RETAINED with reason: violations and trust scores keyed by a `name:` key
//     or an external media-server id that no alias links to the erased user.
//     They live in a different id space and nothing links them to this user.
//     Operators delete a single user's history elsewhere (playback-monitor
//     DeleteUserHistory / a merge before the erasure).
//   - erasure_applied: retained, never pruned. It holds the erasure id, the
//     erased user id, the tenant and a timestamp, no username, and is what
//     makes the module refuse late writes for that id after a restart.
//
// The module stores no tenant; user ids are globally unique (ADR-0035 §3), so
// Tombstone.TenantID is only recorded.

// errUserErased is returned by a write that names an erased user id.
var errUserErased = errors.New("playback-guard: user id has been erased")

// Counts keys reported to the provider with the acknowledgement.
const (
	countViolations = "violations"
	countTrust      = "trust_scores"
	countAliases    = "user_aliases"
)

// notErasedSQL is appended to INSERT ... SELECT ... WHERE so the check and the
// write are one statement: a write cannot interleave with EraseUser and leave
// a row behind for an id whose erasure has already been recorded. The single
// placeholder is the user id.
const notErasedSQL = `NOT EXISTS (SELECT 1 FROM erasure_applied WHERE user_id = ?)`

// execer is the part of *sql.DB and *sql.Tx the guarded writes use.
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// guardOwner adapts the module to erasure.Owner and erasure.Verifier.
type guardOwner struct{ m *Module }

var (
	_ erasure.Owner    = guardOwner{}
	_ erasure.Verifier = guardOwner{}
)

// ModuleID is the module's mesh identity: the certificate CN, which
// modulesdk takes from MUXCORE_MODULE_ID when set.
func (o guardOwner) ModuleID() string {
	if v := strings.TrimSpace(os.Getenv("MUXCORE_MODULE_ID")); v != "" {
		return v
	}
	return o.m.id
}

func (o guardOwner) Applied(ctx context.Context, erasureID string) (bool, error) {
	return o.m.erasureApplied(ctx, erasureID)
}

func (o guardOwner) Apply(ctx context.Context, t erasure.Tombstone) (erasure.Counts, error) {
	return o.m.EraseUser(ctx, t)
}

func (o guardOwner) Verify(ctx context.Context, t erasure.Tombstone) (int, error) {
	return o.m.remainingForUser(ctx, t.UserID)
}

func (m *Module) erasureApplied(ctx context.Context, erasureID string) (bool, error) {
	db, err := m.dbConn()
	if err != nil {
		return false, err
	}
	var one int
	err = db.QueryRowContext(ctx, `SELECT 1 FROM erasure_applied WHERE erasure_id = ?`, erasureID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// EraseUser applies one tombstone: it deletes the user's rows AND records the
// erasure as applied in ONE transaction. On any error nothing has changed.
// Applying an erasure id that is already recorded is a no-op that returns the
// recorded counts.
func (m *Module) EraseUser(ctx context.Context, t erasure.Tombstone) (erasure.Counts, error) {
	if strings.TrimSpace(t.ErasureID) == "" || t.UserID == "" || strings.TrimSpace(t.UserID) != t.UserID {
		return nil, errors.New("erase user: invalid tombstone")
	}
	db, err := m.dbConn()
	if err != nil {
		return nil, err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	var prior string
	err = tx.QueryRowContext(ctx, `SELECT counts_json FROM erasure_applied WHERE erasure_id = ?`, t.ErasureID).Scan(&prior)
	if err == nil {
		return decodeCounts(prior), nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}

	counts := erasure.Counts{}
	err = eraseUserRowsTx(ctx, tx, t.UserID, counts)
	if err != nil {
		return nil, err
	}
	enc, err := json.Marshal(counts)
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO erasure_applied(erasure_id, user_id, tenant_id, applied_at, counts_json)
		VALUES (?, ?, ?, ?, ?)`,
		t.ErasureID, t.UserID, t.TenantID, nowRFC3339(), string(enc),
	); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return counts, nil
}

func decodeCounts(s string) erasure.Counts {
	c := erasure.Counts{}
	if err := json.Unmarshal([]byte(s), &c); err != nil {
		return erasure.Counts{}
	}
	return c
}

// eraseUserRowsTx deletes every row the disposition covers and adds the
// per-table numbers to counts. It runs inside the caller's transaction.
func eraseUserRowsTx(ctx context.Context, tx *sql.Tx, userID string, counts erasure.Counts) error {
	ownKey := "id:" + userID

	// Aliases whose canonical user is the erased id: operator-asserted merges.
	// Collect them first; the rows they reach go before the alias rows do.
	aliasKeys, err := queryStringsTx(ctx, tx, `SELECT alias_key FROM user_aliases WHERE canonical_user_id = ?`, userID)
	if err != nil {
		return err
	}
	var linkedViolations, linkedTrust, n int64
	linkedNames := map[string]struct{}{}
	for _, key := range aliasKeys {
		if key == ownKey {
			continue
		}
		switch {
		case strings.HasPrefix(key, "id:"):
			n, err = execCount(ctx, tx, `DELETE FROM violations WHERE user_id = ?`, strings.TrimPrefix(key, "id:"))
			if err != nil {
				return err
			}
			linkedViolations += n
		case strings.HasPrefix(key, "name:"):
			linkedNames[strings.TrimPrefix(key, "name:")] = struct{}{}
		}
		n, err = execCount(ctx, tx, `DELETE FROM trust_scores WHERE user_key = ?`, key)
		if err != nil {
			return err
		}
		linkedTrust += n
	}
	if len(linkedNames) > 0 {
		n, err = deleteNameKeyedViolationsTx(ctx, tx, linkedNames)
		if err != nil {
			return err
		}
		linkedViolations += n
	}

	v, err := execCount(ctx, tx, `DELETE FROM violations WHERE user_id = ?`, userID)
	if err != nil {
		return err
	}
	ts, err := execCount(ctx, tx, `DELETE FROM trust_scores WHERE user_id = ? OR user_key = ?`, userID, ownKey)
	if err != nil {
		return err
	}
	al, err := execCount(ctx, tx, `DELETE FROM user_aliases WHERE canonical_user_id = ? OR alias_key = ?`, userID, ownKey)
	if err != nil {
		return err
	}
	counts[countViolations] = v + linkedViolations
	counts[countTrust] = ts + linkedTrust
	counts[countAliases] = al
	return nil
}

// deleteNameKeyedViolationsTx deletes violations recorded without a user id
// whose user name maps (as trustUserKey does) to one of names. The match is
// done in Go with the module's own key function so it agrees exactly with how
// the rows were keyed, including non-ASCII names.
func deleteNameKeyedViolationsTx(ctx context.Context, tx *sql.Tx, names map[string]struct{}) (int64, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id, user_name FROM violations WHERE user_id = ''`)
	if err != nil {
		return 0, err
	}
	var ids []string
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			_ = rows.Close()
			return 0, err
		}
		if _, ok := names[strings.TrimPrefix(trustUserKey("", name), "name:")]; ok {
			ids = append(ids, id)
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return 0, err
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}
	var total int64
	for _, id := range ids {
		n, err := execCount(ctx, tx, `DELETE FROM violations WHERE id = ?`, id)
		if err != nil {
			return 0, err
		}
		total += n
	}
	return total, nil
}

func queryStringsTx(ctx context.Context, tx *sql.Tx, q string, args ...any) ([]string, error) {
	rows, err := tx.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func execCount(ctx context.Context, x execer, q string, args ...any) (int64, error) {
	res, err := x.ExecContext(ctx, q, args...)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// remainingForUser counts the rows that still carry userID directly. The
// post-condition after EraseUser is 0.
func (m *Module) remainingForUser(ctx context.Context, userID string) (int, error) {
	db, err := m.dbConn()
	if err != nil {
		return 0, err
	}
	ownKey := "id:" + userID
	total := 0
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`SELECT COUNT(1) FROM violations WHERE user_id = ?`, []any{userID}},
		{`SELECT COUNT(1) FROM trust_scores WHERE user_id = ? OR user_key = ?`, []any{userID, ownKey}},
		{`SELECT COUNT(1) FROM user_aliases WHERE canonical_user_id = ? OR alias_key = ?`, []any{userID, ownKey}},
	} {
		var n int
		if err := db.QueryRowContext(ctx, q.sql, q.args...).Scan(&n); err != nil {
			return 0, err
		}
		total += n
	}
	return total, nil
}

// userErased reports whether writes naming userID must be refused. It checks
// the in-memory ledger hint (an id seen in the ledger but possibly not yet
// applied) and the persisted erasure_applied record, so the refusal survives a
// restart. An empty id is never erased.
func (m *Module) userErased(ctx context.Context, userID string) (bool, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return false, nil
	}
	if m.ledgerErased(userID) {
		return true, nil
	}
	db, err := m.dbConn()
	if err != nil {
		return false, err
	}
	return userErasedTx(ctx, db, userID)
}

type queryRower interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func userErasedTx(ctx context.Context, q queryRower, userID string) (bool, error) {
	var one int
	err := q.QueryRowContext(ctx, `SELECT 1 FROM erasure_applied WHERE user_id = ? LIMIT 1`, userID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func (m *Module) ledgerErased(userID string) bool {
	m.mu.RLock()
	rec := m.reconciler
	m.mu.RUnlock()
	return rec != nil && rec.Erased(userID)
}

// coreFinder answers FindByCapability through whichever core connection the
// module currently holds, so the reconciler survives a core reconnect.
type coreFinder struct{ m *Module }

func (f coreFinder) FindByCapability(ctx context.Context, in *discoveryv1.FindByCapabilityRequest, opts ...grpc.CallOption) (*discoveryv1.FindByCapabilityResponse, error) {
	mc := f.m.eventClient()
	if mc == nil {
		return nil, errors.New("not connected to core")
	}
	return mc.Discovery.Raw().FindByCapability(ctx, in, opts...)
}

// startReconciler starts the ADR-0035 reconciler once, after the module has a
// core connection. It stops with Stop. tune adjusts the reconciler config
// (tests only).
func (m *Module) startReconciler(ctx context.Context, dialer *erasure.ProviderDialer, tune ...func(*erasure.Config)) error {
	cfg := erasure.Config{Owner: guardOwner{m: m}, Dialer: dialer}
	if _, err := erasure.IntervalFromEnv(os.Getenv); err != nil {
		// A bad ERASURE_SWEEP_INTERVAL must not switch erasure off.
		slog.Error("playback-guard: invalid ERASURE_SWEEP_INTERVAL; using the default", "error", err)
		cfg.Interval = erasure.DefaultInterval
	}
	for _, f := range tune {
		f(&cfg)
	}
	rec, err := erasure.New(cfg)
	if err != nil {
		return err
	}

	m.mu.Lock()
	if m.stopped || m.reconciler != nil {
		m.mu.Unlock()
		return nil
	}
	runCtx, cancel := context.WithCancel(ctx)
	m.reconciler = rec
	m.reconcileCancel = cancel
	m.reconcileWG.Add(1)
	m.mu.Unlock()

	go func() {
		defer m.reconcileWG.Done()
		if err := rec.Run(runCtx); err != nil && !errors.Is(err, context.Canceled) {
			slog.Error("playback-guard: erasure reconciler stopped", "error", err)
		}
	}()
	return nil
}

// stopReconciler cancels the reconciler and waits for it. It must run before
// the database is closed and without m.mu held (the reconciler takes it).
func (m *Module) stopReconciler() {
	m.mu.Lock()
	m.stopped = true
	cancel := m.reconcileCancel
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	m.reconcileWG.Wait()
}

// ErasureStatus returns the last reconciler sweep, for health output.
func (m *Module) ErasureStatus() (erasure.Status, bool) {
	m.mu.RLock()
	rec := m.reconciler
	m.mu.RUnlock()
	if rec == nil {
		return erasure.Status{}, false
	}
	return rec.Status(), true
}

func (m *Module) startReconcilerFromCore(ctx context.Context) {
	if err := m.startReconciler(ctx, &erasure.ProviderDialer{Discovery: coreFinder{m: m}}); err != nil {
		slog.Error("playback-guard: erasure reconciler not started", "error", err)
	}
}
