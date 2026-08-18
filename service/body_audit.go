package service

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"

	"github.com/gin-gonic/gin"
)

type limitedAuditBuffer struct {
	mu        sync.Mutex
	data      []byte
	totalSize int64
	truncated bool
	limit     int64
}

func newLimitedAuditBuffer(limit int64) *limitedAuditBuffer {
	return &limitedAuditBuffer{
		data:  make([]byte, 0, min(limit, 64*1024)),
		limit: limit,
	}
}

func (b *limitedAuditBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.totalSize += int64(len(p))
	remaining := b.limit - int64(len(b.data))
	if remaining > 0 {
		writeSize := min(int64(len(p)), remaining)
		b.data = append(b.data, p[:writeSize]...)
	}
	if b.totalSize > b.limit {
		b.truncated = true
	}
	return len(p), nil
}

func (b *limitedAuditBuffer) snapshot() ([]byte, int64, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]byte(nil), b.data...), b.totalSize, b.truncated
}

type auditRequestReadCloser struct {
	reader io.Reader
	closer io.Closer
}

func (r *auditRequestReadCloser) Read(p []byte) (int, error) {
	return r.reader.Read(p)
}

func (r *auditRequestReadCloser) Close() error {
	return r.closer.Close()
}

type bodyAuditCapture struct {
	requestId          string
	userId             int
	channelId          int
	modelName          string
	request            *limitedAuditBuffer
	response           *limitedAuditBuffer
	status             int
	mediaType          string
	complete           bool
	saveOnce           sync.Once
	traceId            int64
	attemptId          int64
	attemptPersistOnce sync.Once
	clientPersistOnce  sync.Once
}

const bodyAuditClientResponseKey = "body_audit_client_response"

type bodyAuditClientResponseCapture struct {
	mu       sync.Mutex
	response *limitedAuditBuffer
	capture  *bodyAuditCapture
	writer   *bodyAuditResponseWriter
}

type bodyAuditResponseWriter struct {
	gin.ResponseWriter
	response *limitedAuditBuffer
	failed   atomic.Bool
}

func (w *bodyAuditResponseWriter) Write(data []byte) (int, error) {
	n, err := w.ResponseWriter.Write(data)
	if n > 0 {
		_, _ = w.response.Write(data[:n])
	}
	if err != nil {
		w.failed.Store(true)
	}
	return n, err
}

func (w *bodyAuditResponseWriter) WriteString(data string) (int, error) {
	n, err := w.ResponseWriter.WriteString(data)
	if n > 0 {
		_, _ = w.response.Write([]byte(data[:n]))
	}
	if err != nil {
		w.failed.Store(true)
	}
	return n, err
}

var lastBodyAuditCleanup atomic.Int64

func BeginBodyAuditClientResponse(c *gin.Context) {
	if !constant.BodyAuditEnabled || c == nil || c.Writer == nil {
		return
	}
	if _, exists := c.Get(bodyAuditClientResponseKey); exists {
		return
	}

	limit := int64(constant.BodyAuditMaxBodyMB) * 1024 * 1024
	response := newLimitedAuditBuffer(limit)
	writer := &bodyAuditResponseWriter{
		ResponseWriter: c.Writer,
		response:       response,
	}
	session := &bodyAuditClientResponseCapture{
		response: response,
		writer:   writer,
	}
	c.Set(bodyAuditClientResponseKey, session)
	c.Writer = writer
}

func FinalizeBodyAuditClientResponse(c *gin.Context) {
	if c == nil {
		return
	}
	value, exists := c.Get(bodyAuditClientResponseKey)
	if !exists {
		return
	}
	session, ok := value.(*bodyAuditClientResponseCapture)
	if !ok || session == nil {
		return
	}

	session.mu.Lock()
	capture := session.capture
	session.mu.Unlock()
	if capture == nil {
		return
	}

	clientBody, clientSize, clientTruncated := session.response.snapshot()
	capture.persist(&bodyAuditClientResponseSnapshot{
		body:        clientBody,
		size:        clientSize,
		truncated:   clientTruncated,
		status:      session.writer.Status(),
		contentType: session.writer.Header().Get("Content-Type"),
		complete:    !session.writer.failed.Load(),
	})
}

