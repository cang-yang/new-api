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
		&model.Channel{}, &model.Token{}, &model.AuditTrace{}, &model.AuditAttempt{},
		&model.AuditBlob{}, &model.AuditConfigSnapshot{}, &model.AuditReplayGrant{},
	))
	model.DB = db
	service.InitHttpClient()
}

func TestClientLevelReplayReentersLoopbackWithCurrentTokenAndIsIdempotent(t *testing.T) {
	setupAuditReplayControllerTest(t)
	token := &model.Token{UserId: 12, Key: "CURRENT-CLIENT-TOKEN", Status: common.TokenStatusEnabled, Name: "replay", UnlimitedQuota: true}
	require.NoError(t, model.DB.Create(token).Error)
	sourceTrace := &model.AuditTrace{
		RequestId: "req-client-source", Source: "relay", Status: "succeeded", UserId: 12, TokenId: token.Id,
		Method: http.MethodPost, Route: "/v1/chat/completions?key=HISTORICAL&alt=sse", RequestModel: "demo",
	}
	require.NoError(t, model.CreateAuditTrace(sourceTrace))
	originalBlob, err := model.CreateAuditBlob(&model.AuditBlob{
		CaptureStage: "client_request", MediaType: "application/json", Complete: true,
		Body: []byte(`{"model":"demo","max_tokens":16,"messages":[{"role":"user","content":"replay me"}]}`),
	})
	require.NoError(t, err)
	assigned, err := model.SetAuditTraceOriginalRequestBlobIfEmpty(sourceTrace.Id, originalBlob.Id)
	require.NoError(t, err)
	require.True(t, assigned)

	callCount := 0
	loopback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		assert.Equal(t, "/v1/chat/completions", r.URL.Path)
		assert.Equal(t, "sse", r.URL.Query().Get("alt"))
		assert.Empty(t, r.URL.Query().Get("key"))
		assert.Equal(t, "Bearer sk-CURRENT-CLIENT-TOKEN", r.Header.Get("Authorization"))
		assert.Empty(t, r.Header.Get("X-Historical-Authorization"))
		body, readErr := io.ReadAll(r.Body)
		require.NoError(t, readErr)
		assert.Equal(t, string(originalBlob.Body), string(body))

		responseBlob, createErr := model.CreateAuditBlob(&model.AuditBlob{
			CaptureStage: "client_response", MediaType: "application/json", Complete: true, Body: []byte(`{"replayed":true}`),
		})
		require.NoError(t, createErr)
		generated := &model.AuditTrace{
			RequestId: "req-client-generated", Source: "relay", Status: "succeeded", UserId: token.UserId,
			TokenId: token.Id, Method: http.MethodPost, Route: r.URL.RequestURI(),
			ClientResponseBlobId: responseBlob.Id, ClientStatus: http.StatusOK,
		}
		require.NoError(t, model.CreateAuditTrace(generated))
		w.Header().Set(common.RequestIdKey, generated.RequestId)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(responseBlob.Body)
	}))
	defer loopback.Close()
	previousLoopback := auditReplayLoopbackBaseURL
	auditReplayLoopbackBaseURL = func() (string, error) { return loopback.URL, nil }
	t.Cleanup(func() { auditReplayLoopbackBaseURL = previousLoopback })

	preview := callAuditReplayHandler(t, PreviewAuditReplay, sourceTrace.RequestId, `{"mode":"client_level"}`)
	assert.Equal(t, true, preview["available"])
	assert.Equal(t, "/v1/chat/completions?alt=sse", preview["target"])
	differences, ok := preview["differences"].([]any)
	require.True(t, ok)
	var queryCredentialChange string
	for _, rawDifference := range differences {
		difference, isMap := rawDifference.(map[string]any)
		if isMap && difference["path"] == "request.query_credentials" {
			queryCredentialChange, _ = difference["change"].(string)
		}
	}
	assert.Equal(t, "historical_credential_query_parameters_removed", queryCredentialChange)
	risks, ok := preview["risks"].([]any)
	require.True(t, ok)
	assert.Contains(t, risks, "re_enters_current_routing_and_billing")
	confirmation := preview["confirmation_token"].(string)
	payload, err := common.Marshal(map[string]any{"confirmation_token": confirmation})
	require.NoError(t, err)
	first := callAuditReplayHandler(t, ExecuteAuditReplay, sourceTrace.RequestId, string(payload))
	second := callAuditReplayHandler(t, ExecuteAuditReplay, sourceTrace.RequestId, string(payload))

	assert.Equal(t, 1, callCount)
	assert.Equal(t, first["replay_trace_id"], second["replay_trace_id"])
	assert.Equal(t, true, second["idempotent_replay"])
	generated, err := model.GetAuditTraceByRequestId("req-client-generated")
	require.NoError(t, err)
	assert.Equal(t, sourceTrace.Id, generated.ReplayOfTraceId)
	assert.Equal(t, "replay_client_level", generated.Source)
}

