package controller

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestPresetScriptEndpointScope(t *testing.T) {
	preset := &dto.SillyTavernPresetConfig{ScriptRuntime: "spreset-v1", Models: []string{"selected"}}
	require.ErrorContains(t, validatePresetScriptEndpoint(preset, "selected", relayconstant.RelayModeResponses), "chat completions")
	require.NoError(t, validatePresetScriptEndpoint(preset, "other", relayconstant.RelayModeResponses))
	require.NoError(t, validatePresetScriptEndpoint(preset, "selected", relayconstant.RelayModeChatCompletions))
	preset.ToolProtocol = &dto.SillyTavernToolProtocol{ClientFinal: "reply"}
	require.ErrorContains(t, validatePresetScriptEndpoint(preset, "selected", relayconstant.RelayModeResponses), "chat completions")
	preset.ScriptRuntime = ""
	require.NoError(t, validatePresetScriptEndpoint(preset, "selected", relayconstant.RelayModeResponses))
}

func TestAdaptationRetryFingerprintIgnoresReceiveOnlyDifferences(t *testing.T) {
	first := &dto.ResponseTextFilter{Mode: "rules", EnableSend: true, FailurePolicy: "passthrough", Rules: []dto.TextRegexRule{
		{ID: "send", Stage: "send", Action: "replace", Pattern: "a", Replacement: "b"},
		{ID: "receive", Stage: "receive", Action: "replace", Pattern: "x"},
	}}
	second := *first
	second.Rules = append([]dto.TextRegexRule(nil), first.Rules...)
	second.Rules[1].Pattern = "other"
	firstFingerprint, err := service.AdaptationRequestFingerprint(service.NewAdaptationProfile(nil, nil, first, first), "m")
	require.NoError(t, err)
	secondFingerprint, err := service.AdaptationRequestFingerprint(service.NewAdaptationProfile(nil, nil, &second, &second), "m")
	require.NoError(t, err)
	assert.Equal(t, firstFingerprint, secondFingerprint)
	second.Rules[0].Replacement = "different"
	secondFingerprint, err = service.AdaptationRequestFingerprint(service.NewAdaptationProfile(nil, nil, &second, &second), "m")
	require.NoError(t, err)
	assert.NotEqual(t, firstFingerprint, secondFingerprint)
	assert.Equal(t, "b", first.Rules[0].Replacement)

	preset := &dto.SillyTavernPresetConfig{Preset: []byte(`{"prompts":[]}`), EnableEmbeddedRegex: true}
	other := *preset
	other.EnableEmbeddedRegex = false
	other.RegexFailurePolicy = "passthrough"
	firstFingerprint, err = service.AdaptationRequestFingerprint(service.NewAdaptationProfile(preset, nil, nil, nil), "m")
	require.NoError(t, err)
	secondFingerprint, err = service.AdaptationRequestFingerprint(service.NewAdaptationProfile(&other, nil, nil, nil), "m")
	require.NoError(t, err)
	assert.Equal(t, firstFingerprint, secondFingerprint)
	assert.True(t, preset.EnableEmbeddedRegex)
	other.User = "different"
	secondFingerprint, err = service.AdaptationRequestFingerprint(service.NewAdaptationProfile(&other, nil, nil, nil), "m")
	require.NoError(t, err)
	assert.NotEqual(t, firstFingerprint, secondFingerprint)
	other.Models = []string{"another-model"}
	secondFingerprint, err = service.AdaptationRequestFingerprint(service.NewAdaptationProfile(&other, nil, nil, nil), "m")
	require.NoError(t, err)
	assert.Empty(t, secondFingerprint, "inactive presets must not interfere with ordinary failover")
	firstProtocol := *preset
	firstProtocol.ToolProtocol = &dto.SillyTavernToolProtocol{ClientFinal: "reply"}
	secondProtocol := firstProtocol
	secondProtocol.ToolProtocol = &dto.SillyTavernToolProtocol{ClientFinal: "other_reply"}
	firstFingerprint, err = service.AdaptationRequestFingerprint(service.NewAdaptationProfile(&firstProtocol, nil, nil, nil), "m")
	require.NoError(t, err)
	secondFingerprint, err = service.AdaptationRequestFingerprint(service.NewAdaptationProfile(&secondProtocol, nil, nil, nil), "m")
	require.NoError(t, err)
	assert.NotEqual(t, firstFingerprint, secondFingerprint)
}

