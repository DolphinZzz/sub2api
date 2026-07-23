package service

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestStudioService_DeleteSessionRejectsRunningRequest(t *testing.T) {
	repo := &studioServiceRepoStub{session: StudioSession{ID: "session-1", UserID: 7, Status: StudioSessionStatusActive}, running: true}
	svc := NewStudioService(repo, &studioServiceStorageStub{}, nil, nil)
	err := svc.DeleteSession(context.Background(), 7, "session-1")
	require.ErrorIs(t, err, ErrStudioSessionBusy)
	require.False(t, repo.marked)
	require.False(t, repo.deleted)
}

func TestStudioService_CleanupRetriesDeletingAndExpiresActive(t *testing.T) {
	now := time.Date(2026, 7, 15, 1, 2, 3, 0, time.UTC)
	repo := &studioServiceRepoStub{candidates: []StudioSession{
		{ID: "deleting", UserID: 7, Status: StudioSessionStatusDeleting},
		{ID: "expired", UserID: 8, Status: StudioSessionStatusActive},
	}}
	storage := &studioServiceStorageStub{}
	svc := NewStudioService(repo, storage, nil, &config.Config{Studio: config.StudioConfig{CleanupBatchSize: 10}})
	svc.now = func() time.Time { return now }
	require.NoError(t, svc.CleanupOnce(context.Background()))
	require.Equal(t, []string{"7/deleting", "8/expired"}, storage.quarantined)
	require.Equal(t, []string{"7/deleting", "8/expired"}, repo.deletedIDs)
	require.Equal(t, 1, repo.markCount, "only the active candidate should be marked")
}

func TestStudioService_DeleteSessionRetriesAlreadyDeletingSession(t *testing.T) {
	repo := &studioServiceRepoStub{session: StudioSession{ID: "session-1", UserID: 7, Status: StudioSessionStatusDeleting}}
	storage := &studioServiceStorageStub{}
	svc := NewStudioService(repo, storage, nil, nil)

	require.NoError(t, svc.DeleteSession(context.Background(), 7, "session-1"))
	require.False(t, repo.marked)
	require.True(t, repo.deleted)
	require.Equal(t, []string{"7/session-1"}, storage.quarantined)
}

func TestStudioService_FinishRequestRefreshesRequestDocument(t *testing.T) {
	now := time.Date(2026, 7, 15, 2, 3, 4, 0, time.UTC)
	repo := &studioServiceRepoStub{session: StudioSession{ID: "session-1", UserID: 7, Status: StudioSessionStatusActive}}
	storage := &studioServiceStorageStub{}
	svc := NewStudioService(repo, storage, nil, nil)
	svc.now = func() time.Time { return now }
	rc := &StudioRequestContext{
		Request:          &StudioRequest{ID: "request-1", SessionID: "session-1", UserID: 7, Status: StudioRequestRunning, CreatedAt: now.Add(-time.Second)},
		UserMessage:      &StudioMessage{Content: "hello"},
		AssistantMessage: &StudioMessage{ID: "assistant-1", SessionID: "session-1", UserID: 7, Role: "assistant", MessageType: "text", Status: "running", CreatedAt: now.Add(-time.Second)},
		StartedAt:        now.Add(-time.Second),
		Mode:             "chat",
	}

	require.NoError(t, svc.FinishRequest(context.Background(), rc, StudioRequestCompleted, "", "", nil))
	require.Len(t, storage.requestWrites, 1)
	require.Equal(t, StudioRequestCompleted, storage.requestWrites[0].Status)
	require.NotNil(t, storage.requestWrites[0].CompletedAt)
	require.Nil(t, storage.requestWrites[0].Response, "response body belongs in response.json")
	require.Equal(t, now.Add(30*24*time.Hour), repo.touchedExpires)
}