// BeginBodyAudit wraps the exact request body consumed by the HTTP transport.
// It must run after all request conversion and parameter overrides are applied.
func BeginBodyAudit(c *gin.Context, req *http.Request, info *relaycommon.RelayInfo) *bodyAuditCapture {
	if !constant.BodyAuditEnabled || req == nil || req.Body == nil {
		return nil
	}
	requestId := c.GetString(common.RequestIdKey)
	if requestId == "" {
		return nil
	}

	limit := int64(constant.BodyAuditMaxBodyMB) * 1024 * 1024
	capture := &bodyAuditCapture{
		requestId: requestId,
		userId:    c.GetInt("id"),
		channelId: c.GetInt("channel_id"),
		modelName: info.UpstreamModelName,
		request:   newLimitedAuditBuffer(limit),
		response:  newLimitedAuditBuffer(limit),
	}
	capture.beginTrace(c, req, info)
	if value, exists := c.Get(bodyAuditClientResponseKey); exists {
		if session, ok := value.(*bodyAuditClientResponseCapture); ok && session != nil {
			session.mu.Lock()
			session.capture = capture
			session.mu.Unlock()
		}
	}
	req.Body = &auditRequestReadCloser{
		reader: io.TeeReader(req.Body, capture.request),
		closer: req.Body,
	}
	return capture
}

// WrapBodyAuditResponse captures raw upstream bytes before any provider
// adaptor converts them for the client. Streaming SSE is captured chunk by
// chunk without delaying delivery.
func WrapBodyAuditResponse(capture *bodyAuditCapture, resp *http.Response) {
	if capture == nil || resp == nil || resp.Body == nil {
		return
	}
	capture.status = resp.StatusCode
	capture.mediaType = resp.Header.Get("Content-Type")
	resp.Body = &auditResponseReadCloser{
		reader:  io.TeeReader(resp.Body, capture.response),
		closer:  resp.Body,
		capture: capture,
	}
}

func SaveBodyAuditRequestFailure(capture *bodyAuditCapture) {
	if capture != nil {
		capture.save(false)
	}
}

type auditResponseReadCloser struct {
	reader  io.Reader
	closer  io.Closer
	capture *bodyAuditCapture
}

func (r *auditResponseReadCloser) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	if err == io.EOF {
		responseBody, _, responseTruncated := r.capture.response.snapshot()
		r.capture.save(bodyAuditResponseLooksComplete(r.capture.mediaType, responseBody, responseTruncated))
	}
	return n, err
}

func (r *auditResponseReadCloser) Close() error {
	err := r.closer.Close()
	responseComplete := false
	if err == nil {
		responseBody, _, responseTruncated := r.capture.response.snapshot()
		responseComplete = bodyAuditResponseLooksComplete(r.capture.mediaType, responseBody, responseTruncated)
	}
	r.capture.save(responseComplete)
	return err
}

// Some streaming adaptors stop reading as soon as they consume the protocol's
// terminal event and then close the upstream body without performing the extra
// read that would return io.EOF. Recognize those terminal payloads so a fully
// captured response is not incorrectly reported as incomplete.
func bodyAuditResponseLooksComplete(mediaType string, body []byte, truncated bool) bool {
	if truncated || len(body) == 0 {
		return false
	}

	mediaType = strings.ToLower(mediaType)
	if strings.Contains(mediaType, "text/event-stream") {
		for _, line := range strings.Split(strings.ReplaceAll(string(body), "\r\n", "\n"), "\n") {
			line = strings.TrimSpace(line)
			if line == "data: [DONE]" || line == "event: message_stop" || line == "event: response.completed" {
				return true
			}
		}
		return false
	}

	return strings.Contains(mediaType, "json") && json.Valid(body)
}

func (capture *bodyAuditCapture) save(responseComplete bool) {
	capture.saveOnce.Do(func() {
		capture.complete = responseComplete
		capture.persist(nil)
	})
}

type bodyAuditClientResponseSnapshot struct {
	body        []byte
	size        int64
	truncated   bool
	status      int
	contentType string
	complete    bool
}

