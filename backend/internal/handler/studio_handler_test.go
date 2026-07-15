package handler

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestStudioCaptureWriter_PersistsAndDeduplicatesImage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &studioHandlerRepoStub{session: service.StudioSession{ID: "session-1", UserID: 42, Title: "old", Mode: "image", Status: service.StudioSessionStatusActive}}
	storage, err := service.NewStudioFileStorage(&config.Config{Studio: config.StudioConfig{StorageRoot: t.TempDir(), RetentionDays: 30}})
	require.NoError(t, err)
	studio := service.NewStudioService(repo, storage, nil, &config.Config{Studio: config.StudioConfig{RetentionDays: 30}})
	now := time.Now().UTC()
	rc := &service.StudioRequestContext{
		Request:          &service.StudioRequest{ID: "request-1", SessionID: "session-1", UserID: 42, TurnID: "turn-1", Status: service.StudioRequestRunning, CreatedAt: now},
		UserMessage:      &service.StudioMessage{ID: "user-message", SessionID: "session-1", UserID: 42, Content: "draw a square"},
		AssistantMessage: &service.StudioMessage{ID: "assistant-message", SessionID: "session-1", UserID: 42, Role: "assistant", MessageType: "images", Status: "running", CreatedAt: now},
		StartedAt:        now, RequestContext: context.Background(), Mode: "image",
	}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	w := newStudioCaptureWriter(ctx.Writer, studio, rc)
	encoded := base64.StdEncoding.EncodeToString([]byte("fake-png"))
	stream := `data: {"type":"response.output_item.done","item":{"type":"image_generation_call","result":"` + encoded + `","output_format":"png"}}` + "\n\n" +
		`data: {"type":"response.image_generation_call.partial_image","partial_image_b64":"do-not-persist"}` + "\n\n" +
		`data: {"type":"response.completed","response":{"output":[{"type":"image_generation_call","result":"` + encoded + `","output_format":"png"}]}}` + "\n\n" +
		"data: [DONE]\n\n"
	for _, part := range []string{stream[:37], stream[37:91], stream[91:]} {
		_, err := w.Write([]byte(part))
		require.NoError(t, err)
	}
	w.Finish(context.Background())

	body := recorder.Body.String()
	require.Equal(t, 1, strings.Count(body, `"type":"studio.image"`))
	require.NotContains(t, body, encoded)
	require.NotContains(t, body, "do-not-persist")
	require.Contains(t, body, `"type":"studio.persisted"`)
	require.True(t, strings.HasSuffix(body, "data: [DONE]\n\n"))
	require.Len(t, repo.assets, 1)
	require.Len(t, repo.generations, 1)
	require.NotContains(t, string(repo.completed.Response), encoded)
	require.NotContains(t, string(repo.completed.Response), "do-not-persist")
}

func TestStudioEndpointNormalizationIsExact(t *testing.T) {
	require.Equal(t, "https://api.example.com", normalizeStudioEndpoint("https://api.example.com/v1/responses/"))
	require.Equal(t, "https://api.example.com", normalizeStudioEndpoint("https://api.example.com/v1"))

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest("POST", "https://api.example.com/api/v1/studio/sessions/x/responses", nil)
	ctx.Request.Host = "api.example.com"
	require.True(t, studioEndpointMatchesRequestOrigin(ctx, "https://api.example.com"))
	require.False(t, studioEndpointMatchesRequestOrigin(ctx, "https://api.example.com.evil.test"))
	require.False(t, studioEndpointMatchesRequestOrigin(ctx, "https://api.example.com/proxy"))
}

func TestForceStudioImageGenerationToolChoice(t *testing.T) {
	payload := json.RawMessage(`{"model":"gpt-5.5","tools":[{"type":"image_generation","size":"1024x1024","aspect_ratio":"1:1","n":2}],"tool_choice":"auto"}`)

	forced, err := forceStudioImageGenerationToolChoice(payload)
	require.NoError(t, err)
	require.Equal(t, "image_generation", gjson.GetBytes(forced, "tool_choice.type").String())
	require.Equal(t, studioImageGenerationInstructions, gjson.GetBytes(forced, "instructions").String())
	require.Equal(t, "gpt-5.5", gjson.GetBytes(forced, "model").String())
	require.Equal(t, "1024x1024", gjson.GetBytes(forced, "tools.0.size").String())
	require.False(t, gjson.GetBytes(forced, "tools.0.aspect_ratio").Exists())
	require.False(t, gjson.GetBytes(forced, "tools.0.n").Exists())
	require.NotContains(t, string(forced), "gpt-image-2")
}

func TestForceStudioImageGenerationToolChoicePreservesExistingInstructions(t *testing.T) {
	payload := json.RawMessage(`{"model":"gpt-5.5","instructions":"Keep the product label accurate.","tools":[{"type":"image_generation"}]}`)

	forced, err := forceStudioImageGenerationToolChoice(payload)
	require.NoError(t, err)
	instructions := gjson.GetBytes(forced, "instructions").String()
	require.Contains(t, instructions, "Keep the product label accurate.")
	require.Contains(t, instructions, studioImageGenerationInstructions)
}

func TestStudioImageModeAlwaysUsesOpenAIResponsesGateway(t *testing.T) {
	require.True(t, studioUsesOpenAIGateway("image", &service.APIKey{}))
	require.True(t, studioUsesOpenAIGateway("image", &service.APIKey{Group: &service.Group{Platform: service.PlatformAnthropic}}))
	require.True(t, studioUsesOpenAIGateway("chat", &service.APIKey{Group: &service.Group{Platform: service.PlatformOpenAI}}))
	require.False(t, studioUsesOpenAIGateway("chat", &service.APIKey{Group: &service.Group{Platform: service.PlatformAnthropic}}))
}