func TestStudioService_FinishRequestMarksPersistenceFailure(t *testing.T) {
	now := time.Now().UTC()
	repo := &studioServiceRepoStub{session: StudioSession{ID: "session-1", UserID: 7, Status: StudioSessionStatusActive}}
	storage := &studioServiceStorageStub{responseErr: errors.New("disk full")}
	svc := NewStudioService(repo, storage, nil, nil)
	rc := &StudioRequestContext{
		Request:          &StudioRequest{ID: "request-1", SessionID: "session-1", UserID: 7, Status: StudioRequestRunning, CreatedAt: now},
		UserMessage:      &StudioMessage{Content: "hello"},
		AssistantMessage: &StudioMessage{ID: "assistant-1", SessionID: "session-1", UserID: 7, Role: "assistant", MessageType: "text", Status: "running", CreatedAt: now},
		StartedAt:        now,
	}

	err := svc.FinishRequest(context.Background(), rc, StudioRequestCompleted, "", "", nil)
	require.ErrorContains(t, err, "disk full")
	require.Equal(t, StudioRequestPersistFailed, repo.completedStatus)
}

func TestStudioService_GetOwnedAPIKeyEnforcesUserOwnership(t *testing.T) {
	keyRepo := &studioAPIKeyRepoStub{key: &APIKey{ID: 99, UserID: 7, Name: "owned"}}
	apiKeys := NewAPIKeyService(keyRepo, nil, nil, nil, nil, nil, nil)
	svc := NewStudioService(&studioServiceRepoStub{}, &studioServiceStorageStub{}, apiKeys, nil)

	key, err := svc.GetOwnedAPIKey(context.Background(), 7, 99)
	require.NoError(t, err)
	require.Equal(t, int64(99), key.ID)
	_, err = svc.GetOwnedAPIKey(context.Background(), 8, 99)
	require.Error(t, err)
	require.ErrorIs(t, err, ErrStudioAPIKeyNotFound)
}

func TestStudioService_PersistInputAssetsRemovesDataURL(t *testing.T) {
	storage, err := NewStudioFileStorage(&config.Config{Studio: config.StudioConfig{StorageRoot: t.TempDir()}})
	require.NoError(t, err)
	svc := NewStudioService(nil, storage, nil, nil)
	encoded := base64.StdEncoding.EncodeToString([]byte("reference-image"))
	payload := map[string]any{
		"input": []any{map[string]any{
			"type":    "message",
			"role":    "user",
			"content": []any{map[string]any{"type": "input_image", "image_url": "data:image/png;base64," + encoded}},
		}},
	}
	request := &StudioRequest{ID: "request-1", SessionID: "session-1", UserID: 7, CreatedAt: time.Now().UTC()}

	sanitized, assets, err := svc.persistInputAssets(context.Background(), request, payload)
	require.NoError(t, err)
	require.Len(t, assets, 1)
	require.NotContains(t, string(sanitized), "data:image")
	require.NotContains(t, string(sanitized), encoded)
	require.Contains(t, string(sanitized), `"asset_id"`)
	require.Equal(t, "image/png", assets[0].MIMEType)
}

type studioServiceRepoStub struct {
	session                  StudioSession
	running, marked, deleted bool
	candidates               []StudioSession
	deletedIDs               []string
	markCount                int
	completedStatus          string
	touchedExpires           time.Time
}