func TestAuditReplayLoopbackClientIgnoresProxyAndBlocksNonLoopbackDial(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	loopback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer loopback.Close()

	client := newAuditReplayLoopbackHTTPClient()
	defer client.CloseIdleConnections()
	transport, ok := client.Transport.(*http.Transport)
	require.True(t, ok)
	assert.Nil(t, transport.Proxy)

	response, err := client.Get(loopback.URL)
	require.NoError(t, err)
	response.Body.Close()
	assert.Equal(t, http.StatusNoContent, response.StatusCode)

	_, err = client.Get("http://192.0.2.1/")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "blocked non-loopback")
}

func TestClientLevelPreviewRejectsMissingCurrentToken(t *testing.T) {
	setupAuditReplayControllerTest(t)
	trace := &model.AuditTrace{
		RequestId: "req-client-missing-token", Source: "relay", Status: "succeeded", TokenId: 999,
		Method: http.MethodPost, Route: "/v1/chat/completions",
	}
	require.NoError(t, model.CreateAuditTrace(trace))
	blob, err := model.CreateAuditBlob(&model.AuditBlob{
		CaptureStage: "client_request", MediaType: "application/json", Complete: true,
		Body: []byte(`{"model":"demo","messages":[]}`),
	})
	require.NoError(t, err)
	assigned, err := model.SetAuditTraceOriginalRequestBlobIfEmpty(trace.Id, blob.Id)
	require.NoError(t, err)
	require.True(t, assigned)

	data := callAuditReplayHandler(t, PreviewAuditReplay, trace.RequestId, `{"mode":"client_level"}`)
	assert.Equal(t, false, data["available"])
	assert.Equal(t, "current_token_not_found", data["unavailable_reason"])
	assert.Empty(t, data["confirmation_token"])
}

func TestClientLevelPreviewRejectsUnsafeHistoricalRequest(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		route  string
		body   string
		reason string
	}{
		{name: "non AI route", route: "/api/user/delete", body: `{"model":"demo"}`, reason: "non_ai_or_side_effecting_path"},
		{name: "credential in body", route: "/v1/chat/completions", body: `{"model":"demo","secret":"HISTORICAL"}`, reason: "historical_credentials_in_request_body"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			setupAuditReplayControllerTest(t)
			token := &model.Token{UserId: 12, Key: "CURRENT", Status: common.TokenStatusEnabled, UnlimitedQuota: true}
			require.NoError(t, model.DB.Create(token).Error)
			trace := &model.AuditTrace{
				RequestId: "req-client-unsafe-" + strings.ReplaceAll(testCase.name, " ", "-"),
				Source:    "relay", Status: "succeeded", UserId: token.UserId, TokenId: token.Id,
				Method: http.MethodPost, Route: testCase.route,
			}
			require.NoError(t, model.CreateAuditTrace(trace))
			blob, err := model.CreateAuditBlob(&model.AuditBlob{
				CaptureStage: "client_request", MediaType: "application/json", Complete: true, Body: []byte(testCase.body),
			})
			require.NoError(t, err)
			assigned, err := model.SetAuditTraceOriginalRequestBlobIfEmpty(trace.Id, blob.Id)
			require.NoError(t, err)
			require.True(t, assigned)

			data := callAuditReplayHandler(t, PreviewAuditReplay, trace.RequestId, `{"mode":"client_level"}`)
			assert.Equal(t, false, data["available"])
			assert.Equal(t, testCase.reason, data["unavailable_reason"])
			assert.Empty(t, data["confirmation_token"])
		})
	}
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
