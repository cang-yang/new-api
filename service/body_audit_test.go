package service

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type singleChunkReadCloser struct {
	data []byte
	read bool
}

func (r *singleChunkReadCloser) Read(p []byte) (int, error) {
	if r.read {
		return 0, nil
	}
	r.read = true
	return copy(p, r.data), nil
}

func (r *singleChunkReadCloser) Close() error { return nil }

func migrateBodyAuditTestModels(t *testing.T, db *gorm.DB) {
	t.Helper()
	require.NoError(t, db.AutoMigrate(
		&model.BodyAudit{},
		&model.AuditTrace{},
		&model.AuditAttempt{},
		&model.AuditWireSend{},
		&model.AuditConfigSnapshot{},
		&model.AuditBlob{},
	))
}

func TestBodyAuditCapturesTransportRequestAndRawResponse(t *testing.T) {
	previousDB := model.DB
	previousEnabled := constant.BodyAuditEnabled
	previousMaxBodyMB := constant.BodyAuditMaxBodyMB
	t.Cleanup(func() {
		model.DB = previousDB
		constant.BodyAuditEnabled = previousEnabled
		constant.BodyAuditMaxBodyMB = previousMaxBodyMB
	})

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	migrateBodyAuditTestModels(t, db)
	model.DB = db
	constant.BodyAuditEnabled = true
	constant.BodyAuditMaxBodyMB = 1

	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Set(common.RequestIdKey, "req-transport-audit")
	context.Set("id", 12)
	context.Set("channel_id", 34)

	requestJSON := `{"model":"mapped-model","messages":[{"role":"user","content":"hello"}]}`
	request, err := http.NewRequest(http.MethodPost, "https://upstream.example/v1/chat/completions", strings.NewReader(requestJSON))
	require.NoError(t, err)
	capture := BeginBodyAudit(context, request, &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "mapped-model"},
	})
	require.NotNil(t, capture)
	sentBody, err := io.ReadAll(request.Body)
	require.NoError(t, err)
	assert.Equal(t, requestJSON, string(sentBody))

	responseSSE := "data: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\ndata: [DONE]\n\n"
	response := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(responseSSE)),
	}
	WrapBodyAuditResponse(capture, response)
	receivedBody, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	assert.Equal(t, responseSSE, string(receivedBody))

	audit, err := model.GetBodyAuditByRequestId("req-transport-audit")
	require.NoError(t, err)
	assert.Equal(t, requestJSON, string(audit.RequestBody))
	assert.Equal(t, responseSSE, string(audit.ResponseBody))
	assert.Equal(t, int64(len(requestJSON)), audit.RequestBodySize)
	assert.Equal(t, int64(len(responseSSE)), audit.ResponseBodySize)
	assert.Equal(t, http.StatusOK, audit.ResponseStatus)
	assert.Equal(t, "text/event-stream", audit.ResponseContentType)
	assert.True(t, audit.ResponseComplete)
}

func TestBodyAuditCapturesNonStreamingJSONResponse(t *testing.T) {
	previousDB := model.DB
	previousEnabled := constant.BodyAuditEnabled
	previousMaxBodyMB := constant.BodyAuditMaxBodyMB
	t.Cleanup(func() {
		model.DB = previousDB
		constant.BodyAuditEnabled = previousEnabled
		constant.BodyAuditMaxBodyMB = previousMaxBodyMB
	})

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	migrateBodyAuditTestModels(t, db)
	model.DB = db
	constant.BodyAuditEnabled = true
	constant.BodyAuditMaxBodyMB = 1

	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Set(common.RequestIdKey, "req-nonstream-audit")
	context.Set("id", 12)
	context.Set("channel_id", 34)

	requestJSON := `{"model":"mapped-model","stream":false,"messages":[{"role":"user","content":"hello"}]}`
	request, err := http.NewRequest(http.MethodPost, "https://upstream.example/v1/chat/completions", strings.NewReader(requestJSON))
	require.NoError(t, err)
	capture := BeginBodyAudit(context, request, &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "mapped-model"},
	})
	require.NotNil(t, capture)
	sentBody, err := io.ReadAll(request.Body)
	require.NoError(t, err)
	assert.Equal(t, requestJSON, string(sentBody))

	responseJSON := `{"id":"chatcmpl-nonstream","choices":[{"message":{"role":"assistant","content":"complete response"},"finish_reason":"stop"}]}`
	response := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json; charset=utf-8"}},
		Body:       io.NopCloser(strings.NewReader(responseJSON)),
	}
	WrapBodyAuditResponse(capture, response)
	receivedBody, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	assert.Equal(t, responseJSON, string(receivedBody))

	audit, err := model.GetBodyAuditByRequestId("req-nonstream-audit")
	require.NoError(t, err)
	assert.Equal(t, requestJSON, string(audit.RequestBody))
	assert.Equal(t, responseJSON, string(audit.ResponseBody))
	assert.Equal(t, int64(len(responseJSON)), audit.ResponseBodySize)
	assert.Equal(t, http.StatusOK, audit.ResponseStatus)
	assert.Equal(t, "application/json; charset=utf-8", audit.ResponseContentType)
	assert.True(t, audit.ResponseComplete)
}

