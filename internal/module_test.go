package internal

import (
	"context"
	"encoding/json"
	"testing"

	playbackevents "github.com/Muxcore-Media/contracts-playback/events"
	eventsv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/events/v1"
	guardv1 "github.com/Muxcore-Media/playback-guard/proto/guardv1"
	monitorv1 "github.com/Muxcore-Media/playback-monitor/proto/monitorv1"
)

func TestModuleInfoHTTPAddr(t *testing.T) {
	m := NewModule(Config{GRPCAddr: "127.0.0.1:9561"})
	info := m.Info()
	if info.HTTPAddr != "127.0.0.1:9561" {
		t.Fatalf("HTTPAddr = %q", info.HTTPAddr)
	}
}

func TestUpsertRuleRejectsInvalid(t *testing.T) {
	m := testModule(t)
	ctx := context.Background()

	_, err := m.UpsertRule(ctx, &guardv1.UpsertRuleRequest{
		Rule: &guardv1.GuardRule{Name: "bad", Enabled: true},
	})
	if err == nil {
		t.Fatal("expected error for unspecified rule type")
	}

	_, err = m.UpsertRule(ctx, &guardv1.UpsertRuleRequest{
		Rule: &guardv1.GuardRule{
			Type:    guardv1.RuleType_RULE_TYPE_CONCURRENT_STREAMS,
			Enabled: true,
		},
	})
	if err == nil {
		t.Fatal("expected error for empty name")
	}
}

func TestDeleteRule(t *testing.T) {
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
	del, err := m.DeleteRule(ctx, &guardv1.DeleteRuleRequest{Id: resp.GetRule().GetId()})
	if err != nil || !del.GetOk() {
		t.Fatalf("delete: ok=%v err=%v", del.GetOk(), err)
	}
	rules, err := m.ListRules(ctx, &guardv1.ListRulesRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rules.GetRules()) != 0 {
		t.Fatalf("expected 0 rules, got %d", len(rules.GetRules()))
	}
}

func TestTerminateSessionEmptySessionID(t *testing.T) {
	m := testModule(t)
	resp, err := m.TerminateSession(context.Background(), &guardv1.TerminateSessionRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if resp.GetOk() || resp.GetError() != "session_id required" {
		t.Fatalf("resp = %+v", resp)
	}
}

func TestHandlePlaybackEventEvaluates(t *testing.T) {
	ctx := context.Background()
	m := testModule(t)
	m.monitorOverride = &fakeMonitorClient{sessions: []*monitorv1.SessionRecord{
		{UserId: "u1", UserName: "alice"},
		{UserId: "u1", UserName: "alice"},
		{UserId: "u1", UserName: "alice"},
	}}
	_, err := m.UpsertRule(ctx, &guardv1.UpsertRuleRequest{
		Rule: &guardv1.GuardRule{
			Type:    guardv1.RuleType_RULE_TYPE_CONCURRENT_STREAMS,
			Name:    "Max 2",
			Enabled: true,
			Params:  map[string]string{"max_streams": "2"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(playbackEvent{UserID: "u1", UserName: "alice"})
	m.handlePlaybackEvent(ctx, playbackevents.EventPlaybackStarted, &eventsv1.Event{Payload: payload})
	violations, err := m.ListViolations(ctx, &guardv1.ListViolationsRequest{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(violations.GetViolations()) != 1 {
		t.Fatalf("expected 1 violation, got %d", len(violations.GetViolations()))
	}
}

func TestNotifyOnViolationPersisted(t *testing.T) {
	dir := t.TempDir()
	dbPath := dir + "/guard.db"
	ctx := context.Background()
	m := NewModule(Config{DBPath: dbPath, GRPCAddr: "127.0.0.1:0"})
	if err := m.Init(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Stop(ctx) })
	if err := m.UpdateSetting("notify_on_violation", "1"); err != nil {
		t.Fatal(err)
	}
	m2 := NewModule(Config{DBPath: dbPath, GRPCAddr: "127.0.0.1:0"})
	if err := m2.Init(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m2.Stop(ctx) })
	if !m2.getNotifyOnViolation() {
		t.Fatal("expected persisted notify_on_violation=true")
	}
}

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
