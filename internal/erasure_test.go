package internal

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/codes"

	playbackevents "github.com/Muxcore-Media/contracts-playback/events"
	authv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/auth/v1"
	eventsv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/events/v1"
	"github.com/Muxcore-Media/core/sdk/go/module/erasure"
	"github.com/Muxcore-Media/core/sdk/go/module/erasure/erasuretest"
	"github.com/Muxcore-Media/core/sdk/go/module/meshtls"
	guardv1 "github.com/Muxcore-Media/playback-guard/proto/guardv1"
	monitorv1 "github.com/Muxcore-Media/playback-monitor/proto/monitorv1"
)

const (
	testProviderID = "auth-local"
	testOwnerID    = "playback-guard"

	victim    = "u-victim"
	bystander = "u-bystander"
)

type syncBuf struct {
	b  bytes.Buffer
	mu sync.Mutex
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func clearMeshEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"MUXCORE_INSECURE_DISABLE_TLS", "MUXCORE_DEV_TLS_SKIP", "MUXCORE_GRPC_INSECURE",
		meshtls.EnvTLSCert, meshtls.EnvTLSKey, meshtls.EnvTLSCA, meshtls.EnvTLSServerName,
		"MUXCORE_PROFILE", "MUXCORE_MESH_DIAL_LOCAL", "MUXCORE_MODULE_ID", erasure.EnvSweepInterval} {
		t.Setenv(k, "")
	}
}

// guardHarness is a seeded module plus a fake auth-local ledger served over
// real mTLS and a fake core discovery.
type guardHarness struct {
	m        *Module
	pki      *erasuretest.PKI
	provider *erasuretest.Provider
	disc     *erasuretest.Discovery
	dialer   *erasure.ProviderDialer
	logs     *syncBuf
}

func newGuardHarness(t *testing.T) *guardHarness {
	t.Helper()
	clearMeshEnv(t)
	h := &guardHarness{m: testModule(t), pki: erasuretest.NewPKI(t), provider: erasuretest.NewProvider(), logs: &syncBuf{}}
	h.provider.Allowed = map[string]bool{testOwnerID: true}
	sc, sk := h.pki.Issue(t, testProviderID)
	addr := erasuretest.ServeTLS(t, h.provider, sc, sk, h.pki.CAFile)
	h.disc = erasuretest.NewDiscovery(erasuretest.Module(testProviderID, addr))
	cc, ck := h.pki.Issue(t, testOwnerID)
	h.dialer = &erasure.ProviderDialer{Discovery: h.disc, CertFile: cc, KeyFile: ck, CAFile: h.pki.CAFile}
	return h
}

