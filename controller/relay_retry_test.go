package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestShouldRetryNeverSwitchesChannelAfterDownstreamBodyStarted(t *testing.T) {
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	_, err := context.Writer.Write([]byte("data: partial\n\n"))
	require.NoError(t, err)
	apiErr := types.NewOpenAIError(assert.AnError, types.ErrorCodeBadResponse, http.StatusBadGateway)

	assert.False(t, shouldRetry(context, apiErr, 2))
}
