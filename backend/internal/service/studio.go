package service

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/google/uuid"
)

const (
	StudioSessionStatusActive   = "active"
	StudioSessionStatusDeleting = "deleting"
	StudioRequestRunning        = "running"
	StudioRequestCompleted      = "completed"
	StudioRequestFailed         = "failed"
	StudioRequestCancelled      = "cancelled"
	StudioRequestPersistFailed  = "persistence_failed"
)

var ErrStudioSessionBusy = infraerrors.Conflict("STUDIO_SESSION_BUSY", "session has a running request")
var (
	ErrStudioSessionNotFound = infraerrors.NotFound("STUDIO_SESSION_NOT_FOUND", "Studio session not found")
	ErrStudioSessionDeleting = infraerrors.Conflict("STUDIO_SESSION_DELETING", "session is being deleted")
	ErrStudioRequestNotFound = infraerrors.NotFound("STUDIO_REQUEST_NOT_FOUND", "Studio request not found")
	ErrStudioAssetNotFound   = infraerrors.NotFound("STUDIO_ASSET_NOT_FOUND", "Studio asset not found")
	ErrStudioAPIKeyNotFound  = infraerrors.NotFound("STUDIO_API_KEY_NOT_FOUND", "API key not found")
)

type StudioSession struct {
	ID           string          `json:"id"`
	UserID       int64           `json:"-"`
	Title        string          `json:"title"`
	Mode         string          `json:"mode"`
	Status       string          `json:"status"`
	MetadataPath string          `json:"-"`
	ExpiresAt    time.Time       `json:"expires_at"`
	CreatedAt    time.Time       `json:"created_at"`
	UpdatedAt    time.Time       `json:"updated_at"`
	Messages     []StudioMessage `json:"messages,omitempty"`
}

type StudioMessage struct {
	ID           string    `json:"id"`
	SessionID    string    `json:"session_id"`
	UserID       int64     `json:"-"`
	TurnID       *string   `json:"turn_id,omitempty"`
	Role         string    `json:"role"`
	MessageType  string    `json:"message_type"`
	Status       string    `json:"status"`
	MetadataPath string    `json:"-"`
	Content      string    `json:"content,omitempty"`
	AssetIDs     []string  `json:"asset_ids,omitempty"`
	RequestIDs   []string  `json:"request_ids,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type StudioRequest struct {
	ID           string          `json:"id"`
	SessionID    string          `json:"session_id"`
	UserID       int64           `json:"-"`
	TurnID       string          `json:"turn_id"`
	APIKeyID     *int64          `json:"api_key_id,omitempty"`
	APIKeyName   string          `json:"api_key_name"`
	Endpoint     string          `json:"endpoint"`
	Model        string          `json:"model"`
	Status       string          `json:"status"`
	AsyncTaskID  *string         `json:"async_task_id,omitempty"`
	RequestPath  string          `json:"-"`
	ResponsePath *string         `json:"-"`
	DurationMS   *int64          `json:"duration_ms,omitempty"`
	ErrorCode    *string         `json:"error_code,omitempty"`
	ErrorMessage *string         `json:"error_message,omitempty"`
	Payload      json.RawMessage `json:"payload,omitempty"`
	Response     json.RawMessage `json:"response,omitempty"`
	CreatedAt    time.Time       `json:"created_at"`
	UpdatedAt    time.Time       `json:"updated_at"`
	CompletedAt  *time.Time      `json:"completed_at,omitempty"`
}

type StudioGenerationRecord struct {
	ID            string    `json:"id"`
	SessionID     string    `json:"session_id"`
	UserID        int64     `json:"-"`
	RequestID     string    `json:"request_id"`
	MessageID     *string   `json:"message_id,omitempty"`
	Status        string    `json:"status"`
	MetadataPath  string    `json:"-"`
	RevisedPrompt string    `json:"revised_prompt,omitempty"`
	AssetID       string    `json:"asset_id,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type StudioAsset struct {
	ID           string    `json:"id"`
	SessionID    string    `json:"session_id"`
	UserID       int64     `json:"-"`
	RequestID    *string   `json:"request_id,omitempty"`
	GenerationID *string   `json:"generation_id,omitempty"`
	Kind         string    `json:"kind"`
	SHA256       string    `json:"sha256"`
	MIMEType     string    `json:"mime_type"`
	ByteSize     int64     `json:"byte_size"`
	RelativePath string    `json:"-"`
	CreatedAt    time.Time `json:"created_at"`
}

