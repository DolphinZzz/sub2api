package handler

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type StudioHandler struct {
	studio        *service.StudioService
	settings      *service.SettingService
	gateway       *GatewayHandler
	openAIGateway *OpenAIGatewayHandler
	asyncImage    *AsyncImageHandler
	apiKeyAuth    middleware2.APIKeyAuthMiddleware
	maxBodySize   int64
	imageClient   *http.Client
	finalizerMu   sync.Mutex
	finalizers    map[string]*studioRequestFinalizer
}

type studioRequestFinalizer struct {
	mu   sync.Mutex
	refs int
}

const studioImageGenerationInstructions = "You are in image generation mode. You must call the attached image_generation tool for this request. Do not return image prompts, instructions, or any text-only substitute."

func NewStudioHandler(studio *service.StudioService, settings *service.SettingService, gateway *GatewayHandler, openAIGateway *OpenAIGatewayHandler, asyncImage *AsyncImageHandler, apiKeyAuth middleware2.APIKeyAuthMiddleware, cfg *config.Config) *StudioHandler {
	maxBodySize := int64(100 << 20)
	if cfg != nil && cfg.Gateway.MaxBodySize > 0 {
		maxBodySize = cfg.Gateway.MaxBodySize
	}
	return &StudioHandler{studio: studio, settings: settings, gateway: gateway, openAIGateway: openAIGateway, asyncImage: asyncImage, apiKeyAuth: apiKeyAuth, maxBodySize: maxBodySize, imageClient: &http.Client{Timeout: 60 * time.Second}, finalizers: make(map[string]*studioRequestFinalizer)}
}

type createStudioSessionRequest struct {
	Title string `json:"title"`
	Mode  string `json:"mode" binding:"required"`
}

type studioResponseRequest struct {
	TurnID   string          `json:"turn_id" binding:"required"`
	APIKeyID int64           `json:"api_key_id" binding:"required"`
	Endpoint string          `json:"endpoint" binding:"required"`
	Payload  json.RawMessage `json:"payload" binding:"required"`
}

type studioImageTaskResponse struct {
	*service.ImageTask
	RequestID string `json:"request_id"`
	Persisted bool   `json:"persisted"`
}

func (h *StudioHandler) ListSessions(c *gin.Context) {
	userID, ok := studioUserID(c)
	if !ok {
		return
	}
	sessions, err := h.studio.ListSessions(c.Request.Context(), userID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, sessions)
}

func (h *StudioHandler) CreateSession(c *gin.Context) {
	userID, ok := studioUserID(c)
	if !ok {
		return
	}
	var req createStudioSessionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request body")
		return
	}
	session, err := h.studio.CreateSession(c.Request.Context(), userID, strings.TrimSpace(req.Title), req.Mode)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Created(c, session)
}

func (h *StudioHandler) GetSession(c *gin.Context) {
	userID, ok := studioUserID(c)
	if !ok {
		return
	}
	session, err := h.studio.GetSession(c.Request.Context(), userID, c.Param("id"))
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, session)
}

func (h *StudioHandler) DeleteSession(c *gin.Context) {
	userID, ok := studioUserID(c)
	if !ok {
		return
	}
	if err := h.studio.DeleteSession(c.Request.Context(), userID, c.Param("id")); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *StudioHandler) GetRequest(c *gin.Context) {
	userID, ok := studioUserID(c)
	if !ok {
		return
	}
	req, err := h.studio.GetRequest(c.Request.Context(), userID, c.Param("id"))
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, req)
}

func (h *StudioHandler) AssetContent(c *gin.Context) {
	userID, ok := studioUserID(c)
	if !ok {
		return
	}
	asset, reader, err := h.studio.OpenAsset(c.Request.Context(), userID, c.Param("id"))
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	defer func() { _ = reader.Close() }()
	c.Header("Content-Type", asset.MIMEType)
	c.Header("Content-Disposition", fmt.Sprintf(`inline; filename=%q`, service.StudioAssetFilename(asset)))
	c.Header("X-Content-Type-Options", "nosniff")
	c.DataFromReader(http.StatusOK, asset.ByteSize, asset.MIMEType, reader, nil)
}

