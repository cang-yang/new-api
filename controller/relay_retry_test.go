package controller

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
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
	assert.Equal(t, presetRequestRetrySettings(preset, "m"), presetRequestRetrySettings(&other, "m"))
	assert.True(t, preset.EnableEmbeddedRegex)
	other.User = "different"
	assert.NotEqual(t, presetRequestRetrySettings(preset, "m"), presetRequestRetrySettings(&other, "m"))
	other.Models = []string{"another-model"}
	assert.Equal(t, presetRequestRetrySettings(nil, "m"), presetRequestRetrySettings(&other, "m"), "inactive presets must not interfere with ordinary failover")
}

func TestSelectedChannelResponseRegexUsesContextSettings(t *testing.T) {
	filter := &dto.ResponseTextFilter{Mode: "rules", Rules: []dto.TextRegexRule{
		{ID: "remove-wrapper", Stage: "receive", Action: "replace", Pattern: `/<\/?Interleaving\s*>/gi`},
		{ID: "remove-thinking", Stage: "receive", Action: "replace", Pattern: `/<thinking>[\s\S]*?<\/thinking>/g`},
		{ID: "remove-inner-tag", Stage: "receive", Action: "replace", Pattern: `/<(?!\/?(?:think|thinking)\b)([^<>]*?)>(?=(?:(?!<(?:think|thinking)\b)[\s\S])*?<\/(?:think|thinking)>)/gi`, Replacement: "$1"},
	}}
	for _, streamed := range []bool{false, true} {
		name := "json"
		if streamed {
			name = "stream"
		}
		t.Run(name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			common.SetContextKey(c, constant.ContextKeyChannelOtherSetting, dto.ChannelOtherSettings{ResponseTextFilter: filter})
			writer := beginSelectedResponseTextFilter(c, "model")
			require.NotNil(t, writer, "the initial routing stub has no settings; the selected context must supply them")
			if streamed {
				c.Writer.Header().Set("Content-Type", "text/event-stream")
				_, err := c.Writer.WriteString("data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"<Interleaving><thinking>hidden\"}}]}\n\n" +
					"data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"</thinking>body</Interleaving><state:+1>\"}}]}\n\n" +
					"data: [DONE]\n\n")
				require.NoError(t, err)
			} else {
				_, err := c.Writer.WriteString(`{"choices":[{"message":{"content":"<Interleaving><thinking>hidden</thinking>body</Interleaving><state:+1>","reasoning":"keep"}}]}`)
				require.NoError(t, err)
			}
			require.NoError(t, writer.Finish(c, true))
			content := ""
			if streamed {
				for line := range strings.SplitSeq(recorder.Body.String(), "\n") {
					if data, ok := strings.CutPrefix(line, "data: {"); ok {
						content += gjson.Get("{"+data, "choices.0.delta.content").String()
					}
				}
			} else {
				content = gjson.Get(recorder.Body.String(), "choices.0.message.content").String()
				assert.Equal(t, "keep", gjson.Get(recorder.Body.String(), "choices.0.message.reasoning").String())
			}
			assert.Equal(t, "body<state:+1>", content)
		})
	}

	preset := &dto.SillyTavernPresetConfig{EnableEmbeddedRegex: true, Preset: []byte(`{"prompts":[{"identifier":"main","content":"hi"}],"prompt_order":[{"character_id":100001,"order":[{"identifier":"main","enabled":true}]}],"extensions":{"regex_scripts":[{"id":"receiver","findRegex":"/<thinking>[\\s\\S]*?<\\/thinking>/g","replaceString":"","placement":[2],"markdownOnly":true}]}}`)}
	presetRecorder := httptest.NewRecorder()
	presetContext, _ := gin.CreateTestContext(presetRecorder)
	common.SetContextKey(presetContext, constant.ContextKeyChannelOtherSetting, dto.ChannelOtherSettings{SillyTavernPreset: preset})
	presetWriter := beginSelectedResponseTextFilter(presetContext, "model")
	require.NotNil(t, presetWriter)
	_, err := presetContext.Writer.WriteString(`{"choices":[{"message":{"content":"<thinking>hidden</thinking>body"}}]}`)
	require.NoError(t, err)
	require.NoError(t, presetWriter.Finish(presetContext, true))
	assert.Equal(t, "body", gjson.Get(presetRecorder.Body.String(), "choices.0.message.content").String())

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	common.SetContextKey(c, constant.ContextKeyChannelOtherSetting, dto.ChannelOtherSettings{})
	assert.Nil(t, beginSelectedResponseTextFilter(c, "model"), "ordinary channels must not buffer or transform responses")
	_, err = c.Writer.WriteString("data: passthrough\n\n")
	require.NoError(t, err)
	assert.Equal(t, "data: passthrough\n\n", recorder.Body.String())
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

func TestSillyTavernInactivePresetPreservesGatewayRequest(t *testing.T) {
	for _, name := range []string{"no preset", "different model"} {
		t.Run(name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader("opaque passthrough body"))
			settings := dto.ChannelOtherSettings{}
			if name == "different model" {
				settings.SillyTavernPreset = &dto.SillyTavernPresetConfig{Models: []string{"other"}, Preset: []byte("invalid inactive preset")}
			}
			common.SetContextKey(c, constant.ContextKeyChannelOtherSetting, settings)
			common.SetContextKey(c, constant.ContextKeyChannelSetting, dto.ChannelSettings{PassThroughBodyEnabled: true})
			request := &dto.GeneralOpenAIRequest{Model: "ordinary", Messages: []dto.Message{
				{Role: "system", Content: "{{user}} must remain literal"},
				{Role: "user", Content: []any{map[string]any{"type": "text", "text": "hello"}, map[string]any{"type": "image_url", "image_url": map[string]any{"url": "https://example.com/image.png"}}}},
			}}
			before, err := common.Marshal(request)
			require.NoError(t, err)
			got, trace, err := compileSelectedSillyTavernPreset(c, request)
			require.NoError(t, err)
			assert.Same(t, request, got)
			assert.Nil(t, trace)
			after, err := common.Marshal(request)
			require.NoError(t, err)
			assert.Equal(t, before, after)
			body, err := io.ReadAll(c.Request.Body)
			require.NoError(t, err)
			assert.Equal(t, "opaque passthrough body", string(body))
			writer := c.Writer
			assert.Nil(t, service.BeginResponseTextFilterWithPreset(c, nil, settings.SillyTavernPreset, request.Model))
			assert.Same(t, writer, c.Writer)
			c.Writer.Header().Set("Content-Type", "text/event-stream")
			firstChunk := "data: {\"choices\":[{\"delta\":{\"content\":\"first\"}}]}\n\n"
			_, err = c.Writer.WriteString(firstChunk)
			require.NoError(t, err)
			c.Writer.Flush()
			assert.Equal(t, firstChunk, recorder.Body.String(), "first chunk must reach the client without waiting for completion")
		})
	}
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
