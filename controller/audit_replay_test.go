package controller

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupAuditReplayControllerTest(t *testing.T) {
	t.Helper()
	previousDB := model.DB
	t.Cleanup(func() { model.DB = previousDB })
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(
		&model.Channel{}, &model.AuditTrace{}, &model.AuditAttempt{},
		&model.AuditBlob{}, &model.AuditConfigSnapshot{}, &model.AuditReplayGrant{},
	))
	model.DB = db
	service.InitHttpClient()
}

func callAuditReplayHandler(t *testing.T, handler gin.HandlerFunc, requestId, payload string) map[string]any {
	t.Helper()
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(payload))
	context.Request.Header.Set("Content-Type", "application/json")
	context.Params = gin.Params{{Key: "request_id", Value: requestId}}
	handler(context)
	require.Equal(t, http.StatusOK, recorder.Code)
	var envelope struct {
		Success bool           `json:"success"`
		Message string         `json:"message"`
		Data    map[string]any `json:"data"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &envelope))
	require.True(t, envelope.Success, envelope.Message)
	return envelope.Data
}

func TestAuditReplayClientModeClearlyUnavailableWithoutOriginalCapture(t *testing.T) {
	setupAuditReplayControllerTest(t)
	trace := &model.AuditTrace{RequestId: "req-no-client", Source: "relay", Status: "succeeded"}
	require.NoError(t, model.CreateAuditTrace(trace))

	data := callAuditReplayHandler(t, PreviewAuditReplay, trace.RequestId, `{"mode":"client_level"}`)
	assert.Equal(t, false, data["available"])
	assert.Equal(t, "original_client_request_not_captured", data["unavailable_reason"])
	assert.Empty(t, data["confirmation_token"])
}

func TestExactUpstreamReplayUsesCurrentChannelCredentialAndIsIdempotent(t *testing.T) {
	setupAuditReplayControllerTest(t)
	var callCount int
	var receivedAuthorization string
	var receivedBody string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		receivedAuthorization = r.Header.Get("Authorization")
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		receivedBody = string(body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	baseURL := upstream.URL
	channel := &model.Channel{
		Type: constant.ChannelTypeOpenAI, Name: "replay-current-channel",
		Key: "CURRENT-CHANNEL-KEY", BaseURL: &baseURL, Models: "demo", Group: "default",
	}
	require.NoError(t, model.DB.Create(channel).Error)
	trace := &model.AuditTrace{RequestId: "req-exact", Source: "relay", Status: "succeeded"}
	require.NoError(t, model.CreateAuditTrace(trace))
	requestBlob, err := model.CreateAuditBlob(&model.AuditBlob{
		CaptureStage: "upstream_request", MediaType: "application/json",
		Body: []byte(`{"model":"demo","max_tokens":64,"messages":[{"role":"user","content":"hello"}]}`), Complete: true,
	})
	require.NoError(t, err)
	attempt := &model.AuditAttempt{
		TraceId: trace.Id, AttemptNo: 0, ChannelId: channel.Id,
		ChannelType: channel.Type, UpstreamModel: "demo", Method: http.MethodPost,
		Target:               "https://old-provider.invalid/v1/chat/completions?api_key=HISTORICAL",
		RequestBlobId:        requestBlob.Id,
		State:                "succeeded",
		SanitizedHeadersJson: []byte(`{"Authorization":"Bearer HISTORICAL-KEY"}`),
	}
	require.NoError(t, model.CreateAuditAttempt(attempt))

	preview := callAuditReplayHandler(t, PreviewAuditReplay, trace.RequestId,
		`{"mode":"exact_upstream","attempt_id":`+strconv.FormatInt(attempt.Id, 10)+`}`)
	assert.Equal(t, true, preview["available"])
	assert.NotContains(t, preview["target"], "HISTORICAL")
	confirmation, ok := preview["confirmation_token"].(string)
	require.True(t, ok)
	require.NotEmpty(t, confirmation)
	var storedGrant model.AuditReplayGrant
	require.NoError(t, model.DB.First(&storedGrant).Error)
	assert.NotEqual(t, confirmation, storedGrant.TokenDigest)
	assert.Equal(t, confirmationDigest(confirmation), storedGrant.TokenDigest)

	payload, err := common.Marshal(map[string]any{"confirmation_token": confirmation})
	require.NoError(t, err)
	first := callAuditReplayHandler(t, ExecuteAuditReplay, trace.RequestId, string(payload))
	second := callAuditReplayHandler(t, ExecuteAuditReplay, trace.RequestId, string(payload))

	assert.Equal(t, 1, callCount, "reusing a confirmation must not send a second request")
	assert.Equal(t, "Bearer CURRENT-CHANNEL-KEY", receivedAuthorization)
	assert.Equal(t, string(requestBlob.Body), receivedBody)
	assert.Equal(t, float64(http.StatusOK), first["response_status"])
	assert.Equal(t, first["replay_trace_id"], second["replay_trace_id"])
	assert.Equal(t, true, second["idempotent_replay"])

	replayTraceId := int64(first["replay_trace_id"].(float64))
	var replayTrace model.AuditTrace
	require.NoError(t, model.DB.First(&replayTrace, replayTraceId).Error)
	assert.Equal(t, trace.Id, replayTrace.ReplayOfTraceId)
	assert.Equal(t, http.StatusOK, replayTrace.ClientStatus)
	assert.Equal(t, "succeeded", replayTrace.Outcome)
	assert.NotZero(t, replayTrace.PolicySnapshotId)
	var snapshot model.AuditConfigSnapshot
	require.NoError(t, model.DB.First(&snapshot, replayTrace.PolicySnapshotId).Error)
	assert.NotContains(t, string(snapshot.CanonicalJson), "CURRENT-CHANNEL-KEY")
	assert.NotContains(t, string(snapshot.CanonicalJson), "HISTORICAL-KEY")
}

func TestExactUpstreamReplayRejectsNonAIPathBeforeIssuingConfirmation(t *testing.T) {
	setupAuditReplayControllerTest(t)
	baseURL := "https://provider.example"
	channel := &model.Channel{Type: constant.ChannelTypeOpenAI, Name: "unsafe", Key: "key", BaseURL: &baseURL}
	require.NoError(t, model.DB.Create(channel).Error)
	trace := &model.AuditTrace{RequestId: "req-unsafe", Source: "relay", Status: "succeeded"}
	require.NoError(t, model.CreateAuditTrace(trace))
	blob, err := model.CreateAuditBlob(&model.AuditBlob{CaptureStage: "upstream_request", Body: []byte(`{}`), Complete: true})
	require.NoError(t, err)
	attempt := &model.AuditAttempt{TraceId: trace.Id, AttemptNo: 0, ChannelId: channel.Id, Method: http.MethodPost, Target: "https://old.example/admin/delete", RequestBlobId: blob.Id}
	require.NoError(t, model.CreateAuditAttempt(attempt))

	data := callAuditReplayHandler(t, PreviewAuditReplay, trace.RequestId,
		`{"mode":"exact_upstream","attempt_id":`+strconv.FormatInt(attempt.Id, 10)+`}`)
	assert.Equal(t, false, data["available"])
	assert.Equal(t, "non_ai_or_side_effecting_path", data["unavailable_reason"])
	assert.Empty(t, data["confirmation_token"])
}

func TestExactUpstreamReplayRejectsHistoricalCredentialInBodyWithoutSending(t *testing.T) {
	setupAuditReplayControllerTest(t)
	callCount := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		callCount++
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()
	baseURL := upstream.URL
	channel := &model.Channel{Type: constant.ChannelTypeOpenAI, Name: "credential-body", Key: "CURRENT", BaseURL: &baseURL}
	require.NoError(t, model.DB.Create(channel).Error)
	trace := &model.AuditTrace{RequestId: "req-credential-body", Source: "relay", Status: "succeeded"}
	require.NoError(t, model.CreateAuditTrace(trace))
	blob, err := model.CreateAuditBlob(&model.AuditBlob{
		CaptureStage: "upstream_request", MediaType: "application/json", Complete: true,
		Body: []byte(`{"model":"demo","metadata":{"access_token":"HISTORICAL"}}`),
	})
	require.NoError(t, err)
	attempt := &model.AuditAttempt{
		TraceId: trace.Id, AttemptNo: 0, ChannelId: channel.Id, ChannelType: channel.Type,
		Method: http.MethodPost, Target: "https://old.example/v1/chat/completions", RequestBlobId: blob.Id,
	}
	require.NoError(t, model.CreateAuditAttempt(attempt))

	data := callAuditReplayHandler(t, PreviewAuditReplay, trace.RequestId,
		`{"mode":"exact_upstream","attempt_id":`+strconv.FormatInt(attempt.Id, 10)+`}`)
	assert.Equal(t, false, data["available"])
	assert.Equal(t, "historical_credentials_in_request_body", data["unavailable_reason"])
	assert.Empty(t, data["confirmation_token"])
	assert.Zero(t, callCount)
}

func TestExecuteAuditReplayReturnsExplicitExpiredConfirmationError(t *testing.T) {
	setupAuditReplayControllerTest(t)
	trace := &model.AuditTrace{RequestId: "req-expired-confirmation", Source: "relay", Status: "succeeded"}
	require.NoError(t, model.CreateAuditTrace(trace))
	confirmation := "expired-confirmation"
	require.NoError(t, model.CreateAuditReplayGrant(&model.AuditReplayGrant{
		TokenDigest: confirmationDigest(confirmation), TraceId: trace.Id,
		Mode: auditReplayModeExactUpstream, State: "prepared", ExpiresAt: time.Now().Add(-time.Minute).UnixMilli(),
	}))

	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"confirmation_token":"`+confirmation+`"}`))
	context.Params = gin.Params{{Key: "request_id", Value: trace.RequestId}}
	ExecuteAuditReplay(context)

	var envelope struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &envelope))
	assert.False(t, envelope.Success)
	assert.Equal(t, "replay confirmation expired; request a new preview", envelope.Message)
}

func TestReadAuditReplayResponsePreservesObservedSizeWhenTruncated(t *testing.T) {
	stored, observedSize, truncated, err := readAuditReplayResponse(strings.NewReader("12345"), 4)
	require.NoError(t, err)
	assert.Equal(t, "1234", string(stored))
	assert.Equal(t, int64(5), observedSize)
	assert.True(t, truncated)
}