func (h *StudioHandler) Responses(c *gin.Context) {
	userID, ok := studioUserID(c)
	if !ok {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, h.maxBodySize)
	var req studioResponseRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			response.Error(c, http.StatusRequestEntityTooLarge, "Request body is too large")
			return
		}
		response.BadRequest(c, "Invalid request body")
		return
	}
	if !h.endpointAllowed(c, req.Endpoint) {
		response.BadRequest(c, "Endpoint is not configured")
		return
	}
	key, err := h.studio.GetOwnedAPIKey(c.Request.Context(), userID, req.APIKeyID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	previousAuthorization := c.GetHeader("Authorization")
	c.Request.Header.Set("Authorization", "Bearer "+key.Key)
	gin.HandlerFunc(h.apiKeyAuth)(c)
	if previousAuthorization == "" {
		c.Request.Header.Del("Authorization")
	} else {
		c.Request.Header.Set("Authorization", previousAuthorization)
	}
	if c.IsAborted() {
		return
	}
	authenticatedKey, _ := middleware2.GetAPIKeyFromContext(c)
	if authenticatedKey != nil && authenticatedKey.GroupID == nil && !h.settings.IsUngroupedKeySchedulingAllowed(c.Request.Context()) {
		response.Forbidden(c, "API Key is not assigned to any group and cannot be used")
		return
	}
	rc, err := h.studio.StartRequest(c.Request.Context(), service.StudioStartRequest{UserID: userID, SessionID: c.Param("id"), TurnID: req.TurnID, APIKeyID: req.APIKeyID, Endpoint: strings.TrimSpace(req.Endpoint), Payload: req.Payload})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	forwardPayload := req.Payload
	if rc.Mode == "image" {
		forwardPayload, err = forceStudioImageGenerationToolChoice(forwardPayload)
		if err != nil {
			response.BadRequest(c, "Invalid image generation payload")
			return
		}
	}

	// Invoke the existing gateway with the selected, owned API key so billing,
	// quota checks, account routing, usage_logs and failover stay on one path.
	c.Set(string(middleware2.ContextKeyAPIKey), rc.APIKey)
	c.Request.Body = http.NoBody
	c.Request.Body = ioNopCloser{Reader: bytes.NewReader(forwardPayload)}
	c.Request.ContentLength = int64(len(forwardPayload))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.Header.Set("Accept", "text/event-stream")
	c.Request.Header.Set("OpenAI-Beta", "responses=experimental")
	c.Request.URL.Path = "/v1/responses"
	InboundEndpointMiddleware()(c)

	original := c.Writer
	w := newStudioCaptureWriter(original, h.studio, rc)
	c.Writer = w
	if studioUsesOpenAIGateway(rc.Mode, rc.APIKey) {
		h.openAIGateway.Responses(c)
	} else {
		h.gateway.Responses(c)
	}
	c.Writer = original
	w.Finish(c.Request.Context())
}

