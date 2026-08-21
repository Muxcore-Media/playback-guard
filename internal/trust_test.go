package internal

import (
	"context"
	"path/filepath"
	"testing"

	guardv1 "github.com/Muxcore-Media/playback-guard/proto/guardv1"
)

func TestTrustScoreLifecycle(t *testing.T) {
	dir := t.TempDir()
	m := NewModule(Config{DBPath: filepath.Join(dir, "guard.db")})
	if err := m.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer m.Stop(context.Background())

	ctx := context.Background()
	score, err := m.GetTrustScore(ctx, &guardv1.GetTrustScoreRequest{UserName: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	if score.GetScore().GetScore() != defaultTrustScore {
		t.Fatalf("expected default %d, got %d", defaultTrustScore, score.GetScore().GetScore())
	}

	if err := m.recordViolation(ctx, "rule-1", guardv1.RuleType_RULE_TYPE_CONCURRENT_STREAMS, "u1", "alice", "test violation", "warning"); err != nil {
		t.Fatal(err)
	}
	score, err = m.GetTrustScore(ctx, &guardv1.GetTrustScoreRequest{UserId: "u1", UserName: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	if score.GetScore().GetScore() != 90 {
		t.Fatalf("expected 90 after violation, got %d", score.GetScore().GetScore())
	}

	reset, err := m.ResetTrustScore(ctx, &guardv1.ResetTrustScoreRequest{UserId: "u1", UserName: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	if reset.GetScore().GetScore() != defaultTrustScore {
		t.Fatalf("expected reset to %d, got %d", defaultTrustScore, reset.GetScore().GetScore())
	}
}