func TestAdaptationRetryMismatchRejectsCrossChannelPlanChanges(t *testing.T) {
	base := dto.ChannelOtherSettings{
		SillyTavernPreset: &dto.SillyTavernPresetConfig{
			Preset: []byte(`{"prompts":[]}`),
			ToolProtocol: &dto.SillyTavernToolProtocol{
				Enabled: true,
			},
		},
	}
	request := &dto.GeneralOpenAIRequest{Model: "m", Messages: []dto.Message{{Role: "user", Content: "hello"}}}
	initialProfile := service.NewAdaptationProfile(base.SillyTavernPreset, base.Worldbook, base.ResponseTextFilter, base.ResponseTextFilter)
	initialPlan, err := service.BuildBasePlan(initialProfile, request, request)
	require.NoError(t, err)
	receiveOnly := base
	receiveOnly.ResponseTextFilter = &dto.ResponseTextFilter{Mode: "rules", Rules: []dto.TextRegexRule{
		{ID: "receive", Stage: "receive", Action: "replace", Pattern: "x", Replacement: "y"},
	}}
	assert.Empty(t, adaptationRetryMismatch(initialPlan, receiveOnly, "m", true), "receive-only rules must not block failover")

	changedProtocol := base
	changedProtocol.SillyTavernPreset = &dto.SillyTavernPresetConfig{
		Preset: []byte(`{"prompts":[]}`),
		ToolProtocol: &dto.SillyTavernToolProtocol{
			Enabled:     true,
			ClientFinal: "reply",
		},
	}
	assert.NotEmpty(t, adaptationRetryMismatch(initialPlan, changedProtocol, "m", true))

	changedWorldbook := base
	changedWorldbook.SillyTavernPreset = nil
	changedWorldbook.Worldbook = &dto.WorldbookConfig{Enabled: true, Books: []dto.Worldbook{{ID: "b", Enabled: true}}}
	assert.NotEmpty(t, adaptationRetryMismatch(initialPlan, changedWorldbook, "m", true))

	changedSend := base
	changedSend.SillyTavernPreset = nil
	changedSend.ResponseTextFilter = &dto.ResponseTextFilter{Mode: "rules", EnableSend: true, Rules: []dto.TextRegexRule{
		{ID: "send", Stage: "send", Action: "replace", Pattern: "a", Replacement: "b"},
	}}
	assert.NotEmpty(t, adaptationRetryMismatch(initialPlan, changedSend, "m", true))
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
	_, _, err := compileSelectedSillyTavernPreset(c, &dto.GeneralOpenAIRequest{Model: "test", Messages: []dto.Message{{Role: "user", Content: "hi"}}}, nil)
	require.ErrorContains(t, err, "disable body passthrough")
}

func TestWorldbookCompilationDoesNotMutateClientRequest(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader("{}"))
	config := &dto.WorldbookConfig{Enabled: true, Books: []dto.Worldbook{{ID: "b", Enabled: true, Entries: []dto.WorldbookEntry{{ID: "e", Enabled: true, Keys: []string{"moon"}, Content: "first lore"}}}}}
	settings := dto.ChannelOtherSettings{Worldbook: config, SillyTavernPreset: &dto.SillyTavernPresetConfig{Models: []string{"other"}, Preset: []byte("inactive")}}
	common.SetContextKey(c, constant.ContextKeyChannelOtherSetting, settings)
	source := &dto.GeneralOpenAIRequest{Model: "model", Messages: []dto.Message{{Role: "user", Content: "moon"}}}
	compiled, _, err := compileSelectedSillyTavernPreset(c, source, source)
	require.NoError(t, err)
	assert.Equal(t, "first lore", compiled.(*dto.GeneralOpenAIRequest).Messages[0].Content)
	compiled.(*dto.GeneralOpenAIRequest).Messages[0].Content = "mutated upstream"
	assert.Equal(t, "moon", source.Messages[0].Content)
	assert.Len(t, source.Messages, 1)
}

func TestWorldbookKeywordsUsePrePresetScanRequest(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader("opaque body"))
	config := &dto.WorldbookConfig{Enabled: true, Books: []dto.Worldbook{{ID: "b", Enabled: true, Entries: []dto.WorldbookEntry{{
		ID: "e", Enabled: true, Keys: []string{"moon"}, Content: "lore",
	}}}}}
	common.SetContextKey(c, constant.ContextKeyChannelOtherSetting, dto.ChannelOtherSettings{Worldbook: config})
	compiledRequest := &dto.GeneralOpenAIRequest{Model: "model", Messages: []dto.Message{{Role: "user", Content: "transformed text"}}}
	clientScanRequest := &dto.GeneralOpenAIRequest{Model: "model", Messages: []dto.Message{{Role: "user", Content: "moon"}}}

	compiled, trace, err := compileSelectedSillyTavernPreset(c, compiledRequest, clientScanRequest)
	require.NoError(t, err)
	require.NotNil(t, trace.Worldbook)
	assert.Equal(t, "included", trace.Worldbook.Entries[0].Reason)
	assert.Equal(t, "lore", compiled.(*dto.GeneralOpenAIRequest).Messages[0].Content)
}

