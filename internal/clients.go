package internal

import (
	"context"
	"fmt"
	"net"
	"os"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	monitorv1 "github.com/Muxcore-Media/playback-monitor/proto/monitorv1"
)

const capPlaybackMonitor = "playback.monitor"

func (m *Module) findCapabilityAddr(ctx context.Context, capability string) (string, error) {
	m.mu.RLock()
	mc := m.mc
	m.mu.RUnlock()
	if mc == nil {
		return "", fmt.Errorf("not connected to core")
	}
	modules, err := mc.Discovery.FindByCapability(ctx, capability)
	if err != nil {
		return "", err
	}
	for _, mod := range modules {
		addr := dialAddrForModule(mod.Id, mod.HttpAddr)
		if addr != "" {
			return addr, nil
		}
	}
	return "", fmt.Errorf("no %s module found", capability)
}

func dialAddrForModule(moduleID, httpAddr string) string {
	if httpAddr == "" {
		return ""
	}
	host, port, err := net.SplitHostPort(httpAddr)
	if err != nil || port == "" {
		return httpAddr
	}
	if host != "" && host != "0.0.0.0" && host != "::" {
		return net.JoinHostPort(host, port)
	}
	if os.Getenv("MUXCORE_MESH_DIAL_LOCAL") == "true" {
		return net.JoinHostPort("127.0.0.1", port)
	}
	if moduleID != "" {
		return net.JoinHostPort(moduleID, port)
	}
	return httpAddr
}

func (m *Module) withMonitorClient(ctx context.Context) (monitorv1.PlaybackMonitorServiceClient, func(), error) {
	addr, err := m.findCapabilityAddr(ctx, capPlaybackMonitor)
	if err != nil {
		return nil, nil, err
	}
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, nil, fmt.Errorf("dial monitor %s: %w", addr, err)
	}
	return monitorv1.NewPlaybackMonitorServiceClient(conn), func() { _ = conn.Close() }, nil
}

func (m *Module) countActiveSessionsForUser(ctx context.Context, sessions []*monitorv1.SessionRecord, userID, userName string) int {
	n := 0
	for _, s := range sessions {
		if m.sessionMatchesGuardUser(ctx, s, userID, userName) {
			n++
		}
	}
	return n
}

func (m *Module) sessionMatchesGuardUser(ctx context.Context, s *monitorv1.SessionRecord, userID, userName string) bool {
	if s == nil {
		return false
	}
	canonID, canonName := m.resolveCanonicalUser(ctx, userID, userName)
	sCanonID, sCanonName := m.resolveCanonicalUser(ctx, s.GetUserId(), s.GetUserName())
	if canonID != "" && sCanonID != "" && canonID == sCanonID {
		return true
	}
	un := strings.TrimSpace(canonName)
	if un != "" && strings.EqualFold(strings.TrimSpace(sCanonName), un) {
		return true
	}
	return sessionMatchesUser(s, userID, userName)
}

func sessionMatchesUser(s *monitorv1.SessionRecord, userID, userName string) bool {
	if s == nil {
		return false
	}
	if userID != "" && s.GetUserId() == userID {
		return true
	}
	un := strings.TrimSpace(userName)
	if un != "" && strings.EqualFold(strings.TrimSpace(s.GetUserName()), un) {
		return true
	}
	return false
}
