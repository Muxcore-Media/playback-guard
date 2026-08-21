package internal

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strings"

	monitorv1 "github.com/Muxcore-Media/playback-monitor/proto/monitorv1"
	guardv1 "github.com/Muxcore-Media/playback-guard/proto/guardv1"
)

func (m *Module) resolveCanonicalUser(ctx context.Context, userID, userName string) (string, string) {
	key := trustUserKey(userID, userName)
	if key == "name:" {
		return userID, userName
	}
	db, err := m.dbConn()
	if err != nil {
		return userID, userName
	}
	var canonID, canonName string
	err = db.QueryRowContext(ctx,
		`SELECT canonical_user_id, canonical_user_name FROM user_aliases WHERE alias_key = ?`, key,
	).Scan(&canonID, &canonName)
	if err == sql.ErrNoRows {
		return userID, userName
	}
	if err != nil {
		return userID, userName
	}
	return firstNonEmpty(canonID, userID), firstNonEmpty(canonName, userName)
}

func (m *Module) mergeUsers(ctx context.Context, sourceID, sourceName, targetID, targetName string) (violationsUpdated, sessionsUpdated int32, err error) {
	sourceID = strings.TrimSpace(sourceID)
	sourceName = strings.TrimSpace(sourceName)
	targetID = strings.TrimSpace(targetID)
	targetName = strings.TrimSpace(targetName)
	if sourceID == "" && sourceName == "" {
		return 0, 0, fmt.Errorf("source user required")
	}
	if targetID == "" && targetName == "" {
		return 0, 0, fmt.Errorf("target user required")
	}
	sourceKey := trustUserKey(sourceID, sourceName)
	targetKey := trustUserKey(targetID, targetName)
	if sourceKey == targetKey {
		return 0, 0, fmt.Errorf("source and target are the same user")
	}
	if sourceKey == "name:" || targetKey == "name:" {
		return 0, 0, fmt.Errorf("user_id or user_name required for both users")
	}

	db, err := m.dbConn()
	if err != nil {
		return 0, 0, err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, err
	}
	defer func() { _ = tx.Rollback() }()

	now := nowRFC3339()
	_, err = tx.ExecContext(ctx, `
		INSERT INTO user_aliases(alias_key, canonical_user_id, canonical_user_name, created_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(alias_key) DO UPDATE SET
			canonical_user_id = excluded.canonical_user_id,
			canonical_user_name = excluded.canonical_user_name`,
		sourceKey, targetID, targetName, now,
	)
	if err != nil {
		return 0, 0, err
	}

	res, err := tx.ExecContext(ctx, `
		UPDATE violations SET user_id = ?, user_name = ?
		WHERE acknowledged = 0 AND (
			(user_id != '' AND user_id = ?) OR
			(user_id = '' AND lower(user_name) = lower(?))
		)`, targetID, targetName, sourceID, sourceName)
	if err != nil {
		return 0, 0, err
	}
	n, _ := res.RowsAffected()

	if err := m.mergeTrustScoresTx(ctx, tx, sourceID, sourceName, targetID, targetName); err != nil {
		return 0, 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, 0, err
	}

	sessionsUpdated = m.mergeMonitorUserIdentity(ctx, sourceID, sourceName, targetID, targetName)
	return int32(n), sessionsUpdated, nil
}

func (m *Module) mergeMonitorUserIdentity(ctx context.Context, sourceID, sourceName, targetID, targetName string) int32 {
	var cli monitorv1.PlaybackMonitorServiceClient
	var closer func()
	if m.monitorOverride != nil {
		cli = m.monitorOverride
		closer = func() {}
	} else {
		var err error
		cli, closer, err = m.withMonitorClient(ctx)
		if err != nil {
			slog.Debug("playback-guard: monitor unavailable for user merge", "error", err)
			return 0
		}
	}
	defer closer()
	resp, err := cli.MergeUserIdentity(ctx, &monitorv1.MergeUserIdentityRequest{
		SourceUserId:   sourceID,
		SourceUserName: sourceName,
		TargetUserId:   targetID,
		TargetUserName: targetName,
	})
	if err != nil {
		slog.Debug("playback-guard: monitor user merge failed", "error", err)
		return 0
	}
	return resp.GetSessionsUpdated()
}

func (m *Module) mergeTrustScoresTx(ctx context.Context, tx *sql.Tx, sourceID, sourceName, targetID, targetName string) error {
	sourceKey := trustUserKey(sourceID, sourceName)
	targetKey := trustUserKey(targetID, targetName)
	var sourceScore sql.NullInt64
	err := tx.QueryRowContext(ctx, `SELECT score FROM trust_scores WHERE user_key = ?`, sourceKey).Scan(&sourceScore)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	targetScore := defaultTrustScore
	err = tx.QueryRowContext(ctx, `SELECT score FROM trust_scores WHERE user_key = ?`, targetKey).Scan(&targetScore)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	merged := targetScore
	if sourceScore.Valid && int(sourceScore.Int64) < merged {
		merged = int(sourceScore.Int64)
	}
	now := nowRFC3339()
	_, err = tx.ExecContext(ctx, `
		INSERT INTO trust_scores(user_key, user_id, user_name, score, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(user_key) DO UPDATE SET
			user_id = excluded.user_id,
			user_name = excluded.user_name,
			score = MIN(trust_scores.score, excluded.score),
			updated_at = excluded.updated_at`,
		targetKey, targetID, targetName, merged, now,
	)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM trust_scores WHERE user_key = ?`, sourceKey)
	return err
}

func (m *Module) MergeUsers(ctx context.Context, req *guardv1.MergeUsersRequest) (*guardv1.MergeUsersResponse, error) {
	violationsUpdated, sessionsUpdated, err := m.mergeUsers(ctx, req.GetSourceUserId(), req.GetSourceUserName(), req.GetTargetUserId(), req.GetTargetUserName())
	if err != nil {
		return nil, err
	}
	return &guardv1.MergeUsersResponse{
		ViolationsUpdated: violationsUpdated,
		AliasesCreated:    1,
		SessionsUpdated:   sessionsUpdated,
	}, nil
}
