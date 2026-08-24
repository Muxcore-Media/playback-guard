package internal

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	guardv1 "github.com/Muxcore-Media/playback-guard/proto/guardv1"
	monitorv1 "github.com/Muxcore-Media/playback-monitor/proto/monitorv1"
)

func (m *Module) evaluateDeviceVelocity(ctx context.Context, pe playbackEvent) {
	userID := strings.TrimSpace(pe.UserID)
	userName := strings.TrimSpace(pe.UserName)
	if userID == "" && userName == "" {
		return
	}
	rules, err := m.loadEnabledRules(ctx, "device_velocity")
	if err != nil || len(rules) == 0 {
		return
	}
	history, err := m.listRecentHistory(ctx, userID, 200)
	if err != nil {
		return
	}
	now := time.Now().UTC()

	for _, rule := range rules {
		windowHours := parseIntDefault(rule.paramsJSON, "window_hours", 24)
		maxIPs := parseIntDefault(rule.paramsJSON, "max_ips", 5)
		if maxIPs <= 0 {
			continue
		}
		cutoff := now.Add(-time.Duration(windowHours) * time.Hour)
		unique := uniquePublicIPsInWindow(history, pe.IPAddress, cutoff, now)
		if len(unique) <= maxIPs {
			continue
		}
		displayUser := firstNonEmpty(userName, userID, "unknown")
		summary := fmt.Sprintf("%s used %d unique public IPs in %dh (limit %d)", displayUser, len(unique), windowHours, maxIPs)
		m.fireViolation(ctx, rule.id, guardv1.RuleType_RULE_TYPE_DEVICE_VELOCITY, userID, userName, summary, "warning")
	}
}

func (m *Module) evaluateAccountInactivity(ctx context.Context, pe playbackEvent) {
	userID := strings.TrimSpace(pe.UserID)
	userName := strings.TrimSpace(pe.UserName)
	if userID == "" && userName == "" {
		return
	}
	rules, err := m.loadEnabledRules(ctx, "account_inactivity")
	if err != nil || len(rules) == 0 {
		return
	}
	history, err := m.listRecentHistory(ctx, userID, 100)
	if err != nil {
		return
	}
	inactiveDays, neverActive := daysSinceLastActivity(history, time.Now().UTC())

	for _, rule := range rules {
		thresholdDays := inactivityThresholdDays(rule.paramsJSON)
		if thresholdDays <= 0 {
			continue
		}
		violates := false
		if neverActive {
			violates = true
		} else if inactiveDays >= thresholdDays {
			violates = true
		}
		if !violates {
			continue
		}
		displayUser := firstNonEmpty(userName, userID, "unknown")
		var summary string
		if neverActive {
			summary = fmt.Sprintf("%s account has no prior watch history (inactivity threshold %d days)", displayUser, thresholdDays)
		} else {
			summary = fmt.Sprintf("%s inactive for %d days (threshold %d days)", displayUser, inactiveDays, thresholdDays)
		}
		m.fireViolation(ctx, rule.id, guardv1.RuleType_RULE_TYPE_ACCOUNT_INACTIVITY, userID, userName, summary, "info")
	}
}

func uniquePublicIPsInWindow(history []*monitorv1.SessionRecord, currentIP string, cutoff, now time.Time) map[string]struct{} {
	ips := map[string]struct{}{}
	add := func(raw string) {
		ip := strings.TrimSpace(raw)
		if ip == "" || isPrivateIP(ip) {
			return
		}
		ips[ip] = struct{}{}
	}
	add(currentIP)
	cutoffUnix := cutoff.Unix()
	for _, s := range history {
		if s == nil {
			continue
		}
		started := s.GetStartedAtUnix()
		if started > 0 && started < cutoffUnix {
			continue
		}
		if started == 0 {
			continue
		}
		add(s.GetIpAddress())
	}
	return ips
}

func daysSinceLastActivity(history []*monitorv1.SessionRecord, now time.Time) (int, bool) {
	var last int64
	for _, s := range history {
		if s == nil {
			continue
		}
		candidate := s.GetStoppedAtUnix()
		if candidate <= 0 {
			candidate = s.GetLastProgressAtUnix()
		}
		if candidate <= 0 {
			candidate = s.GetStartedAtUnix()
		}
		if candidate > last {
			last = candidate
		}
	}
	if last <= 0 {
		return 0, true
	}
	delta := now.Sub(time.Unix(last, 0).UTC())
	if delta < 0 {
		return 0, false
	}
	return int(delta.Hours() / 24), false
}

func inactivityThresholdDays(paramsJSON string) int {
	value := parseIntDefault(paramsJSON, "inactivity_value", 30)
	if value <= 0 {
		value = parseIntDefault(paramsJSON, "inactivity_days", 30)
	}
	unit := strings.ToLower(parseStringDefault(paramsJSON, "inactivity_unit", "days"))
	switch unit {
	case "weeks", "week":
		return value * 7
	case "months", "month":
		return value * 30
	default:
		return value
	}
}

func isPrivateIP(ipStr string) bool {
	ip := net.ParseIP(strings.TrimSpace(ipStr))
	if ip == nil {
		return false
	}
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast()
}
