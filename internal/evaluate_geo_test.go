package internal

import (
	"context"
	"testing"
	"time"

	guardv1 "github.com/Muxcore-Media/playback-guard/proto/guardv1"
	monitorv1 "github.com/Muxcore-Media/playback-monitor/proto/monitorv1"
)

func TestGeoRestrictionViolation(t *testing.T) {
	ctx := context.Background()
	m := testModule(t)
	now := time.Now().Unix()
	m.monitorOverride = &fakeMonitorClient{sessions: []*monitorv1.SessionRecord{
		{
			Id: "s1", UserId: "u1", UserName: "alice", ExternalSessionId: "ext1",
			GeoCountry: "US", GeoLat: 40.7, GeoLon: -74.0, StartedAtUnix: now,
		},
	}}

	_, err := m.UpsertRule(ctx, &guardv1.UpsertRuleRequest{
		Rule: &guardv1.GuardRule{
			Type:    guardv1.RuleType_RULE_TYPE_GEO_RESTRICTION,
			Name:    "Block US",
			Enabled: true,
			Params:  map[string]string{"mode": "blocklist", "countries": "US,CN"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	m.evaluateGeoRestriction(ctx, playbackEvent{UserID: "u1", UserName: "alice", ExternalSession: "ext1"})

	assertViolationCount(t, m, ctx, 1)
}

func TestSimultaneousLocationsViolation(t *testing.T) {
	ctx := context.Background()
	m := testModule(t)
	now := time.Now().Unix()
	m.monitorOverride = &fakeMonitorClient{sessions: []*monitorv1.SessionRecord{
		{Id: "s1", UserId: "u1", UserName: "alice", GeoLat: 40.7, GeoLon: -74.0, StartedAtUnix: now},
		{Id: "s2", UserId: "u1", UserName: "alice", GeoLat: 34.05, GeoLon: -118.24, StartedAtUnix: now},
	}}

	_, err := m.UpsertRule(ctx, &guardv1.UpsertRuleRequest{
		Rule: &guardv1.GuardRule{
			Type:    guardv1.RuleType_RULE_TYPE_SIMULTANEOUS_LOCATIONS,
			Name:    "Distance",
			Enabled: true,
			Params:  map[string]string{"min_distance_km": "100"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	m.evaluateSimultaneousLocations(ctx, playbackEvent{UserID: "u1", UserName: "alice"})
	assertViolationCount(t, m, ctx, 1)
}

func TestImpossibleTravelViolation(t *testing.T) {
	ctx := context.Background()
	m := testModule(t)
	now := time.Now()
	m.monitorOverride = &fakeMonitorClient{
		sessions: []*monitorv1.SessionRecord{
			{
				Id: "current", UserId: "u1", UserName: "alice", ExternalSessionId: "ext-new",
				GeoLat: 34.05, GeoLon: -118.24, StartedAtUnix: now.Unix(),
			},
		},
		history: []*monitorv1.SessionRecord{
			{
				Id: "prev", UserId: "u1", UserName: "alice",
				GeoLat: 40.7, GeoLon: -74.0,
				StartedAtUnix: now.Add(-2 * time.Hour).Unix(),
				StoppedAtUnix: now.Add(-1 * time.Hour).Unix(),
			},
		},
	}

	_, err := m.UpsertRule(ctx, &guardv1.UpsertRuleRequest{
		Rule: &guardv1.GuardRule{
			Type:    guardv1.RuleType_RULE_TYPE_IMPOSSIBLE_TRAVEL,
			Name:    "Speed",
			Enabled: true,
			Params:  map[string]string{"max_speed_kmh": "500"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	m.evaluateImpossibleTravel(ctx, playbackEvent{UserID: "u1", UserName: "alice", ExternalSession: "ext-new"})
	assertViolationCount(t, m, ctx, 1)
}

func assertViolationCount(t *testing.T, m *Module, ctx context.Context, want int) {
	t.Helper()
	violations, err := m.ListViolations(ctx, &guardv1.ListViolationsRequest{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(violations.GetViolations()) != want {
		t.Fatalf("expected %d violation(s), got %d", want, len(violations.GetViolations()))
	}
}
