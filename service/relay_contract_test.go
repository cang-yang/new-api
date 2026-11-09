package service

import (
	"context"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestRelayClientRequirements(t *testing.T) {
	t.Run("none cannot activate an internal transport", func(t *testing.T) {
		original := &dto.GeneralOpenAIRequest{Tools: []dto.ToolCallRequest{{Type: "function", Function: dto.FunctionRequest{Name: "lookup"}}}, ToolChoice: "none"}
		compiled := &dto.GeneralOpenAIRequest{Tools: append(append([]dto.ToolCallRequest{}, original.Tools...), dto.ToolCallRequest{Type: "function", Function: dto.FunctionRequest{Name: "internal_reply"}}), ToolChoice: "required"}
		_, _, err := buildPresetProtocolPlan(original, compiled, &dto.SillyTavernPresetConfig{ToolProtocol: &dto.SillyTavernToolProtocol{Enabled: true}}, &PresetScriptResponse{ownedTools: map[string]bool{"internal_reply": true}})
		require.Error(t, err)
	})
	t.Run("JSON overrides channel text defaults", func(t *testing.T) {
		contract, safe, err := resolvePresetProtocolContract(&dto.GeneralOpenAIRequest{ResponseFormat: &dto.ResponseFormat{Type: "json_object"}}, "", &dto.SillyTavernToolProtocol{ResponseTarget: "text"})
		require.False(t, err == nil && safe && contract == "text")
	})
	for _, input := range []any{`["array"]`, `null`, `1`, `"text"`, []any{"array"}, nil} {
		_, err := presetProtocolJSONArgument(input)
		require.Error(t, err)
	}
	value, err := presetProtocolJSONArgument(`{"body":"hello"}`)
	require.NoError(t, err)
	require.Equal(t, map[string]any{"body": "hello"}, value)
	t.Run("response format survives scripts", func(t *testing.T) {
		original := &dto.GeneralOpenAIRequest{ResponseFormat: &dto.ResponseFormat{Type: "json_object"}}
		require.Error(t, validatePresetClientToolContractWithMode(original, &dto.GeneralOpenAIRequest{}, 0))
	})
}

func TestRelayExplicitReplySources(t *testing.T) {
	pointer := "/payload/a~1b/0/~0text"
	source := dto.ReplyContainer{Name: "reply", ArgumentPointer: &pointer, OutputKind: "text"}
	config := &dto.SillyTavernPresetConfig{ReplyAdapter: &dto.ReplyAdapter{Version: 2, Containers: []dto.ReplyContainer{source}}}
	original := &dto.GeneralOpenAIRequest{Messages: []dto.Message{{Role: "user", Content: "hello"}}}
	compiled := &dto.GeneralOpenAIRequest{Tools: []dto.ToolCallRequest{{Type: "function", Function: dto.FunctionRequest{Name: "reply"}}}}
	response := &PresetScriptResponse{ownedTools: map[string]bool{"reply": true}}
	plan, _, err := buildPresetProtocolPlan(original, compiled, config, response)
	require.NoError(t, err)
	require.True(t, plan.audit.Activated)
	response.protocol = plan
	body := []byte(`{"choices":[{"index":0,"finish_reason":"tool_calls","message":{"role":"assistant","tool_calls":[{"id":"r","type":"function","function":{"name":"reply","arguments":"{\"payload\":{\"a/b\":[{\"~text\":\"hello\"}]}}"}}]}}]}`)
	restored, err := plan.restoreTerminal(response, body, false, time.Now().Add(time.Second))
	require.NoError(t, err)
	require.Contains(t, string(restored), `"content":"hello"`)
	require.NotContains(t, string(restored), `"tool_calls"`)
	for _, tools := range []*dto.GeneralOpenAIRequest{
		{ToolChoice: "none"},
		{Functions: []byte(`[{"name":"lookup"}]`)},
		{Messages: []dto.Message{{Role: "tool", ToolCallId: "t", Content: "opaque"}}},
	} {
		_, _, err = buildPresetProtocolPlan(tools, compiled, config, response)
		require.ErrorContains(t, err, "conflicts with client tools")
	}
	_, _, err = buildPresetProtocolPlan(original, compiled, &dto.SillyTavernPresetConfig{}, response)
	require.ErrorContains(t, err, "no declared reply transport")
	for _, policy := range []*dto.SillyTavernToolProtocol{
		{Enabled: true, ClientFinal: "finish"},
		{Enabled: true, Auxiliary: []string{"lookup"}},
		{Enabled: true, Action: []string{"save"}},
		{Enabled: true, PhasePolicy: "auto"},
	} {
		_, _, err = buildPresetProtocolPlan(original, compiled, &dto.SillyTavernPresetConfig{ToolProtocol: policy}, response)
		require.ErrorContains(t, err, "requires review")
	}
}

func TestRelayScopeUsesExplicitProtocolOnly(t *testing.T) {
	for _, request := range []*dto.GeneralOpenAIRequest{
		{ToolChoice: "none"},
		{ResponseFormat: &dto.ResponseFormat{Type: "json_schema"}},
		{Messages: []dto.Message{{Role: "user", Content: []any{map[string]any{"type": "text", "text": "hi"}}}}},
		{Messages: []dto.Message{{Role: "function", Content: "history"}}},
	} {
		applies, _ := AdaptationApplies("plain_chat_only", request)
		require.False(t, applies)
		applies, _ = AdaptationApplies("", request)
		require.True(t, applies, "old channels retain their explicit legacy scope")
	}
	applies, _ := AdaptationApplies("plain_chat_only", &dto.GeneralOpenAIRequest{Messages: []dto.Message{{Role: "user", Content: "call a tool and return JSON"}}})
	require.True(t, applies, "prose must not trigger business-phase guesses")
}

func TestRelayJSONTextFieldsAndMandatoryExtraction(t *testing.T) {
	request := &dto.GeneralOpenAIRequest{ResponseFormat: &dto.ResponseFormat{Type: "json_object"}}
	requirements, err := SnapshotClientRequirements(request)
	require.NoError(t, err)
	run := func(filter *dto.ResponseTextFilter) ([]byte, error) {
		recorder := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(recorder)
		ctx.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
		ctx.Set(ClientRequirementsKey, requirements)
		writer := BeginResponseTextFilter(ctx, filter, "m")
		require.NotNil(t, writer)
		body, _ := common.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "finish_reason": "stop", "message": map[string]any{"content": `{"body":"<body>hello</body>","count":2}`}}}})
		return writer.processResponse(body, false)
	}
	filter := &dto.ResponseTextFilter{Mode: "regex_extract", Pattern: `<body>(.*?)</body>`, FailurePolicy: "passthrough"}
	untouched, err := run(filter)
	require.NoError(t, err)
	initialChoices, err := responseChoices(untouched, false)
	require.NoError(t, err)
	require.JSONEq(t, `{"body":"<body>hello</body>","count":2}`, initialChoices["0"].text.String())
	filter.JSONTextPointers = []string{"/body"}
	processed, err := run(filter)
	require.NoError(t, err)
	choices, err := responseChoices(processed, false)
	require.NoError(t, err)
	require.JSONEq(t, `{"body":"hello","count":2}`, choices["0"].text.String())
	filter.Pattern = `<missing>(.*?)</missing>`
	filter.MissingMatch = "error"
	_, err = run(filter)
	require.Error(t, err, "mandatory extraction must not be bypassed by passthrough policy")
}

