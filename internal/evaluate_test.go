package internal

import (
	"context"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"

	guardv1 "github.com/Muxcore-Media/playback-guard/proto/guardv1"
	monitorv1 "github.com/Muxcore-Media/playback-monitor/proto/monitorv1"
)

type fakeMonitorClient struct {
	sessions   []*monitorv1.SessionRecord
	history    []*monitorv1.SessionRecord
	watchUsers []*monitorv1.WatchUser
}

func (f *fakeMonitorClient) IngestSessionEvent(context.Context, *monitorv1.IngestSessionEventRequest, ...grpc.CallOption) (*monitorv1.IngestSessionEventResponse, error) {
	return nil, nil
}
func (f *fakeMonitorClient) ListActiveSessions(_ context.Context, _ *monitorv1.ListActiveSessionsRequest, _ ...grpc.CallOption) (*monitorv1.ListActiveSessionsResponse, error) {
	return &monitorv1.ListActiveSessionsResponse{Sessions: f.sessions}, nil
}
func (f *fakeMonitorClient) ListHistory(_ context.Context, req *monitorv1.ListHistoryRequest, _ ...grpc.CallOption) (*monitorv1.ListHistoryResponse, error) {
	sessions := f.history
	if len(sessions) == 0 {
		return &monitorv1.ListHistoryResponse{Sessions: nil}, nil
	}
	if req.GetUserId() != "" {
		filtered := make([]*monitorv1.SessionRecord, 0)
		for _, s := range sessions {
			if s != nil && s.GetUserId() == req.GetUserId() {
				filtered = append(filtered, s)
			}
		}
		return &monitorv1.ListHistoryResponse{Sessions: filtered}, nil
	}
	if q := strings.TrimSpace(req.GetQuery()); q != "" {
		filtered := make([]*monitorv1.SessionRecord, 0)
		for _, s := range sessions {
			if s != nil && strings.EqualFold(s.GetUserName(), q) {
				filtered = append(filtered, s)
			}
		}
		return &monitorv1.ListHistoryResponse{Sessions: filtered}, nil
	}
	return &monitorv1.ListHistoryResponse{Sessions: sessions}, nil
}
func (f *fakeMonitorClient) GetHomeStats(context.Context, *monitorv1.GetHomeStatsRequest, ...grpc.CallOption) (*monitorv1.GetHomeStatsResponse, error) {
	return nil, nil
}
func (f *fakeMonitorClient) GetItemWatchStats(context.Context, *monitorv1.GetItemWatchStatsRequest, ...grpc.CallOption) (*monitorv1.GetItemWatchStatsResponse, error) {
	return nil, nil
}
func (f *fakeMonitorClient) ListWatchUsers(_ context.Context, _ *monitorv1.ListWatchUsersRequest, _ ...grpc.CallOption) (*monitorv1.ListWatchUsersResponse, error) {
	if f.watchUsers != nil {
		return &monitorv1.ListWatchUsersResponse{Users: f.watchUsers}, nil
	}
	return &monitorv1.ListWatchUsersResponse{}, nil
}
func (f *fakeMonitorClient) GetStreamAnalytics(context.Context, *monitorv1.GetStreamAnalyticsRequest, ...grpc.CallOption) (*monitorv1.GetStreamAnalyticsResponse, error) {
	return nil, nil
}
func (f *fakeMonitorClient) ListUserWatchStats(context.Context, *monitorv1.ListUserWatchStatsRequest, ...grpc.CallOption) (*monitorv1.ListUserWatchStatsResponse, error) {
	return nil, nil
}
func (f *fakeMonitorClient) ListServers(context.Context, *monitorv1.ListServersRequest, ...grpc.CallOption) (*monitorv1.ListServersResponse, error) {
	return nil, nil
}
func (f *fakeMonitorClient) RegisterServer(context.Context, *monitorv1.RegisterServerRequest, ...grpc.CallOption) (*monitorv1.RegisterServerResponse, error) {
	return nil, nil
}
func (f *fakeMonitorClient) GetPlaysByDate(context.Context, *monitorv1.GetPlaysByDateRequest, ...grpc.CallOption) (*monitorv1.GetPlaysByDateResponse, error) {
	return nil, nil
}
func (f *fakeMonitorClient) GetPlaysByHour(context.Context, *monitorv1.GetPlaysByHourRequest, ...grpc.CallOption) (*monitorv1.GetPlaysByHourResponse, error) {
	return nil, nil
}
func (f *fakeMonitorClient) GetPlaysByDayOfWeek(context.Context, *monitorv1.GetPlaysByDayOfWeekRequest, ...grpc.CallOption) (*monitorv1.GetPlaysByDayOfWeekResponse, error) {
	return nil, nil
}
func (f *fakeMonitorClient) ListLibraryStats(context.Context, *monitorv1.ListLibraryStatsRequest, ...grpc.CallOption) (*monitorv1.ListLibraryStatsResponse, error) {
	return nil, nil
}
func (f *fakeMonitorClient) ListLibraryDuplicates(context.Context, *monitorv1.ListLibraryDuplicatesRequest, ...grpc.CallOption) (*monitorv1.ListLibraryDuplicatesResponse, error) {
	return nil, nil
}
func (f *fakeMonitorClient) ListStaleLibraryItems(context.Context, *monitorv1.ListStaleLibraryItemsRequest, ...grpc.CallOption) (*monitorv1.ListStaleLibraryItemsResponse, error) {
	return nil, nil
}
func (f *fakeMonitorClient) GetLibraryStorageSummary(context.Context, *monitorv1.GetLibraryStorageSummaryRequest, ...grpc.CallOption) (*monitorv1.GetLibraryStorageSummaryResponse, error) {
	return nil, nil
}
func (f *fakeMonitorClient) GetLibraryStorageHistory(context.Context, *monitorv1.GetLibraryStorageHistoryRequest, ...grpc.CallOption) (*monitorv1.GetLibraryStorageHistoryResponse, error) {
	return nil, nil
}
func (f *fakeMonitorClient) ListTopContent(context.Context, *monitorv1.ListTopContentRequest, ...grpc.CallOption) (*monitorv1.ListTopContentResponse, error) {
	return nil, nil
}
func (f *fakeMonitorClient) GetPlaysByMonth(context.Context, *monitorv1.GetPlaysByMonthRequest, ...grpc.CallOption) (*monitorv1.GetPlaysByMonthResponse, error) {
	return nil, nil
}
func (f *fakeMonitorClient) GetPlaysByStreamType(context.Context, *monitorv1.GetPlaysByStreamTypeRequest, ...grpc.CallOption) (*monitorv1.GetPlaysByStreamTypeResponse, error) {
	return nil, nil
}
func (f *fakeMonitorClient) GetPlaysByStreamResolution(context.Context, *monitorv1.GetPlaysByStreamResolutionRequest, ...grpc.CallOption) (*monitorv1.GetPlaysByStreamResolutionResponse, error) {
	return nil, nil
}
func (f *fakeMonitorClient) GetPlaysBySourceResolution(context.Context, *monitorv1.GetPlaysBySourceResolutionRequest, ...grpc.CallOption) (*monitorv1.GetPlaysBySourceResolutionResponse, error) {
	return nil, nil
}
func (f *fakeMonitorClient) GetPlaysByPlatformResolution(context.Context, *monitorv1.GetPlaysByPlatformResolutionRequest, ...grpc.CallOption) (*monitorv1.GetPlaysByPlatformResolutionResponse, error) {
	return nil, nil
}
func (f *fakeMonitorClient) GetPlaysByTopUsers(context.Context, *monitorv1.GetPlaysByTopUsersRequest, ...grpc.CallOption) (*monitorv1.GetPlaysByTopUsersResponse, error) {
	return nil, nil
}
func (f *fakeMonitorClient) GetPlaysByTopPlatforms(context.Context, *monitorv1.GetPlaysByTopPlatformsRequest, ...grpc.CallOption) (*monitorv1.GetPlaysByTopPlatformsResponse, error) {
	return nil, nil
}
func (f *fakeMonitorClient) GetConcurrentStreams(context.Context, *monitorv1.GetConcurrentStreamsRequest, ...grpc.CallOption) (*monitorv1.GetConcurrentStreamsResponse, error) {
	return nil, nil
}
func (f *fakeMonitorClient) MergeUserIdentity(_ context.Context, req *monitorv1.MergeUserIdentityRequest, _ ...grpc.CallOption) (*monitorv1.MergeUserIdentityResponse, error) {
	return &monitorv1.MergeUserIdentityResponse{SessionsUpdated: 2}, nil
}
func (f *fakeMonitorClient) ImportTautulliHistory(context.Context, *monitorv1.ImportTautulliHistoryRequest, ...grpc.CallOption) (*monitorv1.ImportTautulliHistoryResponse, error) {
	return nil, nil
}
func (f *fakeMonitorClient) ImportJellystatHistory(context.Context, *monitorv1.ImportJellystatHistoryRequest, ...grpc.CallOption) (*monitorv1.ImportJellystatHistoryResponse, error) {
	return nil, nil
}