type StudioRepository interface {
	CreateSession(context.Context, *StudioSession) error
	ListSessions(context.Context, int64) ([]StudioSession, error)
	GetSession(context.Context, int64, string) (*StudioSession, error)
	TouchSession(context.Context, int64, string, string, string, time.Time) error
	CreateMessage(context.Context, *StudioMessage) error
	UpsertMessage(context.Context, *StudioMessage) error
	ListMessages(context.Context, int64, string) ([]StudioMessage, error)
	CreateRequest(context.Context, *StudioRequest) error
	SetRequestAsyncTask(context.Context, int64, string, string) error
	CompleteRequest(context.Context, int64, *StudioRequest) error
	GetRequest(context.Context, int64, string) (*StudioRequest, error)
	CreateGeneration(context.Context, *StudioGenerationRecord) error
	CreateAsset(context.Context, *StudioAsset) error
	GetAsset(context.Context, int64, string) (*StudioAsset, error)
	HasRunningRequests(context.Context, int64, string) (bool, error)
	MarkSessionDeleting(context.Context, int64, string) (bool, error)
	DeleteSession(context.Context, int64, string) error
	ListCleanupCandidates(context.Context, time.Time, int) ([]StudioSession, error)
}

type StudioFileStorage interface {
	WriteSession(context.Context, int64, string, any) (string, error)
	WriteMessage(context.Context, int64, string, string, any) (string, error)
	WriteRequest(context.Context, int64, string, string, any) (string, error)
	WriteResponse(context.Context, int64, string, string, any) (string, error)
	WriteGeneration(context.Context, int64, string, string, any) (string, error)
	WriteInput(context.Context, int64, string, string, string, []byte) (string, error)
	WriteOutput(context.Context, int64, string, string, string, []byte) (string, error)
	ReadJSON(context.Context, string, any) error
	OpenAsset(context.Context, string) (io.ReadCloser, error)
	QuarantineSession(context.Context, int64, string) (string, error)
	DeleteQuarantine(context.Context, string) error
}

type StudioService struct {
	repo      StudioRepository
	storage   StudioFileStorage
	apiKeys   *APIKeyService
	retention time.Duration
	batchSize int
	interval  time.Duration
	now       func() time.Time
	stop      chan struct{}
	startOnce sync.Once
}

func NewStudioService(repo StudioRepository, storage StudioFileStorage, apiKeys *APIKeyService, cfg *config.Config) *StudioService {
	days, batch, minutes := 30, 100, 30
	if cfg != nil {
		if cfg.Studio.RetentionDays > 0 {
			days = cfg.Studio.RetentionDays
		}
		if cfg.Studio.CleanupBatchSize > 0 {
			batch = cfg.Studio.CleanupBatchSize
		}
		if cfg.Studio.CleanupIntervalMinutes > 0 {
			minutes = cfg.Studio.CleanupIntervalMinutes
		}
	}
	return &StudioService{repo: repo, storage: storage, apiKeys: apiKeys, retention: time.Duration(days) * 24 * time.Hour, batchSize: batch, interval: time.Duration(minutes) * time.Minute, now: time.Now, stop: make(chan struct{})}
}

func (s *StudioService) CreateSession(ctx context.Context, userID int64, title, mode string) (*StudioSession, error) {
	if mode != "chat" && mode != "image" {
		return nil, infraerrors.BadRequest("STUDIO_MODE_INVALID", "mode must be chat or image")
	}
	now := s.now().UTC()
	if strings.TrimSpace(title) == "" {
		title = "新会话"
	}
	if len([]rune(title)) > 255 {
		title = string([]rune(title)[:255])
	}
	session := &StudioSession{ID: uuid.NewString(), UserID: userID, Title: title, Mode: mode, Status: StudioSessionStatusActive, ExpiresAt: now.Add(s.retention), CreatedAt: now, UpdatedAt: now}
	path, err := s.storage.WriteSession(ctx, userID, session.ID, session)
	if err != nil {
		return nil, fmt.Errorf("write studio session: %w", err)
	}
	session.MetadataPath = path
	if err := s.repo.CreateSession(ctx, session); err != nil {
		return nil, fmt.Errorf("create studio session: %w", err)
	}
	return session, nil
}

