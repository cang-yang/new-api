package service

import (
	"io"
	"net/http"
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
	saveOnce  sync.Once
}

var lastBodyAuditCleanup atomic.Int64

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
	r.capture.save(false)
	return err
}

func (capture *bodyAuditCapture) save(responseComplete bool) {
	capture.saveOnce.Do(func() {
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
			ResponseComplete:      responseComplete,
		}
		if err := model.UpsertBodyAudit(audit); err != nil {
			logger.LogError(nil, "failed to save body audit: "+err.Error())
			return
		}
		maybeCleanupBodyAudits()
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
}
