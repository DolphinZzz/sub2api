package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
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
	apiKeyAuth    middleware2.APIKeyAuthMiddleware
	maxBodySize   int64
}

func NewStudioHandler(studio *service.StudioService, settings *service.SettingService, gateway *GatewayHandler, openAIGateway *OpenAIGatewayHandler, apiKeyAuth middleware2.APIKeyAuthMiddleware, cfg *config.Config) *StudioHandler {
	maxBodySize := int64(100 << 20)
	if cfg != nil && cfg.Gateway.MaxBodySize > 0 {
		maxBodySize = cfg.Gateway.MaxBodySize
	}
	return &StudioHandler{studio: studio, settings: settings, gateway: gateway, openAIGateway: openAIGateway, apiKeyAuth: apiKeyAuth, maxBodySize: maxBodySize}
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
	defer reader.Close()
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

	// Invoke the existing gateway with the selected, owned API key so billing,
	// quota checks, account routing, usage_logs and failover stay on one path.
	c.Set(string(middleware2.ContextKeyAPIKey), rc.APIKey)
	c.Request.Body = http.NoBody
	c.Request.Body = ioNopCloser{Reader: bytes.NewReader(req.Payload)}
	c.Request.ContentLength = int64(len(req.Payload))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.Header.Set("Accept", "text/event-stream")
	c.Request.Header.Set("OpenAI-Beta", "responses=experimental")
	c.Request.URL.Path = "/v1/responses"
	InboundEndpointMiddleware()(c)

	original := c.Writer
	w := newStudioCaptureWriter(original, h.studio, rc)
	c.Writer = w
	if rc.APIKey.Group != nil && (rc.APIKey.Group.Platform == service.PlatformOpenAI || rc.APIKey.Group.Platform == service.PlatformGrok) {
		h.openAIGateway.Responses(c)
	} else {
		h.gateway.Responses(c)
	}
	c.Writer = original
	w.Finish(c.Request.Context())
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