func (capture *bodyAuditCapture) persist(client *bodyAuditClientResponseSnapshot) {
	requestBody, requestSize, requestTruncated := capture.request.snapshot()
	responseBody, responseSize, responseTruncated := capture.response.snapshot()
	audit := &model.BodyAudit{
		RequestId:             capture.requestId,
		UserId:                capture.userId,
		ChannelId:             capture.channelId,
		ModelName:             capture.modelName,
		RequestBody:           requestBody,
		RequestBodySize:       requestSize,
		RequestBodyTruncated:  requestTruncated,
		ResponseBody:          responseBody,
		ResponseBodySize:      responseSize,
		ResponseBodyTruncated: responseTruncated,
		ResponseStatus:        capture.status,
		ResponseContentType:   capture.mediaType,
		ResponseComplete:      capture.complete,
	}
	if client != nil {
		audit.ClientResponseBody = client.body
		audit.ClientResponseBodySize = client.size
		audit.ClientResponseBodyTruncated = client.truncated
		audit.ClientResponseStatus = client.status
		audit.ClientResponseContentType = client.contentType
		audit.ClientResponseComplete = client.complete
	}
	if err := model.UpsertBodyAudit(audit); err != nil {
		logger.LogError(nil, "failed to save body audit: "+err.Error())
	} else {
		maybeCleanupBodyAudits()
	}
	capture.persistAttemptTrace(requestBody, requestSize, requestTruncated, responseBody, responseSize, responseTruncated)
	if client != nil {
		capture.persistClientTrace(client)
	}
}

func (capture *bodyAuditCapture) beginTrace(c *gin.Context, req *http.Request, info *relaycommon.RelayInfo) {
	if model.DB == nil || info == nil {
		return
	}
	clientMethod := req.Method
	clientRoute := req.URL.Path
	if c.Request != nil && c.Request.URL != nil {
		clientMethod = c.Request.Method
		clientRoute = c.Request.URL.RequestURI()
	}
	trace, err := model.GetOrCreateAuditTrace(&model.AuditTrace{
		RequestId:    capture.requestId,
		Source:       "relay",
		Status:       "recording",
		UserId:       capture.userId,
		TokenId:      info.TokenId,
		Method:       clientMethod,
		Route:        clientRoute,
		RequestModel: info.OriginModelName,
		RelayFormat:  string(info.RelayFormat),
	})
	if err != nil {
		logger.LogError(nil, "failed to create audit trace: "+err.Error())
		return
	}
	capture.traceId = trace.Id
	if trace.OriginalRequestBlobId == 0 {
		captureOriginalClientRequest(c, trace)
	}

	channelType := 0
	if info.ChannelMeta != nil {
		channelType = info.ChannelMeta.ChannelType
	}
	snapshotPayload, snapshotBuildErr := buildAttemptConfigSnapshot(c, info)
	if snapshotBuildErr != nil {
		logger.LogError(nil, "failed to build audit config snapshot: "+snapshotBuildErr.Error())
	}
	snapshot, snapshotErr := model.GetOrCreateAuditConfigSnapshot("attempt", snapshotPayload)
	if snapshotErr != nil {
		logger.LogError(nil, "failed to create audit config snapshot: "+snapshotErr.Error())
	}
	configSnapshotId := int64(0)
	if snapshot != nil {
		configSnapshotId = snapshot.Id
	}
	target := req.URL.Scheme + "://" + req.URL.Host + req.URL.EscapedPath()
	attempt := &model.AuditAttempt{
		TraceId:           trace.Id,
		RoutingRetryIndex: info.RetryIndex,
		ChannelId:         capture.channelId,
		ChannelType:       channelType,
		ChannelName:       common.GetContextKeyString(c, constant.ContextKeyChannelName),
		RequestModel:      info.OriginModelName,
		UpstreamModel:     capture.modelName,
		RequestFormat:     string(info.RelayFormat),
		UpstreamFormat:    string(info.GetFinalRequestRelayFormat()),
		ConfigSnapshotId:  configSnapshotId,
		Method:            req.Method,
		Target:            target,
		State:             "recording",
		StartedAt:         time.Now().UnixMilli(),
	}
	if err := model.CreateNextAuditAttempt(attempt); err != nil {
		logger.LogError(nil, "failed to create audit attempt: "+err.Error())
		return
	}
	capture.attemptId = attempt.Id
}