func TestBodyAuditCapturesConvertedClientResponseSeparately(t *testing.T) {
	previousDB := model.DB
	previousEnabled := constant.BodyAuditEnabled
	previousMaxBodyMB := constant.BodyAuditMaxBodyMB
	t.Cleanup(func() {
		model.DB = previousDB
		constant.BodyAuditEnabled = previousEnabled
		constant.BodyAuditMaxBodyMB = previousMaxBodyMB
	})

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	migrateBodyAuditTestModels(t, db)
	model.DB = db
	constant.BodyAuditEnabled = true
	constant.BodyAuditMaxBodyMB = 1

	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Set(common.RequestIdKey, "req-client-response-audit")
	context.Set("id", 12)
	context.Set("channel_id", 34)
	BeginBodyAuditClientResponse(context)

	request, err := http.NewRequest(http.MethodPost, "https://upstream.example/v1/chat/completions", strings.NewReader(`{"model":"mapped-model"}`))
	require.NoError(t, err)
	capture := BeginBodyAudit(context, request, &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "mapped-model"},
	})
	require.NotNil(t, capture)
	_, err = io.ReadAll(request.Body)
	require.NoError(t, err)

	upstreamJSON := `{"data":{"choices":[{"message":{"content":"provider text"}}]},"success":true}`
	response := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(upstreamJSON)),
	}
	WrapBodyAuditResponse(capture, response)
	_, err = io.ReadAll(response.Body)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())

	clientJSON := `{"choices":[{"message":{"role":"assistant","content":"provider text"}}]}`
	context.Header("Content-Type", "application/json; charset=utf-8")
	context.Status(http.StatusOK)
	_, err = context.Writer.Write([]byte(clientJSON))
	require.NoError(t, err)
	FinalizeBodyAuditClientResponse(context)

	audit, err := model.GetBodyAuditByRequestId("req-client-response-audit")
	require.NoError(t, err)
	assert.Equal(t, upstreamJSON, string(audit.ResponseBody))
	assert.Equal(t, clientJSON, string(audit.ClientResponseBody))
	assert.Equal(t, int64(len(clientJSON)), audit.ClientResponseBodySize)
	assert.Equal(t, http.StatusOK, audit.ClientResponseStatus)
	assert.Equal(t, "application/json; charset=utf-8", audit.ClientResponseContentType)
	assert.True(t, audit.ClientResponseComplete)
}

