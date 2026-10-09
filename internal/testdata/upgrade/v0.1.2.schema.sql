CREATE TABLE guard_rules (
			id TEXT PRIMARY KEY,
			type TEXT NOT NULL,
			name TEXT NOT NULL,
			enabled INTEGER NOT NULL DEFAULT 1,
			params_json TEXT NOT NULL DEFAULT '{}',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);
CREATE TABLE module_settings (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		);
CREATE TABLE trust_scores (
			user_key TEXT PRIMARY KEY,
			user_id TEXT NOT NULL DEFAULT '',
			user_name TEXT NOT NULL DEFAULT '',
			score INTEGER NOT NULL DEFAULT 100,
			updated_at TEXT NOT NULL
		);
CREATE TABLE user_aliases (
			alias_key TEXT PRIMARY KEY,
			canonical_user_id TEXT NOT NULL DEFAULT '',
			canonical_user_name TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL
		);
CREATE TABLE violations (
			id TEXT PRIMARY KEY,
			rule_id TEXT NOT NULL,
			rule_type TEXT NOT NULL,
			user_id TEXT NOT NULL DEFAULT '',
			user_name TEXT NOT NULL DEFAULT '',
			summary TEXT NOT NULL,
			severity TEXT NOT NULL DEFAULT 'warning',
			acknowledged INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL
		);
CREATE INDEX idx_violations_ack ON violations(acknowledged, created_at DESC);
