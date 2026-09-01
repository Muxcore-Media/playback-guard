package internal

import (
	"context"
	"fmt"
	"testing"
	"time"

	guardv1 "github.com/Muxcore-Media/playback-guard/proto/guardv1"
	monitorv1 "github.com/Muxcore-Media/playback-monitor/proto/monitorv1"
)

func TestDeviceVelocityViolation(t *testing.T) {
	ctx := context.Background()
	m := testModule(t)
	now := time.Now()
	history := make([]*monitorv1.SessionRecord, 0, 5)
	for i := range 5 {
		history = append(history, &monitorv1.SessionRecord{
			UserId: "u1", UserName: "alice",
			IpAddress:     fmt.Sprintf("203.0.113.%d", i+1),
			StartedAtUnix: now.Add(-time.Duration(i+1) * time.Hour).Unix(),
		})
	}
	m.monitorOverride = &fakeMonitorClient{history: history}

	_, err := m.UpsertRule(ctx, &guardv1.UpsertRuleRequest{
		Rule: &guardv1.GuardRule{
			Type:    guardv1.RuleType_RULE_TYPE_DEVICE_VELOCITY,
			Name:    "IP velocity",
			Enabled: true,
			Params:  map[string]string{"max_ips": "5", "window_hours": "24"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	m.evaluateDeviceVelocity(ctx, playbackEvent{
		UserID: "u1", UserName: "alice", IPAddress: "198.51.100.10",
	})
	assertViolationCount(t, m, ctx, 1)
}

func TestAccountInactivityViolation(t *testing.T) {
	ctx := context.Background()
	m := testModule(t)
	now := time.Now()
	m.monitorOverride = &fakeMonitorClient{history: []*monitorv1.SessionRecord{
		{
			UserId: "u1", UserName: "alice",
			StartedAtUnix: now.Add(-45 * 24 * time.Hour).Unix(),
			StoppedAtUnix: now.Add(-45 * 24 * time.Hour).Unix(),
		},
	}}

	_, err := m.UpsertRule(ctx, &guardv1.UpsertRuleRequest{
		Rule: &guardv1.GuardRule{
			Type:    guardv1.RuleType_RULE_TYPE_ACCOUNT_INACTIVITY,
			Name:    "Dormant",
			Enabled: true,
			Params:  map[string]string{"inactivity_value": "30", "inactivity_unit": "days"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	m.evaluateAccountInactivity(ctx, playbackEvent{UserID: "u1", UserName: "alice"}, "")
	assertViolationCount(t, m, ctx, 1)
}

func TestAccountInactivityNeverActive(t *testing.T) {
	ctx := context.Background()
	m := testModule(t)
	m.monitorOverride = &fakeMonitorClient{history: nil}

	_, err := m.UpsertRule(ctx, &guardv1.UpsertRuleRequest{
		Rule: &guardv1.GuardRule{
			Type:    guardv1.RuleType_RULE_TYPE_ACCOUNT_INACTIVITY,
			Name:    "Dormant",
			Enabled: true,
			Params:  map[string]string{"inactivity_value": "7", "inactivity_unit": "days"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	m.evaluateAccountInactivity(ctx, playbackEvent{UserID: "u1", UserName: "bob"}, "")
	assertViolationCount(t, m, ctx, 1)
}