func (s *StudioService) ListSessions(ctx context.Context, userID int64) ([]StudioSession, error) {
	return s.repo.ListSessions(ctx, userID)
}

func (s *StudioService) GetSession(ctx context.Context, userID int64, id string) (*StudioSession, error) {
	session, err := s.repo.GetSession(ctx, userID, id)
	if err != nil {
		return nil, err
	}
	messages, err := s.repo.ListMessages(ctx, userID, id)
	if err != nil {
		return nil, fmt.Errorf("list studio messages: %w", err)
	}
	for i := range messages {
		if messages[i].MetadataPath != "" {
			var doc StudioMessage
			if err := s.storage.ReadJSON(ctx, messages[i].MetadataPath, &doc); err != nil {
				return nil, fmt.Errorf("read studio message %s: %w", messages[i].ID, err)
			}
			messages[i].Content, messages[i].AssetIDs, messages[i].RequestIDs = doc.Content, doc.AssetIDs, doc.RequestIDs
		}
	}
	session.Messages = messages
	return session, nil
}

func (s *StudioService) DeleteSession(ctx context.Context, userID int64, id string) error {
	session, err := s.repo.GetSession(ctx, userID, id)
	if err != nil {
		return err
	}
	running, err := s.repo.HasRunningRequests(ctx, userID, id)
	if err != nil {
		return fmt.Errorf("check studio requests: %w", err)
	}
	if running {
		return ErrStudioSessionBusy
	}
	if session.Status != StudioSessionStatusDeleting {
		marked, markErr := s.repo.MarkSessionDeleting(ctx, userID, id)
		if markErr != nil {
			return fmt.Errorf("mark studio session deleting: %w", markErr)
		}
		if !marked {
			return infraerrors.Conflict("STUDIO_SESSION_STATE_CHANGED", "session state changed during deletion")
		}
	}
	return s.finishDelete(ctx, userID, id)
}

func (s *StudioService) finishDelete(ctx context.Context, userID int64, id string) error {
	quarantine, err := s.storage.QuarantineSession(ctx, userID, id)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("quarantine studio session: %w", err)
	}
	if quarantine != "" {
		if err := s.storage.DeleteQuarantine(ctx, quarantine); err != nil {
			return fmt.Errorf("delete studio quarantine: %w", err)
		}
	}
	if err := s.repo.DeleteSession(ctx, userID, id); err != nil {
		return fmt.Errorf("delete studio session: %w", err)
	}
	return nil
}

type StudioStartRequest struct {
	UserID                      int64
	SessionID, TurnID, Endpoint string
	APIKeyID                    int64
	Payload                     json.RawMessage
}

type StudioRequestContext struct {
	Request          *StudioRequest
	APIKey           *APIKey
	UserMessage      *StudioMessage
	AssistantMessage *StudioMessage
	StartedAt        time.Time
	RequestContext   context.Context
	Mode             string
	UpdateTitle      bool
}

func (s *StudioService) GetOwnedAPIKey(ctx context.Context, userID, apiKeyID int64) (*APIKey, error) {
	key, err := s.apiKeys.GetByID(ctx, apiKeyID)
	if err != nil {
		if errors.Is(err, ErrAPIKeyNotFound) {
			return nil, ErrStudioAPIKeyNotFound
		}
		return nil, fmt.Errorf("load Studio API key: %w", err)
	}
	if key == nil || key.UserID != userID {
		return nil, ErrStudioAPIKeyNotFound
	}
	return key, nil
}