func (h *guardHarness) tune(cfg *erasure.Config) {
	cfg.Logger = slog.New(slog.NewTextHandler(h.logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	cfg.AckBackoff = time.Millisecond
	cfg.RetryBackoff = 10 * time.Millisecond
	cfg.CallTimeout = 5 * time.Second
}

// reconciler builds a reconciler that is not running; tests drive it with
// SweepOnce. With register it is also installed as the module's in-memory
// ledger hint, as startReconciler does.
func (h *guardHarness) reconciler(t *testing.T, register bool) *erasure.Reconciler {
	t.Helper()
	cfg := erasure.Config{Owner: guardOwner{m: h.m}, Dialer: h.dialer}
	h.tune(&cfg)
	cfg.Interval = time.Minute
	r, err := erasure.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if register {
		h.m.mu.Lock()
		h.m.reconciler = r
		h.m.mu.Unlock()
	}
	return r
}

func sweepOnce(t *testing.T, r *erasure.Reconciler) (erasure.Result, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return r.SweepOnce(ctx)
}

func mustDB(t *testing.T, m *Module) *sql.DB {
	t.Helper()
	db, err := m.dbConn()
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func mustExec(t *testing.T, m *Module, q string, args ...any) {
	t.Helper()
	if _, err := mustDB(t, m).ExecContext(context.Background(), q, args...); err != nil {
		t.Fatalf("exec %q: %v", q, err)
	}
}

func insViolation(t *testing.T, m *Module, id, userID, userName string) {
	t.Helper()
	mustExec(t, m, `INSERT INTO violations(id, rule_id, rule_type, user_id, user_name, summary, severity, acknowledged, created_at)
		VALUES (?, 'r1', 'concurrent_streams', ?, ?, 's', 'warning', 0, ?)`, id, userID, userName, nowRFC3339())
}

func insTrust(t *testing.T, m *Module, key, userID, userName string, score int) {
	t.Helper()
	mustExec(t, m, `INSERT INTO trust_scores(user_key, user_id, user_name, score, updated_at) VALUES (?, ?, ?, ?, ?)`,
		key, userID, userName, score, nowRFC3339())
}

func insAlias(t *testing.T, m *Module, key, canonID, canonName string) {
	t.Helper()
	mustExec(t, m, `INSERT INTO user_aliases(alias_key, canonical_user_id, canonical_user_name, created_at) VALUES (?, ?, ?, ?)`,
		key, canonID, canonName, nowRFC3339())
}

// seedGuard writes the fixture described in the comments. Ids of rows that
// the victim's erasure must remove start with "gone-"; every other id must
// survive untouched.
func seedGuard(t *testing.T, m *Module) {
	t.Helper()
	// victim, keyed by id
	insViolation(t, m, "gone-v1", victim, "alice")
	insViolation(t, m, "gone-v2", victim, "alice")
	insTrust(t, m, "id:"+victim, victim, "alice", 80)
	// aliases that merge other identities INTO the victim (canonical = victim),
	// and the rows reachable through them
	insAlias(t, m, "id:ext-1", victim, "alice")
	insViolation(t, m, "gone-v3", "ext-1", "alice-jf")
	insTrust(t, m, "id:ext-1", "ext-1", "alice-jf", 70)
	insAlias(t, m, "name:alice-old", victim, "alice")
	insViolation(t, m, "gone-v4", "", "  Alice-Old ") // trustUserKey normalises to name:alice-old
	insTrust(t, m, "name:alice-old", "", "Alice-Old", 70)
	// the victim merged into the bystander: an alias keyed by the victim's id
	insAlias(t, m, "id:"+victim, bystander, "bob")
	// bystander, with its own aliases and their rows
	insViolation(t, m, "keep-v5", bystander, "bob")
	insTrust(t, m, "id:"+bystander, bystander, "bob", 90)
	insAlias(t, m, "id:ext-2", bystander, "bob")
	insViolation(t, m, "keep-v6", "ext-2", "bob-jf")
	insTrust(t, m, "id:ext-2", "ext-2", "bob-jf", 60)
	insAlias(t, m, "name:bob-old", bystander, "bob")
	insViolation(t, m, "keep-v7", "", "bob-old")
	insTrust(t, m, "name:bob-old", "", "bob-old", 60)
	// keyed by a name or an external id that nothing links to the victim
	insViolation(t, m, "keep-v8", "", "carol")
	insTrust(t, m, "name:carol", "", "carol", 90)
	insViolation(t, m, "keep-v9", "ext-3", "dave-jf")
	insTrust(t, m, "id:ext-3", "ext-3", "dave-jf", 90)
}

// dump returns every row of the three personal-data tables as sorted strings.
func dump(t *testing.T, m *Module) map[string][]string {
	t.Helper()
	db := mustDB(t, m)
	out := map[string][]string{}
	for table, q := range map[string]string{
		"violations":   `SELECT id, user_id, user_name, summary, acknowledged FROM violations`,
		"trust_scores": `SELECT user_key, user_id, user_name, score FROM trust_scores`,
		"user_aliases": `SELECT alias_key, canonical_user_id, canonical_user_name FROM user_aliases`,
	} {
		rows, err := db.QueryContext(context.Background(), q)
		if err != nil {
			t.Fatal(err)
		}
		cols, _ := rows.Columns()
		for rows.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				t.Fatal(err)
			}
			parts := make([]string, len(vals))
			for i, v := range vals {
				parts[i] = fmt.Sprint(v)
			}
			out[table] = append(out[table], strings.Join(parts, "|"))
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		_ = rows.Close()
		sort.Strings(out[table])
	}
	return out
}

// without returns d minus the rows whose first column is listed in drop.
func without(d map[string][]string, drop map[string][]string) map[string][]string {
	out := map[string][]string{}
	for table, rows := range d {
		for _, r := range rows {
			if !slices.Contains(drop[table], strings.SplitN(r, "|", 2)[0]) {
				out[table] = append(out[table], r)
			}
		}
	}
	return out
}

var victimRows = map[string][]string{
	"violations":   {"gone-v1", "gone-v2", "gone-v3", "gone-v4"},
	"trust_scores": {"id:" + victim, "id:ext-1", "name:alice-old"},
	"user_aliases": {"id:ext-1", "name:alice-old", "id:" + victim},
}

func sameDump(t *testing.T, got, want map[string][]string) {
	t.Helper()
	for _, table := range []string{"violations", "trust_scores", "user_aliases"} {
		if !slices.Equal(got[table], want[table]) {
			t.Errorf("%s rows differ\n got: %q\nwant: %q", table, got[table], want[table])
		}
	}
}

func count(t *testing.T, m *Module, q string, args ...any) int {
	t.Helper()
	var n int
	if err := mustDB(t, m).QueryRowContext(context.Background(), q, args...).Scan(&n); err != nil {
		t.Fatalf("count %q: %v", q, err)
	}
	return n
}

func appliedRows(t *testing.T, m *Module) int {
	return count(t, m, `SELECT COUNT(1) FROM erasure_applied`)
}

func latestAck(t *testing.T, p *erasuretest.Provider, erasureID string) erasuretest.Ack {
	t.Helper()
	a, ok := p.Latest(erasureID, testOwnerID)
	if !ok {
		t.Fatalf("no acknowledgement for %s", erasureID)
	}
	return a
}

// ---------------------------------------------------------------------------

func TestOwnerModuleID(t *testing.T) {
	clearMeshEnv(t)
	m := testModule(t)
	if got := (guardOwner{m: m}).ModuleID(); got != "playback-guard" {
		t.Fatalf("ModuleID = %q", got)
	}
	t.Setenv("MUXCORE_MODULE_ID", "guard-two")
	if got := (guardOwner{m: m}).ModuleID(); got != "guard-two" {
		t.Fatalf("ModuleID with env = %q", got)
	}
}

func TestEraseVictimKeepsBystanderAndSecondSweepIsNoop(t *testing.T) {
	h := newGuardHarness(t)
	seedGuard(t, h.m)
	before := dump(t, h.m)
	eid := h.provider.AddErasure(victim, "")
	r := h.reconciler(t, true)

	res, err := sweepOnce(t, r)
	if err != nil {
		t.Fatalf("sweep: %v (%+v)", err, res)
	}
	if res.Applied != 1 || res.Acked != 1 || res.Failed != 0 {
		t.Fatalf("result = %+v", res)
	}

	// Exactly the victim's rows are gone; everything else is byte-identical.
	after := dump(t, h.m)
	sameDump(t, after, without(before, victimRows))
	if n, _ := h.m.remainingForUser(context.Background(), victim); n != 0 {
		t.Fatalf("post-condition: %d rows still carry the id", n)
	}

	// Retained with reason: name-keyed and external-id rows no alias links.
	for _, k := range []string{"keep-v8", "keep-v9"} {
		if count(t, h.m, `SELECT COUNT(1) FROM violations WHERE id = ?`, k) != 1 {
			t.Errorf("unlinked row %s was removed", k)
		}
	}

	a := latestAck(t, h.provider, eid)
	if a.Outcome != authv1.ErasureOutcome_ERASURE_OUTCOME_OK {
		t.Fatalf("ack = %+v", a)
	}
	want := map[string]int64{countViolations: 4, countTrust: 3, countAliases: 3}
	for k, v := range want {
		if a.Counts[k] != v {
			t.Errorf("ack count %s = %d, want %d (%v)", k, a.Counts[k], v, a.Counts)
		}
	}
	if appliedRows(t, h.m) != 1 {
		t.Fatalf("erasure_applied rows = %d", appliedRows(t, h.m))
	}
	var uid, applied, countsJSON string
	if err := mustDB(t, h.m).QueryRow(`SELECT user_id, applied_at, counts_json FROM erasure_applied WHERE erasure_id = ?`, eid).Scan(&uid, &applied, &countsJSON); err != nil {
		t.Fatal(err)
	}
	if uid != victim || applied == "" || !strings.Contains(countsJSON, `"violations":4`) {
		t.Fatalf("applied row = %q %q %q", uid, applied, countsJSON)
	}

	// A second sweep changes nothing.
	snap := dump(t, h.m)
	res2, err := sweepOnce(t, r)
	if err != nil {
		t.Fatalf("second sweep: %v", err)
	}
	if res2.Applied != 0 || res2.Skipped != 1 || res2.Failed != 0 {
		t.Fatalf("second sweep result = %+v", res2)
	}
	sameDump(t, dump(t, h.m), snap)
	if appliedRows(t, h.m) != 1 {
		t.Fatalf("erasure_applied rows after second sweep = %d", appliedRows(t, h.m))
	}
}

func TestEraseUserIsIdempotentAndReturnsRecordedCounts(t *testing.T) {
	m := testModule(t)
	seedGuard(t, m)
	tomb := erasure.Tombstone{ErasureID: "e-1", UserID: victim}
	first, err := m.EraseUser(context.Background(), tomb)
	if err != nil {
		t.Fatal(err)
	}
	// New victim-keyed data arrives out of band (e.g. a restored backup row
	// written under a different code path). Re-applying the same erasure id
	// must not touch it: the record says it was applied.
	insViolation(t, m, "late", victim, "alice")
	second, err := m.EraseUser(context.Background(), tomb)
	if err != nil {
		t.Fatal(err)
	}
	if first[countViolations] != 4 || second[countViolations] != 4 {
		t.Fatalf("counts first=%v second=%v", first, second)
	}
	if count(t, m, `SELECT COUNT(1) FROM violations WHERE id = 'late'`) != 1 {
		t.Fatal("re-applying an applied erasure id must be a no-op")
	}
	if n, _ := (guardOwner{m: m}).Verify(context.Background(), tomb); n != 1 {
		t.Fatalf("Verify must report the leftover row, got %d", n)
	}
	if _, err := m.EraseUser(context.Background(), erasure.Tombstone{ErasureID: "e-2"}); err == nil {
		t.Fatal("tombstone without a user id must be rejected")
	}
	if _, err := m.EraseUser(context.Background(), erasure.Tombstone{ErasureID: "e-3", UserID: " u "}); err == nil {
		t.Fatal("padded user id must be rejected")
	}
}

func TestMidTransactionFailureRollsBackAndNextSweepCompletes(t *testing.T) {
	h := newGuardHarness(t)
	seedGuard(t, h.m)
	before := dump(t, h.m)
	eid := h.provider.AddErasure(victim, "")
	r := h.reconciler(t, false)

	// The deletions run first; failing the final INSERT of the applied record
	// is a failure in the middle of the transaction.
	mustExec(t, h.m, `CREATE TRIGGER inject_fail BEFORE INSERT ON erasure_applied
		BEGIN SELECT RAISE(ABORT, 'injected failure'); END`)
	res, err := sweepOnce(t, r)
	if err == nil || res.Failed != 1 || res.Applied != 0 {
		t.Fatalf("sweep with injected failure: res=%+v err=%v", res, err)
	}
	sameDump(t, dump(t, h.m), before) // every deletion rolled back
	if appliedRows(t, h.m) != 0 {
		t.Fatalf("applied record written despite failure")
	}
	if a := latestAck(t, h.provider, eid); a.Outcome != authv1.ErasureOutcome_ERASURE_OUTCOME_FAILED || a.Detail != erasure.DetailApplyFailed {
		t.Fatalf("ack = %+v", a)
	}
	if strings.Contains(h.logs.String(), victim) {
		t.Fatalf("the erased user id was logged:\n%s", h.logs.String())
	}

	mustExec(t, h.m, `DROP TRIGGER inject_fail`)
	res, err = sweepOnce(t, r)
	if err != nil || res.Applied != 1 || res.Failed != 0 {
		t.Fatalf("next sweep: res=%+v err=%v", res, err)
	}
	sameDump(t, dump(t, h.m), without(before, victimRows))
	if n, _ := h.m.remainingForUser(context.Background(), victim); n != 0 {
		t.Fatalf("post-condition: %d", n)
	}
	if a := latestAck(t, h.provider, eid); a.Outcome != authv1.ErasureOutcome_ERASURE_OUTCOME_OK {
		t.Fatalf("final ack = %+v", a)
	}
}

func TestVerifyDetectsRemainingRows(t *testing.T) {
	m := testModule(t)
	seedGuard(t, m)
	tomb := erasure.Tombstone{ErasureID: "e-1", UserID: victim}
	owner := guardOwner{m: m}
	// Carrying the id directly: 2 violations, trust id:u-victim, and 3 aliases
	// (ext-1 and alice-old point at it, id:u-victim is keyed by it).
	if n, err := owner.Verify(context.Background(), tomb); err != nil || n != 6 {
		t.Fatalf("Verify before erasure = %d, %v", n, err)
	}
	if _, err := m.EraseUser(context.Background(), tomb); err != nil {
		t.Fatal(err)
	}
	if n, err := owner.Verify(context.Background(), tomb); err != nil || n != 0 {
		t.Fatalf("Verify after erasure = %d, %v", n, err)
	}
}

func TestAliasHandling(t *testing.T) {
	m := testModule(t)
	seedGuard(t, m)
	if _, err := m.EraseUser(context.Background(), erasure.Tombstone{ErasureID: "e-1", UserID: victim}); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"id:ext-1", "name:alice-old", "id:" + victim} {
		if count(t, m, `SELECT COUNT(1) FROM user_aliases WHERE alias_key = ?`, key) != 0 {
			t.Errorf("alias %s survived", key)
		}
	}
	if count(t, m, `SELECT COUNT(1) FROM user_aliases WHERE canonical_user_id = ?`, victim) != 0 {
		t.Error("an alias still points at the erased canonical user")
	}
	// The bystander's aliases and the rows reachable through them are intact.
	for _, key := range []string{"id:ext-2", "name:bob-old"} {
		if count(t, m, `SELECT COUNT(1) FROM user_aliases WHERE alias_key = ? AND canonical_user_id = ?`, key, bystander) != 1 {
			t.Errorf("bystander alias %s was touched", key)
		}
	}
	for _, id := range []string{"keep-v6", "keep-v7"} {
		if count(t, m, `SELECT COUNT(1) FROM violations WHERE id = ?`, id) != 1 {
			t.Errorf("bystander violation %s was touched", id)
		}
	}
	for _, key := range []string{"id:ext-2", "name:bob-old", "id:" + bystander} {
		if count(t, m, `SELECT COUNT(1) FROM trust_scores WHERE user_key = ?`, key) != 1 {
			t.Errorf("bystander trust score %s was touched", key)
		}
	}
}

func TestBusEventCannotErase(t *testing.T) {
	h := newGuardHarness(t)
	seedGuard(t, h.m)
	before := dump(t, h.m)
	// The module's only event input is the playback handler. A user-deleted
	// or erasure hint event, however well-formed, must not change anything.
	for _, typ := range []string{"identity.user.deleted", "identity.erasure.recorded", playbackevents.EventPlaybackStopped} {
		payload, _ := json.Marshal(map[string]string{"user_id": victim, "erasure_id": "forged"})
		h.m.handlePlaybackEvent(context.Background(), typ, &eventsv1.Event{Type: typ, Source: "auth-local", Payload: payload})
	}
	sameDump(t, dump(t, h.m), before)
	if appliedRows(t, h.m) != 0 {
		t.Fatal("an event produced an erasure record")
	}
	// And a ledger with no tombstone for the victim erases nothing.
	h.provider.AddErasure("someone-else", "")
	if res, err := sweepOnce(t, h.reconciler(t, false)); err != nil || res.Applied != 1 {
		t.Fatalf("sweep: %+v %v", res, err)
	}
	sameDump(t, dump(t, h.m), before)
}

func TestLedgerFromWrongCNIsNotActedOn(t *testing.T) {
	clearMeshEnv(t)
	m := testModule(t)
	seedGuard(t, m)
	before := dump(t, m)
	pki := erasuretest.NewPKI(t)
	provider := erasuretest.NewProvider()
	provider.AddErasure(victim, "")
	// Same CA, certificate valid for "auth-local", but the CN is another module.
	sc, sk := pki.Issue(t, "evil-module", testProviderID, "localhost")
	addr := erasuretest.ServeTLS(t, provider, sc, sk, pki.CAFile)
	cc, ck := pki.Issue(t, testOwnerID)
	h := &guardHarness{m: m, logs: &syncBuf{}}
	h.dialer = &erasure.ProviderDialer{Discovery: erasuretest.NewDiscovery(erasuretest.Module(testProviderID, addr)), CertFile: cc, KeyFile: ck, CAFile: pki.CAFile}
	res, err := sweepOnce(t, h.reconciler(t, false))
	if err == nil || res.Applied != 0 {
		t.Fatalf("wrong-CN ledger: res=%+v err=%v", res, err)
	}
	sameDump(t, dump(t, m), before)
	if appliedRows(t, m) != 0 {
		t.Fatal("recorded an erasure from a wrong-CN ledger")
	}
}

func TestProviderUnreachableErasesNothing(t *testing.T) {
	h := newGuardHarness(t)
	seedGuard(t, h.m)
	before := dump(t, h.m)
	h.provider.AddErasure(victim, "")
	h.provider.SetListError(errors.New("provider down"))
	res, err := sweepOnce(t, h.reconciler(t, false))
	if err == nil || res.Applied != 0 {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	sameDump(t, dump(t, h.m), before)
	// A discovery that finds no provider is equally inert.
	h.disc.Set("identity")
	res, err = sweepOnce(t, h.reconciler(t, false))
	if err == nil || res.Applied != 0 {
		t.Fatalf("no provider: res=%+v err=%v", res, err)
	}
	sameDump(t, dump(t, h.m), before)
}

func TestLateWritesForErasedUserAreRefused(t *testing.T) {
	m := testModule(t)
	ctx := context.Background()
	seedGuard(t, m)
	if _, err := m.EraseUser(ctx, erasure.Tombstone{ErasureID: "e-1", UserID: victim}); err != nil {
		t.Fatal(err)
	}
	snap := dump(t, m)

	if err := m.recordViolation(ctx, "r1", guardv1.RuleType_RULE_TYPE_CONCURRENT_STREAMS, victim, "alice", "late", "warning"); !errors.Is(err, errUserErased) {
		t.Fatalf("recordViolation err = %v", err)
	}
	if err := m.adjustTrustOnViolation(ctx, victim, "alice", 10); !errors.Is(err, errUserErased) {
		t.Fatalf("adjustTrustOnViolation err = %v", err)
	}
	if _, err := m.resetTrustScore(ctx, victim, "alice"); !errors.Is(err, errUserErased) {
		t.Fatalf("resetTrustScore err = %v", err)
	}
	if _, err := m.ResetTrustScore(ctx, &guardv1.ResetTrustScoreRequest{UserId: victim}); err == nil {
		t.Fatal("ResetTrustScore RPC must refuse")
	}
	if _, _, err := m.mergeUsers(ctx, "ext-9", "x", victim, "alice"); !errors.Is(err, errUserErased) {
		t.Fatalf("merge into erased target err = %v", err)
	}
	if _, _, err := m.mergeUsers(ctx, victim, "alice", bystander, "bob"); !errors.Is(err, errUserErased) {
		t.Fatalf("merge from erased source err = %v", err)
	}
	sameDump(t, dump(t, m), snap)

	// A playback event for the erased id that trips a rule is dropped end to
	// end: the evaluator records nothing.
	if _, err := m.UpsertRule(ctx, &guardv1.UpsertRuleRequest{Rule: &guardv1.GuardRule{
		Type: guardv1.RuleType_RULE_TYPE_CONCURRENT_STREAMS, Name: "Max 1", Enabled: true,
		Params: map[string]string{"max_streams": "1"},
	}}); err != nil {
		t.Fatal(err)
	}
	m.monitorOverride = &fakeMonitorClient{sessions: []*monitorv1.SessionRecord{
		{UserId: victim, UserName: "alice"}, {UserId: victim, UserName: "alice"}, {UserId: victim, UserName: "alice"},
	}}
	payload, _ := json.Marshal(playbackEvent{UserID: victim, UserName: "alice"})
	m.handlePlaybackEvent(ctx, playbackevents.EventPlaybackStarted, &eventsv1.Event{Payload: payload})
	sameDump(t, dump(t, m), snap)
	// Positive control: the same event shape for a live user does record.
	m.monitorOverride = &fakeMonitorClient{sessions: []*monitorv1.SessionRecord{
		{UserId: bystander, UserName: "bob"}, {UserId: bystander, UserName: "bob"}, {UserId: bystander, UserName: "bob"},
	}}
	payload, _ = json.Marshal(playbackEvent{UserID: bystander, UserName: "bob"})
	m.handlePlaybackEvent(ctx, playbackevents.EventPlaybackStarted, &eventsv1.Event{Payload: payload})
	if n := count(t, m, `SELECT COUNT(1) FROM violations WHERE user_id = ? AND summary LIKE '%active streams%'`, bystander); n != 1 {
		t.Fatalf("control: bystander streams violation count = %d, want 1", n)
	}

	// The bystander is unaffected: writes still work.
	if _, err := m.resetTrustScore(ctx, bystander, "bob"); err != nil {
		t.Fatalf("bystander reset: %v", err)
	}
	if _, _, err := m.mergeUsers(ctx, "ext-5", "e5", bystander, "bob"); err != nil {
		t.Fatalf("bystander merge: %v", err)
	}
	if n := count(t, m, `SELECT COUNT(1) FROM violations WHERE user_id = ?`, victim); n != 0 {
		t.Fatalf("%d violations carry the erased id", n)
	}
	if n, _ := m.remainingForUser(ctx, victim); n != 0 {
		t.Fatalf("post-condition after refused writes = %d", n)
	}
}

func TestLateWriteRefusalSurvivesRestart(t *testing.T) {
	clearMeshEnv(t)
	ctx := context.Background()
	dbPath := t.TempDir() + "/guard.db"
	m1 := NewModule(Config{DBPath: dbPath, GRPCAddr: "127.0.0.1:0"})
	if err := m1.Init(ctx); err != nil {
		t.Fatal(err)
	}
	seedGuard(t, m1)
	if _, err := m1.EraseUser(ctx, erasure.Tombstone{ErasureID: "e-1", UserID: victim}); err != nil {
		t.Fatal(err)
	}
	if err := m1.Stop(ctx); err != nil {
		t.Fatal(err)
	}

	// A new process: no reconciler, no in-memory Erased set. Only the table.
	m2 := NewModule(Config{DBPath: dbPath, GRPCAddr: "127.0.0.1:0"})
	if err := m2.Init(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m2.Stop(ctx) })
	if m2.ledgerErased(victim) {
		t.Fatal("test premise: the in-memory set must be empty after restart")
	}
	if err := m2.recordViolation(ctx, "r1", guardv1.RuleType_RULE_TYPE_CONCURRENT_STREAMS, victim, "alice", "late", "warning"); !errors.Is(err, errUserErased) {
		t.Fatalf("after restart: recordViolation err = %v", err)
	}
	if err := m2.adjustTrustOnViolation(ctx, victim, "alice", 10); !errors.Is(err, errUserErased) {
		t.Fatalf("after restart: adjust err = %v", err)
	}
	if _, err := m2.resetTrustScore(ctx, victim, "alice"); !errors.Is(err, errUserErased) {
		t.Fatalf("after restart: reset err = %v", err)
	}
	if _, _, err := m2.mergeUsers(ctx, "x", "x", victim, "alice"); !errors.Is(err, errUserErased) {
		t.Fatalf("after restart: merge err = %v", err)
	}
	if erased, err := m2.userErased(ctx, victim); err != nil || !erased {
		t.Fatalf("userErased = %v, %v", erased, err)
	}
	if erased, _ := m2.userErased(ctx, bystander); erased {
		t.Fatal("bystander reported erased")
	}
	if n, _ := m2.remainingForUser(ctx, victim); n != 0 {
		t.Fatalf("post-condition after restart = %d", n)
	}
	// The applied record is what the reconciler consults on its first sweep.
	if ok, err := (guardOwner{m: m2}).Applied(ctx, "e-1"); err != nil || !ok {
		t.Fatalf("Applied after restart = %v, %v", ok, err)
	}
	if ok, _ := (guardOwner{m: m2}).Applied(ctx, "e-other"); ok {
		t.Fatal("unknown erasure reported applied")
	}
}

func TestPendingTombstoneRefusesWritesBeforeApply(t *testing.T) {
	h := newGuardHarness(t)
	seedGuard(t, h.m)
	h.provider.AddErasure(victim, "")
	r := h.reconciler(t, true)
	mustExec(t, h.m, `CREATE TRIGGER inject_fail BEFORE INSERT ON erasure_applied
		BEGIN SELECT RAISE(ABORT, 'injected failure'); END`)
	if _, err := sweepOnce(t, r); err == nil {
		t.Fatal("expected the sweep to fail")
	}
	if appliedRows(t, h.m) != 0 {
		t.Fatal("premise: nothing applied")
	}
	// Tombstoned but not yet erased: the ledger hint already refuses new rows.
	if err := h.m.recordViolation(context.Background(), "r1", guardv1.RuleType_RULE_TYPE_CONCURRENT_STREAMS, victim, "alice", "late", "warning"); !errors.Is(err, errUserErased) {
		t.Fatalf("recordViolation during pending erasure: %v", err)
	}
}

func TestSchemaHasErasureAppliedTable(t *testing.T) {
	m := testModule(t)
	db := mustDB(t, m)
	rows, err := db.Query(`SELECT name FROM pragma_table_info('erasure_applied') ORDER BY cid`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var cols []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			t.Fatal(err)
		}
		cols = append(cols, c)
	}
	want := []string{"erasure_id", "user_id", "tenant_id", "applied_at", "counts_json"}
	if !slices.Equal(cols, want) {
		t.Fatalf("columns = %v, want %v", cols, want)
	}
}

