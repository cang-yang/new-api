package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSendRegexRetryIgnoresReceiveOnlyDifferences(t *testing.T) {
	first := &dto.ResponseTextFilter{Mode: "rules", EnableSend: true, FailurePolicy: "passthrough", Rules: []dto.TextRegexRule{
		{ID: "send", Stage: "send", Action: "replace", Pattern: "a", Replacement: "b"},
		{ID: "receive", Stage: "receive", Action: "replace", Pattern: "x"},
	}}
	second := *first
	second.Rules = append([]dto.TextRegexRule(nil), first.Rules...)
	second.Rules[1].Pattern = "other"
	assert.Equal(t, sendRegexRetrySettings(first), sendRegexRetrySettings(&second))
	second.Rules[0].Replacement = "different"
	assert.NotEqual(t, sendRegexRetrySettings(first), sendRegexRetrySettings(&second))
	assert.Equal(t, "b", first.Rules[0].Replacement)
	preset := &dto.SillyTavernPresetConfig{Preset: []byte(`{"prompts":[]}`), EnableEmbeddedRegex: true}
	other := *preset
	other.EnableEmbeddedRegex = false
	other.RegexFailurePolicy = "passthrough"
	assert.Equal(t, presetRequestRetrySettings(preset), presetRequestRetrySettings(&other))
	assert.True(t, preset.EnableEmbeddedRegex)
	other.User = "different"
	assert.NotEqual(t, presetRequestRetrySettings(preset), presetRequestRetrySettings(&other))
}

func TestSillyTavernRejectsRoutePassthrough(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"messages":[{"role":"user","content":"hi"}]}`))
	common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeAdvancedCustom)
	common.SetContextKey(c, constant.ContextKeyChannelOtherSetting, dto.ChannelOtherSettings{
		SillyTavernPreset: &dto.SillyTavernPresetConfig{Preset: []byte(`{"prompts":[{"identifier":"main","content":"Hi"}],"prompt_order":[{"order":[{"identifier":"main","enabled":true}]}]}`)},
		AdvancedCustom:    &dto.AdvancedCustomConfig{Routes: []dto.AdvancedCustomRoute{{IncomingPath: "/v1/chat/completions", PassThroughBodyEnabled: true}}},
	})
	_, _, err := compileSelectedSillyTavernPreset(c, &dto.GeneralOpenAIRequest{Model: "test", Messages: []dto.Message{{Role: "user", Content: "hi"}}})
	require.ErrorContains(t, err, "disable body passthrough")
}

func TestSendRegexRejectsRoutePassthrough(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeAdvancedCustom)
	common.SetContextKey(c, constant.ContextKeyChannelOtherSetting, dto.ChannelOtherSettings{
		ResponseTextFilter: &dto.ResponseTextFilter{Mode: "rules", EnableSend: true, Rules: []dto.TextRegexRule{{ID: "x", Stage: "send", Action: "replace", Pattern: "x", Replacement: "y"}}},
		AdvancedCustom:     &dto.AdvancedCustomConfig{Routes: []dto.AdvancedCustomRoute{{IncomingPath: "/v1/chat/completions", PassThroughBodyEnabled: true}}},
	})
	_, err := applySelectedRequestTextRegex(c, &dto.GeneralOpenAIRequest{Model: "m", Messages: []dto.Message{{Role: "user", Content: "x"}}}, "m")
	require.ErrorContains(t, err, "disable body passthrough")
}

func TestShouldRetryNeverSwitchesChannelAfterDownstreamBodyStarted(t *testing.T) {
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	_, err := context.Writer.Write([]byte("data: partial\n\n"))
	require.NoError(t, err)
	apiErr := types.NewOpenAIError(assert.AnError, types.ErrorCodeBadResponse, http.StatusBadGateway)

	assert.False(t, shouldRetry(context, apiErr, 2))
}
