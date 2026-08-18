package router

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestAuditReplayRoutesRequireAdminAuthentication(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetApiRouter(engine)

	for _, requestPath := range []string{
		"/api/log/body-audit/req-1/replay/preview",
		"/api/log/body-audit/req-1/replay/execute",
	} {
		request := httptest.NewRequest(http.MethodPost, requestPath, strings.NewReader(`{}`))
		request.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, request)
		assert.Equal(t, http.StatusUnauthorized, recorder.Code, requestPath)
	}
}