func (s *StudioService) StartRequest(ctx context.Context, in StudioStartRequest) (*StudioRequestContext, error) {
	if strings.TrimSpace(in.TurnID) == "" || len(in.TurnID) > 64 {
		return nil, infraerrors.BadRequest("STUDIO_TURN_INVALID", "turn_id is required and must not exceed 64 characters")
	}
	session, err := s.repo.GetSession(ctx, in.UserID, in.SessionID)
	if err != nil {
		return nil, err
	}
	if session.Status != StudioSessionStatusActive {
		return nil, infraerrors.Conflict("STUDIO_SESSION_DELETING", "session is being deleted")
	}
	key, err := s.GetOwnedAPIKey(ctx, in.UserID, in.APIKeyID)
	if err != nil {
		return nil, err
	}
	var payload map[string]any
	if err := json.Unmarshal(in.Payload, &payload); err != nil {
		return nil, infraerrors.BadRequest("STUDIO_PAYLOAD_INVALID", "payload must be valid JSON")
	}
	model, _ := payload["model"].(string)
	if strings.TrimSpace(model) == "" {
		return nil, infraerrors.BadRequest("STUDIO_MODEL_REQUIRED", "model is required")
	}
	now := s.now().UTC()
	req := &StudioRequest{ID: uuid.NewString(), SessionID: in.SessionID, UserID: in.UserID, TurnID: in.TurnID, APIKeyID: &key.ID, APIKeyName: key.Name, Endpoint: in.Endpoint, Model: model, Status: StudioRequestRunning, Payload: in.Payload, CreatedAt: now, UpdatedAt: now}
	sanitized, assets, err := s.persistInputAssets(ctx, req, payload)
	if err != nil {
		return nil, err
	}
	req.Payload = sanitized
	reqPath, err := s.storage.WriteRequest(ctx, in.UserID, in.SessionID, req.ID, req)
	if err != nil {
		return nil, fmt.Errorf("write studio request: %w", err)
	}
	req.RequestPath = reqPath
	if err := s.repo.CreateRequest(ctx, req); err != nil {
		return nil, fmt.Errorf("create studio request: %w", err)
	}
	for i := range assets {
		if err := s.repo.CreateAsset(ctx, &assets[i]); err != nil {
			return nil, fmt.Errorf("create studio input asset: %w", err)
		}
	}
	text := studioPrompt(payload)
	turn := in.TurnID
	userMessage := &StudioMessage{ID: studioMessageID(in.SessionID, in.TurnID, "user"), SessionID: in.SessionID, UserID: in.UserID, TurnID: &turn, Role: "user", MessageType: "text", Status: "completed", Content: text, CreatedAt: now, UpdatedAt: now}
	assistant := &StudioMessage{ID: studioMessageID(in.SessionID, in.TurnID, "assistant"), SessionID: in.SessionID, UserID: in.UserID, TurnID: &turn, Role: "assistant", MessageType: "text", Status: "running", RequestIDs: []string{req.ID}, CreatedAt: now, UpdatedAt: now}
	updateTitle := true
	if existing, listErr := s.repo.ListMessages(ctx, in.UserID, in.SessionID); listErr == nil {
		for i := range existing {
			if existing[i].Role == "user" {
				updateTitle = false
			}
			if existing[i].TurnID == nil || *existing[i].TurnID != in.TurnID {
				continue
			}
			if existing[i].MetadataPath != "" {
				var doc StudioMessage
				if s.storage.ReadJSON(ctx, existing[i].MetadataPath, &doc) == nil {
					existing[i].Content, existing[i].AssetIDs, existing[i].RequestIDs = doc.Content, doc.AssetIDs, doc.RequestIDs
				}
			}
			switch existing[i].Role {
			case "user":
				userMessage = &existing[i]
			case "assistant":
				assistant = &existing[i]
			}
		}
	}
	if !containsStudioString(assistant.RequestIDs, req.ID) {
		assistant.RequestIDs = append(assistant.RequestIDs, req.ID)
	}
	assistant.Status, assistant.UpdatedAt = "running", now
	if err := s.writeMessage(ctx, userMessage, true); err != nil {
		return nil, err
	}
	if err := s.writeMessage(ctx, assistant, true); err != nil {
		return nil, err
	}
	return &StudioRequestContext{Request: req, APIKey: key, UserMessage: userMessage, AssistantMessage: assistant, StartedAt: now, RequestContext: ctx, Mode: studioPayloadMode(payload), UpdateTitle: updateTitle}, nil
}

