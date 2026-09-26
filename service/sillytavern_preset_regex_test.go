package service

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/dlclark/regexp2/v2"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestPresetRegexBoundsExpansion(t *testing.T) {
	for _, tc := range []struct{ name, pattern, input, replacement string }{
		{"global literal", "a", strings.Repeat("a", 9000), strings.Repeat("x", 1024)},
		{"single capture", "(.*)", strings.Repeat("x", 1<<20), strings.Repeat("$1", 9)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			re, err := regexp2.Compile(tc.pattern, regexp2.ECMAScript)
			require.NoError(t, err)
			rule := presetResponseRegex{pattern: re, global: true, replaceBy: tc.replacement}
			_, err = rule.replace(tc.input)
			require.Error(t, err, "expansion must stop before constructing an oversized result")
		})
	}
}

func TestPresetRegexJavaScriptLiteralAndCaptures(t *testing.T) {
	for _, tc := range []struct{ name, pattern, input, replacement, want string }{
		{"escaped backslash", `/\\/g`, `a\b\c`, "/", "a/b/c"},
		{"named capture order", `/(?<first>a)(b)/`, "ab", "$1:$2:$<first>", "a:b:a"},
		{"unicode unmatched suffix", `/(?=你)/g`, "你你好", "!", "!你!你好"},
		{"unicode flag escape", `/\u{1F600}/gu`, "😀 smile", "happy", "happy smile"},
		{"case insensitive match macro", `/a/`, "a", "{{MATCH}}!", "a!"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			preset := map[string]any{"prompts": []any{map[string]any{"identifier": "main"}}, "prompt_order": []any{map[string]any{"character_id": 100001}}, "extensions": map[string]any{"regex_scripts": []any{map[string]any{"id": "test", "placement": []int{2}, "findRegex": tc.pattern, "replaceString": tc.replacement}}}}
			raw, err := common.Marshal(preset)
			require.NoError(t, err)
			rules, warnings := compilePresetResponseRegex(&dto.SillyTavernPresetConfig{Preset: raw, EnableEmbeddedRegex: true}, "model")
			require.Empty(t, warnings)
			require.Len(t, rules, 1)
			got, err := rules[0].replace(tc.input)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestTextRegexActionsAndOrder(t *testing.T) {
	for _, tc := range []struct{ action, replacement, pattern, input, want string }{
		{"replace", "", `/<主体>.*?<\/主体>/gs`, "前<主体>你好</主体>后", "前后"},
		{"replace", "$1", `/<主体>(.*?)<\/主体>/s`, "前<主体>你好</主体>后", "前你好后"},
		{"extract", "$1", `/<主体>(.*?)<\/主体>/s`, "前<主体>你好</主体>后", "你好"},
		{"extract", "$<body>!", `/<主体>(?<body>.*?)<\/主体>/gs`, "<主体>一</主体>外<主体>二</主体>", "一!二!"},
		{"replace", "x", `/a/`, "aaa", "xaa"},
	} {
		cfg := &dto.ResponseTextFilter{Mode: "rules", Rules: []dto.TextRegexRule{{ID: "test", Stage: "receive", Action: tc.action, Pattern: tc.pattern, Replacement: tc.replacement}}}
		ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
		writer := BeginResponseTextFilter(ctx, cfg, "model")
		require.NotNil(t, writer)
		got, _ := writer.transform(tc.input)
		assert.Equal(t, tc.want, got)
	}
}

func TestSendTextRegexOptInScopesAndMetadata(t *testing.T) {
	config := &dto.ResponseTextFilter{Mode: "rules", Rules: []dto.TextRegexRule{
		{ID: "one", Stage: "send", Action: "replace", Pattern: `/secret/g`, Replacement: "clean", Roles: []string{"user"}},
		{ID: "two", Stage: "send", Action: "replace", Pattern: `/clean/g`, Replacement: "ready", Roles: []string{"user"}},
	}}
	request := &dto.GeneralOpenAIRequest{Model: "m", Messages: []dto.Message{{Role: "system", Content: "secret"}, {Role: "user", Content: "secret"}, {Role: "assistant", Content: "secret"}, {Role: "tool", Content: "secret"}}}
	disabled, err := ApplyRequestTextRegex(config, nil, request, "m")
	require.NoError(t, err)
	assert.Same(t, request, disabled)
	config.EnableSend = true
	filtered, err := ApplyRequestTextRegex(config, nil, request, "m")
	require.NoError(t, err)
	chat := filtered.(*dto.GeneralOpenAIRequest)
	assert.Equal(t, "ready", chat.Messages[1].Content)
	assert.Equal(t, "secret", request.Messages[1].Content)
	for _, i := range []int{0, 2, 3} {
		assert.Equal(t, "secret", chat.Messages[i].Content)
	}
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	assert.Nil(t, BeginResponseTextFilter(ctx, config, "m"), "send-only rules must not buffer streaming responses")
}

func TestSendTextRegexNativeProtocols(t *testing.T) {
	config := &dto.ResponseTextFilter{Mode: "rules", EnableSend: true, Rules: []dto.TextRegexRule{{ID: "x", Stage: "send", Action: "replace", Pattern: `/secret/g`, Replacement: "clean"}}}
	for _, tc := range []struct {
		request              dto.Request
		raw, path, protected string
	}{
		{&dto.ClaudeRequest{}, `{"model":"m","messages":[{"role":"user","content":[{"type":"text","text":"secret"},{"type":"tool_result","tool_use_id":"x","content":"secret"}]}]}`, "messages.0.content.0.text", "messages.0.content.1.content"},
		{&dto.GeminiChatRequest{}, `{"contents":[{"role":"model","parts":[{"text":"secret"},{"text":"secret","thought":true}]}]}`, "contents.0.parts.0.text", "contents.0.parts.1.text"},
		{&dto.OpenAIResponsesRequest{}, `{"model":"m","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"secret"}]},{"type":"function_call_output","call_id":"x","output":"secret"}]}`, "input.0.content.0.text", "input.1.output"},
	} {
		require.NoError(t, common.Unmarshal([]byte(tc.raw), tc.request))
		filtered, err := ApplyRequestTextRegex(config, nil, tc.request, "m")
		require.NoError(t, err)
		raw, err := common.Marshal(filtered)
		require.NoError(t, err)
		assert.Equal(t, "clean", gjson.GetBytes(raw, tc.path).String())
		assert.Equal(t, "secret", gjson.GetBytes(raw, tc.protected).String())
	}
}

func TestTextRegexMissingMatchDepthAndValidation(t *testing.T) {
	depth := 0
	cfg := &dto.ResponseTextFilter{Mode: "rules", EnableSend: true, Rules: []dto.TextRegexRule{{ID: "last", Stage: "send", Action: "extract", Pattern: `/<b>(.*?)<\/b>/s`, Replacement: "$1", MissingMatch: "empty", MaxDepth: &depth}}}
	request := &dto.GeneralOpenAIRequest{Model: "m", Messages: []dto.Message{{Role: "user", Content: "old"}, {Role: "user", Content: "unmatched"}}}
	filtered, err := ApplyRequestTextRegex(cfg, nil, request, "m")
	require.NoError(t, err)
	assert.Equal(t, "old", filtered.(*dto.GeneralOpenAIRequest).Messages[0].Content)
	assert.Equal(t, "", filtered.(*dto.GeneralOpenAIRequest).Messages[1].Content)
	cfg.Rules[0].MissingMatch = "passthrough"
	filtered, err = ApplyRequestTextRegex(cfg, nil, request, "m")
	require.NoError(t, err)
	assert.Equal(t, "unmatched", filtered.(*dto.GeneralOpenAIRequest).Messages[1].Content)
	for _, pattern := range []string{"/[a/", "/a/gg", "/a/z"} {
		cfg.Rules[0].Pattern = pattern
		require.Error(t, cfg.Validate())
	}
	cfg.Rules[0].Disabled = true
	require.NoError(t, cfg.Validate(), "disabled imported syntax must remain retainable without executing")
	filtered, err = ApplyRequestTextRegex(cfg, nil, request, "m")
	require.NoError(t, err)
	assert.Same(t, request, filtered)
}

func TestTextRegexPresetSendDirections(t *testing.T) {
	preset := &dto.SillyTavernPresetConfig{EnableSendRegex: true, EnableEmbeddedRegex: true, Preset: []byte(`{"prompts":[{"identifier":"main"}],"prompt_order":[{"order":[]}],"extensions":{"regex_scripts":[{"id":"send","placement":[1],"promptOnly":true,"findRegex":"/a/g","replaceString":"b"},{"id":"display","placement":[2],"markdownOnly":true,"findRegex":"/a/g","replaceString":"c"}]}}`)}
	request := &dto.GeneralOpenAIRequest{Model: "m", Messages: []dto.Message{{Role: "user", Content: "a"}, {Role: "assistant", Content: "a"}}}
	filtered, err := ApplyRequestTextRegex(nil, preset, request, "m")
	require.NoError(t, err)
	assert.Equal(t, "b", filtered.(*dto.GeneralOpenAIRequest).Messages[0].Content)
	assert.Equal(t, "a", filtered.(*dto.GeneralOpenAIRequest).Messages[1].Content)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	w := BeginResponseTextFilterWithPreset(ctx, nil, preset, "m")
	require.NotNil(t, w)
	got, _ := w.transform("a")
	assert.Equal(t, "c", got)
}

func TestTextRegexSSEReplaceAcrossChunksAndEmptyRules(t *testing.T) {
	cfg := &dto.ResponseTextFilter{Mode: "rules", Rules: []dto.TextRegexRule{{ID: "delete", Stage: "receive", Action: "replace", Pattern: `/<hide>.*?<\/hide>/gs`, Replacement: ""}}}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	w := BeginResponseTextFilter(ctx, cfg, "m")
	require.NotNil(t, w)
	ctx.Writer.Header().Set("Content-Type", "text/event-stream")
	_, err := w.WriteString("data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"A<hi\"}}]}\n\ndata: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"de>secret</hide>B\"}}]}\n\ndata: [DONE]\n\n")
	require.NoError(t, err)
	require.NoError(t, w.Finish(ctx, true))
	assert.Contains(t, recorder.Body.String(), `"content":"AB"`)
	assert.NotContains(t, recorder.Body.String(), "secret")
	cfg.Rules[0].Disabled = true
	ctx, _ = gin.CreateTestContext(httptest.NewRecorder())
	assert.Nil(t, BeginResponseTextFilter(ctx, cfg, "m"))
	cfg.Rules = nil
	assert.Nil(t, BeginResponseTextFilter(ctx, cfg, "m"))
}

func TestTextRegexExecutionFailureDoesNotPublishUnfilteredResponse(t *testing.T) {
	cfg := &dto.ResponseTextFilter{Mode: "rules", Rules: []dto.TextRegexRule{{ID: "expand", Stage: "receive", Action: "replace", Pattern: "/a/g", Replacement: strings.Repeat("x", 1024)}}}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	w := BeginResponseTextFilter(ctx, cfg, "m")
	require.NotNil(t, w)
	body := `{"choices":[{"message":{"content":"` + strings.Repeat("a", 9000) + `"}}]}`
	_, err := w.WriteString(body)
	require.NoError(t, err)
	require.Error(t, w.Finish(ctx, true))
	assert.Empty(t, recorder.Body.String())
}

func TestTextRegexFailurePolicyRetainsOriginalAtomically(t *testing.T) {
	for _, policy := range []string{"passthrough", "error", ""} {
		t.Run(policy, func(t *testing.T) {
			cfg := &dto.ResponseTextFilter{Mode: "rules", EnableSend: true, FailurePolicy: policy, Rules: []dto.TextRegexRule{
				{ID: "first", Stage: "receive", Action: "replace", Pattern: "/a/g", Replacement: "b"},
				{ID: "expand", Stage: "receive", Action: "replace", Pattern: "/b/g", Replacement: strings.Repeat("x", 1024)},
			}}
			body := `{"choices":[{"message":{"content":"` + strings.Repeat("a", 9000) + `"}}]}`
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			w := BeginResponseTextFilter(ctx, cfg, "m")
			require.NotNil(t, w)
			_, err := w.WriteString(body)
			require.NoError(t, err)
			err = w.Finish(ctx, true)
			if policy == "passthrough" {
				require.NoError(t, err)
				assert.Equal(t, body, recorder.Body.String())
			} else {
				require.Error(t, err)
				assert.Empty(t, recorder.Body.String())
			}
			for i := range cfg.Rules {
				cfg.Rules[i].Stage = "send"
			}
			req := &dto.GeneralOpenAIRequest{Messages: []dto.Message{{Role: "user", Content: strings.Repeat("a", 9000)}}}
			got, err := ApplyRequestTextRegex(cfg, nil, req, "m")
			if policy == "passthrough" {
				require.NoError(t, err)
				assert.Same(t, req, got)
			} else {
				require.Error(t, err)
			}
			assert.Equal(t, strings.Repeat("a", 9000), req.Messages[0].Content)
		})
	}
}

func TestTextRegexOverflowPolicyAndStrictSourcePrecedence(t *testing.T) {
	for _, policy := range []string{"passthrough", "error"} {
		cfg := &dto.ResponseTextFilter{Mode: "rules", FailurePolicy: policy, Rules: []dto.TextRegexRule{{ID: "r", Stage: "receive", Action: "replace", Pattern: "secret"}}}
		recorder := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(recorder)
		w := BeginResponseTextFilter(ctx, cfg, "m")
		require.NotNil(t, w)
		body := strings.Repeat("x", maxFilteredResponseBytes+1)
		_, err := w.WriteString(body)
		require.NoError(t, err)
		err = w.Finish(ctx, true)
		if policy == "passthrough" {
			require.NoError(t, err)
			assert.Equal(t, body, recorder.Body.String())
		} else {
			require.Error(t, err)
			assert.Empty(t, recorder.Body.String())
		}
	}
	preset := &dto.SillyTavernPresetConfig{EnableEmbeddedRegex: true, RegexFailurePolicy: "error", Preset: []byte(`{"prompts":[],"extensions":{"regex_scripts":[{"id":"p","findRegex":"secret","placement":[2]}]}}`)}
	cfg := &dto.ResponseTextFilter{Mode: "rules", FailurePolicy: "passthrough"}
	assert.True(t, textRegexRequiresStrictFailure(cfg, preset, "m", false))
	preset.Models = []string{"other"}
	assert.False(t, textRegexRequiresStrictFailure(cfg, preset, "m", false))
}

func TestTextRegexInvalidStoredConfigurationUsesFailurePolicy(t *testing.T) {
	for _, policy := range []string{"passthrough", "error"} {
		cfg := &dto.ResponseTextFilter{Mode: "regex_extract", Pattern: "[", FailurePolicy: policy}
		recorder := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(recorder)
		w := BeginResponseTextFilter(ctx, cfg, "m")
		require.NotNil(t, w)
		body := `{"choices":[{"message":{"content":"original"}}]}`
		_, err := w.WriteString(body)
		require.NoError(t, err)
		err = w.Finish(ctx, true)
		if policy == "passthrough" {
			require.NoError(t, err)
			assert.Equal(t, body, recorder.Body.String())
		} else {
			require.Error(t, err)
			assert.Empty(t, recorder.Body.String())
		}
	}
}
