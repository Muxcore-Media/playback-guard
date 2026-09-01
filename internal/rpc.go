package internal

import (
	"context"
	"encoding/json"
	"strings"

	guardv1 "github.com/Muxcore-Media/playback-guard/proto/guardv1"
)

func (m *Module) ListRules(ctx context.Context, _ *guardv1.ListRulesRequest) (*guardv1.ListRulesResponse, error) {
	db, err := m.dbConn()
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `
		SELECT id, type, name, enabled, params_json FROM guard_rules ORDER BY name ASC`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make([]*guardv1.GuardRule, 0)
	for rows.Next() {
		var id, typ, name, paramsJSON string
		var enabled int
		if err := rows.Scan(&id, &typ, &name, &enabled, &paramsJSON); err != nil {
			return nil, err
		}
		params := map[string]string{}
		_ = json.Unmarshal([]byte(paramsJSON), &params)
		out = append(out, &guardv1.GuardRule{
			Id:      id,
			Type:    ruleTypeFromString(typ),
			Name:    name,
			Enabled: enabled == 1,
			Params:  params,
		})
	}
	return &guardv1.ListRulesResponse{Rules: out}, rows.Err()
}

func (m *Module) UpsertRule(ctx context.Context, req *guardv1.UpsertRuleRequest) (*guardv1.UpsertRuleResponse, error) {
	rule := req.GetRule()
	if rule == nil {
		return nil, errInvalidRule
	}
	if rule.GetType() == guardv1.RuleType_RULE_TYPE_UNSPECIFIED {
		return nil, errInvalidRule
	}
	if strings.TrimSpace(rule.GetName()) == "" {
		return nil, errInvalidRule
	}
	db, err := m.dbConn()
	if err != nil {
		return nil, err
	}
	id := rule.GetId()
	if id == "" {
		id = newID()
	}
	paramsJSON, _ := json.Marshal(rule.GetParams())
	now := nowRFC3339()
	_, err = db.ExecContext(ctx, `
		INSERT INTO guard_rules(id, type, name, enabled, params_json, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			type = excluded.type,
			name = excluded.name,
			enabled = excluded.enabled,
			params_json = excluded.params_json,
			updated_at = excluded.updated_at`,
		id, ruleTypeToString(rule.GetType()), rule.GetName(), boolToInt(rule.GetEnabled()), string(paramsJSON), now, now,
	)
	if err != nil {
		return nil, err
	}
	rule.Id = id
	return &guardv1.UpsertRuleResponse{Rule: rule}, nil
}

func (m *Module) DeleteRule(ctx context.Context, req *guardv1.DeleteRuleRequest) (*guardv1.DeleteRuleResponse, error) {
	id := strings.TrimSpace(req.GetId())
	if id == "" {
		return &guardv1.DeleteRuleResponse{Ok: false}, nil
	}
	db, err := m.dbConn()
	if err != nil {
		return nil, err
	}
	res, err := db.ExecContext(ctx, `DELETE FROM guard_rules WHERE id = ?`, id)
	if err != nil {
		return nil, err
	}
	n, _ := res.RowsAffected()
	return &guardv1.DeleteRuleResponse{Ok: n > 0}, nil
}

func (m *Module) ListViolations(ctx context.Context, req *guardv1.ListViolationsRequest) (*guardv1.ListViolationsResponse, error) {
	db, err := m.dbConn()
	if err != nil {
		return nil, err
	}
	limit := int(req.GetLimit())
	if limit <= 0 {
		limit = 50
	}
	query := `SELECT id, rule_id, rule_type, user_id, user_name, summary, severity, acknowledged, created_at
		FROM violations WHERE 1=1`
	args := make([]any, 0, 1)
	if !req.GetIncludeAcknowledged() {
		query += ` AND acknowledged = 0`
	}
	query += ` ORDER BY created_at DESC LIMIT ?`
	args = append(args, limit)
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make([]*guardv1.Violation, 0)
	for rows.Next() {
		var id, ruleID, ruleTypeStr, userID, userName, summary, severity, createdAt string
		var ack int
		if err := rows.Scan(&id, &ruleID, &ruleTypeStr, &userID, &userName, &summary, &severity, &ack, &createdAt); err != nil {
			return nil, err
		}
		v := &guardv1.Violation{
			Id:           id,
			RuleId:       ruleID,
			RuleType:     ruleTypeFromString(ruleTypeStr),
			UserId:       userID,
			UserName:     userName,
			Summary:      summary,
			Severity:     severity,
			Acknowledged: ack == 1,
		}
		if t, err := timeParseRFC3339(createdAt); err == nil {
			v.CreatedAtUnix = t.Unix()
		}
		out = append(out, v)
	}
	return &guardv1.ListViolationsResponse{Violations: out}, rows.Err()
}

func (m *Module) AcknowledgeViolations(ctx context.Context, req *guardv1.AcknowledgeViolationsRequest) (*guardv1.AcknowledgeViolationsResponse, error) {
	db, err := m.dbConn()
	if err != nil {
		return nil, err
	}
	updated := int32(0)
	for _, id := range req.GetViolationIds() {
		res, err := db.ExecContext(ctx, `UPDATE violations SET acknowledged = 1 WHERE id = ?`, id)
		if err != nil {
			return nil, err
		}
		n, _ := res.RowsAffected()
		updated += clampInt64ToInt32(n)
	}
	return &guardv1.AcknowledgeViolationsResponse{Updated: updated}, nil
}
