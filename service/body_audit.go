package service

import (
	"encoding/json"
	"io"
	"net/http"
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
	requestId string
	userId    int
	channelId int
	modelName string
	request   *limitedAuditBuffer
	response  *limitedAuditBuffer
	status    int
	mediaType string
	complete  bool
	saveOnce  sync.Once
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
		r.capture.save(true)
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
		return
	}
	maybeCleanupBodyAudits()
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
}
