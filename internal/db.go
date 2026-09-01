package internal

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

func (m *Module) initDB(ctx context.Context) error {
	dir := filepath.Dir(m.dbPath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create db dir: %w", err)
	}
	db, err := sql.Open("sqlite", m.dbPath)
	if err != nil {
		return err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.ExecContext(ctx, `PRAGMA journal_mode=WAL`); err != nil {
		_ = db.Close()
		return err
	}
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS guard_rules (
			id TEXT PRIMARY KEY,
			type TEXT NOT NULL,
			name TEXT NOT NULL,
			enabled INTEGER NOT NULL DEFAULT 1,
			params_json TEXT NOT NULL DEFAULT '{}',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS violations (
			id TEXT PRIMARY KEY,
			rule_id TEXT NOT NULL,
			rule_type TEXT NOT NULL,
			user_id TEXT NOT NULL DEFAULT '',
			user_name TEXT NOT NULL DEFAULT '',
			summary TEXT NOT NULL,
			severity TEXT NOT NULL DEFAULT 'warning',
			acknowledged INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_violations_ack ON violations(acknowledged, created_at DESC)`,
		`CREATE TABLE IF NOT EXISTS trust_scores (
			user_key TEXT PRIMARY KEY,
			user_id TEXT NOT NULL DEFAULT '',
			user_name TEXT NOT NULL DEFAULT '',
			score INTEGER NOT NULL DEFAULT 100,
			updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS user_aliases (
			alias_key TEXT PRIMARY KEY,
			canonical_user_id TEXT NOT NULL DEFAULT '',
			canonical_user_name TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS module_settings (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		)`,
	}
	for _, stmt := range stmts {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			_ = db.Close()
			return err
		}
	}
	m.mu.Lock()
	m.db = db
	m.mu.Unlock()
	return nil
}

func (m *Module) dbConn() (*sql.DB, error) {
	m.mu.RLock()
	db := m.db
	m.mu.RUnlock()
	if db == nil {
		return nil, fmt.Errorf("db not initialized")
	}
	return db, nil
}

func nowRFC3339() string {
	return time.Now().UTC().Format(time.RFC3339)
}
