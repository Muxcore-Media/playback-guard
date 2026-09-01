package internal

import (
	"context"
	"encoding/json"
	"log/slog"

	notificationv1 "github.com/Muxcore-Media/contracts-notification/muxcore/notification/v1"
	playbackevents "github.com/Muxcore-Media/contracts-playback/events"
)

type guardViolationEventPayload struct {
	User         string `json:"user"`
	Summary      string `json:"summary"`
	RuleType     string `json:"rule_type"`
	LegacyNotify bool   `json:"legacy_notify,omitempty"`
}

func (m *Module) notifyViolation(ctx context.Context, user, summary, ruleType string) {
	payload := guardViolationEventPayload{
		User:         user,
		Summary:      summary,
		RuleType:     ruleType,
		LegacyNotify: m.getNotifyOnViolation(),
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	mc := m.eventClient()
	if mc != nil {
		if pubErr := mc.Events.Publish(ctx, playbackevents.EventPlaybackGuardViolation, m.id, data); pubErr != nil {
			slog.Debug("playback-guard: publish violation event failed", "error", pubErr)
		}
	}
	if !payload.LegacyNotify {
		return
	}
	addr, err := m.findCapabilityAddr(ctx, "notification")
	if err != nil {
		return
	}
	conn, err := dialPeer(addr)
	if err != nil {
		return
	}
	defer func() { _ = conn.Close() }()
	cli := notificationv1.NewNotificationServiceClient(conn)
	_, _ = cli.Notify(ctx, &notificationv1.NotifyRequest{
		Title:        "Playback guard violation",
		Message:      summary,
		Severity:     notificationv1.Severity_SEVERITY_WARNING,
		SourceModule: m.id,
		Fields: map[string]string{
			"user":      user,
			"type":      ruleType,
			"rule_type": ruleType,
		},
	})
}

func (m *Module) getNotifyOnViolation() bool {
	m.cfgMu.RLock()
	defer m.cfgMu.RUnlock()
	return m.notifyOnViolation
}