func TestRelayValidationDoesNotMergeChoicesOrToolIndices(t *testing.T) {
	requirements, err := SnapshotClientRequirements(&dto.GeneralOpenAIRequest{Tools: []dto.ToolCallRequest{{Type: "function", Function: dto.FunctionRequest{Name: "lookup"}}}, ToolChoice: "required"})
	require.NoError(t, err)
	writer := &ResponseTextFilterWriter{requirements: requirements}
	stream := "data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"a\",\"function\":{\"name\":\"lookup\",\"arguments\":\"{}\"}}]}}]}\n\n" +
		"data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n"
	require.NoError(t, writer.validateDelivery([]byte(stream), true))
	require.Error(t, writer.validateDelivery([]byte(strings.ReplaceAll(stream, "data: [DONE]\n\n", "")), true))
	changedID := strings.Replace(stream, `"delta":{},"finish_reason"`, `"delta":{"tool_calls":[{"index":0,"id":"b","function":{"arguments":""}}]},"finish_reason"`, 1)
	require.ErrorContains(t, writer.validateDelivery([]byte(changedID), true), "ID changed")
	secondChoice := strings.Replace(stream, `{"index":0,"delta":{},"finish_reason":"tool_calls"}`, `{"index":0,"delta":{},"finish_reason":"tool_calls"},{"index":1,"delta":{"content":"not a tool"},"finish_reason":"stop"}`, 1)
	require.ErrorContains(t, writer.validateDelivery([]byte(secondChoice), true), "omitted a required")
	require.Error(t, writer.validateDelivery([]byte(stream+`data: {"choices":[]}`+"\n\n"), true))
	afterFinish := strings.Replace(stream, "data: [DONE]", `data: {"choices":[{"index":0,"delta":{"content":"late"}}]}`+"\n\ndata: [DONE]", 1)
	require.ErrorContains(t, writer.validateDelivery([]byte(afterFinish), true), "after its finish")
}