func (s *StudioService) SetRequestAsyncTask(ctx context.Context, rc *StudioRequestContext, taskID string) error {
	taskID = strings.TrimSpace(taskID)
	if rc == nil || rc.Request == nil || taskID == "" || len(taskID) > 128 {
		return infraerrors.BadRequest("STUDIO_IMAGE_TASK_INVALID", "image task id is invalid")
	}
	if err := s.repo.SetRequestAsyncTask(ctx, rc.Request.UserID, rc.Request.ID, taskID); err != nil {
		return fmt.Errorf("set studio image task: %w", err)
	}
	rc.Request.AsyncTaskID = &taskID
	if _, err := s.storage.WriteRequest(ctx, rc.Request.UserID, rc.Request.SessionID, rc.Request.ID, rc.Request); err != nil {
		return fmt.Errorf("refresh studio request task: %w", err)
	}
	return nil
}

func (s *StudioService) ResumeRequest(ctx context.Context, userID int64, requestID string) (*StudioRequestContext, error) {
	req, err := s.GetRequest(ctx, userID, requestID)
	if err != nil {
		return nil, err
	}
	if req.Status != StudioRequestRunning {
		return nil, infraerrors.Conflict("STUDIO_REQUEST_NOT_RUNNING", "Studio request is not running")
	}
	if req.APIKeyID == nil {
		return nil, ErrStudioAPIKeyNotFound
	}
	key, err := s.GetOwnedAPIKey(ctx, userID, *req.APIKeyID)
	if err != nil {
		return nil, err
	}
	messages, err := s.repo.ListMessages(ctx, userID, req.SessionID)
	if err != nil {
		return nil, fmt.Errorf("list studio messages: %w", err)
	}
	var userMessage, assistantMessage *StudioMessage
	for i := range messages {
		message := &messages[i]
		if message.TurnID == nil || *message.TurnID != req.TurnID {
			continue
		}
		if message.MetadataPath != "" {
			var doc StudioMessage
			if s.storage.ReadJSON(ctx, message.MetadataPath, &doc) == nil {
				message.Content, message.AssetIDs, message.RequestIDs = doc.Content, doc.AssetIDs, doc.RequestIDs
			}
		}
		if message.Role == "user" {
			userMessage = message
		} else if message.Role == "assistant" {
			assistantMessage = message
		}
	}
	if userMessage == nil || assistantMessage == nil {
		return nil, errors.New("studio request messages are missing")
	}
	var payload map[string]any
	if err := json.Unmarshal(req.Payload, &payload); err != nil {
		return nil, fmt.Errorf("decode studio request payload: %w", err)
	}
	return &StudioRequestContext{
		Request: req, APIKey: key, UserMessage: userMessage, AssistantMessage: assistantMessage,
		StartedAt: req.CreatedAt, RequestContext: ctx, Mode: studioPayloadMode(payload),
	}, nil
}

func (s *StudioService) writeMessage(ctx context.Context, msg *StudioMessage, upsert bool) error {
	path, err := s.storage.WriteMessage(ctx, msg.UserID, msg.SessionID, msg.ID, msg)
	if err != nil {
		return fmt.Errorf("write studio message: %w", err)
	}
	msg.MetadataPath = path
	if upsert {
		err = s.repo.UpsertMessage(ctx, msg)
	} else {
		err = s.repo.CreateMessage(ctx, msg)
	}
	if err != nil {
		return fmt.Errorf("persist studio message: %w", err)
	}
	return nil
}

func (s *StudioService) AppendText(rc *StudioRequestContext, delta string) {
	rc.AssistantMessage.Content += delta
}

func (s *StudioService) PersistOutputImage(ctx context.Context, rc *StudioRequestContext, encoded, format, revisedPrompt string) (*StudioAsset, error) {
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("decode generated image: %w", err)
	}
	return s.PersistOutputImageBytes(ctx, rc, data, format, revisedPrompt)
}

