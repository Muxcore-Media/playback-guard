package internal

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"time"

	playbackevents "github.com/Muxcore-Media/contracts-playback/events"
	eventsv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/events/v1"
	"github.com/Muxcore-Media/core/sdk/go/client"
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

func (m *Module) subscribePlaybackEvents(ctx context.Context) {
	for {
		select {
		case <-m.stopCh:
			return
		case <-ctx.Done():
			return
		default:
		}
		if !m.runPlaybackSubscriptions(ctx) {
			select {
			case <-m.stopCh:
				return
			case <-ctx.Done():
				return
			case <-time.After(time.Second):
			}
		}
	}
}

func (m *Module) runPlaybackSubscriptions(ctx context.Context) bool {
	mc := m.eventClient()
	if mc == nil {
		return false
	}
	var wg sync.WaitGroup
	active := 0
	for _, et := range []string{playbackevents.EventPlaybackStarted, playbackevents.EventPlaybackStopped} {
		ch, cancel, err := mc.Events.Subscribe(ctx, et)
		if err != nil {
			slog.Debug("playback-guard: subscribe failed", "type", et, "error", err)
			continue
		}
		active++
		wg.Add(1)
		go func(events <-chan *eventsv1.Event, eventType string, cancel context.CancelFunc) {
			defer wg.Done()
			defer cancel()
			for evt := range events {
				m.handlePlaybackEvent(ctx, eventType, evt)
			}
		}(ch, et, cancel)
	}
	m.subscribeUserDeleted(ctx, mc, &wg, &active)
	if active == 0 {
		return false
	}
	wg.Wait()
	return true
}

func (m *Module) eventClient() *client.Client {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.mc
}

func (m *Module) handlePlaybackEvent(ctx context.Context, eventType string, evt *eventsv1.Event) {
	if evt == nil || len(evt.Payload) == 0 {
		return
	}
	var pe playbackEvent
	if err := json.Unmarshal(evt.Payload, &pe); err != nil {
		slog.Debug("playback-guard: bad playback payload", "error", err)
		return
	}
	if strings.EqualFold(eventType, playbackevents.EventPlaybackStarted) {
		m.evaluateOnSessionStart(ctx, pe)
	}
}