// ---------------------------------------------------------------------------
// Lifecycle

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestReconcilerLifecycleErasesOnStartupAndStopsCleanly(t *testing.T) {
	h := newGuardHarness(t)
	seedGuard(t, h.m)
	before := dump(t, h.m)
	eid := h.provider.AddErasure(victim, "")
	baseline := runtime.NumGoroutine()

	if err := h.m.startReconciler(context.Background(), h.dialer, h.tune); err != nil {
		t.Fatal(err)
	}
	// Starting twice is a no-op, not a second reconciler.
	if err := h.m.startReconciler(context.Background(), h.dialer, h.tune); err != nil {
		t.Fatal(err)
	}
	// The startup sweep erases without any trigger.
	waitFor(t, "startup sweep to erase the victim", func() bool { return appliedRows(t, h.m) == 1 })
	waitFor(t, "acknowledgement", func() bool {
		a, ok := h.provider.Latest(eid, testOwnerID)
		return ok && a.Outcome == authv1.ErasureOutcome_ERASURE_OUTCOME_OK
	})
	sameDump(t, dump(t, h.m), without(before, victimRows))
	if st, ok := h.m.ErasureStatus(); !ok || st.Sweeps < 1 {
		t.Fatalf("status = %+v %v", st, ok)
	}

	// A tombstone added later is picked up on an operator trigger.
	eid2 := h.provider.AddErasure(bystander, "")
	h.m.mu.RLock()
	rec := h.m.reconciler
	h.m.mu.RUnlock()
	rec.Trigger()
	waitFor(t, "triggered sweep", func() bool { return appliedRows(t, h.m) == 2 })
	waitFor(t, "second acknowledgement", func() bool { _, ok := h.provider.Latest(eid2, testOwnerID); return ok })

	if err := h.m.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Stop returned only after the reconciler goroutine exited.
	done := make(chan struct{})
	go func() { h.m.reconcileWG.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("reconciler goroutine still running after Stop")
	}
	waitFor(t, "goroutines to settle", func() bool { return runtime.NumGoroutine() <= baseline })

	// Nothing starts after Stop.
	h.m.mu.Lock()
	h.m.reconciler = nil
	h.m.mu.Unlock()
	if err := h.m.startReconciler(context.Background(), h.dialer, h.tune); err != nil {
		t.Fatal(err)
	}
	if _, ok := h.m.ErasureStatus(); ok {
		t.Fatal("reconciler started after Stop")
	}
}

