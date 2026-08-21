package internal

import (
	"context"
	"testing"

	guardv1 "github.com/Muxcore-Media/playback-guard/proto/guardv1"
)

func TestGuardRulesAndViolations(t *testing.T) {
	m := testModule(t)
	ctx := context.Background()
	resp, err := m.UpsertRule(ctx, &guardv1.UpsertRuleRequest{
		Rule: &guardv1.GuardRule{
			Type:    guardv1.RuleType_RULE_TYPE_CONCURRENT_STREAMS,
			Name:    "Max 2 streams",
			Enabled: true,
			Params:  map[string]string{"max_streams": "2"},
		},
	})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if resp.GetRule().GetId() == "" {
		t.Fatal("expected rule id")
	}

	rules, err := m.ListRules(ctx, &guardv1.ListRulesRequest{})
	if err != nil {
		t.Fatalf("list rules: %v", err)
	}
	if len(rules.GetRules()) != 1 {
		t.Fatalf("rules: %+v", rules.GetRules())
	}

	if err := m.recordViolation(ctx, resp.GetRule().GetId(), guardv1.RuleType_RULE_TYPE_CONCURRENT_STREAMS, "u1", "alice", "too many streams", "warning"); err != nil {
		t.Fatalf("record: %v", err)
	}
	violations, err := m.ListViolations(ctx, &guardv1.ListViolationsRequest{Limit: 10})
	if err != nil {
		t.Fatalf("list violations: %v", err)
	}
	if len(violations.GetViolations()) != 1 {
		t.Fatalf("violations: %+v", violations.GetViolations())
	}

	ack, err := m.AcknowledgeViolations(ctx, &guardv1.AcknowledgeViolationsRequest{
		ViolationIds: []string{violations.GetViolations()[0].GetId()},
	})
	if err != nil {
		t.Fatalf("ack: %v", err)
	}
	if ack.GetUpdated() != 1 {
		t.Fatalf("updated %d", ack.GetUpdated())
	}
	open, err := m.ListViolations(ctx, &guardv1.ListViolationsRequest{Limit: 10})
	if err != nil {
		t.Fatalf("list open: %v", err)
	}
	if len(open.GetViolations()) != 0 {
		t.Fatalf("expected 0 open violations, got %d", len(open.GetViolations()))
	}
}