func TestRelayResponseAdmissionAndCancellationAreAtomic(t *testing.T) {
	var writers []*ResponseTextFilterWriter
	defer func() {
		for _, writer := range writers {
			if writer.admitted {
				<-responseProcessingSlots
				writer.admitted = false
			}
		}
	}()
	for range cap(responseProcessingSlots) {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest("POST", "/", nil)
		writer := BeginResponseTextFilter(c, &dto.ResponseTextFilter{Mode: "regex_extract", Pattern: "(.*)"}, "m")
		require.NoError(t, writer.Admit(c))
		writers = append(writers, writer)
	}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/", nil)
	extra := BeginResponseTextFilter(c, &dto.ResponseTextFilter{Mode: "regex_extract", Pattern: "(.*)"}, "m")
	require.ErrorContains(t, extra.Admit(c), "busy")
	recorder := httptest.NewRecorder()
	c, _ = gin.CreateTestContext(recorder)
	base, cancel := context.WithCancel(context.Background())
	c.Request = httptest.NewRequest("POST", "/", nil).WithContext(base)
	writer := writers[0]
	writer.ResponseWriter = c.Writer
	_, err := writer.Write([]byte(`{"choices":[]}`))
	require.NoError(t, err)
	cancel()
	require.ErrorIs(t, writer.Finish(c, true), context.Canceled)
	require.Empty(t, recorder.Body.String())
	require.Equal(t, 0, writer.buffer.Len())
	require.False(t, writer.admitted)
	next, _ := gin.CreateTestContext(httptest.NewRecorder())
	next.Request = httptest.NewRequest("POST", "/", nil)
	require.NoError(t, extra.Admit(next))
	writers = append(writers, extra)
}

func TestRelayFallbackUsesWholePreviousCheckpoint(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, outputFails := range []bool{false, true} {
			t.Run(fmt.Sprintf("stream=%v/outputFails=%v", stream, outputFails), func(t *testing.T) {
				script := `({buffer}) => ({output: buffer + "-processed", state:{}})`
				if outputFails {
					script = `() => { throw new Error("private script failure"); }`
				}
				preset := scriptPresetConfig(t, map[string]any{"OutputPreprocessing": map[string]any{"enabled": true, "script": script}})
				preset.FailurePolicy = &dto.SillyTavernFailurePolicy{OutputProcessing: "preserve", Regex: "preserve"}
				_, trace, err := CompileSillyTavernPreset(preset, &dto.GeneralOpenAIRequest{Model: "m"}, SillyTavernContext{})
				require.NoError(t, err)
				ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
				AttachPresetScriptResponse(ctx, trace)
				filter := &dto.ResponseTextFilter{Mode: "rules", FailurePolicy: "passthrough", Rules: []dto.TextRegexRule{
					{ID: "first", Stage: "receive", Action: "replace", Pattern: "one", Replacement: "changed"},
					{ID: "second", Stage: "receive", Action: "replace", Pattern: "/two/g", Replacement: strings.Repeat("x", 65536)},
				}}
				if outputFails {
					// P failure preserves R and stops; F must not run afterward.
					filter.Rules = filter.Rules[:1]
				}
				writer := BeginResponseTextFilterWithPreset(ctx, filter, preset, "m")
				require.NotNil(t, writer)
				// Choice 0 is changed before choice 1 exceeds the output limit.
				// The optional regex fallback must restore both choices to P.
				second := strings.Repeat("two", 130)
				body := []byte(fmt.Sprintf(`{"choices":[{"index":0,"finish_reason":"stop","message":{"content":"one"}},{"index":1,"finish_reason":"stop","message":{"content":%q}}]}`, second))
				if stream {
					body = []byte(fmt.Sprintf("data: "+`{"choices":[{"index":0,"finish_reason":"stop","delta":{"content":"one"}},{"index":1,"finish_reason":"stop","delta":{"content":%q}}]}`+"\n\ndata: [DONE]\n\n", second))
				}
				result, err := writer.processResponse(body, stream)
				require.NoError(t, err)
				choices, err := responseChoices(result, stream)
				require.NoError(t, err)
				if outputFails {
					require.Equal(t, "one", choices["0"].text.String())
					require.Equal(t, second, choices["1"].text.String())
				} else {
					require.Equal(t, "one-processed", choices["0"].text.String())
					require.Equal(t, second+"-processed", choices["1"].text.String())
				}
			})
		}
	}
}