func (r *studioServiceRepoStub) CreateSession(context.Context, *StudioSession) error { return nil }
func (r *studioServiceRepoStub) ListSessions(context.Context, int64) ([]StudioSession, error) {
	return nil, nil
}
func (r *studioServiceRepoStub) GetSession(_ context.Context, _ int64, _ string) (*StudioSession, error) {
	v := r.session
	return &v, nil
}
func (r *studioServiceRepoStub) TouchSession(_ context.Context, _ int64, _ string, _ string, _ string, expires time.Time) error {
	r.touchedExpires = expires
	return nil
}
func (r *studioServiceRepoStub) CreateMessage(context.Context, *StudioMessage) error { return nil }
func (r *studioServiceRepoStub) UpsertMessage(context.Context, *StudioMessage) error { return nil }
func (r *studioServiceRepoStub) ListMessages(context.Context, int64, string) ([]StudioMessage, error) {
	return nil, nil
}
func (r *studioServiceRepoStub) CreateRequest(context.Context, *StudioRequest) error { return nil }
func (r *studioServiceRepoStub) SetRequestAsyncTask(context.Context, int64, string, string) error {
	return nil
}
func (r *studioServiceRepoStub) CompleteRequest(_ context.Context, _ int64, request *StudioRequest) error {
	r.completedStatus = request.Status
	return nil
}
func (r *studioServiceRepoStub) GetRequest(context.Context, int64, string) (*StudioRequest, error) {
	return nil, ErrStudioRequestNotFound
}
func (r *studioServiceRepoStub) CreateGeneration(context.Context, *StudioGenerationRecord) error {
	return nil
}
func (r *studioServiceRepoStub) CreateAsset(context.Context, *StudioAsset) error { return nil }
func (r *studioServiceRepoStub) GetAsset(context.Context, int64, string) (*StudioAsset, error) {
	return nil, ErrStudioAssetNotFound
}
func (r *studioServiceRepoStub) HasRunningRequests(context.Context, int64, string) (bool, error) {
	return r.running, nil
}
func (r *studioServiceRepoStub) MarkSessionDeleting(context.Context, int64, string) (bool, error) {
	r.marked = true
	r.markCount++
	return true, nil
}
func (r *studioServiceRepoStub) DeleteSession(_ context.Context, userID int64, id string) error {
	r.deleted = true
	r.deletedIDs = append(r.deletedIDs, strings.Join([]string{strconv.FormatInt(userID, 10), id}, "/"))
	return nil
}
func (r *studioServiceRepoStub) ListCleanupCandidates(context.Context, time.Time, int) ([]StudioSession, error) {
	return r.candidates, nil
}

type studioServiceStorageStub struct {
	quarantined   []string
	requestWrites []StudioRequest
	outputData    []byte
	outputFormat  string
	responseErr   error
}

func (*studioServiceStorageStub) WriteSession(context.Context, int64, string, any) (string, error) {
	return "session.json", nil
}
func (*studioServiceStorageStub) WriteMessage(context.Context, int64, string, string, any) (string, error) {
	return "message.json", nil
}

func (s *studioServiceStorageStub) WriteRequest(_ context.Context, _ int64, _ string, _ string, value any) (string, error) {
	if request, ok := value.(*StudioRequest); ok {
		s.requestWrites = append(s.requestWrites, *request)
	}
	return "request.json", nil
}
func (s *studioServiceStorageStub) WriteResponse(context.Context, int64, string, string, any) (string, error) {
	if s.responseErr != nil {
		return "", s.responseErr
	}
	return "response.json", nil
}
func (*studioServiceStorageStub) WriteGeneration(context.Context, int64, string, string, any) (string, error) {
	return "generation.json", nil
}
func (*studioServiceStorageStub) WriteInput(context.Context, int64, string, string, string, []byte) (string, error) {
	return "input.png", nil
}
func (s *studioServiceStorageStub) WriteOutput(_ context.Context, _ int64, _ string, _ string, format string, data []byte) (string, error) {
	s.outputData = append([]byte(nil), data...)
	s.outputFormat = format
	return "output.png", nil
}
func (*studioServiceStorageStub) ReadJSON(context.Context, string, any) error { return nil }
func (*studioServiceStorageStub) OpenAsset(context.Context, string) (io.ReadCloser, error) {
	return os.Open(os.DevNull)
}
func (s *studioServiceStorageStub) QuarantineSession(_ context.Context, userID int64, id string) (string, error) {
	value := strings.Join([]string{strconv.FormatInt(userID, 10), id}, "/")
	s.quarantined = append(s.quarantined, value)
	return ".trash/" + value, nil
}
func (*studioServiceStorageStub) DeleteQuarantine(context.Context, string) error { return nil }

var _ StudioRepository = (*studioServiceRepoStub)(nil)
var _ StudioFileStorage = (*studioServiceStorageStub)(nil)

type studioAPIKeyRepoStub struct {
	APIKeyRepository
	key *APIKey
}

func (r *studioAPIKeyRepoStub) GetByID(context.Context, int64) (*APIKey, error) {
	if r.key == nil {
		return nil, ErrAPIKeyNotFound
	}
	copy := *r.key
	return &copy, nil
}