func TestBodyAuditCapturesStreamingClientWritesWithoutBlockingFlush(t *testing.T) {
	previousDB := model.DB
	previousEnabled := constant.BodyAuditEnabled
	previousMaxBodyMB := constant.BodyAuditMaxBodyMB
	t.Cleanup(func() {
		model.DB = previousDB
		constant.BodyAuditEnabled = previousEnabled
		constant.BodyAuditMaxBodyMB = previousMaxBodyMB
	})

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	migrateBodyAuditTestModels(t, db)
	model.DB = db
	constant.BodyAuditEnabled = true
	constant.BodyAuditMaxBodyMB = 1

	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Set(common.RequestIdKey, "req-client-stream-audit")
	context.Set("id", 12)
	context.Set("channel_id", 34)
	BeginBodyAuditClientResponse(context)

	request, err := http.NewRequest(http.MethodPost, "https://upstream.example/v1/chat/completions", strings.NewReader(`{"stream":true}`))
	require.NoError(t, err)
	capture := BeginBodyAudit(context, request, &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "mapped-model"},
	})
	require.NotNil(t, capture)
	_, err = io.ReadAll(request.Body)
	require.NoError(t, err)

	upstreamSSE := "data: {\"provider\":\"chunk\"}\n\ndata: [DONE]\n\n"
	response := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(upstreamSSE)),
	}
	WrapBodyAuditResponse(capture, response)
	_, err = io.ReadAll(response.Body)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())

	clientChunks := []string{
		"data: {\"choices\":[{\"delta\":{\"content\":\"hel\"}}]}\n\n",
		"data: {\"choices\":[{\"delta\":{\"content\":\"lo\"}}]}\n\n",
		"data: [DONE]\n\n",
	}
	context.Header("Content-Type", "text/event-stream")
	for _, chunk := range clientChunks {
		_, err = context.Writer.WriteString(chunk)
		require.NoError(t, err)
		context.Writer.Flush()
	}
	FinalizeBodyAuditClientResponse(context)

	audit, err := model.GetBodyAuditByRequestId("req-client-stream-audit")
	require.NoError(t, err)
	assert.Equal(t, strings.Join(clientChunks, ""), string(audit.ClientResponseBody))
	assert.True(t, recorder.Flushed)
	assert.True(t, audit.ClientResponseComplete)
}

func TestBodyAuditMarksTerminatedSSECompleteWhenConsumerClosesBeforeEOF(t *testing.T) {
	previousDB := model.DB
	previousEnabled := constant.BodyAuditEnabled
	previousMaxBodyMB := constant.BodyAuditMaxBodyMB
	t.Cleanup(func() {
		model.DB = previousDB
		constant.BodyAuditEnabled = previousEnabled
		constant.BodyAuditMaxBodyMB = previousMaxBodyMB
	})

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	migrateBodyAuditTestModels(t, db)
	model.DB = db
	constant.BodyAuditEnabled = true
	constant.BodyAuditMaxBodyMB = 1

	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Set(common.RequestIdKey, "req-stream-close-audit")
	context.Set("id", 12)
	context.Set("channel_id", 34)

	request, err := http.NewRequest(http.MethodPost, "https://upstream.example/v1/chat/completions", strings.NewReader(`{"stream":true}`))
	require.NoError(t, err)
	capture := BeginBodyAudit(context, request, &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "mapped-model"},
	})
	require.NotNil(t, capture)
	_, err = io.ReadAll(request.Body)
	require.NoError(t, err)

	responseSSE := "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n"
	response := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       &singleChunkReadCloser{data: []byte(responseSSE)},
	}
	WrapBodyAuditResponse(capture, response)
	buffer := make([]byte, len(responseSSE)+16)
	n, err := response.Body.Read(buffer)
	require.NoError(t, err)
	assert.Equal(t, responseSSE, string(buffer[:n]))
	require.NoError(t, response.Body.Close())

	audit, err := model.GetBodyAuditByRequestId("req-stream-close-audit")
	require.NoError(t, err)
	assert.Equal(t, responseSSE, string(audit.ResponseBody))
	assert.True(t, audit.ResponseComplete)
}

func TestBodyAuditDoesNotTreatEOFAsStreamingProtocolCompletion(t *testing.T) {
	previousDB := model.DB
	previousEnabled := constant.BodyAuditEnabled
	previousMaxBodyMB := constant.BodyAuditMaxBodyMB
	t.Cleanup(func() {
		model.DB = previousDB
		constant.BodyAuditEnabled = previousEnabled
		constant.BodyAuditMaxBodyMB = previousMaxBodyMB
	})

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	migrateBodyAuditTestModels(t, db)
	model.DB = db
	constant.BodyAuditEnabled = true
	constant.BodyAuditMaxBodyMB = 1

	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Set(common.RequestIdKey, "req-stream-missing-terminal")
	context.Set("id", 12)
	context.Set("channel_id", 34)
	request, err := http.NewRequest(http.MethodPost, "https://upstream.example/v1/chat/completions", strings.NewReader(`{"stream":true}`))
	require.NoError(t, err)
	capture := BeginBodyAudit(context, request, &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{ChannelId: 34, UpstreamModelName: "mapped-model"},
	})
	require.NotNil(t, capture)
	_, err = io.ReadAll(request.Body)
	require.NoError(t, err)

	response := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader("data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n")),
	}
	WrapBodyAuditResponse(capture, response)
	_, err = io.ReadAll(response.Body)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())

	audit, err := model.GetBodyAuditByRequestId("req-stream-missing-terminal")
	require.NoError(t, err)
	assert.False(t, audit.ResponseComplete)
	trace, err := model.GetAuditTraceByRequestId("req-stream-missing-terminal")
	require.NoError(t, err)
	attempts, err := model.ListAuditAttempts(trace.Id)
	require.NoError(t, err)
	require.Len(t, attempts, 1)
	assert.Equal(t, "failed", attempts[0].State)
	assert.Equal(t, "incomplete", attempts[0].TerminalKind)
}

