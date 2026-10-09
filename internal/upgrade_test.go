package internal

import (
	"context"
	"path/filepath"
	"slices"
	"testing"

	"github.com/Muxcore-Media/core/sdk/go/module/erasure"
	"github.com/Muxcore-Media/core/sdk/go/module/moduletest"
	guardv1 "github.com/Muxcore-Media/playback-guard/proto/guardv1"
)

// upgradeSnapshots lists committed snapshots produced by the named tag's own
// code (ADR-0015). v0.1.2 is the release this change upgrades from; it has no
// erasure_applied table. See testdata/upgrade/README.md.
var upgradeSnapshots = []string{"v0.1.2"}

func openUpgradeModule(t *testing.T, dbPath string) *Module {
	t.Helper()
	m := NewModule(Config{DBPath: dbPath, GRPCAddr: "127.0.0.1:0"})
	if err := m.Init(context.Background()); err != nil {
		t.Fatalf("Init(%s): %v", dbPath, err)
	}
	return m
}

func closeUpgradeModule(t *testing.T, m *Module) {
	t.Helper()
	if m.grpcLis != nil {
		_ = m.grpcLis.Close()
	}
	if err := m.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

func TestUpgradeFromSnapshots(t *testing.T) {
	for _, tag := range upgradeSnapshots {
		t.Run(tag, func(t *testing.T) {
			fixture := filepath.Join("testdata", "upgrade", tag+".db")
			ctx := context.Background()

			fresh := openUpgradeModule(t, filepath.Join(t.TempDir(), "fresh.db"))
			defer closeUpgradeModule(t, fresh)
			freshSchema := moduletest.Schema(t, mustDB(t, fresh))

			// The first open performs the upgrade; the second proves startup
			// against an already-upgraded database is idempotent.
			path := moduletest.CopyFixture(t, fixture)
			for pass := 1; pass <= 2; pass++ {
				m := openUpgradeModule(t, path)
				db := mustDB(t, m)
				upSchema := moduletest.Schema(t, db)
				moduletest.RequireSchemaSuperset(t, upSchema, freshSchema)
				requireColumnDefaults(t, upSchema, freshSchema)
				requireSeededRows(t, ctx, m)
				requireErasureTableEmpty(t, m)
				moduletest.RequireIntegrity(t, db)
				closeUpgradeModule(t, m)
			}

			// The upgraded database erases correctly, and a second module
			// start afterwards still sees the applied record.
			m := openUpgradeModule(t, path)
			if _, err := m.EraseUser(ctx, erasure.Tombstone{ErasureID: "upgrade-e1", UserID: "u-victim"}); err != nil {
				t.Fatalf("EraseUser on upgraded db: %v", err)
			}
			if n, _ := m.remainingForUser(ctx, "u-victim"); n != 0 {
				t.Fatalf("post-condition on upgraded db = %d", n)
			}
			// The bystander and its alias survive; the victim's merged-in
			// identity (ext-jf-9) went with it.
			if count(t, m, `SELECT COUNT(1) FROM violations WHERE user_id = 'u-bystander'`) != 2 ||
				count(t, m, `SELECT COUNT(1) FROM user_aliases WHERE alias_key = 'name:bob-old'`) != 1 ||
				count(t, m, `SELECT COUNT(1) FROM user_aliases WHERE alias_key = 'id:ext-jf-9'`) != 0 ||
				count(t, m, `SELECT COUNT(1) FROM violations WHERE user_id = ''`) != 1 {
				t.Errorf("unexpected rows after erasing from upgraded db: %v", dump(t, m))
			}
			closeUpgradeModule(t, m)
			m = openUpgradeModule(t, path)
			defer closeUpgradeModule(t, m)
			if ok, err := (guardOwner{m: m}).Applied(ctx, "upgrade-e1"); err != nil || !ok {
				t.Fatalf("Applied after reopen = %v, %v", ok, err)
			}
		})
	}
}

// requireColumnDefaults asserts that every column the current code defines has
// the same NOT NULL and DEFAULT contract after upgrade as in a fresh database.
func requireColumnDefaults(t *testing.T, upgraded, fresh moduletest.SchemaInfo) {
	t.Helper()
	for name, ft := range fresh.Tables {
		have := map[string]moduletest.Column{}
		for _, c := range upgraded.Tables[name].Columns {
			have[c.Name] = c
		}
		for _, fc := range ft.Columns {
			uc, ok := have[fc.Name]
			if !ok {
				continue // reported by RequireSchemaSuperset
			}
			if uc.HasDflt != fc.HasDflt || uc.Default != fc.Default || uc.NotNull != fc.NotNull {
				t.Errorf("column %s.%s: upgraded (notnull=%v default=%q has=%v), fresh (notnull=%v default=%q has=%v)",
					name, fc.Name, uc.NotNull, uc.Default, uc.HasDflt, fc.NotNull, fc.Default, fc.HasDflt)
			}
		}
	}
}

func requireErasureTableEmpty(t *testing.T, m *Module) {
	t.Helper()
	if n := appliedRows(t, m); n != 0 {
		t.Errorf("erasure_applied has %d rows on a database that never applied an erasure", n)
	}
}

func requireSeededRows(t *testing.T, ctx context.Context, m *Module) {
	t.Helper()
	for table, want := range map[string]int{
		"guard_rules": 1, "violations": 6, "trust_scores": 3, "user_aliases": 2, "module_settings": 1,
	} {
		if got := count(t, m, "SELECT COUNT(1) FROM "+table); got != want {
			t.Errorf("%s: %d rows after upgrade, want %d (data loss or duplication)", table, got, want)
		}
	}

	// through the store API
	v, err := m.ListViolations(ctx, &guardv1.ListViolationsRequest{Limit: 50})
	if err != nil || len(v.GetViolations()) != 6 {
		t.Fatalf("ListViolations = %d, %v", len(v.GetViolations()), err)
	}
	var summaries []string
	for _, x := range v.GetViolations() {
		summaries = append(summaries, x.GetSummary())
	}
	slices.Sort(summaries)
	want := []string{"alice streams 1", "alice streams 2", "alice-jf streams", "bob streams", "bob-old streams", "carol streams"}
	if !slices.Equal(summaries, want) {
		t.Errorf("violations = %q, want %q", summaries, want)
	}
	for id, score := range map[string]int32{"u-victim": 80, "u-bystander": 90} {
		ts, err := m.getTrustScore(ctx, id, "")
		if err != nil || ts.GetScore() != score || ts.GetUserId() != id {
			t.Errorf("trust %s = %+v, %v; want %d", id, ts, err, score)
		}
	}
	if ts, err := m.getTrustScore(ctx, "", "carol"); err != nil || ts.GetScore() != 90 {
		t.Errorf("trust carol = %+v, %v", ts, err)
	}
	if id, name := m.resolveCanonicalUser(ctx, "ext-jf-9", "alice-jf"); id != "u-victim" || name != "alice" {
		t.Errorf("alias ext-jf-9 resolves to %q/%q", id, name)
	}
	if id, name := m.resolveCanonicalUser(ctx, "", "bob-old"); id != "u-bystander" || name != "bob" {
		t.Errorf("alias bob-old resolves to %q/%q", id, name)
	}
	if !m.getNotifyOnViolation() {
		t.Error("notify_on_violation setting lost in upgrade")
	}
}