func (s *StudioService) PersistOutputImageBytes(ctx context.Context, rc *StudioRequestContext, data []byte, format, revisedPrompt string) (*StudioAsset, error) {
	if len(data) == 0 {
		return nil, errors.New("generated image is empty")
	}
	format = normalizeStudioImageFormat(format)
	var err error
	if len(data) > studioLowQualityMaxBytes && studioImageRequestQuality(rc.Request.Payload) == "low" {
		data, err = compressStudioImageToLimit(data, format, studioLowQualityMaxBytes)
		if err != nil {
			return nil, fmt.Errorf("compress low-quality generated image: %w", err)
		}
	}
	mimeType := mime.TypeByExtension("." + format)
	if mimeType == "" {
		mimeType = "image/" + format
	}
	now := s.now().UTC()
	generationID, assetID := uuid.NewString(), uuid.NewString()
	path, err := s.storage.WriteOutput(ctx, rc.Request.UserID, rc.Request.SessionID, assetID, format, data)
	if err != nil {
		return nil, fmt.Errorf("write generated image: %w", err)
	}
	generation := &StudioGenerationRecord{ID: generationID, SessionID: rc.Request.SessionID, UserID: rc.Request.UserID, RequestID: rc.Request.ID, MessageID: &rc.AssistantMessage.ID, Status: "completed", RevisedPrompt: revisedPrompt, AssetID: assetID, CreatedAt: now, UpdatedAt: now}
	metaPath, err := s.storage.WriteGeneration(ctx, generation.UserID, generation.SessionID, generation.ID, generation)
	if err != nil {
		return nil, fmt.Errorf("write generation metadata: %w", err)
	}
	generation.MetadataPath = metaPath
	if err := s.repo.CreateGeneration(ctx, generation); err != nil {
		return nil, fmt.Errorf("create generation: %w", err)
	}
	sum := sha256.Sum256(data)
	asset := &StudioAsset{ID: assetID, SessionID: generation.SessionID, UserID: generation.UserID, RequestID: &generation.RequestID, GenerationID: &generation.ID, Kind: "output", SHA256: hex.EncodeToString(sum[:]), MIMEType: mimeType, ByteSize: int64(len(data)), RelativePath: path, CreatedAt: now}
	if err := s.repo.CreateAsset(ctx, asset); err != nil {
		return nil, fmt.Errorf("create generated asset: %w", err)
	}
	rc.AssistantMessage.MessageType = "images"
	rc.AssistantMessage.AssetIDs = append(rc.AssistantMessage.AssetIDs, asset.ID)
	return asset, nil
}

func (s *StudioService) FinishRequest(ctx context.Context, rc *StudioRequestContext, status, errorCode, errorMessage string, responseEvents []json.RawMessage) error {
	now := s.now().UTC()
	duration := now.Sub(rc.StartedAt).Milliseconds()
	if status == "" {
		status = StudioRequestCompleted
	}
	rc.Request.Status, rc.Request.DurationMS, rc.Request.CompletedAt, rc.Request.UpdatedAt = status, &duration, &now, now
	if errorCode != "" {
		rc.Request.ErrorCode = &errorCode
	}
	if errorMessage != "" {
		rc.Request.ErrorMessage = &errorMessage
	}
	responseDoc := struct {
		Events []json.RawMessage `json:"events"`
	}{responseEvents}
	path, err := s.storage.WriteResponse(ctx, rc.Request.UserID, rc.Request.SessionID, rc.Request.ID, responseDoc)
	if err != nil {
		rc.Request.Status = StudioRequestPersistFailed
		_ = s.repo.CompleteRequest(ctx, rc.Request.UserID, rc.Request)
		return fmt.Errorf("write studio response: %w", err)
	}
	rc.Request.ResponsePath = &path
	if _, err := s.storage.WriteRequest(ctx, rc.Request.UserID, rc.Request.SessionID, rc.Request.ID, rc.Request); err != nil {
		rc.Request.Status = StudioRequestPersistFailed
		_ = s.repo.CompleteRequest(ctx, rc.Request.UserID, rc.Request)
		return fmt.Errorf("refresh studio request: %w", err)
	}
	if encoded, marshalErr := json.Marshal(responseDoc); marshalErr == nil {
		rc.Request.Response = encoded
	}
	if err := s.repo.CompleteRequest(ctx, rc.Request.UserID, rc.Request); err != nil {
		return fmt.Errorf("complete studio request: %w", err)
	}
	rc.AssistantMessage.Status = status
	if status != StudioRequestCompleted && rc.AssistantMessage.Content == "" {
		rc.AssistantMessage.Content = errorMessage
		if len(rc.AssistantMessage.AssetIDs) == 0 {
			rc.AssistantMessage.MessageType = "error"
		}
	}
	rc.AssistantMessage.UpdatedAt = now
	if err := s.writeMessage(ctx, rc.AssistantMessage, true); err != nil {
		return err
	}
	title := ""
	if rc.UpdateTitle {
		title = studioTitle(rc.UserMessage.Content)
	}
	if err := s.repo.TouchSession(ctx, rc.Request.UserID, rc.Request.SessionID, title, rc.Mode, now.Add(s.retention)); err != nil {
		return fmt.Errorf("touch studio session: %w", err)
	}
	session, err := s.repo.GetSession(ctx, rc.Request.UserID, rc.Request.SessionID)
	if err != nil {
		return fmt.Errorf("reload studio session: %w", err)
	}
	if _, err := s.storage.WriteSession(ctx, session.UserID, session.ID, session); err != nil {
		return fmt.Errorf("refresh studio session metadata: %w", err)
	}
	return nil
}