func TestBodyAuditTracePreservesFailedAndSuccessfulAttempts(t *testing.T) {
	previousDB := model.DB
	previousEnabled := constant.BodyAuditEnabled
	previousMaxBodyMB := constant.BodyAuditMaxBodyMB
	t.Cleanup(func() {
		model.DB = previousDB
		constant.BodyAuditEnabled = previousEnabled
		constant.BodyAuditMaxBodyMB = previousMaxBodyMB
	})

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	migrateBodyAuditTestModels(t, db)
	model.DB = db
	constant.BodyAuditEnabled = true
	constant.BodyAuditMaxBodyMB = 1

	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Set(common.RequestIdKey, "req-two-attempts")
	context.Set("id", 12)
	BeginBodyAuditClientResponse(context)

	runAttempt := func(channelId, retryIndex, status int, requestBody, responseBody, mediaType string) {
		context.Set("channel_id", channelId)
		request, requestErr := http.NewRequest(http.MethodPost, "https://upstream.example/v1/chat/completions", strings.NewReader(requestBody))
		require.NoError(t, requestErr)
		capture := BeginBodyAudit(context, request, &relaycommon.RelayInfo{
			RetryIndex: retryIndex,
			ChannelMeta: &relaycommon.ChannelMeta{
				ChannelId: channelId, UpstreamModelName: "mapped-model",
			},
		})
		require.NotNil(t, capture)
		_, requestErr = io.ReadAll(request.Body)
		require.NoError(t, requestErr)
		response := &http.Response{
			StatusCode: status,
			Header:     http.Header{"Content-Type": []string{mediaType}},
			Body:       io.NopCloser(strings.NewReader(responseBody)),
		}
		WrapBodyAuditResponse(capture, response)
		_, requestErr = io.ReadAll(response.Body)
		require.NoError(t, requestErr)
		require.NoError(t, response.Body.Close())
	}

	runAttempt(3, 0, http.StatusInternalServerError, `{"attempt":0}`, `{"error":"temporary"}`, "application/json")
	runAttempt(8, 1, http.StatusOK, `{"attempt":1}`, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n", "text/event-stream")
	_, err = context.Writer.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	require.NoError(t, err)
	FinalizeBodyAuditClientResponse(context)

	trace, err := model.GetAuditTraceByRequestId("req-two-attempts")
	require.NoError(t, err)
	attempts, err := model.ListAuditAttempts(trace.Id)
	require.NoError(t, err)
	require.Len(t, attempts, 2)
	assert.Equal(t, 0, attempts[0].AttemptNo)
	assert.Equal(t, 3, attempts[0].ChannelId)
	assert.Equal(t, "failed", attempts[0].State)
	assert.Equal(t, 1, attempts[1].AttemptNo)
	assert.Equal(t, 8, attempts[1].ChannelId)
	assert.Equal(t, "succeeded", attempts[1].State)
	assert.Equal(t, attempts[1].Id, trace.FinalAttemptId)
	assert.NotZero(t, trace.ClientResponseBlobId)
}

func TestBodyAuditCapturesOriginalClientRequestOnceFromIndependentStorageReader(t *testing.T) {
	previousDB := model.DB
	previousEnabled := constant.BodyAuditEnabled
	previousMaxBodyMB := constant.BodyAuditMaxBodyMB
	t.Cleanup(func() {
		model.DB = previousDB
		constant.BodyAuditEnabled = previousEnabled
		constant.BodyAuditMaxBodyMB = previousMaxBodyMB
	})
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	migrateBodyAuditTestModels(t, db)
	model.DB = db
	constant.BodyAuditEnabled = true
	constant.BodyAuditMaxBodyMB = 1

	originalBody := `{"model":"client-model","messages":[{"role":"user","content":"original"}]}`
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Request = httptest.NewRequest(http.MethodPost, "/v1/messages?key=HISTORICAL&alt=sse", strings.NewReader(originalBody))
	context.Request.Header.Set("Content-Type", "application/json")
	context.Set(common.RequestIdKey, "req-original-client")
	context.Set("id", 12)
	context.Set("channel_id", 34)
	storage, err := common.GetBodyStorage(context)
	require.NoError(t, err)
	t.Cleanup(func() { _ = storage.Close() })

	beginAttempt := func(upstreamBody string) {
		upstreamRequest, requestErr := http.NewRequest(http.MethodPut, "https://upstream.example/internal", strings.NewReader(upstreamBody))
		require.NoError(t, requestErr)
		capture := BeginBodyAudit(context, upstreamRequest, &relaycommon.RelayInfo{
			TokenId: 91, OriginModelName: "client-model",
			ChannelMeta: &relaycommon.ChannelMeta{ChannelId: 34, UpstreamModelName: "mapped-model"},
		})
		require.NotNil(t, capture)
	}
	beginAttempt(`{"attempt":0}`)
	trace, err := model.GetAuditTraceByRequestId("req-original-client")
	require.NoError(t, err)
	firstBlobId := trace.OriginalRequestBlobId
	require.NotZero(t, firstBlobId)
	assert.Equal(t, http.MethodPost, trace.Method)
	assert.Equal(t, "/v1/messages?key=HISTORICAL&alt=sse", trace.Route)
	blob, err := model.GetAuditBlob(firstBlobId)
	require.NoError(t, err)
	assert.Equal(t, originalBody, string(blob.Body))
	assert.True(t, blob.Complete)
	assert.False(t, blob.Truncated)
	assert.Equal(t, "client_request", blob.CaptureStage)

	// A concurrent caller may hold a stale trace that still appears unlinked.
	// Its losing conditional assignment must not leave an orphan blob behind.
	captureOriginalClientRequest(context, &model.AuditTrace{Id: trace.Id})
	beginAttempt(`{"attempt":1}`)
	trace, err = model.GetAuditTraceByRequestId("req-original-client")
	require.NoError(t, err)
	assert.Equal(t, firstBlobId, trace.OriginalRequestBlobId)
	var originalBlobCount int64
	require.NoError(t, model.DB.Model(&model.AuditBlob{}).Where("capture_stage = ?", "client_request").Count(&originalBlobCount).Error)
	assert.Equal(t, int64(1), originalBlobCount)
}

func TestBodyAuditMarksOversizedOriginalClientRequestUnavailableForReplay(t *testing.T) {
	previousDB := model.DB
	previousEnabled := constant.BodyAuditEnabled
	previousMaxBodyMB := constant.BodyAuditMaxBodyMB
	t.Cleanup(func() {
		model.DB = previousDB
		constant.BodyAuditEnabled = previousEnabled
		constant.BodyAuditMaxBodyMB = previousMaxBodyMB
	})
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	migrateBodyAuditTestModels(t, db)
	model.DB = db
	constant.BodyAuditEnabled = true
	constant.BodyAuditMaxBodyMB = 1

	limit := int64(constant.BodyAuditMaxBodyMB) * 1024 * 1024
	originalBody := strings.Repeat("x", int(limit+64))
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(originalBody))
	context.Request.Header.Set("Content-Type", "application/json")
	context.Set(common.RequestIdKey, "req-original-truncated")
	storage, err := common.GetBodyStorage(context)
	require.NoError(t, err)
	t.Cleanup(func() { _ = storage.Close() })
	upstreamRequest, err := http.NewRequest(http.MethodPost, "https://upstream.example/v1/chat/completions", strings.NewReader(`{}`))
	require.NoError(t, err)
	require.NotNil(t, BeginBodyAudit(context, upstreamRequest, &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "model"},
	}))

	trace, err := model.GetAuditTraceByRequestId("req-original-truncated")
	require.NoError(t, err)
	blob, err := model.GetAuditBlob(trace.OriginalRequestBlobId)
	require.NoError(t, err)
	assert.Len(t, blob.Body, int(limit))
	assert.Equal(t, int64(len(originalBody)), blob.OriginalSize)
	assert.True(t, blob.Truncated)
	assert.False(t, blob.Complete)
}

