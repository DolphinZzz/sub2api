package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"hash/fnv"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

type studioRepository struct {
	db *sql.DB
}

func NewStudioRepository(db *sql.DB) service.StudioRepository {
	return &studioRepository{db: db}
}

func (r *studioRepository) CreateSession(ctx context.Context, session *service.StudioSession) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO studio_sessions
			(id, user_id, title, mode, status, metadata_path, expires_at, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		session.ID, session.UserID, session.Title, session.Mode, session.Status,
		session.MetadataPath, session.ExpiresAt, session.CreatedAt, session.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert studio session: %w", err)
	}
	return nil
}

func (r *studioRepository) ListSessions(ctx context.Context, userID int64) ([]service.StudioSession, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, user_id, title, mode, status, metadata_path, expires_at, created_at, updated_at
		FROM studio_sessions
		WHERE user_id = $1 AND status = $2
		ORDER BY updated_at DESC, id DESC`, userID, service.StudioSessionStatusActive)
	if err != nil {
		return nil, fmt.Errorf("query studio sessions: %w", err)
	}
	defer func() { _ = rows.Close() }()

	sessions := make([]service.StudioSession, 0)
	for rows.Next() {
		var session service.StudioSession
		if err := scanStudioSession(rows, &session); err != nil {
			return nil, err
		}
		sessions = append(sessions, session)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate studio sessions: %w", err)
	}
	return sessions, nil
}

func (r *studioRepository) GetSession(ctx context.Context, userID int64, id string) (*service.StudioSession, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, user_id, title, mode, status, metadata_path, expires_at, created_at, updated_at
		FROM studio_sessions
		WHERE user_id = $1 AND id = $2`, userID, id)
	var session service.StudioSession
	if err := scanStudioSession(row, &session); err != nil {
		return nil, studioRowError("session", err)
	}
	return &session, nil
}

func (r *studioRepository) TouchSession(ctx context.Context, userID int64, id, title, mode string, expiresAt time.Time) error {
	result, err := r.db.ExecContext(ctx, `
		UPDATE studio_sessions
		SET title = CASE WHEN $3 = '' THEN title ELSE $3 END,
			mode = CASE WHEN $4 = '' THEN mode ELSE $4 END,
			expires_at = $5,
			updated_at = NOW()
		WHERE user_id = $1 AND id = $2 AND status = $6`,
		userID, id, title, mode, expiresAt, service.StudioSessionStatusActive)
	if err != nil {
		return fmt.Errorf("touch studio session: %w", err)
	}
	return studioRequireAffected(result, "session")
}

func (r *studioRepository) CreateMessage(ctx context.Context, message *service.StudioMessage) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO studio_messages
			(id, session_id, user_id, turn_id, role, message_type, status, metadata_path, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		message.ID, message.SessionID, message.UserID, message.TurnID, message.Role,
		message.MessageType, message.Status, message.MetadataPath, message.CreatedAt, message.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert studio message: %w", err)
	}
	return nil
}

func (r *studioRepository) UpsertMessage(ctx context.Context, message *service.StudioMessage) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO studio_messages
			(id, session_id, user_id, turn_id, role, message_type, status, metadata_path, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (id) DO UPDATE SET
			message_type = EXCLUDED.message_type,
			status = EXCLUDED.status,
			metadata_path = EXCLUDED.metadata_path,
			updated_at = EXCLUDED.updated_at
		WHERE studio_messages.user_id = EXCLUDED.user_id
			AND studio_messages.session_id = EXCLUDED.session_id`,
		message.ID, message.SessionID, message.UserID, message.TurnID, message.Role,
		message.MessageType, message.Status, message.MetadataPath, message.CreatedAt, message.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("upsert studio message: %w", err)
	}
	return nil
}

func (r *studioRepository) ListMessages(ctx context.Context, userID int64, sessionID string) ([]service.StudioMessage, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, session_id, user_id, turn_id, role, message_type, status, metadata_path, created_at, updated_at
		FROM studio_messages
		WHERE user_id = $1 AND session_id = $2
		ORDER BY created_at ASC, id ASC`, userID, sessionID)
	if err != nil {
		return nil, fmt.Errorf("query studio messages: %w", err)
	}
	defer func() { _ = rows.Close() }()

	messages := make([]service.StudioMessage, 0)
	for rows.Next() {
		var message service.StudioMessage
		if err := scanStudioMessage(rows, &message); err != nil {
			return nil, err
		}
		messages = append(messages, message)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate studio messages: %w", err)
	}
	return messages, nil
}

