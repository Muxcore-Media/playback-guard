package internal

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	guardv1 "github.com/Muxcore-Media/playback-guard/proto/guardv1"
)

const defaultTrustScore = 100

func trustUserKey(userID, userName string) string {
	if id := strings.TrimSpace(userID); id != "" {
		return "id:" + id
	}
	return "name:" + strings.ToLower(strings.TrimSpace(userName))
}

func (m *Module) getTrustScore(ctx context.Context, userID, userName string) (*guardv1.TrustScore, error) {
	key := trustUserKey(userID, userName)
	if key == "name:" {
		return nil, fmt.Errorf("user_id or user_name required")
	}
	db, err := m.dbConn()
	if err != nil {
		return nil, err
	}
	var score int
	var uid, uname, updated string
	err = db.QueryRowContext(ctx,
		`SELECT user_id, user_name, score, updated_at FROM trust_scores WHERE user_key = ?`, key,
	).Scan(&uid, &uname, &score, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return &guardv1.TrustScore{
			UserId:   userID,
			UserName: userName,
			Score:    defaultTrustScore,
		}, nil
	}
	if err != nil {
		return nil, err
	}
	ts := &guardv1.TrustScore{UserId: uid, UserName: uname, Score: clampInt32(score)}
	if t, err := timeParseRFC3339(updated); err == nil {
		ts.UpdatedAtUnix = t.Unix()
	}
	return ts, nil
}

func (m *Module) listTrustScores(ctx context.Context, limit int) ([]*guardv1.TrustScore, error) {
	if limit <= 0 {
		limit = 100
	}
	db, err := m.dbConn()
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `
		SELECT user_id, user_name, score, updated_at FROM trust_scores
		ORDER BY score ASC, updated_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make([]*guardv1.TrustScore, 0)
	for rows.Next() {
		var ts guardv1.TrustScore
		var updated string
		if err := rows.Scan(&ts.UserId, &ts.UserName, &ts.Score, &updated); err != nil {
			return nil, err
		}
		if t, err := timeParseRFC3339(updated); err == nil {
			ts.UpdatedAtUnix = t.Unix()
		}
		out = append(out, &ts)
	}
	return out, rows.Err()
}

func (m *Module) adjustTrustOnViolation(ctx context.Context, userID, userName string, penalty int) error {
	userID = strings.TrimSpace(userID)
	if m.ledgerErased(userID) {
		return errUserErased
	}
	db, err := m.dbConn()
	if err != nil {
		return err
	}
	return adjustTrustExec(ctx, db, userID, userName, penalty)
}

// adjustTrustExec lowers a trust score. The write is one guarded statement: it
// does nothing for an erased user id (errUserErased), whether the erasure was
// recorded before or after this process started.
func adjustTrustExec(ctx context.Context, x execer, userID, userName string, penalty int) error {
	if penalty <= 0 {
		penalty = 10
	}
	key := trustUserKey(userID, userName)
	if key == "name:" {
		return nil
	}
	now := nowRFC3339()
	res, err := x.ExecContext(ctx, `
		INSERT INTO trust_scores(user_key, user_id, user_name, score, updated_at)
		SELECT ?, ?, ?, ?, ?
		WHERE `+notErasedSQL+`
		ON CONFLICT(user_key) DO UPDATE SET
			user_id = excluded.user_id,
			user_name = excluded.user_name,
			score = MAX(0, trust_scores.score - ?),
			updated_at = excluded.updated_at`,
		key, userID, userName, defaultTrustScore-penalty, now, strings.TrimSpace(userID), penalty,
	)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		if err != nil {
			return err
		}
		return errUserErased
	}
	return nil
}

func (m *Module) resetTrustScore(ctx context.Context, userID, userName string) (*guardv1.TrustScore, error) {
	key := trustUserKey(userID, userName)
	if key == "name:" {
		return nil, fmt.Errorf("user_id or user_name required")
	}
	userID = strings.TrimSpace(userID)
	if m.ledgerErased(userID) {
		return nil, errUserErased
	}
	db, err := m.dbConn()
	if err != nil {
		return nil, err
	}
	now := nowRFC3339()
	res, err := db.ExecContext(ctx, `
		INSERT INTO trust_scores(user_key, user_id, user_name, score, updated_at)
		SELECT ?, ?, ?, ?, ?
		WHERE `+notErasedSQL+`
		ON CONFLICT(user_key) DO UPDATE SET
			score = ?,
			updated_at = excluded.updated_at`,
		key, userID, userName, defaultTrustScore, now, userID, defaultTrustScore,
	)
	if err != nil {
		return nil, err
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		if err != nil {
			return nil, err
		}
		return nil, errUserErased
	}
	return m.getTrustScore(ctx, userID, userName)
}

func (m *Module) GetTrustScore(ctx context.Context, req *guardv1.GetTrustScoreRequest) (*guardv1.GetTrustScoreResponse, error) {
	score, err := m.getTrustScore(ctx, req.GetUserId(), req.GetUserName())
	if err != nil {
		return nil, err
	}
	return &guardv1.GetTrustScoreResponse{Score: score}, nil
}

func (m *Module) ListTrustScores(ctx context.Context, req *guardv1.ListTrustScoresRequest) (*guardv1.ListTrustScoresResponse, error) {
	scores, err := m.listTrustScores(ctx, int(req.GetLimit()))
	if err != nil {
		return nil, err
	}
	return &guardv1.ListTrustScoresResponse{Scores: scores}, nil
}

func (m *Module) ResetTrustScore(ctx context.Context, req *guardv1.ResetTrustScoreRequest) (*guardv1.ResetTrustScoreResponse, error) {
	score, err := m.resetTrustScore(ctx, req.GetUserId(), req.GetUserName())
	if err != nil {
		return nil, err
	}
	return &guardv1.ResetTrustScoreResponse{Score: score}, nil
}
