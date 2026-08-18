package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestGetBodyAuditReturnsClientResponseCapture(t *testing.T) {
	previousDB := model.DB
	t.Cleanup(func() { model.DB = previousDB })

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(
		&model.BodyAudit{}, &model.AuditTrace{}, &model.AuditAttempt{},
		&model.AuditWireSend{}, &model.AuditConfigSnapshot{}, &model.AuditBlob{},
	))
	model.DB = db

	require.NoError(t, model.UpsertBodyAudit(&model.BodyAudit{
		RequestId:                   "req-controller-audit",
		ResponseBody:                []byte(`{"provider":"raw"}`),
		ResponseBodySize:            18,
		ClientResponseBody:          []byte(`{"content":"converted"}`),
		ClientResponseBodySize:      23,
		ClientResponseStatus:        http.StatusOK,
		ClientResponseContentType:   "application/json",
		ClientResponseComplete:      true,
		ClientResponseBodyTruncated: false,
	}))
	trace, err := model.GetOrCreateAuditTrace(&model.AuditTrace{
		RequestId: "req-controller-audit", Source: "relay", Status: "succeeded",
	})
	require.NoError(t, err)
	requestBlob, err := model.CreateAuditBlob(&model.AuditBlob{CaptureStage: "upstream_request", Body: []byte(`{"attempt":0}`), Complete: true})
	require.NoError(t, err)
	responseBlob, err := model.CreateAuditBlob(&model.AuditBlob{CaptureStage: "upstream_response", Body: []byte(`{"provider":"raw"}`), Complete: true})
	require.NoError(t, err)
	snapshot, err := model.GetOrCreateAuditConfigSnapshot("attempt", []byte(`{"channel":{"id":9},"overrides":{"header_names":["Authorization"]}}`))
	require.NoError(t, err)
	attempt := &model.AuditAttempt{
		TraceId: trace.Id, AttemptNo: 0, ChannelId: 9, State: "succeeded", HTTPStatus: http.StatusOK,
		RequestBlobId: requestBlob.Id, ResponseBlobId: responseBlob.Id, Complete: true,
		ConfigSnapshotId: snapshot.Id,
	}
	require.NoError(t, model.CreateAuditAttempt(attempt))

	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Params = gin.Params{{Key: "request_id", Value: "req-controller-audit"}}

	GetBodyAudit(context)

	assert.Equal(t, http.StatusOK, recorder.Code)
	var payload struct {
		Success bool                   `json:"success"`
		Data    map[string]interface{} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &payload))
	assert.True(t, payload.Success)
	assert.Equal(t, `{"content":"converted"}`, payload.Data["client_response_body"])
	assert.Equal(t, "utf-8", payload.Data["client_response_body_encoding"])
	assert.Equal(t, float64(23), payload.Data["client_response_body_size"])
	assert.Equal(t, float64(http.StatusOK), payload.Data["client_response_status"])
	assert.Equal(t, "application/json", payload.Data["client_response_content_type"])
	assert.Equal(t, true, payload.Data["client_response_complete"])
	attempts, ok := payload.Data["attempts"].([]interface{})
	require.True(t, ok)
	require.Len(t, attempts, 1)
	firstAttempt, ok := attempts[0].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, float64(9), firstAttempt["channel_id"])
	assert.Equal(t, `{"attempt":0}`, firstAttempt["request_body"])
	assert.Equal(t, `{"provider":"raw"}`, firstAttempt["response_body"])
	configSnapshot, ok := firstAttempt["config_snapshot"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, snapshot.Digest, configSnapshot["digest"])
	canonical, ok := configSnapshot["canonical_json"].(map[string]interface{})
	require.True(t, ok)
	assert.Contains(t, canonical, "overrides")
}
