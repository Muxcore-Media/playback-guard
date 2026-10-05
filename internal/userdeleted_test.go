package internal

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Muxcore-Media/contracts-media/events"
)

func TestDeleteAccountGuard(t *testing.T) {
	ctx := context.Background()
	m := testModule(t)
	db, err := m.dbConn()
	if err != nil {
		t.Fatal(err)
	}
	now := nowRFC3339()
	stmts := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO violations(id, rule_id, rule_type, user_id, user_name, summary, severity, acknowledged, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, 0, ?)`,
			[]any{"v1", "r1", "concurrent", "acct", "Ada", "too many", "warning", now}},
		{`INSERT INTO violations(id, rule_id, rule_type, user_id, user_name, summary, severity, acknowledged, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, 0, ?)`,
			[]any{"v2", "r1", "concurrent", "other", "Bea", "too many", "warning", now}},
		{`INSERT INTO trust_scores(user_key, user_id, user_name, score, updated_at) VALUES (?, ?, ?, ?, ?)`,
			[]any{"id:acct", "acct", "Ada", 40, now}},
		{`INSERT INTO trust_scores(user_key, user_id, user_name, score, updated_at) VALUES (?, ?, ?, ?, ?)`,
			[]any{"id:other", "other", "Bea", 90, now}},
		{`INSERT INTO user_aliases(alias_key, canonical_user_id, canonical_user_name, created_at) VALUES (?, ?, ?, ?)`,
			[]any{"id:old", "acct", "Ada", now}},
		{`INSERT INTO user_aliases(alias_key, canonical_user_id, canonical_user_name, created_at) VALUES (?, ?, ?, ?)`,
			[]any{"id:acct", "kept", "Kept", now}},
		{`INSERT INTO user_aliases(alias_key, canonical_user_id, canonical_user_name, created_at) VALUES (?, ?, ?, ?)`,
			[]any{"id:other", "other", "Bea", now}},
		{`INSERT INTO guard_rules(id, type, name, enabled, params_json, created_at, updated_at) VALUES (?, ?, ?, 1, '{}', ?, ?)`,
			[]any{"r1", "concurrent", "Max", now, now}},
	}
	for _, stmt := range stmts {
		if _, err := db.ExecContext(ctx, stmt.query, stmt.args...); err != nil {
			t.Fatal(err)
		}
	}

	n, err := m.DeleteAccountGuard(ctx, "acct")
	if err != nil {
		t.Fatal(err)
	}
	if n != 4 {
		t.Fatalf("deleted %d, want 4", n)
	}
	assertCount(t, m, ctx, `SELECT COUNT(1) FROM violations WHERE user_id = ?`, []any{"acct"}, 0)
	assertCount(t, m, ctx, `SELECT COUNT(1) FROM violations WHERE user_id = ?`, []any{"other"}, 1)
	assertCount(t, m, ctx, `SELECT COUNT(1) FROM trust_scores WHERE user_id = ?`, []any{"acct"}, 0)
	assertCount(t, m, ctx, `SELECT COUNT(1) FROM trust_scores WHERE user_id = ?`, []any{"other"}, 1)
	assertCount(t, m, ctx, `SELECT COUNT(1) FROM user_aliases WHERE canonical_user_id = ? OR alias_key = ?`, []any{"acct", "id:acct"}, 0)
	assertCount(t, m, ctx, `SELECT COUNT(1) FROM user_aliases WHERE alias_key = ?`, []any{"id:other"}, 1)
	assertCount(t, m, ctx, `SELECT COUNT(1) FROM guard_rules`, nil, 1)

	n, err = m.DeleteAccountGuard(ctx, "acct")
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("second pass deleted %d", n)
	}
	if _, err := m.DeleteAccountGuard(ctx, " "); err == nil {
		t.Fatal("expected blank user id to fail")
	}
}

func TestApplyUserDeletedGuard(t *testing.T) {
	ctx := context.Background()
	m := testModule(t)
	db, err := m.dbConn()
	if err != nil {
		t.Fatal(err)
	}
	now := nowRFC3339()
	if _, err := db.ExecContext(ctx,
		`INSERT INTO violations(id, rule_id, rule_type, user_id, user_name, summary, severity, acknowledged, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, 0, ?)`,
		"v1", "r1", "concurrent", "acct", "Ada", "too many", "warning", now,
	); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(events.UserDeletedPayload{UserID: "acct"})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.ApplyUserDeleted(ctx, raw); err != nil {
		t.Fatal(err)
	}
	assertCount(t, m, ctx, `SELECT COUNT(1) FROM violations WHERE user_id = ?`, []any{"acct"}, 0)
	if err := m.ApplyUserDeleted(ctx, []byte(`{}`)); err == nil {
		t.Fatal("expected missing user_id to fail")
	}
	if err := m.ApplyUserDeleted(ctx, []byte(`not-json`)); err == nil {
		t.Fatal("expected bad JSON to fail")
	}
}

func assertCount(t *testing.T, m *Module, ctx context.Context, query string, args []any, want int) {
	t.Helper()
	db, err := m.dbConn()
	if err != nil {
		t.Fatal(err)
	}
	var got int
	if err := db.QueryRowContext(ctx, query, args...).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("%s = %d, want %d", query, got, want)
	}
}
