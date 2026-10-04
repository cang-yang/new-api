package controller

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestChannelRegexPreviewAcceptsLargeSupportedPreset(t *testing.T) {
	body, err := common.Marshal(map[string]any{
		"stage": "receive", "role": "assistant", "text": "SAMPLE",
		"preset": map[string]any{"enable_embedded_regex": true, "preset": map[string]any{
			"prompts":      []any{map[string]any{"identifier": "main", "content": strings.Repeat("a", dto.MaxSillyTavernPresetBytes-1024)}},
			"prompt_order": []any{map[string]any{"order": []any{}}},
		}},
	})
	require.NoError(t, err)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodPost, "/api/channel/regex/preview", bytes.NewReader(body))
	PreviewChannelRegex(context)
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	require.Contains(t, recorder.Body.String(), `"output":"SAMPLE"`)
}

func TestChannelRegexPreviewRejectsOversizedBody(t *testing.T) {
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodPost, "/api/channel/regex/preview", strings.NewReader(`{"text":"`+strings.Repeat("a", 8<<20)+`"}`))
	PreviewChannelRegex(context)
	require.Equal(t, http.StatusBadRequest, recorder.Code)
}
