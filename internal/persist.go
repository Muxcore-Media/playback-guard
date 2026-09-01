package internal

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	monitorv1 "github.com/Muxcore-Media/playback-monitor/proto/monitorv1"
)

const settingNotifyOnViolation = "notify_on_violation"

func (m *Module) loadPersistedSettings(ctx context.Context) error {
	db, err := m.dbConn()
	if err != nil {
		return err
	}
	var value string
	err = db.QueryRowContext(ctx,
		`SELECT value FROM module_settings WHERE key = ?`, settingNotifyOnViolation,
	).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return m.persistNotifyOnViolation(ctx)
	}
	if err != nil {
		return err
	}
	m.cfgMu.Lock()
	m.notifyOnViolation = envTruthy(value)
	m.cfgMu.Unlock()
	return nil
}

func (m *Module) persistNotifyOnViolation(ctx context.Context) error {
	db, err := m.dbConn()
	if err != nil {
		return err
	}
	m.cfgMu.RLock()
	notify := m.notifyOnViolation
	m.cfgMu.RUnlock()
	val := "0"
	if notify {
		val = "1"
	}
	_, err = db.ExecContext(ctx, `
		INSERT INTO module_settings(key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		settingNotifyOnViolation, val,
	)
	return err
}

func (m *Module) setNotifyOnViolation(ctx context.Context, enabled bool) error {
	m.cfgMu.Lock()
	m.notifyOnViolation = enabled
	m.cfgMu.Unlock()
	return m.persistNotifyOnViolation(ctx)
}

func (m *Module) listWatchUsers(ctx context.Context) ([]*monitorv1.WatchUser, error) {
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
	resp, err := cli.ListWatchUsers(ctx, &monitorv1.ListWatchUsersRequest{Limit: 500})
	if err != nil {
		return nil, fmt.Errorf("list watch users: %w", err)
	}
	return resp.GetUsers(), nil
}