func (h *StudioHandler) SubmitImage(c *gin.Context) {
	userID, ok := studioUserID(c)
	if !ok {
		return
	}
	if h.asyncImage == nil || !h.asyncImage.enabled() {
		response.Error(c, http.StatusServiceUnavailable, "Async image generation is not enabled")
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, h.maxBodySize)
	var req studioResponseRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request body")
		return
	}
	if !h.endpointAllowed(c, req.Endpoint) {
		response.BadRequest(c, "Endpoint is not configured")
		return
	}
	key, ok := h.authenticateStudioAPIKey(c, userID, req.APIKeyID)
	if !ok {
		return
	}
	rc, err := h.studio.StartRequest(c.Request.Context(), service.StudioStartRequest{
		UserID: userID, SessionID: c.Param("id"), TurnID: req.TurnID,
		APIKeyID: req.APIKeyID, Endpoint: strings.TrimSpace(req.Endpoint), Payload: req.Payload,
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	path := "/v1/images/generations/async"
	var payload map[string]any
	if json.Unmarshal(req.Payload, &payload) != nil {
		h.finishAsyncImageFailure(rc, "invalid_request_error", "Invalid image generation payload", nil)
		response.BadRequest(c, "Invalid image generation payload")
		return
	}
	if images, _ := payload["images"].([]any); len(images) > 0 {
		path = "/v1/images/edits/async"
	}

	internalRecorder := httptest.NewRecorder()
	internalWriter, _ := gin.CreateTestContext(internalRecorder)
	internal := c.Copy()
	internal.Writer = internalWriter.Writer
	internal.Request = c.Request.Clone(c.Request.Context())
	internal.Request.Body = io.NopCloser(bytes.NewReader(req.Payload))
	internal.Request.ContentLength = int64(len(req.Payload))
	internal.Request.URL.Path = path
	internal.Request.Header.Set("Content-Type", "application/json")
	internal.Set(string(middleware2.ContextKeyAPIKey), key)
	h.asyncImage.Submit(internal)

	body := bytes.TrimSpace(internalRecorder.Body.Bytes())
	if internalRecorder.Code != http.StatusAccepted {
		message := studioAsyncErrorMessage(body, http.StatusText(internalRecorder.Code))
		h.finishAsyncImageFailure(rc, strconv.Itoa(internalRecorder.Code), message, body)
		c.Data(internalRecorder.Code, "application/json", body)
		return
	}
	var task service.ImageTask
	if err := json.Unmarshal(body, &task); err != nil || task.ID == "" {
		h.finishAsyncImageFailure(rc, "invalid_task_response", "Async image service returned an invalid task", body)
		response.Error(c, http.StatusBadGateway, "Async image service returned an invalid task")
		return
	}
	if err := h.studio.SetRequestAsyncTask(c.Request.Context(), rc, task.ID); err != nil {
		h.finishAsyncImageFailure(rc, "persistence_failed", err.Error(), body)
		response.ErrorFrom(c, err)
		return
	}
	go h.monitorStudioImageTask(userID, rc.Request.ID, key.ID, task.ID)
	c.Header("Cache-Control", "no-store")
	c.Header("Retry-After", "3")
	c.Header("Location", "/api/v1/studio/requests/"+rc.Request.ID+"/image-task")
	c.JSON(http.StatusAccepted, studioImageTaskResponse{ImageTask: &task, RequestID: rc.Request.ID})
}

func (h *StudioHandler) GetImageTask(c *gin.Context) {
	userID, ok := studioUserID(c)
	if !ok {
		return
	}
	requestID := c.Param("id")
	req, err := h.studio.GetRequest(c.Request.Context(), userID, requestID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if req.AsyncTaskID == nil || req.APIKeyID == nil {
		response.BadRequest(c, "Studio request is not an asynchronous image task")
		return
	}
	if req.Status != service.StudioRequestRunning {
		task := &service.ImageTask{ID: *req.AsyncTaskID, TaskID: *req.AsyncTaskID, Object: "image.generation.task", Status: req.Status}
		if req.ErrorMessage != nil {
			task.Error, _ = json.Marshal(map[string]string{"type": "studio_error", "message": *req.ErrorMessage})
		}
		c.JSON(http.StatusOK, studioImageTaskResponse{ImageTask: task, RequestID: req.ID, Persisted: req.Status == service.StudioRequestCompleted})
		return
	}
	if h.asyncImage == nil || h.asyncImage.tasks == nil {
		response.Error(c, http.StatusServiceUnavailable, "Async image task storage is unavailable")
		return
	}
	task, err := h.asyncImage.tasks.Get(c.Request.Context(), service.ImageTaskOwner{UserID: userID, APIKeyID: *req.APIKeyID}, *req.AsyncTaskID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if task.Status == service.ImageTaskStatusProcessing {
		c.Header("Cache-Control", "no-store")
		c.Header("Retry-After", "3")
		c.JSON(http.StatusOK, studioImageTaskResponse{ImageTask: task, RequestID: req.ID})
		return
	}

	persisted, err := h.finalizeStudioImageTask(c.Request.Context(), userID, requestID, task)
	if err != nil {
		response.Error(c, http.StatusBadGateway, err.Error())
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, studioImageTaskResponse{ImageTask: task, RequestID: req.ID, Persisted: persisted})
}

func (h *StudioHandler) monitorStudioImageTask(userID int64, requestID string, apiKeyID int64, taskID string) {
	if h.asyncImage == nil || h.asyncImage.tasks == nil {
		return
	}
	timeout := h.asyncImage.tasks.ExecutionTimeout() + time.Minute
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	owner := service.ImageTaskOwner{UserID: userID, APIKeyID: apiKeyID}
	for {
		task, err := h.asyncImage.tasks.Get(ctx, owner, taskID)
		if err == nil && task.Status != service.ImageTaskStatusProcessing {
			_, _ = h.finalizeStudioImageTask(ctx, userID, requestID, task)
			return
		}
		select {
		case <-ctx.Done():
			rc, resumeErr := h.studio.ResumeRequest(context.Background(), userID, requestID)
			if resumeErr == nil {
				h.finishAsyncImageFailure(rc, "task_timeout", "Image generation task timed out", nil)
			}
			return
		case <-ticker.C:
		}
	}
}

func (h *StudioHandler) finalizeStudioImageTask(ctx context.Context, userID int64, requestID string, task *service.ImageTask) (bool, error) {
	unlock := h.lockStudioFinalizer(requestID)
	defer unlock()

	req, err := h.studio.GetRequest(ctx, userID, requestID)
	if err != nil {
		return false, err
	}
	if req.Status != service.StudioRequestRunning {
		return req.Status == service.StudioRequestCompleted, nil
	}
	rc, err := h.studio.ResumeRequest(ctx, userID, requestID)
	if err != nil {
		return false, err
	}
	if task == nil || task.Status == service.ImageTaskStatusFailed {
		message := "Image generation failed"
		var taskID string
		var status int
		var taskError json.RawMessage
		if task != nil {
			taskID, status, taskError = task.ID, task.HTTPStatus, task.Error
			message = studioAsyncErrorMessage(task.Error, message)
		}
		event, _ := json.Marshal(map[string]any{"type": "studio.async_image.failed", "task_id": taskID, "http_status": status, "error": taskError})
		persistCtx, cancel := studioPersistenceContext(ctx)
		err = h.studio.FinishRequest(persistCtx, rc, service.StudioRequestFailed, "upstream_error", message, []json.RawMessage{event})
		cancel()
		return false, err
	}
	if task.Status != service.ImageTaskStatusCompleted {
		return false, errors.New("image generation task is not complete")
	}
	assetIDs, err := h.persistAsyncTaskImages(ctx, rc, task)
	if err != nil {
		h.finishAsyncImageFailure(rc, "persistence_failed", err.Error(), nil)
		return false, err
	}
	event, _ := json.Marshal(map[string]any{"type": "studio.async_image.completed", "task_id": task.ID, "asset_ids": assetIDs})
	persistCtx, cancel := studioPersistenceContext(ctx)
	err = h.studio.FinishRequest(persistCtx, rc, service.StudioRequestCompleted, "", "", []json.RawMessage{event})
	cancel()
	return err == nil, err
}

func (h *StudioHandler) lockStudioFinalizer(requestID string) func() {
	h.finalizerMu.Lock()
	if h.finalizers == nil {
		h.finalizers = make(map[string]*studioRequestFinalizer)
	}
	entry := h.finalizers[requestID]
	if entry == nil {
		entry = &studioRequestFinalizer{}
		h.finalizers[requestID] = entry
	}
	entry.refs++
	h.finalizerMu.Unlock()

	entry.mu.Lock()
	return func() {
		entry.mu.Unlock()
		h.finalizerMu.Lock()
		entry.refs--
		if entry.refs == 0 {
			delete(h.finalizers, requestID)
		}
		h.finalizerMu.Unlock()
	}
}

func (h *StudioHandler) authenticateStudioAPIKey(c *gin.Context, userID, apiKeyID int64) (*service.APIKey, bool) {
	key, err := h.studio.GetOwnedAPIKey(c.Request.Context(), userID, apiKeyID)
	if err != nil {
		response.ErrorFrom(c, err)
		return nil, false
	}
	previousAuthorization := c.GetHeader("Authorization")
	c.Request.Header.Set("Authorization", "Bearer "+key.Key)
	gin.HandlerFunc(h.apiKeyAuth)(c)
	if previousAuthorization == "" {
		c.Request.Header.Del("Authorization")
	} else {
		c.Request.Header.Set("Authorization", previousAuthorization)
	}
	if c.IsAborted() {
		return nil, false
	}
	authenticatedKey, _ := middleware2.GetAPIKeyFromContext(c)
	if authenticatedKey != nil && authenticatedKey.GroupID == nil && !h.settings.IsUngroupedKeySchedulingAllowed(c.Request.Context()) {
		response.Forbidden(c, "API Key is not assigned to any group and cannot be used")
		return nil, false
	}
	return authenticatedKey, authenticatedKey != nil
}

func (h *StudioHandler) finishAsyncImageFailure(rc *service.StudioRequestContext, code, message string, raw []byte) {
	if rc == nil {
		return
	}
	event := json.RawMessage(nil)
	if json.Valid(raw) {
		event = append(json.RawMessage(nil), raw...)
	}
	events := []json.RawMessage(nil)
	if len(event) > 0 {
		events = append(events, event)
	}
	ctx, cancel := studioPersistenceContext(rc.RequestContext)
	defer cancel()
	_ = h.studio.FinishRequest(ctx, rc, service.StudioRequestFailed, code, message, events)
}

type studioAsyncImageItem struct {
	URL           string `json:"url"`
	B64JSON       string `json:"b64_json"`
	RevisedPrompt string `json:"revised_prompt"`
}

func (h *StudioHandler) persistAsyncTaskImages(ctx context.Context, rc *service.StudioRequestContext, task *service.ImageTask) ([]string, error) {
	var direct struct {
		Data   []studioAsyncImageItem `json:"data"`
		Result struct {
			Data []studioAsyncImageItem `json:"data"`
		} `json:"result"`
	}
	if task == nil || len(task.Result) == 0 || json.Unmarshal(task.Result, &direct) != nil {
		return nil, errors.New("async image task returned an invalid result")
	}
	items := direct.Data
	if len(items) == 0 {
		items = direct.Result.Data
	}
	if len(items) == 0 && task.ImageURL != "" {
		items = []studioAsyncImageItem{{URL: task.ImageURL}}
	}
	if len(items) == 0 {
		return nil, errors.New("async image task returned no images")
	}
	format := "png"
	var payload map[string]any
	if json.Unmarshal(rc.Request.Payload, &payload) == nil {
		if value, _ := payload["output_format"].(string); value != "" {
			format = value
		}
	}
	assetIDs := make([]string, 0, len(items))
	for _, item := range items {
		data, detectedFormat, err := h.readAsyncImage(ctx, item)
		if err != nil {
			return nil, err
		}
		if detectedFormat != "" {
			format = detectedFormat
		}
		asset, err := h.studio.PersistOutputImageBytes(ctx, rc, data, format, item.RevisedPrompt)
		if err != nil {
			return nil, err
		}
		assetIDs = append(assetIDs, asset.ID)
	}
	return assetIDs, nil
}

func (h *StudioHandler) readAsyncImage(ctx context.Context, item studioAsyncImageItem) ([]byte, string, error) {
	if encoded := strings.TrimSpace(item.B64JSON); encoded != "" {
		data, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return nil, "", fmt.Errorf("decode async image: %w", err)
		}
		return data, studioImageFormatFromBytes(data, ""), nil
	}
	rawURL := strings.TrimSpace(item.URL)
	if rawURL == "" {
		return nil, "", errors.New("async image result has no URL")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, "", fmt.Errorf("build async image download: %w", err)
	}
	resp, err := h.imageClient.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("download async image: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, "", fmt.Errorf("download async image: unexpected status %d", resp.StatusCode)
	}
	const limit int64 = 32 << 20
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, "", fmt.Errorf("read async image: %w", err)
	}
	if int64(len(data)) > limit {
		return nil, "", errors.New("async image exceeds 32 MiB")
	}
	contentType := strings.TrimSpace(strings.Split(resp.Header.Get("Content-Type"), ";")[0])
	detected := strings.TrimSpace(strings.Split(http.DetectContentType(data), ";")[0])
	if !strings.HasPrefix(contentType, "image/") && !strings.HasPrefix(detected, "image/") {
		return nil, "", errors.New("async image URL did not return an image")
	}
	return data, studioImageFormatFromBytes(data, contentType), nil
}

func studioImageFormatFromBytes(data []byte, contentType string) string {
	if contentType == "" {
		contentType = strings.TrimSpace(strings.Split(http.DetectContentType(data), ";")[0])
	}
	switch strings.ToLower(contentType) {
	case "image/jpeg", "image/jpg":
		return "jpeg"
	case "image/webp":
		return "webp"
	default:
		return "png"
	}
}

func studioAsyncErrorMessage(raw []byte, fallback string) string {
	if len(raw) > 0 && json.Valid(raw) {
		var envelope struct {
			Message string `json:"message"`
			Error   struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(raw, &envelope) == nil {
			if envelope.Error.Message != "" {
				return envelope.Error.Message
			}
			if envelope.Message != "" {
				return envelope.Message
			}
		}
	}
	return fallback
}

func studioUsesOpenAIGateway(mode string, apiKey *service.APIKey) bool {
	if mode == "image" {
		return true
	}
	return apiKey != nil && apiKey.Group != nil &&
		(apiKey.Group.Platform == service.PlatformOpenAI || apiKey.Group.Platform == service.PlatformGrok)
}

func forceStudioImageGenerationToolChoice(payload json.RawMessage) (json.RawMessage, error) {
	var body map[string]any
	if err := json.Unmarshal(payload, &body); err != nil {
		return nil, err
	}
	if tools, ok := body["tools"].([]any); ok {
		for _, value := range tools {
			tool, ok := value.(map[string]any)
			if !ok || tool["type"] != "image_generation" {
				continue
			}
			delete(tool, "n")
			delete(tool, "aspect_ratio")
		}
	}
	body["tool_choice"] = map[string]any{"type": "image_generation"}
	existing, _ := body["instructions"].(string)
	if !strings.Contains(existing, studioImageGenerationInstructions) {
		existing = strings.TrimSpace(existing)
		if existing == "" {
			body["instructions"] = studioImageGenerationInstructions
		} else {
			body["instructions"] = existing + "\n\n" + studioImageGenerationInstructions
		}
	}
	return json.Marshal(body)
}

func (h *StudioHandler) endpointAllowed(c *gin.Context, endpoint string) bool {
	if h.settings == nil {
		return false
	}
	settings, err := h.settings.GetPublicSettings(c.Request.Context())
	if err != nil {
		return false
	}
	return studioEndpointAllowed(c, endpoint, settings.APIBaseURL, settings.CustomEndpoints)
}

func studioEndpointAllowed(c *gin.Context, endpoint, apiBaseURL, customEndpoints string) bool {
	endpoint = normalizeStudioEndpoint(endpoint)
	if endpoint == "" {
		return false
	}
	if normalizeStudioEndpoint(apiBaseURL) == endpoint {
		return true
	}
	if studioEndpointMatchesRequestOrigin(c, endpoint) {
		return true
	}
	var custom []struct {
		Endpoint string `json:"endpoint"`
	}
	if json.Unmarshal([]byte(customEndpoints), &custom) != nil {
		return false
	}
	for _, item := range custom {
		if normalizeStudioEndpoint(item.Endpoint) == endpoint {
			return true
		}
	}
	return false
}

func normalizeStudioEndpoint(value string) string {
	value = strings.TrimRight(strings.TrimSpace(value), "/")
	value = strings.TrimSuffix(value, "/v1/responses")
	value = strings.TrimSuffix(value, "/v1")
	return strings.TrimRight(value, "/")
}

func studioEndpointMatchesRequestOrigin(c *gin.Context, endpoint string) bool {
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Path != "" {
		return false
	}
	if origin := strings.TrimSpace(c.GetHeader("Origin")); origin != "" {
		originURL, err := url.Parse(origin)
		if err == nil && originURL.Host != "" && (originURL.Scheme == "http" || originURL.Scheme == "https") && originURL.Path == "" && originURL.RawQuery == "" && originURL.Fragment == "" {
			return strings.EqualFold(parsed.Scheme, originURL.Scheme) && strings.EqualFold(parsed.Host, originURL.Host)
		}
	}
	return strings.EqualFold(parsed.Host, c.Request.Host)
}

func studioUserID(c *gin.Context) (int64, bool) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return 0, false
	}
	return subject.UserID, true
}