// buildAttemptConfigSnapshot produces the immutable, deliberately secret-free
// explanation of an upstream attempt. It is kept separate from the captured
// wire request: this object describes routing and policy decisions, while the
// wire blob records their result.
func buildAttemptConfigSnapshot(c *gin.Context, info *relaycommon.RelayInfo) ([]byte, error) {
	channelId := 0
	channelName := ""
	if c != nil {
		channelId = common.GetContextKeyInt(c, constant.ContextKeyChannelId)
		channelName = common.GetContextKeyString(c, constant.ContextKeyChannelName)
	}
	if info == nil {
		return common.Marshal(map[string]any{"schema_version": 2, "channel": map[string]any{"id": channelId, "name": channelName}})
	}

	channelType := 0
	baseOrigin := ""
	apiType := 0
	apiVersion := ""
	isMultiKey := false
	multiKeyIndex := 0
	isModelMapped := false
	supportStreamOptions := false
	paramOverride := map[string]interface{}(nil)
	headerNames := make([]string, 0)
	if info.ChannelMeta != nil {
		channelType = info.ChannelMeta.ChannelType
		if channelId == 0 {
			channelId = info.ChannelMeta.ChannelId
		}
		baseOrigin = sanitizeBaseOrigin(info.ChannelMeta.ChannelBaseUrl)
		apiType = info.ChannelMeta.ApiType
		apiVersion = info.ChannelMeta.ApiVersion
		isMultiKey = info.ChannelMeta.ChannelIsMultiKey
		multiKeyIndex = info.ChannelMeta.ChannelMultiKeyIndex
		isModelMapped = info.ChannelMeta.IsModelMapped
		supportStreamOptions = info.ChannelMeta.SupportStreamOptions
		paramOverride = info.ChannelMeta.ParamOverride
		headerNames = append(headerNames, mapKeys(info.ChannelMeta.HeadersOverride)...)
	}
	headerNames = append(headerNames, mapKeys(info.RuntimeHeadersOverride)...)
	headerNames = uniqueSortedStrings(headerNames)

	sanitizedOverride, err := sanitizeParamOverrideForSnapshot(paramOverride)
	if err != nil {
		return nil, err
	}
	conversionChain := make([]string, 0, len(info.RequestConversionChain))
	for _, format := range info.RequestConversionChain {
		conversionChain = append(conversionChain, string(format))
	}
	payload := map[string]any{
		"schema_version": 2,
		"channel": map[string]any{
			"id": channelId, "type": channelType, "name": channelName,
			"base_origin": baseOrigin, "api_type": apiType, "api_version": apiVersion,
			"is_multi_key": isMultiKey, "multi_key_index": multiKeyIndex,
		},
		"models": map[string]any{
			"requested": info.OriginModelName, "upstream": info.UpstreamModelName,
			"mapped": isModelMapped,
		},
		"formats": map[string]any{
			"request": string(info.RelayFormat), "upstream": string(info.GetFinalRequestRelayFormat()),
			"conversion_chain": conversionChain,
		},
		"routing": map[string]any{
			"retry_index": info.RetryIndex, "token_group": info.TokenGroup,
			"user_group": info.UserGroup, "using_group": info.UsingGroup,
		},
		"overrides": map[string]any{
			"parameter_config": sanitizedOverride, "parameter_audit": sanitizeParamOverrideAudit(info.ParamOverrideAudit),
			"header_names": headerNames,
		},
		"policies": map[string]any{
			"stream": info.IsStream, "include_usage": info.ShouldIncludeUsage,
			"disable_ping": info.DisablePing, "support_stream_options": supportStreamOptions,
			"playground": info.IsPlayground, "channel_test": info.IsChannelTest,
			"use_price": info.UsePrice, "relay_mode": info.RelayMode,
			"reasoning_effort": info.ReasoningEffort, "force_preconsume": info.ForcePreConsume,
			"billing_source": info.BillingSource,
		},
	}
	return common.Marshal(payload)
}