func (r *studioRepository) CreateRequest(ctx context.Context, request *service.StudioRequest) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin studio request transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockStudioSession(ctx, tx, request.SessionID); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `
		INSERT INTO studio_requests
			(id, session_id, user_id, turn_id, api_key_id, api_key_name, endpoint, model, status,
			 async_task_id, request_path, response_path, duration_ms, error_code, error_message, created_at, updated_at, completed_at)
		SELECT $1::varchar, $2::varchar, $3::bigint, $4::varchar, $5::bigint, $6::varchar, $7::varchar,
			$8::varchar, $9::varchar, $10::varchar, $11::varchar, $12::varchar, $13::bigint, $14::varchar, $15::text,
			$16::timestamptz, $17::timestamptz, $18::timestamptz
		FROM studio_sessions
		WHERE id = $2::varchar AND user_id = $3::bigint AND status = $19::varchar`,
		request.ID, request.SessionID, request.UserID, request.TurnID, request.APIKeyID,
		request.APIKeyName, request.Endpoint, request.Model, request.Status, request.AsyncTaskID,
		request.RequestPath, request.ResponsePath, request.DurationMS, request.ErrorCode, request.ErrorMessage,
		request.CreatedAt, request.UpdatedAt, request.CompletedAt, service.StudioSessionStatusActive,
	)
	if err != nil {
		return fmt.Errorf("insert studio request: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read studio request insert result: %w", err)
	}
	if affected == 0 {
		var status string
		err := tx.QueryRowContext(ctx, `
			SELECT status FROM studio_sessions WHERE id = $1 AND user_id = $2`,
			request.SessionID, request.UserID).Scan(&status)
		if errors.Is(err, sql.ErrNoRows) {
			return service.ErrStudioSessionNotFound.WithCause(err)
		}
		if err != nil {
			return fmt.Errorf("read studio session state: %w", err)
		}
		return service.ErrStudioSessionDeleting
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit studio request: %w", err)
	}
	return nil
}

func (r *studioRepository) SetRequestAsyncTask(ctx context.Context, userID int64, requestID, taskID string) error {
	result, err := r.db.ExecContext(ctx, `
		UPDATE studio_requests
		SET async_task_id = $3, updated_at = NOW()
		WHERE user_id = $1 AND id = $2 AND status = $4`,
		userID, requestID, taskID, service.StudioRequestRunning)
	if err != nil {
		return fmt.Errorf("set studio request async task: %w", err)
	}
	return studioRequireAffected(result, "request")
}

func (r *studioRepository) CompleteRequest(ctx context.Context, userID int64, request *service.StudioRequest) error {
	result, err := r.db.ExecContext(ctx, `
		UPDATE studio_requests
		SET status = $3, response_path = $4, duration_ms = $5, error_code = $6,
			error_message = $7, updated_at = $8, completed_at = $9
		WHERE user_id = $1 AND id = $2`,
		userID, request.ID, request.Status, request.ResponsePath, request.DurationMS,
		request.ErrorCode, request.ErrorMessage, request.UpdatedAt, request.CompletedAt,
	)
	if err != nil {
		return fmt.Errorf("complete studio request: %w", err)
	}
	return studioRequireAffected(result, "request")
}