// ioNopCloser keeps the request body local without importing a second copy helper.
type ioNopCloser struct{ *bytes.Reader }

func (ioNopCloser) Close() error { return nil }

type studioCaptureWriter struct {
	gin.ResponseWriter
	studio                    *service.StudioService
	rc                        *service.StudioRequestContext
	buffer                    bytes.Buffer
	events                    []json.RawMessage
	seenImages                map[string]struct{}
	failedCode, failedMessage string
	persistErr                error
	wroteSSE                  bool
}

func newStudioCaptureWriter(out gin.ResponseWriter, studio *service.StudioService, rc *service.StudioRequestContext) *studioCaptureWriter {
	return &studioCaptureWriter{ResponseWriter: out, studio: studio, rc: rc, seenImages: make(map[string]struct{})}
}

func (w *studioCaptureWriter) Write(data []byte) (int, error) {
	if w.Status() >= http.StatusBadRequest {
		return w.buffer.Write(data)
	}
	_, _ = w.buffer.Write(data)
	w.consume(false)
	return len(data), nil
}

func (w *studioCaptureWriter) WriteString(value string) (int, error) { return w.Write([]byte(value)) }
func (w *studioCaptureWriter) Flush()                                { w.consume(false); w.ResponseWriter.Flush() }

func (w *studioCaptureWriter) consume(final bool) {
	for {
		data := w.buffer.Bytes()
		idx, width := studioSSEBoundary(data)
		if idx < 0 {
			if final && len(data) > 0 {
				block := append([]byte(nil), data...)
				w.buffer.Reset()
				w.consumeBlock(block)
			}
			return
		}
		block := append([]byte(nil), data[:idx]...)
		w.buffer.Next(idx + width)
		w.consumeBlock(block)
	}
}