func TestInvalidSweepIntervalDoesNotDisableErasure(t *testing.T) {
	h := newGuardHarness(t)
	t.Setenv(erasure.EnvSweepInterval, "not-a-duration")
	seedGuard(t, h.m)
	h.provider.AddErasure(victim, "")
	// tune leaves Interval alone, so startReconciler's own handling of the bad
	// environment value is what runs.
	if err := h.m.startReconciler(context.Background(), h.dialer, h.tune); err != nil {
		t.Fatalf("a bad ERASURE_SWEEP_INTERVAL must not stop erasure from starting: %v", err)
	}
	waitFor(t, "erasure despite a bad interval", func() bool { return appliedRows(t, h.m) == 1 })
}

func TestStartReconcilerWithoutCoreFindsNoProvider(t *testing.T) {
	clearMeshEnv(t)
	m := testModule(t)
	f := coreFinder{m: m}
	if _, err := f.FindByCapability(context.Background(), nil); err == nil {
		t.Fatal("expected an error while not connected to core")
	}
}

func TestInjectedAckFailureCodesAreRetried(t *testing.T) {
	h := newGuardHarness(t)
	seedGuard(t, h.m)
	eid := h.provider.AddErasure(victim, "")
	h.provider.FailAcks(2, codes.Unavailable)
	r := h.reconciler(t, false)
	res, err := sweepOnce(t, r)
	if err != nil || res.Applied != 1 {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if a := latestAck(t, h.provider, eid); a.Outcome != authv1.ErasureOutcome_ERASURE_OUTCOME_OK {
		t.Fatalf("ack = %+v", a)
	}
}