func TestAttemptConfigSnapshotIsSecretFreeImmutableAndDeduplicated(t *testing.T) {
	previousDB := model.DB
	t.Cleanup(func() { model.DB = previousDB })
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	migrateBodyAuditTestModels(t, db)
	model.DB = db

	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Set(string(constant.ContextKeyChannelId), 42)
	context.Set(string(constant.ContextKeyChannelName), "private-upstream")
	baseInfo := func(prompt string) *relaycommon.RelayInfo {
		return &relaycommon.RelayInfo{
			OriginModelName: "client-model", RetryIndex: 2, TokenGroup: "vip",
			UserGroup: "default", UsingGroup: "vip", IsStream: true,
			ParamOverrideAudit: []string{"set_header Authorization = audit-secret", "set temperature = 0.8", "set max_tokens = 4096"},
			ChannelMeta: &relaycommon.ChannelMeta{
				ChannelId: 42, ChannelType: 1, ChannelBaseUrl: "https://user:pass@api.example.com/v1?key=query-secret",
				UpstreamModelName: "mapped-model", IsModelMapped: true,
				ApiKey: "channel-secret", Organization: "org-secret",
				ParamOverride: map[string]interface{}{
					"max_tokens": 4096,
					"operations": []interface{}{
						map[string]interface{}{"mode": "set", "path": "instructions", "value": prompt},
						map[string]interface{}{"mode": "set_header", "path": "Authorization", "value": "Bearer override-secret"},
						map[string]interface{}{"mode": "set", "path": "provider_access_token", "value": "operation-access-secret"},
						map[string]interface{}{"mode": "set", "path": "x-api-key", "value": "operation-api-secret"},
					},
					"api_key": "nested-secret",
					"legacy": map[string]interface{}{
						"credentials":           map[string]interface{}{"my_client_secret": "legacy-client-secret"},
						"provider_access_token": "legacy-access-secret",
						"x-api-key":             "legacy-api-secret",
					},
				},
				HeadersOverride: map[string]interface{}{"Authorization": "Bearer header-secret", "X-Debug": "header-value"},
			},
			RuntimeHeadersOverride: map[string]interface{}{"Cookie": "runtime-cookie"},
		}
	}

	firstPayload, err := buildAttemptConfigSnapshot(context, baseInfo("prompt-a"))
	require.NoError(t, err)
	first, err := model.GetOrCreateAuditConfigSnapshot("attempt", firstPayload)
	require.NoError(t, err)
	secondPayload, err := buildAttemptConfigSnapshot(context, baseInfo("prompt-a"))
	require.NoError(t, err)
	second, err := model.GetOrCreateAuditConfigSnapshot("attempt", secondPayload)
	require.NoError(t, err)
	changedPayload, err := buildAttemptConfigSnapshot(context, baseInfo("prompt-b"))
	require.NoError(t, err)
	changed, err := model.GetOrCreateAuditConfigSnapshot("attempt", changedPayload)
	require.NoError(t, err)

	assert.Equal(t, first.Id, second.Id)
	assert.Equal(t, first.Digest, second.Digest)
	assert.Equal(t, 2, first.SchemaVersion)
	assert.NotEqual(t, first.Digest, changed.Digest)
	canonical := string(first.CanonicalJson)
	for _, forbidden := range []string{
		"channel-secret", "org-secret", "query-secret", "override-secret", "nested-secret",
		"header-secret", "header-value", "runtime-cookie", "audit-secret", "user:pass",
		"operation-access-secret", "operation-api-secret", "legacy-client-secret",
		"legacy-access-secret", "legacy-api-secret",
	} {
		assert.NotContains(t, canonical, forbidden)
	}
	assert.Contains(t, canonical, `"base_origin":"https://api.example.com"`)
	assert.Contains(t, canonical, `"header_names":["Authorization","Cookie","X-Debug"]`)
	assert.Contains(t, canonical, `"value":"[REDACTED]"`)
	assert.Contains(t, canonical, `"max_tokens":4096`)
	assert.Contains(t, canonical, `"set max_tokens = 4096"`)
	assert.True(t, json.Valid(first.CanonicalJson))
}