func studioSSEBoundary(data []byte) (int, int) {
	a := bytes.Index(data, []byte("\n\n"))
	b := bytes.Index(data, []byte("\r\n\r\n"))
	if a < 0 {
		if b < 0 {
			return -1, 0
		}
		return b, 4
	}
	if b >= 0 && b < a {
		return b, 4
	}
	return a, 2
}

func (w *studioCaptureWriter) consumeBlock(block []byte) {
	trimmed := bytes.TrimSpace(block)
	if len(trimmed) == 0 {
		return
	}
	if bytes.HasPrefix(trimmed, []byte(":")) {
		w.forward(block)
		return
	}
	var payload []byte
	for _, line := range bytes.Split(trimmed, []byte("\n")) {
		line = bytes.TrimSuffix(line, []byte("\r"))
		if bytes.HasPrefix(line, []byte("data:")) {
			if len(payload) > 0 {
				payload = append(payload, '\n')
			}
			payload = append(payload, bytes.TrimSpace(line[5:])...)
		}
	}
	if bytes.Equal(payload, []byte("[DONE]")) {
		return
	}
	if !json.Valid(payload) {
		w.forward(block)
		return
	}
	var event map[string]any
	if json.Unmarshal(payload, &event) != nil {
		w.forward(block)
		return
	}
	w.captureEvent(event)
	sanitizeStudioResponseValue(event)
	sanitized, _ := json.Marshal(event)
	w.events = append(w.events, append(json.RawMessage(nil), sanitized...))
	w.forward([]byte("data: " + string(sanitized)))
}