func sanitizeParamOverrideAudit(lines []string) []string {
	result := append([]string(nil), lines...)
	for index, line := range result {
		trimmed := strings.TrimSpace(line)
		separator := strings.Index(line, "=")
		target := ""
		if separator >= 0 {
			fields := strings.Fields(line[:separator])
			if len(fields) > 1 {
				target = fields[len(fields)-1]
			}
		}
		if separator >= 0 && (strings.HasPrefix(trimmed, "set_header ") || isSnapshotSecretPath(target)) {
			result[index] = strings.TrimSpace(line[:separator]) + "= [REDACTED]"
		}
	}
	return result
}

func sanitizeBaseOrigin(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return ""
	}
	parsed.User = nil
	parsed.Path = ""
	parsed.RawPath = ""
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.Scheme + "://" + parsed.Host
}

func mapKeys(values map[string]interface{}) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	return keys
}

func uniqueSortedStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func sanitizeParamOverrideForSnapshot(value map[string]interface{}) (any, error) {
	if len(value) == 0 {
		return map[string]any{}, nil
	}
	raw, err := common.Marshal(value)
	if err != nil {
		return nil, err
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, err
	}
	return sanitizeSnapshotValue(decoded, false), nil
}

func sanitizeSnapshotValue(value any, redactValue bool) any {
	if redactValue {
		return "[REDACTED]"
	}
	switch typed := value.(type) {
	case []any:
		result := make([]any, len(typed))
		for index, item := range typed {
			result[index] = sanitizeSnapshotValue(item, false)
		}
		return result
	case map[string]any:
		result := make(map[string]any, len(typed))
		for key, item := range typed {
			result[key] = sanitizeSnapshotValue(item, isSnapshotSecretKey(key))
		}
		mode, _ := result["mode"].(string)
		path, _ := result["path"].(string)
		// Header operations and writes to credential-shaped JSON paths keep
		// their target name, but never their value.
		if mode == "set_header" || isSnapshotSecretPath(path) {
			if _, exists := result["value"]; exists {
				result["value"] = "[REDACTED]"
			}
		}
		return result
	default:
		return value
	}
}

func isSnapshotSecretPath(path string) bool {
	parts := strings.FieldsFunc(path, func(r rune) bool {
		return r == '.' || r == '/' || r == '[' || r == ']'
	})
	if len(parts) == 0 {
		return false
	}
	return isSnapshotSecretKey(parts[len(parts)-1])
}

func isSnapshotSecretKey(key string) bool {
	normalized := strings.ToLower(strings.NewReplacer("-", "", "_", "", " ", "").Replace(key))
	switch normalized {
	case "key", "auth", "bearer":
		return true
	}
	return strings.Contains(normalized, "apikey") ||
		strings.Contains(normalized, "authorization") ||
		strings.Contains(normalized, "cookie") ||
		strings.Contains(normalized, "password") ||
		strings.Contains(normalized, "credential") ||
		strings.HasSuffix(normalized, "secret") ||
		strings.HasSuffix(normalized, "token")
}

func captureOriginalClientRequest(c *gin.Context, trace *model.AuditTrace) {
	if c == nil || c.Request == nil || trace == nil || trace.Id == 0 {
		return
	}
	value, exists := c.Get(common.KeyBodyStorage)
	if !exists {
		return
	}
	storage, ok := value.(common.BodyStorage)
	if !ok || storage == nil {
		return
	}
	reader, err := storage.NewReader()
	if err != nil {
		logger.LogError(nil, "failed to open original client request for audit: "+err.Error())
		return
	}
	defer reader.Close()
	limit := int64(constant.BodyAuditMaxBodyMB) * 1024 * 1024
	originalSize := storage.Size()
	readLimit := min(originalSize, limit)
	observed, err := io.ReadAll(io.LimitReader(reader, readLimit))
	if err != nil {
		logger.LogError(nil, "failed to read original client request for audit: "+err.Error())
		return
	}
	truncated := originalSize > limit
	blob, err := model.CreateAuditBlob(&model.AuditBlob{
		CaptureStage: "client_request", MediaType: c.Request.Header.Get("Content-Type"), Body: observed,
		OriginalSize: originalSize, Complete: !truncated && int64(len(observed)) == originalSize, Truncated: truncated,
	})
	if err != nil {
		logger.LogError(nil, "failed to save original client request audit blob: "+err.Error())
		return
	}
	assigned, err := model.SetAuditTraceOriginalRequestBlobIfEmpty(trace.Id, blob.Id)
	if err != nil {
		if cleanupErr := model.DeleteAuditBlob(blob.Id); cleanupErr != nil {
			logger.LogError(nil, "failed to clean unlinked original client request audit blob: "+cleanupErr.Error())
		}
		logger.LogError(nil, "failed to link original client request audit blob: "+err.Error())
		return
	}
	if assigned {
		trace.OriginalRequestBlobId = blob.Id
	} else {
		if cleanupErr := model.DeleteAuditBlob(blob.Id); cleanupErr != nil {
			logger.LogError(nil, "failed to clean duplicate original client request audit blob: "+cleanupErr.Error())
		}
	}
}

