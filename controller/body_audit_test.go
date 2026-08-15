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
	require.NoError(t, db.AutoMigrate(&model.BodyAudit{}))
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
}