func sanitizeStudioResponseValue(value any) {
	switch typed := value.(type) {
	case map[string]any:
		if typed["type"] == "image_generation_call" {
			delete(typed, "result")
		}
		delete(typed, "partial_image_b64")
		delete(typed, "b64_json")
		delete(typed, "image_base64")
		for _, child := range typed {
			sanitizeStudioResponseValue(child)
		}
	case []any:
		for _, child := range typed {
			sanitizeStudioResponseValue(child)
		}
	}
}

func (w *studioCaptureWriter) captureEvent(event map[string]any) {
	typeName, _ := event["type"].(string)
	if typeName == "response.output_text.delta" {
		if delta, ok := event["delta"].(string); ok {
			w.studio.AppendText(w.rc, delta)
		}
	}
	if typeName == "response.completed" && w.rc.AssistantMessage.Content == "" {
		if responseValue, ok := event["response"].(map[string]any); ok {
			if output, ok := responseValue["output"].([]any); ok {
				for _, raw := range output {
					item, _ := raw.(map[string]any)
					if item["type"] != "message" {
						continue
					}
					content, _ := item["content"].([]any)
					for _, rawPart := range content {
						part, _ := rawPart.(map[string]any)
						if part["type"] == "output_text" {
							if text, ok := part["text"].(string); ok {
								w.studio.AppendText(w.rc, text)
							}
						}
					}
				}
			}
		}
	}
	if typeName == "error" || typeName == "response.failed" {
		w.failedCode, w.failedMessage = studioEventError(event)
	}
	items := []map[string]any{}
	if item, ok := event["item"].(map[string]any); ok {
		items = append(items, item)
	}
	if responseValue, ok := event["response"].(map[string]any); ok {
		if output, ok := responseValue["output"].([]any); ok {
			for _, raw := range output {
				if item, ok := raw.(map[string]any); ok {
					items = append(items, item)
				}
			}
		}
	}
	for _, item := range items {
		if item["type"] != "image_generation_call" {
			continue
		}
		encoded, _ := item["result"].(string)
		if encoded == "" {
			continue
		}
		delete(item, "result")
		if _, exists := w.seenImages[encoded]; exists {
			continue
		}
		w.seenImages[encoded] = struct{}{}
		format, _ := item["output_format"].(string)
		revised, _ := item["revised_prompt"].(string)
		persistCtx, cancel := studioPersistenceContext(w.rc.RequestContext)
		asset, err := w.studio.PersistOutputImage(persistCtx, w.rc, encoded, format, revised)
		cancel()
		if err != nil {
			w.persistErr = err
			return
		}
		w.forwardJSON(map[string]any{"type": "studio.image", "asset_id": asset.ID, "mime_type": asset.MIMEType, "byte_size": asset.ByteSize, "url": "/api/v1/studio/assets/" + asset.ID + "/content", "revised_prompt": revised})
	}
}