func TestWorldbookFailureHonorsSkipPolicy(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader("{}"))
	config := &dto.WorldbookConfig{Enabled: true, Books: []dto.Worldbook{{
		ID: "b", Enabled: true, Entries: []dto.WorldbookEntry{{
			ID: "e", Enabled: true, Keys: []string{"/(?=moon)/"}, Content: "lore",
		}},
	}}}
	common.SetContextKey(c, constant.ContextKeyChannelOtherSetting, dto.ChannelOtherSettings{
		Worldbook: config,
		SillyTavernPreset: &dto.SillyTavernPresetConfig{
			Preset:        []byte(`{"prompts":[{"identifier":"main","content":"x"}],"prompt_order":[{"order":[{"identifier":"main","enabled":true}]}]}`),
			Models:        []string{"other"},
			FailurePolicy: &dto.SillyTavernFailurePolicy{Worldbook: "skip"},
		},
	})
	request := &dto.GeneralOpenAIRequest{Model: "model", Messages: []dto.Message{{Role: "user", Content: "moon"}}}

	compiled, trace, err := compileSelectedSillyTavernPreset(c, request, request)
	require.NoError(t, err)
	assert.Equal(t, request, compiled)
	assert.NotEmpty(t, trace.Warnings)
}

func TestPresetFailureHonorsRequestPassthroughPolicy(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader("{}"))
	common.SetContextKey(c, constant.ContextKeyChannelOtherSetting, dto.ChannelOtherSettings{
		SillyTavernPreset: &dto.SillyTavernPresetConfig{
			Preset: []byte(`{"prompts":[}`),
			FailurePolicy: &dto.SillyTavernFailurePolicy{
				Preset: "request_passthrough",
			},
		},
	})
	request := &dto.GeneralOpenAIRequest{Model: "model", Messages: []dto.Message{{Role: "user", Content: "hello"}}}

	compiled, trace, err := compileSelectedSillyTavernPreset(c, request, request)
	require.NoError(t, err)
	assert.Equal(t, request, compiled)
	assert.NotEmpty(t, trace.Warnings)
}

func TestWorldbookAPIContract(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/channel/worldbook/import", strings.NewReader(`{"name":"Lore","book":{"entries":[{"key":["moon"],"content":"lore"}]}}`))
	ImportChannelWorldbook(c)
	require.Equal(t, http.StatusOK, recorder.Code)
	assert.True(t, gjson.Get(recorder.Body.String(), "success").Bool())
	var book dto.Worldbook
	require.NoError(t, common.Unmarshal([]byte(gjson.Get(recorder.Body.String(), "data").Raw), &book))
	payload, err := common.Marshal(map[string]any{"config": dto.WorldbookConfig{Enabled: true, Books: []dto.Worldbook{book}}, "request": map[string]any{"messages": []dto.Message{{Role: "user", Content: "moon"}}}})
	require.NoError(t, err)
	recorder = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/channel/worldbook/preview", strings.NewReader(string(payload)))
	PreviewChannelWorldbook(c)
	require.Equal(t, http.StatusOK, recorder.Code)
	assert.True(t, gjson.Get(recorder.Body.String(), "success").Bool())
	assert.Equal(t, "lore", gjson.Get(recorder.Body.String(), "data.text").String())
	assert.Equal(t, "included", gjson.Get(recorder.Body.String(), "data.entries.0.status").String())
	assert.Equal(t, "included", gjson.Get(recorder.Body.String(), "data.entries.0.reason").String())
	assert.True(t, gjson.Get(recorder.Body.String(), "data.warnings").IsArray())
	assert.Equal(t, "system_prelude", gjson.Get(recorder.Body.String(), "data.placement.mode").String())
	assert.Equal(t, int64(0), gjson.Get(recorder.Body.String(), "data.placement.index").Int())
}

func TestSillyTavernCapabilityAPIContract(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/channel/sillytavern/inspect", strings.NewReader(`{
		"sillytavern_preset": {
			"preset": {
				"prompts": [{"identifier":"main","content":"hello"}],
				"prompt_order": [{"order":[{"identifier":"main","enabled":true}]}]
			},
			"tool_protocol": {"enabled": true}
		},
		"request": {"model":"m","messages":[{"role":"user","content":"probe"}]}
	}`))
	InspectChannelSillyTavernPreset(c)
	require.Equal(t, http.StatusOK, recorder.Code)
	assert.True(t, gjson.Get(recorder.Body.String(), "success").Bool())
	assert.Equal(t, "m", gjson.Get(recorder.Body.String(), "data.model").String())
	assert.True(t, gjson.Get(recorder.Body.String(), "data.entries.0.status").String() == "enabled")
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
			got, trace, err := compileSelectedSillyTavernPreset(c, request, request)
			require.NoError(t, err)
			assert.Same(t, request, got)
			assert.Empty(t, trace)
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
