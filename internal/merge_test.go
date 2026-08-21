package internal

import (
	"context"
	"path/filepath"
	"testing"

	guardv1 "github.com/Muxcore-Media/playback-guard/proto/guardv1"
)

func TestMergeUsersAndResolveCanonical(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	m := NewModule(Config{
		DBPath:   filepath.Join(dir, "guard.db"),
		GRPCAddr: "127.0.0.1:0",
	})
	if err := m.Init(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Stop(ctx) })

	if err := m.recordViolation(ctx, "rule1", guardv1.RuleType_RULE_TYPE_CONCURRENT_STREAMS, "u-old", "AliceOld", "test", "warning"); err != nil {
		t.Fatal(err)
	}

	m.monitorOverride = &fakeMonitorClient{}
	violationsUpdated, sessionsUpdated, err := m.mergeUsers(ctx, "u-old", "AliceOld", "u-new", "Alice")
	if err != nil {
		t.Fatal(err)
	}
	if violationsUpdated != 1 {
		t.Fatalf("violations updated: %d", violationsUpdated)
	}
	if sessionsUpdated != 2 {
		t.Fatalf("sessions updated: %d", sessionsUpdated)
	}

	canonID, canonName := m.resolveCanonicalUser(ctx, "u-old", "AliceOld")
	if canonID != "u-new" || canonName != "Alice" {
		t.Fatalf("resolve: %q %q", canonID, canonName)
	}

	score, err := m.getTrustScore(ctx, "u-new", "Alice")
	if err != nil {
		t.Fatal(err)
	}
	if score.Score != 90 {
		t.Fatalf("merged trust score: %+v", score)
	}
}