func (capture *bodyAuditCapture) persistAttemptTrace(requestBody []byte, requestSize int64, requestTruncated bool, responseBody []byte, responseSize int64, responseTruncated bool) {
	if capture.traceId == 0 || capture.attemptId == 0 {
		return
	}
	capture.attemptPersistOnce.Do(func() {
		requestBlob, err := model.CreateAuditBlob(&model.AuditBlob{
			CaptureStage: "upstream_request", MediaType: "application/json", Body: requestBody,
			OriginalSize: requestSize, Complete: !requestTruncated, Truncated: requestTruncated,
		})
		if err != nil {
			logger.LogError(nil, "failed to save audit request blob: "+err.Error())
			return
		}
		responseBlob, err := model.CreateAuditBlob(&model.AuditBlob{
			CaptureStage: "upstream_response", MediaType: capture.mediaType, Body: responseBody,
			OriginalSize: responseSize, Complete: capture.complete, Truncated: responseTruncated,
		})
		if err != nil {
			logger.LogError(nil, "failed to save audit response blob: "+err.Error())
			return
		}
		state := "succeeded"
		terminalKind := "protocol_terminal"
		if capture.status < http.StatusOK || capture.status >= http.StatusBadRequest || !capture.complete {
			state = "failed"
			terminalKind = "incomplete"
		}
		if err := model.UpdateAuditAttemptCapture(capture.attemptId, requestBlob.Id, responseBlob.Id, state, capture.status, capture.complete, terminalKind); err != nil {
			logger.LogError(nil, "failed to finalize audit attempt: "+err.Error())
		}
	})
}

func (capture *bodyAuditCapture) persistClientTrace(client *bodyAuditClientResponseSnapshot) {
	if capture.traceId == 0 || capture.attemptId == 0 || client == nil {
		return
	}
	capture.clientPersistOnce.Do(func() {
		blob, err := model.CreateAuditBlob(&model.AuditBlob{
			CaptureStage: "client_response", MediaType: client.contentType, Body: client.body,
			OriginalSize: client.size, Complete: client.complete && capture.complete, Truncated: client.truncated,
		})
		if err != nil {
			logger.LogError(nil, "failed to save audit client response blob: "+err.Error())
			return
		}
		status := "succeeded"
		terminalKind := "protocol_terminal"
		if client.status < http.StatusOK || client.status >= http.StatusBadRequest || !client.complete || !capture.complete {
			status = "failed"
			terminalKind = "incomplete"
		}
		if err := model.FinalizeAuditTrace(capture.traceId, capture.attemptId, blob.Id, status, terminalKind); err != nil {
			logger.LogError(nil, "failed to finalize audit trace: "+err.Error())
		}
	})
}

func maybeCleanupBodyAudits() {
	now := time.Now().Unix()
	last := lastBodyAuditCleanup.Load()
	if now-last < int64(time.Hour/time.Second) || !lastBodyAuditCleanup.CompareAndSwap(last, now) {
		return
	}
	cutoff := now - int64(constant.BodyAuditRetentionDays)*int64(24*time.Hour/time.Second)
	if err := model.DeleteBodyAuditsBefore(cutoff); err != nil {
		logger.LogError(nil, "failed to clean expired body audits: "+err.Error())
	}
	if err := model.DeleteAuditTracesBefore(cutoff * 1000); err != nil {
		logger.LogError(nil, "failed to clean expired audit traces: "+err.Error())
	}
}
