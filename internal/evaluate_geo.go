package internal

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"time"

	monitorv1 "github.com/Muxcore-Media/playback-monitor/proto/monitorv1"
	guardv1 "github.com/Muxcore-Media/playback-guard/proto/guardv1"
)

type ruleRow struct {
	id         string
	paramsJSON string
}

func (m *Module) loadEnabledRules(ctx context.Context, ruleType string) ([]ruleRow, error) {
	db, err := m.dbConn()
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `
		SELECT id, params_json FROM guard_rules
		WHERE enabled = 1 AND type = ?`, ruleType)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]ruleRow, 0)
	for rows.Next() {
		var r ruleRow
		if err := rows.Scan(&r.id, &r.paramsJSON); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (m *Module) fireViolation(ctx context.Context, ruleID string, ruleType guardv1.RuleType, userID, userName, summary, severity string) {
	if m.hasOpenViolation(ctx, ruleID, userID, userName) {
		return
	}
	if err := m.recordViolation(ctx, ruleID, ruleType, userID, userName, summary, severity); err != nil {
		slog.Debug("playback-guard: record violation failed", "error", err)
		return
	}
	displayUser := firstNonEmpty(userName, userID, "unknown")
	go m.notifyViolation(context.Background(), displayUser, summary, ruleTypeToString(ruleType))
}

func (m *Module) evaluateOnSessionStart(ctx context.Context, pe playbackEvent) {
	pe.UserID, pe.UserName = m.resolveCanonicalUser(ctx, pe.UserID, pe.UserName)
	m.evaluateConcurrentStreams(ctx, pe)
	m.evaluateGeoRestriction(ctx, pe)
	m.evaluateSimultaneousLocations(ctx, pe)
	m.evaluateImpossibleTravel(ctx, pe)
	m.evaluateDeviceVelocity(ctx, pe)
	m.evaluateAccountInactivity(ctx, pe)
}

func (m *Module) evaluateGeoRestriction(ctx context.Context, pe playbackEvent) {
	userID := strings.TrimSpace(pe.UserID)
	userName := strings.TrimSpace(pe.UserName)
	if userID == "" && userName == "" {
		return
	}
	rules, err := m.loadEnabledRules(ctx, "geo_restriction")
	if err != nil || len(rules) == 0 {
		return
	}
	active, err := m.listActiveSessions(ctx)
	if err != nil {
		return
	}
	current := m.findUserSession(ctx, active, pe, userID, userName)
	if current == nil || !sessionHasGeoCoords(current) {
		return
	}
	country := normalizeCountryCode(current.GetGeoCountry())
	if country == "" || country == "LOCAL NETWORK" {
		return
	}

	for _, rule := range rules {
		mode := parseStringDefault(rule.paramsJSON, "mode", "blocklist")
		countries := parseCountries(rule.paramsJSON)
		if len(countries) == 0 {
			continue
		}
		if !countryMatchesRestriction(country, mode, countries) {
			continue
		}
		displayUser := firstNonEmpty(userName, userID, "unknown")
		summary := fmt.Sprintf("%s streaming from restricted country %s (%s)", displayUser, country, mode)
		m.fireViolation(ctx, rule.id, guardv1.RuleType_RULE_TYPE_GEO_RESTRICTION, userID, userName, summary, "warning")
	}
}

func (m *Module) evaluateSimultaneousLocations(ctx context.Context, pe playbackEvent) {
	userID := strings.TrimSpace(pe.UserID)
	userName := strings.TrimSpace(pe.UserName)
	if userID == "" && userName == "" {
		return
	}
	rules, err := m.loadEnabledRules(ctx, "simultaneous_locations")
	if err != nil || len(rules) == 0 {
		return
	}
	active, err := m.listActiveSessions(ctx)
	if err != nil {
		return
	}
	userSessions := sessionsWithGeoCoords(m.filterSessionsForGuardUser(ctx, active, userID, userName))
	if len(userSessions) < 2 {
		return
	}
	maxDistance := maxPairwiseDistanceKm(userSessions)
	if maxDistance <= 0 {
		return
	}

	for _, rule := range rules {
		minKm := parseFloatDefault(rule.paramsJSON, "min_distance_km", 100)
		if maxDistance <= minKm {
			continue
		}
		displayUser := firstNonEmpty(userName, userID, "unknown")
		summary := fmt.Sprintf("%s has active sessions ~%.0f km apart (limit %.0f km)", displayUser, maxDistance, minKm)
		m.fireViolation(ctx, rule.id, guardv1.RuleType_RULE_TYPE_SIMULTANEOUS_LOCATIONS, userID, userName, summary, "warning")
	}
}

func (m *Module) evaluateImpossibleTravel(ctx context.Context, pe playbackEvent) {
	userID := strings.TrimSpace(pe.UserID)
	userName := strings.TrimSpace(pe.UserName)
	if userID == "" && userName == "" {
		return
	}
	rules, err := m.loadEnabledRules(ctx, "impossible_travel")
	if err != nil || len(rules) == 0 {
		return
	}
	active, err := m.listActiveSessions(ctx)
	if err != nil {
		return
	}
	current := m.findUserSession(ctx, active, pe, userID, userName)
	if current == nil || !sessionHasGeoCoords(current) {
		return
	}
	history, err := m.listRecentHistory(ctx, userID, 20)
	if err != nil {
		return
	}
	previous := findPreviousGeoSession(history, current.GetId())
	if previous == nil {
		return
	}
	distance, ok := haversineDistanceKm(current.GetGeoLat(), current.GetGeoLon(), previous.GetGeoLat(), previous.GetGeoLon())
	if !ok {
		return
	}
	prevTime := previous.GetStoppedAtUnix()
	if prevTime <= 0 {
		prevTime = previous.GetStartedAtUnix()
	}
	currTime := current.GetStartedAtUnix()
	if currTime <= 0 {
		currTime = time.Now().Unix()
	}
	speed := travelSpeedKmh(distance, prevTime, currTime)
	if speed <= 0 || math.IsInf(speed, 1) {
		if distance <= 0 {
			return
		}
		speed = math.MaxFloat64
	}

	for _, rule := range rules {
		maxSpeed := parseFloatDefault(rule.paramsJSON, "max_speed_kmh", 500)
		if speed <= maxSpeed {
			continue
		}
		displayUser := firstNonEmpty(userName, userID, "unknown")
		summary := fmt.Sprintf("%s travel speed %.0f km/h exceeds limit %.0f km/h (%.0f km)", displayUser, speed, maxSpeed, distance)
		m.fireViolation(ctx, rule.id, guardv1.RuleType_RULE_TYPE_IMPOSSIBLE_TRAVEL, userID, userName, summary, "critical")
	}
}

func (m *Module) listRecentHistory(ctx context.Context, userID string, limit int32) ([]*monitorv1.SessionRecord, error) {
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
	resp, err := cli.ListHistory(ctx, &monitorv1.ListHistoryRequest{
		UserId: userID,
		Limit:  limit,
	})
	if err != nil {
		return nil, err
	}
	return resp.GetSessions(), nil
}

func (m *Module) filterSessionsForGuardUser(ctx context.Context, sessions []*monitorv1.SessionRecord, userID, userName string) []*monitorv1.SessionRecord {
	out := make([]*monitorv1.SessionRecord, 0)
	for _, s := range sessions {
		if m.sessionMatchesGuardUser(ctx, s, userID, userName) {
			out = append(out, s)
		}
	}
	return out
}

func (m *Module) findUserSession(ctx context.Context, active []*monitorv1.SessionRecord, pe playbackEvent, userID, userName string) *monitorv1.SessionRecord {
	ext := strings.TrimSpace(pe.ExternalSession)
	for _, s := range active {
		if !m.sessionMatchesGuardUser(ctx, s, userID, userName) {
			continue
		}
		if ext != "" && s.GetExternalSessionId() == ext {
			return s
		}
	}
	for _, s := range active {
		if m.sessionMatchesGuardUser(ctx, s, userID, userName) {
			return s
		}
	}
	return nil
}

func sessionsWithGeoCoords(sessions []*monitorv1.SessionRecord) []*monitorv1.SessionRecord {
	out := make([]*monitorv1.SessionRecord, 0, len(sessions))
	for _, s := range sessions {
		if sessionHasGeoCoords(s) {
			out = append(out, s)
		}
	}
	return out
}

func sessionHasGeoCoords(s *monitorv1.SessionRecord) bool {
	if s == nil {
		return false
	}
	if strings.TrimSpace(s.GetGeoCountry()) == "Local Network" {
		return false
	}
	return s.GetGeoLat() != 0 || s.GetGeoLon() != 0
}

func findPreviousGeoSession(history []*monitorv1.SessionRecord, currentID string) *monitorv1.SessionRecord {
	for _, s := range history {
		if s == nil || s.GetId() == currentID {
			continue
		}
		if sessionHasGeoCoords(s) {
			return s
		}
	}
	return nil
}

func maxPairwiseDistanceKm(sessions []*monitorv1.SessionRecord) float64 {
	maxDist := 0.0
	for i := 0; i < len(sessions); i++ {
		for j := i + 1; j < len(sessions); j++ {
			d, ok := haversineDistanceKm(
				sessions[i].GetGeoLat(), sessions[i].GetGeoLon(),
				sessions[j].GetGeoLat(), sessions[j].GetGeoLon(),
			)
			if ok && d > maxDist {
				maxDist = d
			}
		}
	}
	return maxDist
}

func haversineDistanceKm(lat1, lon1, lat2, lon2 float64) (float64, bool) {
	if lat1 == 0 && lon1 == 0 && lat2 == 0 && lon2 == 0 {
		return 0, false
	}
	const earthRadiusKm = 6371.0
	toRad := func(d float64) float64 { return d * math.Pi / 180 }
	dLat := toRad(lat2 - lat1)
	dLon := toRad(lon2 - lon1)
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(toRad(lat1))*math.Cos(toRad(lat2))*math.Sin(dLon/2)*math.Sin(dLon/2)
	c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
	return earthRadiusKm * c, true
}

func travelSpeedKmh(distanceKm float64, fromUnix, toUnix int64) float64 {
	deltaSec := toUnix - fromUnix
	if deltaSec <= 0 {
		if distanceKm > 0 {
			return math.Inf(1)
		}
		return 0
	}
	hours := float64(deltaSec) / 3600.0
	return distanceKm / hours
}

func normalizeCountryCode(raw string) string {
	s := strings.ToUpper(strings.TrimSpace(raw))
	if s == "" || strings.EqualFold(s, "Local Network") {
		return ""
	}
	if len(s) == 2 {
		return s
	}
	return s
}

func countryMatchesRestriction(country, mode string, countries []string) bool {
	inList := false
	for _, c := range countries {
		if c == country {
			inList = true
			break
		}
	}
	if strings.EqualFold(mode, "allowlist") {
		return !inList
	}
	return inList
}

func parseCountries(paramsJSON string) []string {
	var params map[string]string
	if err := json.Unmarshal([]byte(paramsJSON), &params); err != nil {
		return nil
	}
	raw := params["countries"]
	if raw == "" {
		return nil
	}
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ' ' || r == ';'
	})
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if c := normalizeCountryCode(p); c != "" {
			out = append(out, c)
		}
	}
	return out
}

func parseStringDefault(paramsJSON, key, def string) string {
	var params map[string]string
	if err := json.Unmarshal([]byte(paramsJSON), &params); err != nil {
		return def
	}
	if v, ok := params[key]; ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return def
}

func parseFloatDefault(paramsJSON, key string, def float64) float64 {
	var params map[string]string
	if err := json.Unmarshal([]byte(paramsJSON), &params); err != nil {
		return def
	}
	if v, ok := params[key]; ok {
		var f float64
		if _, err := fmt.Sscanf(v, "%f", &f); err == nil && f > 0 {
			return f
		}
	}
	return def
}
