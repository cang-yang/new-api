package router

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestErrorIncidentRoutesRequireAdminAuthentication(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetApiRouter(engine)
	for _, route := range []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/log/error-incidents"},
		{http.MethodGet, "/api/log/error-incidents/cef1_0123456789abcdef01234567"},
		{http.MethodPatch, "/api/log/error-incidents/cef1_0123456789abcdef01234567/resolved"},
	} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(route.method, route.path, nil)
		engine.ServeHTTP(recorder, request)
		assert.Equal(t, http.StatusUnauthorized, recorder.Code, route.path)
	}
}