func (s *StudioService) GetRequest(ctx context.Context, userID int64, id string) (*StudioRequest, error) {
	req, err := s.repo.GetRequest(ctx, userID, id)
	if err != nil {
		return nil, err
	}
	if req.RequestPath != "" {
		var doc StudioRequest
		if err := s.storage.ReadJSON(ctx, req.RequestPath, &doc); err == nil {
			req.Payload = doc.Payload
		}
	}
	if req.ResponsePath != nil {
		var doc json.RawMessage
		if err := s.storage.ReadJSON(ctx, *req.ResponsePath, &doc); err == nil {
			req.Response = doc
		}
	}
	return req, nil
}

func (s *StudioService) OpenAsset(ctx context.Context, userID int64, id string) (*StudioAsset, io.ReadCloser, error) {
	asset, err := s.repo.GetAsset(ctx, userID, id)
	if err != nil {
		return nil, nil, err
	}
	reader, err := s.storage.OpenAsset(ctx, asset.RelativePath)
	if err != nil {
		return nil, nil, fmt.Errorf("open studio asset: %w", err)
	}
	return asset, reader, nil
}

func (s *StudioService) CleanupOnce(ctx context.Context) error {
	sessions, err := s.repo.ListCleanupCandidates(ctx, s.now().UTC(), s.batchSize)
	if err != nil {
		return fmt.Errorf("list studio cleanup candidates: %w", err)
	}
	var errs []error
	for _, session := range sessions {
		if session.Status != StudioSessionStatusDeleting {
			running, checkErr := s.repo.HasRunningRequests(ctx, session.UserID, session.ID)
			if checkErr != nil {
				errs = append(errs, checkErr)
				continue
			}
			if running {
				continue
			}
			if _, markErr := s.repo.MarkSessionDeleting(ctx, session.UserID, session.ID); markErr != nil {
				errs = append(errs, markErr)
				continue
			}
		}
		if deleteErr := s.finishDelete(ctx, session.UserID, session.ID); deleteErr != nil {
			errs = append(errs, deleteErr)
		}
	}
	return errors.Join(errs...)
}

func (s *StudioService) Start() {
	if s == nil || s.repo == nil || s.storage == nil {
		return
	}
	s.startOnce.Do(func() {
		go func() {
			ticker := time.NewTicker(s.interval)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
					_ = s.CleanupOnce(ctx)
					cancel()
				case <-s.stop:
					return
				}
			}
		}()
	})
}

func (s *StudioService) Stop() {
	if s != nil {
		select {
		case <-s.stop:
		default:
			close(s.stop)
		}
	}
}