func studioEventError(event map[string]any) (string, string) {
	if e, ok := event["error"].(map[string]any); ok {
		code, _ := e["code"].(string)
		message, _ := e["message"].(string)
		return code, message
	}
	if r, ok := event["response"].(map[string]any); ok {
		if e, ok := r["error"].(map[string]any); ok {
			code, _ := e["code"].(string)
			message, _ := e["message"].(string)
			return code, message
		}
	}
	return "upstream_error", "Studio request failed"
}

func (w *studioCaptureWriter) forward(block []byte) {
	if w.persistErr != nil {
		return
	}
	if !w.wroteSSE {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.wroteSSE = true
	}
	_, _ = w.ResponseWriter.Write(append(append([]byte(nil), block...), '\n', '\n'))
}

func (w *studioCaptureWriter) forwardJSON(value any) {
	encoded, _ := json.Marshal(value)
	w.forward([]byte("data: " + string(encoded)))
}

func (w *studioCaptureWriter) Finish(ctx context.Context) {
	if w.Status() < http.StatusBadRequest {
		w.consume(true)
	}
	status := service.StudioRequestCompleted
	if ctx.Err() != nil {
		status = service.StudioRequestCancelled
		w.failedCode = "request_cancelled"
		w.failedMessage = "Studio request was cancelled"
	}
	if w.Status() >= http.StatusBadRequest {
		status = service.StudioRequestFailed
		w.failedCode = strconv.Itoa(w.Status())
		w.failedMessage = strings.TrimSpace(w.buffer.String())
		if w.failedMessage == "" {
			w.failedMessage = http.StatusText(w.Status())
		}
	} else if status != service.StudioRequestCancelled && w.failedMessage != "" {
		status = service.StudioRequestFailed
	}
	if w.persistErr != nil {
		status, w.failedCode, w.failedMessage = service.StudioRequestPersistFailed, "persistence_failed", w.persistErr.Error()
	}
	persistCtx, cancel := studioPersistenceContext(ctx)
	defer cancel()
	if err := w.studio.FinishRequest(persistCtx, w.rc, status, w.failedCode, w.failedMessage, w.events); err != nil {
		status, w.failedCode, w.failedMessage = service.StudioRequestPersistFailed, "persistence_failed", err.Error()
	}
	if w.Status() >= http.StatusBadRequest && !w.wroteSSE {
		_, _ = w.ResponseWriter.Write(w.buffer.Bytes())
		return
	}
	if status == service.StudioRequestPersistFailed {
		w.persistErr = nil
		w.forwardJSON(map[string]any{"type": "studio.persistence_failed", "error": map[string]string{"code": w.failedCode, "message": w.failedMessage}})
	} else {
		w.forwardJSON(map[string]any{"type": "studio.persisted", "request_id": w.rc.Request.ID, "message_id": w.rc.AssistantMessage.ID, "status": status})
	}
	w.forward([]byte("data: [DONE]"))
	w.ResponseWriter.Flush()
}

func studioPersistenceContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
}

var _ gin.ResponseWriter = (*studioCaptureWriter)(nil)
