package controller

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/types"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupErrorIncidentControllerDB(t *testing.T) {
	t.Helper()
	previousDB := model.DB
	t.Cleanup(func() { model.DB = previousDB })
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.ErrorIncident{}))
	model.DB = db
}

func TestSetErrorIncidentResolvedReturnsNotFound(t *testing.T) {
	setupErrorIncidentControllerDB(t)
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPatch, "/", strings.NewReader(`{"resolved":true}`))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = gin.Params{{Key: "fingerprint", Value: "cef1_0123456789abcdef01234567"}}
	SetErrorIncidentResolved(c)
	assert.Equal(t, http.StatusNotFound, recorder.Code)
}

func TestProcessChannelErrorAggregatesWhenErrorLogsDisabled(t *testing.T) {
	setupErrorIncidentControllerDB(t)
	previousErrorLogEnabled := constant.ErrorLogEnabled
	constant.ErrorLogEnabled = false
	t.Cleanup(func() { constant.ErrorLogEnabled = previousErrorLogEnabled })

	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	c.Set(common.RequestIdKey, "req-no-error-log")
	c.Set("original_model", "demo")
	c.Set("channel_type", 1)
	c.Set("channel_id", 7)
	apiErr := types.NewOpenAIError(errors.New("upstream unavailable"), types.ErrorCodeBadResponseStatusCode, http.StatusBadGateway)
	processChannelError(c, types.ChannelError{ChannelId: 7, ChannelType: 1, AutoBan: false}, apiErr, nil)

	incidents, total, err := model.ListErrorIncidents(0, 20, false)
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	require.Len(t, incidents, 1)
	assert.Equal(t, "req-no-error-log", incidents[0].LastRequestId)
}