func (s *StudioService) persistInputAssets(ctx context.Context, req *StudioRequest, payload map[string]any) (json.RawMessage, []StudioAsset, error) {
	var assets []StudioAsset
	persist := func(part map[string]any, key string) error {
		rawURL, _ := part[key].(string)
		if !strings.HasPrefix(rawURL, "data:") {
			return nil
		}
		mimeType, data, ext, err := decodeStudioDataURL(rawURL)
		if err != nil {
			return infraerrors.BadRequest("STUDIO_REFERENCE_INVALID", err.Error())
		}
		sum := sha256.Sum256(data)
		digest := hex.EncodeToString(sum[:])
		assetID := uuid.NewString()
		path, err := s.storage.WriteInput(ctx, req.UserID, req.SessionID, digest, ext, data)
		if err != nil {
			return fmt.Errorf("write studio input: %w", err)
		}
		requestID := req.ID
		assets = append(assets, StudioAsset{ID: assetID, SessionID: req.SessionID, UserID: req.UserID, RequestID: &requestID, Kind: "input", SHA256: digest, MIMEType: mimeType, ByteSize: int64(len(data)), RelativePath: path, CreatedAt: req.CreatedAt})
		part[key] = map[string]any{"asset_id": assetID, "sha256": digest, "mime_type": mimeType, "byte_size": len(data)}
		return nil
	}
	input, _ := payload["input"].([]any)
	for _, item := range input {
		msg, _ := item.(map[string]any)
		content, _ := msg["content"].([]any)
		for _, rawPart := range content {
			part, _ := rawPart.(map[string]any)
			if err := persist(part, "image_url"); err != nil {
				return nil, nil, err
			}
		}
	}
	images, _ := payload["images"].([]any)
	for _, rawImage := range images {
		image, _ := rawImage.(map[string]any)
		if err := persist(image, "image_url"); err != nil {
			return nil, nil, err
		}
	}
	encoded, err := json.Marshal(payload)
	return encoded, assets, err
}

func decodeStudioDataURL(value string) (string, []byte, string, error) {
	header, encoded, ok := strings.Cut(value, ",")
	if !ok || !strings.HasSuffix(header, ";base64") {
		return "", nil, "", errors.New("reference image must be a base64 data URL")
	}
	mimeType := strings.TrimPrefix(strings.TrimSuffix(header, ";base64"), "data:")
	if !strings.HasPrefix(mimeType, "image/") {
		return "", nil, "", errors.New("reference file must be an image")
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(data) == 0 {
		return "", nil, "", errors.New("reference image base64 is invalid")
	}
	ext := normalizeStudioImageFormat(strings.TrimPrefix(mimeType, "image/"))
	return mimeType, data, ext, nil
}

func normalizeStudioImageFormat(format string) string {
	format = strings.ToLower(strings.TrimSpace(format))
	if format == "jpg" {
		return "jpeg"
	}
	if format != "png" && format != "jpeg" && format != "webp" {
		return "png"
	}
	return format
}
func studioPrompt(payload map[string]any) string {
	if prompt, _ := payload["prompt"].(string); strings.TrimSpace(prompt) != "" {
		return prompt
	}
	input, _ := payload["input"].([]any)
	for _, raw := range input {
		msg, _ := raw.(map[string]any)
		if msg["role"] != "user" {
			continue
		}
		content, _ := msg["content"].([]any)
		for _, rp := range content {
			p, _ := rp.(map[string]any)
			if text, ok := p["text"].(string); ok && text != "" {
				return text
			}
		}
	}
	return ""
}
func studioPayloadMode(payload map[string]any) string {
	if _, ok := payload["prompt"].(string); ok {
		return "image"
	}
	tools, _ := payload["tools"].([]any)
	for _, raw := range tools {
		if tool, ok := raw.(map[string]any); ok && tool["type"] == "image_generation" {
			return "image"
		}
	}
	return "chat"
}
func studioTitle(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	r := []rune(value)
	if len(r) > 32 {
		r = r[:32]
	}
	return string(r)
}

func studioMessageID(sessionID, turnID, role string) string {
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte("sub2api/studio/"+sessionID+"/"+turnID+"/"+role)).String()
}

func containsStudioString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func StudioAssetFilename(asset *StudioAsset) string {
	ext := normalizeStudioImageFormat(strings.TrimPrefix(asset.MIMEType, "image/"))
	return "studio-" + asset.ID + "." + ext
}

func StudioStoragePathIsSafe(root, candidate string) bool {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	candidateAbs, err := filepath.Abs(candidate)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(rootAbs, candidateAbs)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
