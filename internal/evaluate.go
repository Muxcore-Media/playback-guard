package internal

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	guardv1 "github.com/Muxcore-Media/playback-guard/proto/guardv1"
	monitorv1 "github.com/Muxcore-Media/playback-monitor/proto/monitorv1"
)

func (m *Module) evaluateConcurrentStreams(ctx context.Context, pe playbackEvent) {
	userID := strings.TrimSpace(pe.UserID)
	userName := strings.TrimSpace(pe.UserName)
	if userID == "" && userName == "" {
		return
	}
	rules, err := m.loadEnabledRules(ctx, "concurrent_streams")
	if err != nil || len(rules) == 0 {
		return
	}

	active, err := m.listActiveSessions(ctx)
	if err != nil {
		slog.Debug("playback-guard: list active sessions failed", "error", err)
		return
	}
	count := m.countActiveSessionsForUser(ctx, active, userID, userName)

	for _, rule := range rules {
		maxStreams := parseIntDefault(rule.paramsJSON, "max_streams", 2)
		if maxStreams <= 0 || count <= maxStreams {
			continue
		}
		displayUser := firstNonEmpty(userName, userID, "unknown")
		summary := fmt.Sprintf("%s has %d active streams (limit %d)", displayUser, count, maxStreams)
		m.fireViolation(ctx, rule.id, guardv1.RuleType_RULE_TYPE_CONCURRENT_STREAMS, userID, userName, summary, "warning")
	}
}

func (m *Module) listActiveSessions(ctx context.Context) ([]*monitorv1.SessionRecord, error) {
	var cli monitorv1.PlaybackMonitorServiceClient
	var closer func()
	var err error
	if m.monitorOverride != nil {
		cli = m.monitorOverride
		closer = func() {}
	} else {
		cli, closer, err = m.withMonitorClient(ctx)
		if err != nil {
			return nil, err
		}
	}
	defer closer()
	resp, err := cli.ListActiveSessions(ctx, &monitorv1.ListActiveSessionsRequest{Limit: 200})
	if err != nil {
		return nil, err
	}
	return resp.GetSessions(), nil
}

func (m *Module) hasOpenViolation(ctx context.Context, ruleID, userID, userName string) bool {
	db, err := m.dbConn()
	if err != nil {
		return false
	}
	var n int
	if userID != "" {
		err = db.QueryRowContext(ctx,
			`SELECT COUNT(1) FROM violations WHERE rule_id = ? AND user_id = ? AND acknowledged = 0`,
			ruleID, userID,
		).Scan(&n)
	} else {
		err = db.QueryRowContext(ctx,
			`SELECT COUNT(1) FROM violations WHERE rule_id = ? AND user_name = ? AND acknowledged = 0`,
			ruleID, userName,
		).Scan(&n)
	}
	return err == nil && n > 0
}

func (m *Module) recordViolation(ctx context.Context, ruleID string, ruleType guardv1.RuleType, userID, userName, summary, severity string) error {
	db, err := m.dbConn()
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, `
		INSERT INTO violations(id, rule_id, rule_type, user_id, user_name, summary, severity, acknowledged, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, 0, ?)`,
		newID(), ruleID, ruleTypeToString(ruleType), userID, userName, summary, severity, nowRFC3339(),
	)
	if err != nil {
		return err
	}
	_ = m.adjustTrustOnViolation(ctx, userID, userName, 10)
	return nil
}

func parseIntDefault(paramsJSON, key string, def int) int {
	var params map[string]string
	if err := json.Unmarshal([]byte(paramsJSON), &params); err != nil {
		return def
	}
	if v, ok := params[key]; ok {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
