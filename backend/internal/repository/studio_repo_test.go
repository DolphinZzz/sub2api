package repository

import (
	"context"
	"database/sql"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func newStudioRepositoryMock(t *testing.T) (*studioRepository, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	// Closing sqlmock after all assertions may report an unconfigured Close call;
	// the query expectations below are the lifecycle contract under test.
	t.Cleanup(func() { _ = db.Close() })
	return &studioRepository{db: db}, mock
}

func TestStudioRepositoryGetAssetScopesByUser(t *testing.T) {
	repo, mock := newStudioRepositoryMock(t)
	now := time.Now().UTC()
	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT id, session_id, user_id, request_id, generation_id, kind, sha256, mime_type, byte_size, relative_path, created_at
		FROM studio_assets
		WHERE user_id = $1 AND id = $2`)).
		WithArgs(int64(42), "asset-1").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "session_id", "user_id", "request_id", "generation_id", "kind",
			"sha256", "mime_type", "byte_size", "relative_path", "created_at",
		}).AddRow("asset-1", "session-1", int64(42), "request-1", "generation-1", "output",
			"abc", "image/png", int64(3), "users/42/sessions/session-1/images/asset-1.png", now))

	asset, err := repo.GetAsset(context.Background(), 42, "asset-1")
	require.NoError(t, err)
	require.Equal(t, int64(42), asset.UserID)
	require.Equal(t, "request-1", *asset.RequestID)
	require.Equal(t, "generation-1", *asset.GenerationID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestStudioRepositoryGetSessionPreservesNotFoundCause(t *testing.T) {
	repo, mock := newStudioRepositoryMock(t)
	mock.ExpectQuery("FROM studio_sessions").
		WithArgs(int64(7), "missing").
		WillReturnError(sql.ErrNoRows)

	_, err := repo.GetSession(context.Background(), 7, "missing")
	require.Error(t, err)
	require.ErrorIs(t, err, sql.ErrNoRows)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestStudioRepositoryCleanupIncludesDeletingAndExpired(t *testing.T) {
	repo, mock := newStudioRepositoryMock(t)
	now := time.Now().UTC()
	expired := now.Add(-time.Hour)
	created := now.Add(-48 * time.Hour)
	mock.ExpectQuery("WHERE status = \\$1 OR \\(status = \\$2 AND expires_at <= \\$3\\)").
		WithArgs(service.StudioSessionStatusDeleting, service.StudioSessionStatusActive, now, 25).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "user_id", "title", "mode", "status", "metadata_path", "expires_at", "created_at", "updated_at",
		}).
			AddRow("deleting-1", int64(1), "old", "chat", service.StudioSessionStatusDeleting,
				"users/1/sessions/deleting-1/session.json", expired, created, expired).
			AddRow("expired-1", int64(2), "expired", "image", service.StudioSessionStatusActive,
				"users/2/sessions/expired-1/session.json", expired, created, expired))

	sessions, err := repo.ListCleanupCandidates(context.Background(), now, 25)
	require.NoError(t, err)
	require.Len(t, sessions, 2)
	require.Equal(t, service.StudioSessionStatusDeleting, sessions[0].Status)
	require.Equal(t, int64(2), sessions[1].UserID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestStudioRepositoryCompleteRequestDoesNotStorePayload(t *testing.T) {
	repo, mock := newStudioRepositoryMock(t)
	now := time.Now().UTC()
	duration := int64(123)
	completed := now
	request := &service.StudioRequest{
		ID:          "request-1",
		UserID:      9,
		Status:      service.StudioRequestCompleted,
		DurationMS:  &duration,
		UpdatedAt:   now,
		CompletedAt: &completed,
		Payload:     []byte(`{"secret":"must stay in file"}`),
	}
	mock.ExpectExec("UPDATE studio_requests").
		WithArgs(int64(9), "request-1", service.StudioRequestCompleted, nil, &duration, nil, nil, now, &completed).
		WillReturnResult(sqlmock.NewResult(0, 1))

	require.NoError(t, repo.CompleteRequest(context.Background(), 9, request))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestStudioRepositoryCreateRequestRequiresActiveSession(t *testing.T) {
	repo, mock := newStudioRepositoryMock(t)
	now := time.Now().UTC()
	request := &service.StudioRequest{
		ID: "request-1", SessionID: "session-1", UserID: 9, TurnID: "turn-1",
		APIKeyName: "key", Endpoint: "/v1/responses", Model: "gpt-5.5",
		Status: service.StudioRequestRunning, RequestPath: "users/9/sessions/session-1/requests/request-1.request.json",
		CreatedAt: now, UpdatedAt: now,
	}

	mock.ExpectBegin()
	mock.ExpectExec("SELECT pg_advisory_xact_lock").WithArgs(sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO studio_requests").
		WithArgs(
			request.ID, request.SessionID, request.UserID, request.TurnID, request.APIKeyID,
			request.APIKeyName, request.Endpoint, request.Model, request.Status, request.AsyncTaskID,
			request.RequestPath, request.ResponsePath, request.DurationMS, request.ErrorCode, request.ErrorMessage,
			request.CreatedAt, request.UpdatedAt, request.CompletedAt, service.StudioSessionStatusActive,
		).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	require.NoError(t, repo.CreateRequest(context.Background(), request))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestStudioRepositoryCreateRequestRejectsDeletingSession(t *testing.T) {
	repo, mock := newStudioRepositoryMock(t)
	now := time.Now().UTC()
	request := &service.StudioRequest{
		ID: "request-1", SessionID: "session-1", UserID: 9, TurnID: "turn-1",
		APIKeyName: "key", Endpoint: "/v1/responses", Model: "gpt-5.5",
		Status: service.StudioRequestRunning, RequestPath: "request.json", CreatedAt: now, UpdatedAt: now,
	}

	mock.ExpectBegin()
	mock.ExpectExec("SELECT pg_advisory_xact_lock").WithArgs(sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO studio_requests").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("SELECT status FROM studio_sessions").
		WithArgs(request.SessionID, request.UserID).
		WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow(service.StudioSessionStatusDeleting))
	mock.ExpectRollback()

	err := repo.CreateRequest(context.Background(), request)
	require.ErrorIs(t, err, service.ErrStudioSessionDeleting)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestStudioRepositoryMarkDeletingRechecksRunningUnderLock(t *testing.T) {
	repo, mock := newStudioRepositoryMock(t)
	mock.ExpectBegin()
	mock.ExpectExec("SELECT pg_advisory_xact_lock").WithArgs(sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE studio_sessions").
		WithArgs(int64(9), "session-1", service.StudioSessionStatusDeleting, service.StudioSessionStatusActive, service.StudioRequestRunning).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("SELECT status, EXISTS").
		WithArgs(int64(9), "session-1", service.StudioRequestRunning).
		WillReturnRows(sqlmock.NewRows([]string{"status", "exists"}).AddRow(service.StudioSessionStatusActive, true))
	mock.ExpectRollback()

	marked, err := repo.MarkSessionDeleting(context.Background(), 9, "session-1")
	require.False(t, marked)
	require.ErrorIs(t, err, service.ErrStudioSessionBusy)
	require.NoError(t, mock.ExpectationsWereMet())
}
