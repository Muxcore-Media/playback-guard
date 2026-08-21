package internal

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"

	"github.com/Muxcore-Media/core/sdk/go/client"
	playbackevents "github.com/Muxcore-Media/contracts-playback/events"
	eventsv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/events/v1"
)

type playbackEvent struct {
	SessionID       string `json:"session_id"`
	UserID          string `json:"user_id"`
	UserName        string `json:"user_name"`
	IPAddress       string `json:"ip_address"`
	Platform        string `json:"platform"`
	ServerType      string `json:"server_type"`
	ExternalSession string `json:"external_session_id"`
}

func (m *Module) subscribePlaybackEvents() {
	mc := m.eventClient()
	if mc == nil {
		return
	}
	for _, et := range []string{playbackevents.EventPlaybackStarted, playbackevents.EventPlaybackStopped} {
		ch, cancel, err := mc.Events.Subscribe(context.Background(), et)
		if err != nil {
			slog.Debug("playback-guard: subscribe failed", "type", et, "error", err)
			continue
		}
		go func(events <-chan *eventsv1.Event, eventType string, cancel context.CancelFunc) {
			defer cancel()
			for evt := range events {
				m.handlePlaybackEvent(eventType, evt)
			}
		}(ch, et, cancel)
	}
}

func (m *Module) eventClient() *client.Client {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.mc
}

func (m *Module) handlePlaybackEvent(eventType string, evt *eventsv1.Event) {
	if evt == nil || len(evt.Payload) == 0 {
		return
	}
	var pe playbackEvent
	if err := json.Unmarshal(evt.Payload, &pe); err != nil {
		slog.Debug("playback-guard: bad playback payload", "error", err)
		return
	}
	if strings.EqualFold(eventType, playbackevents.EventPlaybackStarted) {
		m.evaluateOnSessionStart(context.Background(), pe)
	}
}
