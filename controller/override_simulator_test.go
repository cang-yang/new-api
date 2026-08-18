package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSimulateParamOverrideReturnsTraceAndNeverReturnsSecrets(t *testing.T) {
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodPost, "/api/param-override/simulate", strings.NewReader(`{
		"upstream_request":{"model":"demo"},
		"param_override":{"operations":[
			{"mode":"set","path":"temperature","value":0.5},
			{"mode":"set_header","path":"Authorization","value":"Bearer should-not-leak"}
		]},
		"context":{"request_headers":{"X-Api-Key":"also-private"}}
	}`))
	context.Request.Header.Set("Content-Type", "application/json")

	SimulateParamOverride(context)

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.NotContains(t, recorder.Body.String(), "should-not-leak")
	assert.NotContains(t, recorder.Body.String(), "also-private")
	var payload struct {
		Success bool `json:"success"`
		Data    struct {
			After      map[string]interface{} `json:"after"`
			Operations []struct {
				Status string `json:"status"`
			} `json:"operations"`
			Headers map[string]interface{} `json:"headers"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &payload))
	assert.True(t, payload.Success)
	assert.Equal(t, 0.5, payload.Data.After["temperature"])
	require.Len(t, payload.Data.Operations, 2)
	assert.Equal(t, "applied", payload.Data.Operations[0].Status)
	assert.Equal(t, "[REDACTED]", payload.Data.Headers["authorization"])
}

func TestSimulateParamOverrideReturnsStructuredCompileDiagnostics(t *testing.T) {
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodPost, "/api/param-override/simulate", strings.NewReader(`{
		"upstream_request":{"model":"demo"},
		"param_override":{"operations":[{"mode":"set","path":"temperature"}]}
	}`))
	context.Request.Header.Set("Content-Type", "application/json")

	SimulateParamOverride(context)

	assert.Equal(t, http.StatusBadRequest, recorder.Code)
	var payload struct {
		Success bool `json:"success"`
		Data    struct {
			Diagnostics []struct {
				Code string `json:"code"`
			} `json:"diagnostics"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &payload))
	assert.False(t, payload.Success)
	require.Len(t, payload.Data.Diagnostics, 1)
	assert.Equal(t, "value_required", payload.Data.Diagnostics[0].Code)
}