func TestConcurrentStreamsViolation(t *testing.T) {
	ctx := context.Background()
	m := testModule(t)
	m.monitorOverride = &fakeMonitorClient{sessions: []*monitorv1.SessionRecord{
		{UserId: "u1", UserName: "alice"},
		{UserId: "u1", UserName: "alice"},
		{UserId: "u1", UserName: "alice"},
	}}

	rule, err := m.UpsertRule(ctx, &guardv1.UpsertRuleRequest{
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

	m.evaluateConcurrentStreams(ctx, playbackEvent{UserID: "u1", UserName: "alice"})

	violations, err := m.ListViolations(ctx, &guardv1.ListViolationsRequest{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(violations.GetViolations()) != 1 {
		t.Fatalf("expected 1 violation, got %d", len(violations.GetViolations()))
	}
	if violations.GetViolations()[0].GetRuleId() != rule.GetRule().GetId() {
		t.Fatalf("rule id mismatch")
	}

	m.evaluateConcurrentStreams(ctx, playbackEvent{UserID: "u1", UserName: "alice"})
	violations, err = m.ListViolations(ctx, &guardv1.ListViolationsRequest{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(violations.GetViolations()) != 1 {
		t.Fatalf("expected deduped violation, got %d", len(violations.GetViolations()))
	}
}

func TestTerminateOnViolation(t *testing.T) {
	ctx := context.Background()
	m := testModule(t)
	var terminated bool
	m.terminateHook = func(_ context.Context, req *guardv1.TerminateSessionRequest) (*guardv1.TerminateSessionResponse, error) {
		if req.GetSessionId() == "ext-share" && req.GetServerType() == "jellyfin" {
			terminated = true
		}
		return &guardv1.TerminateSessionResponse{Ok: true}, nil
	}
	m.monitorOverride = &fakeMonitorClient{sessions: []*monitorv1.SessionRecord{
		{UserId: "u1", UserName: "alice", ExternalSessionId: "ext-share", GeoCountry: "US", GeoLat: 40.7, GeoLon: -74.0, StartedAtUnix: time.Now().Unix()},
	}}
	_, err := m.UpsertRule(ctx, &guardv1.UpsertRuleRequest{
		Rule: &guardv1.GuardRule{
			Type:    guardv1.RuleType_RULE_TYPE_GEO_RESTRICTION,
			Name:    "Block US",
			Enabled: true,
			Params:  map[string]string{"mode": "blocklist", "countries": "US", "action": "terminate"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	m.evaluateGeoRestriction(ctx, playbackEvent{
		UserID: "u1", UserName: "alice", ExternalSession: "ext-share", ServerType: "jellyfin",
	})
	if !terminated {
		t.Fatal("expected terminate on geo violation")
	}
}

func TestListRecentHistoryByUserName(t *testing.T) {
	ctx := context.Background()
	m := testModule(t)
	now := time.Now()
	m.monitorOverride = &fakeMonitorClient{history: []*monitorv1.SessionRecord{
		{UserName: "alice", IpAddress: "203.0.113.1", StartedAtUnix: now.Add(-time.Hour).Unix()},
	}}
	_, err := m.UpsertRule(ctx, &guardv1.UpsertRuleRequest{
		Rule: &guardv1.GuardRule{
			Type:    guardv1.RuleType_RULE_TYPE_DEVICE_VELOCITY,
			Name:    "IPs",
			Enabled: true,
			Params:  map[string]string{"max_ips": "1", "window_hours": "24"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	m.evaluateDeviceVelocity(ctx, playbackEvent{UserName: "alice", IPAddress: "198.51.100.2"})
	assertViolationCount(t, m, ctx, 1)
}

func testModule(t *testing.T) *Module {
	t.Helper()
	dir := t.TempDir()
	m := NewModule(Config{
		DBPath:   dir + "/guard.db",
		GRPCAddr: "127.0.0.1:0",
	})
	if err := m.Init(context.Background()); err != nil {
		t.Fatalf("init: %v", err)
	}
	t.Cleanup(func() { _ = m.Stop(context.Background()) })
	return m
}