func (r *studioRepository) GetRequest(ctx context.Context, userID int64, id string) (*service.StudioRequest, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, session_id, user_id, turn_id, api_key_id, api_key_name, endpoint, model, status,
			async_task_id, request_path, response_path, duration_ms, error_code, error_message, created_at, updated_at, completed_at
		FROM studio_requests
		WHERE user_id = $1 AND id = $2`, userID, id)
	var request service.StudioRequest
	if err := scanStudioRequest(row, &request); err != nil {
		return nil, studioRowError("request", err)
	}
	return &request, nil
}

func (r *studioRepository) CreateGeneration(ctx context.Context, generation *service.StudioGenerationRecord) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO studio_generation_records
			(id, session_id, user_id, request_id, message_id, status, metadata_path, revised_prompt, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		generation.ID, generation.SessionID, generation.UserID, generation.RequestID,
		generation.MessageID, generation.Status, generation.MetadataPath, generation.RevisedPrompt,
		generation.CreatedAt, generation.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert studio generation: %w", err)
	}
	return nil
}

func (r *studioRepository) CreateAsset(ctx context.Context, asset *service.StudioAsset) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO studio_assets
			(id, session_id, user_id, request_id, generation_id, kind, sha256, mime_type, byte_size, relative_path, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		asset.ID, asset.SessionID, asset.UserID, asset.RequestID, asset.GenerationID,
		asset.Kind, asset.SHA256, asset.MIMEType, asset.ByteSize, asset.RelativePath, asset.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert studio asset: %w", err)
	}
	return nil
}

func (r *studioRepository) GetAsset(ctx context.Context, userID int64, id string) (*service.StudioAsset, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, session_id, user_id, request_id, generation_id, kind, sha256, mime_type, byte_size, relative_path, created_at
		FROM studio_assets
		WHERE user_id = $1 AND id = $2`, userID, id)
	var asset service.StudioAsset
	if err := scanStudioAsset(row, &asset); err != nil {
		return nil, studioRowError("asset", err)
	}
	return &asset, nil
}

func (r *studioRepository) HasRunningRequests(ctx context.Context, userID int64, sessionID string) (bool, error) {
	var exists bool
	err := r.db.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM studio_requests
			WHERE user_id = $1 AND session_id = $2 AND status = $3
		)`, userID, sessionID, service.StudioRequestRunning).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("query running studio requests: %w", err)
	}
	return exists, nil
}

func (r *studioRepository) MarkSessionDeleting(ctx context.Context, userID int64, id string) (bool, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin studio delete transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockStudioSession(ctx, tx, id); err != nil {
		return false, err
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE studio_sessions
		SET status = $3, updated_at = NOW()
		WHERE user_id = $1 AND id = $2 AND status = $4
			AND NOT EXISTS (
				SELECT 1 FROM studio_requests
				WHERE user_id = $1 AND session_id = $2 AND status = $5
			)`,
		userID, id, service.StudioSessionStatusDeleting, service.StudioSessionStatusActive, service.StudioRequestRunning)
	if err != nil {
		return false, fmt.Errorf("mark studio session deleting: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("read studio delete result: %w", err)
	}
	if affected > 0 {
		if err := tx.Commit(); err != nil {
			return false, fmt.Errorf("commit studio session deleting: %w", err)
		}
		return true, nil
	}
	var status string
	var running bool
	err = tx.QueryRowContext(ctx, `
		SELECT status, EXISTS (
			SELECT 1 FROM studio_requests
			WHERE user_id = $1 AND session_id = $2 AND status = $3
		)
		FROM studio_sessions
		WHERE user_id = $1 AND id = $2`, userID, id, service.StudioRequestRunning).Scan(&status, &running)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read studio delete state: %w", err)
	}
	if running {
		return false, service.ErrStudioSessionBusy
	}
	return false, nil
}

func (r *studioRepository) DeleteSession(ctx context.Context, userID int64, id string) error {
	_, err := r.db.ExecContext(ctx, `
		DELETE FROM studio_sessions
		WHERE user_id = $1 AND id = $2 AND status = $3`,
		userID, id, service.StudioSessionStatusDeleting)
	if err != nil {
		return fmt.Errorf("delete studio session: %w", err)
	}
	return nil
}