func TestStudioEndpointAllowsCurrentOriginEvenWhenAPIBaseURLConfigured(t *testing.T) {
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest("POST", "http://localhost:3001/api/v1/studio/sessions/x/responses", nil)
	ctx.Request.Host = "localhost:3001"

	require.True(t, studioEndpointAllowed(ctx, "http://localhost:3001/v1", "https://configured.example.com/v1", "[]"))
}

func TestStudioEndpointAllowsBrowserOriginBehindDevelopmentProxy(t *testing.T) {
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest("POST", "http://127.0.0.1:3000/api/v1/studio/sessions/x/responses", nil)
	ctx.Request.Host = "127.0.0.1:3000"
	ctx.Request.Header.Set("Origin", "http://localhost:3001")

	require.True(t, studioEndpointAllowed(ctx, "http://localhost:3001/v1", "", "[]"))
	require.False(t, studioEndpointAllowed(ctx, "http://localhost:3002/v1", "", "[]"))
}

func TestStudioCaptureWriter_CancelledRequestStillPersistsTerminalStatus(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &studioHandlerRepoStub{session: service.StudioSession{ID: "session-1", UserID: 42, Title: "old", Mode: "chat", Status: service.StudioSessionStatusActive}}
	storage, err := service.NewStudioFileStorage(&config.Config{Studio: config.StudioConfig{StorageRoot: t.TempDir(), RetentionDays: 30}})
	require.NoError(t, err)
	studio := service.NewStudioService(repo, storage, nil, &config.Config{Studio: config.StudioConfig{RetentionDays: 30}})
	now := time.Now().UTC()
	rc := &service.StudioRequestContext{
		Request:          &service.StudioRequest{ID: "request-1", SessionID: "session-1", UserID: 42, TurnID: "turn-1", Status: service.StudioRequestRunning, CreatedAt: now},
		UserMessage:      &service.StudioMessage{ID: "user-message", SessionID: "session-1", UserID: 42, Content: "hello"},
		AssistantMessage: &service.StudioMessage{ID: "assistant-message", SessionID: "session-1", UserID: 42, Role: "assistant", MessageType: "text", Status: "running", CreatedAt: now},
		StartedAt:        now,
		RequestContext:   context.Background(),
		Mode:             "chat",
	}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	w := newStudioCaptureWriter(ctx.Writer, studio, rc)
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()

	w.Finish(cancelled)

	require.Equal(t, service.StudioRequestCancelled, repo.completed.Status)
	require.Contains(t, recorder.Body.String(), `"status":"cancelled"`)
	require.Contains(t, recorder.Body.String(), "data: [DONE]")
}

type studioHandlerRepoStub struct {
	mu          sync.Mutex
	session     service.StudioSession
	messages    []service.StudioMessage
	assets      []service.StudioAsset
	generations []service.StudioGenerationRecord
	completed   service.StudioRequest
}

func (r *studioHandlerRepoStub) CreateSession(_ context.Context, value *service.StudioSession) error {
	r.session = *value
	return nil
}
func (r *studioHandlerRepoStub) ListSessions(context.Context, int64) ([]service.StudioSession, error) {
	return []service.StudioSession{r.session}, nil
}
func (r *studioHandlerRepoStub) GetSession(_ context.Context, userID int64, id string) (*service.StudioSession, error) {
	copy := r.session
	return &copy, nil
}
func (r *studioHandlerRepoStub) TouchSession(_ context.Context, _ int64, _ string, title, mode string, expires time.Time) error {
	if title != "" {
		r.session.Title = title
	}
	if mode != "" {
		r.session.Mode = mode
	}
	r.session.ExpiresAt = expires
	return nil
}
func (r *studioHandlerRepoStub) CreateMessage(_ context.Context, value *service.StudioMessage) error {
	r.messages = append(r.messages, *value)
	return nil
}
func (r *studioHandlerRepoStub) UpsertMessage(_ context.Context, value *service.StudioMessage) error {
	r.messages = append(r.messages, *value)
	return nil
}
func (r *studioHandlerRepoStub) ListMessages(context.Context, int64, string) ([]service.StudioMessage, error) {
	return append([]service.StudioMessage(nil), r.messages...), nil
}
func (r *studioHandlerRepoStub) CreateRequest(context.Context, *service.StudioRequest) error {
	return nil
}
func (r *studioHandlerRepoStub) CompleteRequest(_ context.Context, _ int64, value *service.StudioRequest) error {
	r.completed = *value
	return nil
}
func (r *studioHandlerRepoStub) GetRequest(context.Context, int64, string) (*service.StudioRequest, error) {
	copy := r.completed
	return &copy, nil
}
func (r *studioHandlerRepoStub) CreateGeneration(_ context.Context, value *service.StudioGenerationRecord) error {
	r.generations = append(r.generations, *value)
	return nil
}
func (r *studioHandlerRepoStub) CreateAsset(_ context.Context, value *service.StudioAsset) error {
	r.assets = append(r.assets, *value)
	return nil
}
func (r *studioHandlerRepoStub) GetAsset(context.Context, int64, string) (*service.StudioAsset, error) {
	copy := r.assets[0]
	return &copy, nil
}
func (r *studioHandlerRepoStub) HasRunningRequests(context.Context, int64, string) (bool, error) {
	return false, nil
}
func (r *studioHandlerRepoStub) MarkSessionDeleting(context.Context, int64, string) (bool, error) {
	return true, nil
}
func (r *studioHandlerRepoStub) DeleteSession(context.Context, int64, string) error { return nil }
func (r *studioHandlerRepoStub) ListCleanupCandidates(context.Context, time.Time, int) ([]service.StudioSession, error) {
	return nil, nil
}

var _ service.StudioRepository = (*studioHandlerRepoStub)(nil)
