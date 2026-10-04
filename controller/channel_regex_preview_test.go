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

func TestChannelRegexPreviewFallbackKeepsArrayContract(t *testing.T) {
	body, err := common.Marshal(map[string]any{
		"stage": "receive", "role": "assistant", "text": "secret",
		"preset": map[string]any{
			"enable_embedded_regex": true, "regex_failure_policy": "passthrough",
			"preset": map[string]any{
				"prompts":      []any{map[string]any{"identifier": "main"}},
				"prompt_order": []any{map[string]any{"order": []any{}}},
				"extensions": map[string]any{"regex_scripts": []any{map[string]any{
					"id": "unsupported-macro", "placement": []int{2},
					"findRegex": "secret", "replaceString": "{{user}}",
				}}},
			},
		},
	})
	require.NoError(t, err)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodPost, "/api/channel/regex/preview", bytes.NewReader(body))
	PreviewChannelRegex(context)

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	var response struct {
		Success bool `json:"success"`
		Data    struct {
			Output     string   `json:"output"`
			RolledBack bool     `json:"rolled_back"`
			Steps      []any    `json:"steps"`
			Warnings   []string `json:"warnings"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	require.True(t, response.Success)
	require.Equal(t, "secret", response.Data.Output)
	require.True(t, response.Data.RolledBack)
	require.NotEmpty(t, response.Data.Warnings)
	// The browser renders steps with .map(), including an atomic fallback.
	// A null value would break that UI even though the fallback text is correct.
	require.NotNil(t, response.Data.Steps)
	require.Empty(t, response.Data.Steps)
}