func (r *studioRepository) ListCleanupCandidates(ctx context.Context, now time.Time, limit int) ([]service.StudioSession, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, user_id, title, mode, status, metadata_path, expires_at, created_at, updated_at
		FROM studio_sessions
		WHERE status = $1 OR (status = $2 AND expires_at <= $3)
		ORDER BY CASE WHEN status = $1 THEN 0 ELSE 1 END, expires_at ASC, id ASC
		LIMIT $4`, service.StudioSessionStatusDeleting, service.StudioSessionStatusActive, now, limit)
	if err != nil {
		return nil, fmt.Errorf("query studio cleanup candidates: %w", err)
	}
	defer func() { _ = rows.Close() }()

	sessions := make([]service.StudioSession, 0)
	for rows.Next() {
		var session service.StudioSession
		if err := scanStudioSession(rows, &session); err != nil {
			return nil, err
		}
		sessions = append(sessions, session)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate studio cleanup candidates: %w", err)
	}
	return sessions, nil
}

type studioScanner interface {
	Scan(dest ...any) error
}

func scanStudioSession(row studioScanner, session *service.StudioSession) error {
	if err := row.Scan(&session.ID, &session.UserID, &session.Title, &session.Mode, &session.Status,
		&session.MetadataPath, &session.ExpiresAt, &session.CreatedAt, &session.UpdatedAt); err != nil {
		return fmt.Errorf("scan studio session: %w", err)
	}
	return nil
}

func scanStudioMessage(row studioScanner, message *service.StudioMessage) error {
	var turnID sql.NullString
	if err := row.Scan(&message.ID, &message.SessionID, &message.UserID, &turnID, &message.Role,
		&message.MessageType, &message.Status, &message.MetadataPath, &message.CreatedAt, &message.UpdatedAt); err != nil {
		return fmt.Errorf("scan studio message: %w", err)
	}
	if turnID.Valid {
		message.TurnID = &turnID.String
	}
	return nil
}

func scanStudioRequest(row studioScanner, request *service.StudioRequest) error {
	var apiKeyID, duration sql.NullInt64
	var asyncTaskID, responsePath, errorCode, errorMessage sql.NullString
	var completedAt sql.NullTime
	if err := row.Scan(&request.ID, &request.SessionID, &request.UserID, &request.TurnID,
		&apiKeyID, &request.APIKeyName, &request.Endpoint, &request.Model, &request.Status,
		&asyncTaskID, &request.RequestPath, &responsePath, &duration, &errorCode, &errorMessage,
		&request.CreatedAt, &request.UpdatedAt, &completedAt); err != nil {
		return fmt.Errorf("scan studio request: %w", err)
	}
	if apiKeyID.Valid {
		request.APIKeyID = &apiKeyID.Int64
	}
	if asyncTaskID.Valid {
		request.AsyncTaskID = &asyncTaskID.String
	}
	if responsePath.Valid {
		request.ResponsePath = &responsePath.String
	}
	if duration.Valid {
		request.DurationMS = &duration.Int64
	}
	if errorCode.Valid {
		request.ErrorCode = &errorCode.String
	}
	if errorMessage.Valid {
		request.ErrorMessage = &errorMessage.String
	}
	if completedAt.Valid {
		request.CompletedAt = &completedAt.Time
	}
	return nil
}

func scanStudioAsset(row studioScanner, asset *service.StudioAsset) error {
	var requestID, generationID sql.NullString
	if err := row.Scan(&asset.ID, &asset.SessionID, &asset.UserID, &requestID, &generationID,
		&asset.Kind, &asset.SHA256, &asset.MIMEType, &asset.ByteSize, &asset.RelativePath, &asset.CreatedAt); err != nil {
		return fmt.Errorf("scan studio asset: %w", err)
	}
	if requestID.Valid {
		asset.RequestID = &requestID.String
	}
	if generationID.Valid {
		asset.GenerationID = &generationID.String
	}
	return nil
}

func studioRowError(kind string, err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return translatePersistenceError(err, studioNotFoundError(kind), nil)
	}
	return err
}

func studioRequireAffected(result sql.Result, kind string) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read studio %s result: %w", kind, err)
	}
	if affected == 0 {
		return translatePersistenceError(sql.ErrNoRows, studioNotFoundError(kind), nil)
	}
	return nil
}

func studioNotFoundError(kind string) *infraerrors.ApplicationError {
	switch kind {
	case "session":
		return service.ErrStudioSessionNotFound
	case "request":
		return service.ErrStudioRequestNotFound
	case "asset":
		return service.ErrStudioAssetNotFound
	default:
		return nil
	}
}

var _ service.StudioRepository = (*studioRepository)(nil)

func lockStudioSession(ctx context.Context, tx *sql.Tx, sessionID string) error {
	h := fnv.New64a()
	_, _ = h.Write([]byte("studio-session:" + sessionID))
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, int64(h.Sum64())); err != nil {
		return fmt.Errorf("lock studio session: %w", err)
	}
	return nil
}
