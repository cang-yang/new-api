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

func TestRelayPresetScriptsWithClientDelivery(t *testing.T) {
	t.Run("context data preserves its text representation", func(t *testing.T) {
		text := `{"path":"C:\\notes\\new.txt","literal":"\\n","quote":"他说\"你好\"","html":"<thinking>data</thinking>"}`
		message := dto.Message{Role: "tool", ToolCallId: "context_result", Content: text}
		projected, err := presetClientContextMessages([]dto.Message{message})
		require.NoError(t, err)
		require.Len(t, projected, 1)
		content, ok := projected[0].Content.(string)
		require.True(t, ok)
		metadata, body, found := strings.Cut(content, "\n")
		require.True(t, found, "context metadata must not re-encode the tool result as a JSON string")
		require.Equal(t, text, body, "model-facing context must retain the original representation of paths, quotes and tags")
		require.Contains(t, metadata, `"context_only":true`)
		require.Empty(t, projected[0].ToolCallId, "a context record must not become a live client tool result")
		require.Equal(t, text, message.Content, "the original protocol message must remain untouched")
	})
	pointer := "/body"
	config := scriptPresetConfig(t, map[string]any{
		"ToolBindings": map[string]any{"reply": map[string]any{
			"enabled": true, "code": `return {name:"preset_reply",parameters:{type:"object",properties:{body:{type:"string"}}}};`,
		}},
		"FinalRequestProcessing": map[string]any{
			"enabled": true,
			"script": `({request}) => {
				const context = JSON.stringify(request.messages);
				request.messages = [{role:"system",content:"SCRIPT_CONTEXT:" + context}];
				request.tool_choice = {type:"function",function:{name:"preset_reply"}};
				return request;
			}`,
		},
		"OutputPreprocessing": map[string]any{
			"enabled": true,
			"script":  `() => { throw new Error("client tool calls must not reach text hooks"); }`,
		},
	})
	config.ReplyAdapter = &dto.ReplyAdapter{Version: 2, Containers: []dto.ReplyContainer{
		{Name: "preset_reply", ArgumentPointer: &pointer, OutputKind: "text"},
	}}
	clientTool := dto.ToolCallRequest{Type: "function", Function: dto.FunctionRequest{
		Name: "lookup", Parameters: []byte(`{"type":"object","properties":{"id":{"type":"string"}}}`),
	}}
	for _, kind := range []string{"auto", "required", "none", "named", "json_object", "json_schema", "history", "explicit_history", "legacy", "name_collision"} {
		t.Run(kind, func(t *testing.T) {
			request := &dto.GeneralOpenAIRequest{Model: "m", Messages: []dto.Message{{Role: "user", Content: "CLIENT_INPUT"}}}
			context := SillyTavernContext{}
			switch kind {
			case "json_object", "json_schema":
				request.ResponseFormat = &dto.ResponseFormat{Type: kind}
				if kind == "json_schema" {
					request.ResponseFormat.JsonSchema = []byte(`{"name":"result","strict":true,"schema":{"type":"object","properties":{"ok":{"type":"boolean"}},"required":["ok"],"additionalProperties":false}}`)
				}
			case "legacy":
				request.Functions = []byte(`[{"name":"lookup","parameters":{"type":"object"}}]`)
				request.FunctionCall = []byte(`{"name":"lookup"}`)
			case "history", "explicit_history":
				request.Tools = []dto.ToolCallRequest{clientTool}
				request.Messages = append(request.Messages,
					dto.Message{Role: "assistant", ToolCalls: []byte(`[{"id":"native_call","type":"function","function":{"name":"lookup","arguments":"{\"id\":\"a\"}"}}]`)},
					dto.Message{Role: "tool", ToolCallId: "native_call", Content: `{"value":"C:\\notes\\new.txt"}`},
					dto.Message{Role: "user", Content: "CONTINUE_AFTER_TOOL"})
				if kind == "explicit_history" {
					context.ChatHistory = request.Messages
				}
			default:
				request.Tools = []dto.ToolCallRequest{clientTool}
				request.ParallelTooCalls = common.GetPointer(false)
				request.ToolChoice = kind
				if kind == "named" {
					request.ToolChoice = map[string]any{"type": "function", "function": map[string]any{"name": "lookup"}}
				}
				if kind == "name_collision" {
					request.Tools[0].Function.Name = "preset_reply"
					request.ToolChoice = "required"
				}
			}
			before, err := common.Marshal(request)
			require.NoError(t, err)
			compiled, trace, err := CompileSillyTavernPreset(config, request, context)
			require.NoError(t, err, "a client protocol must not disable the preset's context script")
			contextIndex := 0
			if kind == "history" || kind == "explicit_history" {
				contextIndex = 3
			}
			require.Contains(t, compiled.Messages[contextIndex].Content, "SCRIPT_CONTEXT:")
			require.Contains(t, compiled.Messages[contextIndex].Content, "CLIENT_INPUT")
			require.Equal(t, request.Tools, compiled.Tools)
			require.Equal(t, request.ToolChoice, compiled.ToolChoice)
			require.Equal(t, request.ResponseFormat, compiled.ResponseFormat)
			require.Equal(t, request.Functions, compiled.Functions)
			require.Equal(t, request.FunctionCall, compiled.FunctionCall)
			require.Equal(t, request.ParallelTooCalls, compiled.ParallelTooCalls)
			require.NoError(t, SealClientRequirements(&ClientRequirements{request: request}, compiled, nil))
			require.NotNil(t, trace.ScriptResponse)
			require.Equal(t, "client_delivery", trace.Protocol.Reason)
			require.Equal(t, "completed", BuildAdaptationDiagnostic(&trace).Stages[2].Status)
			withoutRequirements := &ResponseTextFilterWriter{scriptResponse: trace.ScriptResponse}
			require.ErrorContains(t, withoutRequirements.validateDelivery([]byte(`{"choices":[]}`), false), "saved client requirements")
			if kind == "history" || kind == "explicit_history" {
				require.Equal(t, "user", compiled.Messages[0].Role,
					"a native function-call history cannot start with an assistant call on Gemini")
				require.Equal(t, request.Messages[1:3], compiled.Messages[1:3],
					"native calls/results must remain intact before the script's current context")
				require.Contains(t, compiled.Messages[3].Content, "SCRIPT_CONTEXT:")
			}
			after, err := common.Marshal(request)
			require.NoError(t, err)
			require.Equal(t, string(before), string(after), "compilation must not mutate the client request")

			// A real client call must not be swallowed by the preset formatter,
			// including when its name equals the preset's reply transport.
			if len(request.Tools) > 0 && kind != "none" {
				for _, stream := range []bool{false, true} {
					field := "message"
					if stream {
						field = "delta"
					}
					body := []byte(fmt.Sprintf(`{"choices":[{"index":0,"finish_reason":"tool_calls",%q:{"tool_calls":[{"index":0,"id":"native_response","type":"function","function":{"name":%q,"arguments":"{}"}}]}}]}`, field, request.Tools[0].Function.Name))
					if stream {
						body = []byte("data: " + string(body) + "\n\ndata: [DONE]\n\n")
					}
					diagnostic := newAdaptationDiagnostic()
					writer := &ResponseTextFilterWriter{requirements: &ClientRequirements{request: request}, scriptResponse: trace.ScriptResponse, diagnostic: diagnostic}
					result, err := writer.processResponse(body, stream)
					require.NoError(t, err)
					require.Equal(t, string(body), string(result))
					require.Contains(t, diagnostic.Stages, AdaptationDiagnosticStage{
						Stage: string(FailureStageOutputProcessing), Status: "skipped",
					}, "a skipped reply hook must not be reported as completed")
					if kind != "name_collision" {
						private := []byte(strings.Replace(string(body), `"name":"lookup"`, `"name":"preset_reply"`, 1))
						_, err = writer.processResponse(private, stream)
						require.Error(t, err, "private preset tools must not leak through native delivery")
					}
				}
			}
		})
	}
	t.Run("compiled context owns the current task without replaying raw instructions", func(t *testing.T) {
		messages := []dto.Message{
			{Role: "system", Content: "GENERAL_CONTEXT"},
			{Role: "user", Content: "PREVIOUS_INPUT"},
			{Role: "assistant", Content: "PREVIOUS_REPLY"},
			{Role: "system", Content: "CURRENT_TASK: return only the requested update block"},
			{Role: "user", Content: "CURRENT_INPUT"},
			{Role: "system", Content: "CURRENT_SUFFIX"},
		}
		for _, kind := range []string{"plain", "json", "explicit_json"} {
			request := &dto.GeneralOpenAIRequest{
				Model: "m", Messages: messages,
			}
			if kind != "plain" {
				request.ResponseFormat = &dto.ResponseFormat{Type: "json_object"}
			}
			context := SillyTavernContext{}
			if kind == "explicit_json" {
				context.ChatHistory = messages
				request.Messages = []dto.Message{{Role: "user", Content: "NOT_THE_EXPLICIT_HISTORY"}}
			}
			compiled, trace, err := CompileSillyTavernPreset(config, request, context)
			require.NoError(t, err)
			require.Contains(t, compiled.Messages[0].Content, "SCRIPT_CONTEXT:",
				"preserving the client task must not bypass its context script")
			require.Len(t, compiled.Messages, 2, "the gateway must not append raw client messages after the script")
			require.Contains(t, compiled.Messages[0].Content, "CURRENT_TASK:")
			require.Contains(t, compiled.Messages[0].Content, "CURRENT_INPUT")
			require.Contains(t, compiled.Messages[0].Content, "CURRENT_SUFFIX")
			require.NotContains(t, compiled.Messages[1].Content, "CURRENT_INPUT",
				"the delivery instruction must not repeat client content")
			if kind == "plain" {
				require.Equal(t, "preset_reply", compiled.Tools[0].Function.Name,
					"plain task preservation must retain the preset's reply transport")
				require.False(t, trace.ScriptResponse.nativeDelivery)
			} else {
				require.Equal(t, "client_delivery", trace.Protocol.Reason)
			}
		}
	})
	t.Run("explicit client delivery keeps scripts without requiring a client tool", func(t *testing.T) {
		copied, err := common.DeepCopy(config)
		require.NoError(t, err)
		copied.DeliveryMode = "client"
		request := &dto.GeneralOpenAIRequest{Model: "m", Messages: []dto.Message{{Role: "user", Content: "Return the update, not a story"}}}
		compiled, trace, err := CompileSillyTavernPreset(copied, request, SillyTavernContext{})
		require.NoError(t, err)
		require.Empty(t, compiled.Tools, "the channel must not reintroduce a private reply tool")
		require.Contains(t, compiled.Messages[0].Content, "SCRIPT_CONTEXT:")
		require.Len(t, compiled.Messages, 2)
		require.Contains(t, compiled.Messages[0].Content, "Return the update, not a story")
		require.Equal(t, "text", trace.Protocol.Contract)
		require.Equal(t, "client_delivery", trace.Protocol.Reason)
		require.True(t, trace.ScriptResponse.nativeDelivery)
		copied.DeliveryMode = "unknown"
		_, _, err = CompileSillyTavernPreset(copied, request, SillyTavernContext{})
		require.ErrorContains(t, err, "delivery_mode")
	})
	t.Run("request edits and ordinary reply hooks remain effective", func(t *testing.T) {
		for _, delivery := range []string{"preset", "client"} {
			t.Run(delivery, func(t *testing.T) {
				preset := scriptPresetConfig(t, map[string]any{
					"ToolBindings": map[string]any{"reply": map[string]any{
						"enabled": true, "code": `return {name:"preset_reply",parameters:{type:"object",properties:{body:{type:"string"}}}};`,
					}},
					"FinalRequestProcessing": map[string]any{
						"enabled": true,
						"script": `({request}) => {
							request.messages = request.messages.map(m => ({...m,content:String(m.content).replaceAll("RAW_SENTINEL","NORMALIZED")}));
							request.tool_choice = {type:"function",function:{name:"preset_reply"}};
							return request;
						}`,
					},
					"OutputPreprocessing": map[string]any{
						"enabled": true,
						"script": `({buffer}) => {
							if (!buffer.startsWith("<answer>")) throw new Error("formatter did not run");
							return {output:buffer.replace("raw-reply","processed-reply"),state:{}};
						}`,
						"consumeToolCalls": true,
						"toolCallFormatter": map[string]any{
							"enabled": true, "script": `({calls}) => "<answer>"+calls[0].arguments.body+"</answer>"`,
						},
					},
				})
				preset.ReplyAdapter = &dto.ReplyAdapter{Version: 2, Containers: []dto.ReplyContainer{
					{Name: "preset_reply", Formatter: true, OutputKind: "text"},
				}}
				if delivery == "client" {
					preset.DeliveryMode = "client"
				}
				request := &dto.GeneralOpenAIRequest{Model: "m", Messages: []dto.Message{
					{Role: "user", Content: "RAW_SENTINEL"},
					{Role: "system", Content: "RAW_SENTINEL trailing instruction"},
				}}
				compiled, trace, err := CompileSillyTavernPreset(preset, request, SillyTavernContext{})
				require.NoError(t, err)
				encoded, err := common.Marshal(compiled.Messages)
				require.NoError(t, err)
				require.NotContains(t, string(encoded), "RAW_SENTINEL", "script edits must not be undone by raw task replay")
				require.Equal(t, "NORMALIZED", compiled.Messages[1].Content)
				require.Equal(t, "NORMALIZED trailing instruction", compiled.Messages[2].Content)
				require.Equal(t, "RAW_SENTINEL", request.Messages[0].Content)
				details := BuildAdaptationDiagnostic(&trace).Stages[1].Details
				require.Equal(t, true, details["request_script_executed"])
				require.Equal(t, true, details["request_messages_changed"])
				require.Equal(t, 3, details["request_input_messages"])
				require.Equal(t, 3, details["request_output_messages"])
				require.Equal(t, delivery, details["delivery"])

				ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
				requirements, err := SnapshotClientRequirements(request)
				require.NoError(t, err)
				ctx.Set(ClientRequirementsKey, requirements)
				AttachPresetScriptResponse(ctx, trace)
				filter := &dto.ResponseTextFilter{Mode: "rules", Rules: []dto.TextRegexRule{
					{ID: "unwrap", Stage: "receive", Action: "replace", Pattern: `/<\/?answer>/g`},
				}}
				writer := BeginResponseTextFilterWithPreset(ctx, filter, preset, "m")
				require.NotNil(t, writer)
				body := []byte(`{"choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":"<answer>raw-reply</answer>"}}]}`)
				if delivery == "preset" {
					body = []byte(`{"choices":[{"index":0,"finish_reason":"tool_calls","message":{"role":"assistant","tool_calls":[{"id":"reply","type":"function","function":{"name":"preset_reply","arguments":"{\"body\":\"raw-reply\"}"}}]}}]}`)
				}
				result, err := writer.processResponse(body, false)
				require.NoError(t, err)
				choices, err := responseChoices(result, false)
				require.NoError(t, err)
				require.Equal(t, "processed-reply", choices["0"].text.String(),
					"formatter, output hook and receive regex must compose, not merely be configured")
				require.Empty(t, choices["0"].tools)
			})
		}
	})
	t.Run("multimodal content is not replayed after the script", func(t *testing.T) {
		request := &dto.GeneralOpenAIRequest{
			Model: "m", ResponseFormat: &dto.ResponseFormat{Type: "json_object"},
			Messages: []dto.Message{{Role: "user", Content: []any{
				map[string]any{"type": "text", "text": "describe the attachment"},
				map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64,IMAGE_SENTINEL"}},
			}}},
		}
		compiled, _, err := CompileSillyTavernPreset(config, request, SillyTavernContext{})
		require.NoError(t, err)
		encoded, err := common.Marshal(compiled.Messages)
		require.NoError(t, err)
		require.Equal(t, 1, strings.Count(string(encoded), "IMAGE_SENTINEL"),
			"client multimodal parts must follow one script-controlled path")
	})
	t.Run("protocol history survives without replaying surrounding client messages", func(t *testing.T) {
		for _, legacy := range []bool{false, true} {
			t.Run(fmt.Sprintf("legacy=%v", legacy), func(t *testing.T) {
				request := &dto.GeneralOpenAIRequest{Model: "m", Messages: []dto.Message{
					{Role: "system", Content: "BACKGROUND_ONLY"},
					{Role: "user", Content: "LOOKUP_EARLIER"},
					{Role: "assistant", ToolCalls: []byte(`[{"id":"a","type":"function","function":{"name":"lookup","arguments":"{}"}},{"id":"b","type":"function","function":{"name":"lookup","arguments":"{}"}}]`)},
					{Role: "tool", ToolCallId: "b", Content: `{"value":2}`},
					{Role: "tool", ToolCallId: "a", Content: `{"value":1}`},
					{Role: "assistant", Content: "EARLIER_SUMMARY"},
					{Role: "user", Content: "LATEST_TASK"},
					{Role: "developer", Content: "LATEST_CONSTRAINT"},
				}}
				if legacy {
					request.Messages[2] = dto.Message{Role: "assistant", FunctionCall: []byte(`{"name":"lookup","arguments":"{}"}`)}
					request.Messages[3] = dto.Message{Role: "function", Name: common.GetPointer("lookup"), Content: `{"value":2}`}
					request.Messages = append(request.Messages[:4], request.Messages[5:]...)
				}
				expected := request.Messages[2:5]
				if legacy {
					expected = request.Messages[2:4]
				}
				compiled, trace, err := CompileSillyTavernPreset(config, request, SillyTavernContext{})
				require.NoError(t, err, "completed tool history does not require definitions to be repeated")
				require.Equal(t, "user", compiled.Messages[0].Role)
				require.NotContains(t, compiled.Messages[0].Content, "LOOKUP_EARLIER",
					"provider-compatible history framing must not replay raw user input")
				require.Equal(t, expected, compiled.Messages[1:len(expected)+1])
				require.Empty(t, compiled.Tools)
				require.NoError(t, validatePresetClientToolContract(request, compiled))
				require.Contains(t, compiled.Messages[len(expected)+1].Content, "LATEST_TASK")
				require.Contains(t, compiled.Messages[len(expected)+1].Content, "LATEST_CONSTRAINT")
				require.Len(t, compiled.Messages, len(expected)+3)
				for _, source := range trace.Messages[1 : len(expected)+1] {
					require.Equal(t, "client_tool_history", source.Source)
				}
				require.Equal(t, "client_tool_history_context", trace.Messages[0].Source)
				require.Equal(t, "preset_script", trace.Messages[len(expected)+1].Source)
				require.Equal(t, "delivery_contract", trace.Messages[len(trace.Messages)-1].Source)
			})
		}
	})
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
