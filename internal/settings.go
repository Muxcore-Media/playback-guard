package internal

import (
	"context"
	"fmt"
	"strings"

	"github.com/Muxcore-Media/core/pkg/contracts"
)

func (m *Module) Settings() []contracts.SettingDef {
	return m.settingsDefs()
}

func (m *Module) UpdateSetting(key, value string) error {
	return m.updateSetting(key, value)
}

func (m *Module) settingsDefs() []contracts.SettingDef {
	m.cfgMu.RLock()
	dbPath := m.dbPath
	notify := m.notifyOnViolation
	m.cfgMu.RUnlock()
	notifyVal := "0"
	if notify {
		notifyVal = "1"
	}
	return []contracts.SettingDef{
		{
			Key:         "db_path",
			Label:       "Database Path",
			Type:        contracts.SettingTypeString,
			Value:       dbPath,
			Description: "SQLite path for guard rules and violations (PLAYBACK_GUARD_DB_PATH)",
			Required:    false,
			Group:       "Storage",
		},
		{
			Key:         "notify_on_violation",
			Label:       "Notify On Violation",
			Type:        contracts.SettingTypeString,
			Value:       notifyVal,
			Description: "1 sends notification when a guard rule fires (requires notification module)",
			Required:    false,
			Group:       "Notifications",
		},
	}
}

func (m *Module) updateSetting(key, value string) error {
	switch key {
	case "db_path", "PLAYBACK_GUARD_DB_PATH":
		m.cfgMu.Lock()
		m.dbPath = value
		m.cfgMu.Unlock()
		return fmt.Errorf("db_path change requires module restart")
	case "notify_on_violation", "PLAYBACK_GUARD_NOTIFY_ON_VIOLATION":
		enabled := envTruthy(value)
		if err := m.setNotifyOnViolation(context.Background(), enabled); err != nil {
			return fmt.Errorf("persist notify_on_violation: %w", err)
		}
		return nil
	default:
		return fmt.Errorf("unknown setting %q", key)
	}
}

func envTruthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}
