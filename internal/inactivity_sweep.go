package internal

import (
	"context"
	"os"
	"strconv"
	"time"
)

func (m *Module) accountInactivityLoop(ctx context.Context) {
	interval := time.Hour
	if v := os.Getenv("PLAYBACK_GUARD_INACTIVITY_SWEEP_SEC"); v != "" {
		if sec, err := strconv.Atoi(v); err == nil && sec > 0 {
			interval = time.Duration(sec) * time.Second
		}
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-m.stopCh:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.sweepAccountInactivity(ctx)
		}
	}
}

func (m *Module) sweepAccountInactivity(ctx context.Context) {
	users, err := m.listWatchUsers(ctx)
	if err != nil {
		return
	}
	for _, u := range users {
		if u == nil {
			continue
		}
		m.evaluateAccountInactivity(ctx, playbackEvent{UserName: u.GetUsername()}, "")
	}
}
