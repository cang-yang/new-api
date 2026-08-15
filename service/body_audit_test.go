package service

import (
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
	require.NoError(t, db.AutoMigrate(&model.BodyAudit{}))
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
	require.NoError(t, db.AutoMigrate(&model.BodyAudit{}))
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
	require.NoError(t, db.AutoMigrate(&model.BodyAudit{}))
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
	require.NoError(t, db.AutoMigrate(&model.BodyAudit{}))
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
	require.NoError(t, db.AutoMigrate(&model.BodyAudit{}))
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
