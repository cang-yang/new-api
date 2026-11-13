package service

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRelayCorePresetPreservesClientTaskAndDelivery(t *testing.T) {
	hook, err := os.ReadFile("../scripts/presets/meowssiah-relay-request.js")
	require.NoError(t, err)
	formatter, err := os.ReadFile("../scripts/presets/meowssiah-relay-formatter.js")
	require.NoError(t, err)
	preset := map[string]any{
		"prompts": []any{
			map[string]any{"identifier": "main", "role": "system", "content": "Preset background: the library opens at nine."},
			map[string]any{"identifier": "reply", "role": "system", "content": "Use the declared reply transport for final answers."},
			map[string]any{"identifier": "chatHistory", "marker": true},
		},
		"prompt_order": []any{map[string]any{"character_id": 100001, "order": []any{
			map[string]any{"identifier": "main", "enabled": true},
			map[string]any{"identifier": "reply", "enabled": true},
			map[string]any{"identifier": "chatHistory", "enabled": true},
		}}},
		"extensions": map[string]any{"new_api_relay": map[string]any{"revision": "relay-core-2", "reply_tool": "preset_reply"}, "SPreset": map[string]any{
			"ToolBindings":           map[string]any{"reply": map[string]any{"enabled": true, "code": `return {name:'preset_reply',parameters:{type:'object'}};`}},
			"FinalRequestProcessing": map[string]any{"enabled": true, "script": string(hook)},
			"OutputPreprocessing": map[string]any{"enabled": true, "script": `({buffer}) => ({output: buffer, hold: ''})`, "consumeToolCalls": true,
				"toolCallFormatter": map[string]any{"enabled": true, "script": string(formatter)}},
		}},
	}
	encoded, err := common.Marshal(preset)
	require.NoError(t, err)
	config := &dto.SillyTavernPresetConfig{Preset: encoded, ScriptRuntime: "spreset-v1", ParameterPolicy: "client",
		ReplyAdapter: &dto.ReplyAdapter{Version: 2, StructuredOutput: true, RequireContainer: true, FormattedOutput: "client_format",
			Containers: []dto.ReplyContainer{{Name: "preset_reply", Formatter: true, OutputKind: "text"}}}}
	for _, variant := range []string{"text", "schema", "none", "auto", "required", "named", "history"} {
		t.Run(variant, func(t *testing.T) {
			request := &dto.GeneralOpenAIRequest{Model: "test", Messages: []dto.Message{
				{Role: "system", Content: "Only update variables; do not continue the story."},
				{Role: "user", Content: `Keep literal escapes: C:\notes and \n. Return the requested result.`},
			}}
			if variant == "schema" {
				request.ResponseFormat = &dto.ResponseFormat{Type: "json_schema", JsonSchema: []byte(`{"name":"patch","schema":{"type":"object","properties":{"json_patch":{"type":"array"}},"required":["json_patch"]}}`)}
			}
			if variant == "none" || variant == "named" || variant == "history" || variant == "auto" || variant == "required" {
				request.Tools = []dto.ToolCallRequest{{Type: "function", Function: dto.FunctionRequest{Name: "lookup", Parameters: []byte(`{"type":"object"}`)}}}
				request.ToolChoice = "none"
				if variant == "auto" || variant == "required" {
					request.ToolChoice = variant
				}
				if variant == "named" {
					request.ToolChoice = map[string]any{"type": "function", "function": map[string]string{"name": "lookup"}}
				}
				if variant == "history" {
					request.Messages = append(request.Messages,
						dto.Message{Role: "assistant", ToolCalls: []byte(`[{"id":"lookup_1","type":"function","function":{"name":"lookup","arguments":"{}"}}]`)},
						dto.Message{Role: "tool", ToolCallId: "lookup_1", Content: `{"value":"literal \\n"}`},
						dto.Message{Role: "user", Content: "Use the result; do not continue the story."})
				}
			}
			compiled, trace, err := CompileSillyTavernPreset(config, request, SillyTavernContext{})
			require.NoError(t, err)
			require.NotNil(t, trace.ScriptResponse)
			require.True(t, trace.ScriptResponse.requestHookExecuted)
			require.True(t, trace.ScriptResponse.ownedTools["preset_reply"])
			require.NotNil(t, trace.ScriptResponse.generationRequest, "the preset reply transport must remain active")
			require.Nil(t, compiled.ResponseFormat, "the explicit format is converted and locally validated, not advertised as native enforcement")
			require.False(t, trace.ScriptResponse.textToClientFormat, "client-format output must not enter legacy whole-text JSON cleanup")
			if variant == "named" || variant == "required" {
				require.True(t, samePresetValue(request.ToolChoice, compiled.ToolChoice))
				require.Equal(t, request.Tools, compiled.Tools)
			} else if variant == "auto" {
				require.Equal(t, "required", compiled.ToolChoice, "auto may choose business calls or an internal completion, never bypass a necessary container")
				require.Len(t, compiled.Tools, 2)
			} else {
				require.Equal(t, "preset_reply", presetToolChoiceName(compiled.ToolChoice))
				require.Len(t, compiled.Tools, 1)
			}
			for _, original := range request.Messages {
				require.Contains(t, compiled.Messages, original, "client task and history must survive whole")
			}
			require.Contains(t, fmt.Sprint(compiled.Messages), "预设内部回复约定")
			requirements, err := SnapshotClientRequirements(request)
			require.NoError(t, err)
			filter := &dto.ResponseTextFilter{Mode: "rules", Rules: []dto.TextRegexRule{{ID: "literal-tag", Stage: "receive", Action: "replace", Pattern: `/<game>/g`, Replacement: "CORRUPTED"}}}
			require.NoError(t, SealClientRequirements(requirements, compiled, filter, trace.ScriptResponse))
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx.Set(ClientRequirementsKey, requirements)
			AttachPresetScriptResponse(ctx, trace)
			writer := BeginResponseTextFilterWithPreset(ctx, filter, config, request.Model)
			require.NotNil(t, writer)
			if variant != "named" && variant != "required" {
				_, err = writer.processResponse([]byte(`{"choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":"bare reply"}}]}`), false)
				require.ErrorContains(t, err, "omitted the required tool call")
				text := "The library opens at nine."
				if variant == "schema" {
					text = `{"json_patch":[{"op":"replace","path":"/value","value":"<game>C:\\notes and literal \\n</game>"}]}`
				}
				args, err := common.Marshal(map[string]string{"think_nya": "not the final result", "game_content": text})
				require.NoError(t, err)
				body := []byte(fmt.Sprintf(`{"choices":[{"index":0,"finish_reason":"tool_calls","message":{"role":"assistant","tool_calls":[{"id":"reply","type":"function","function":{"name":"preset_reply","arguments":%q}}]}}]}`, args))
				result, err := writer.processResponse(body, false)
				require.NoError(t, err)
				choices, err := responseChoices(result, false)
				require.NoError(t, err)
				require.Equal(t, text, choices["0"].text.String(), "the formatter must deliver final content, not notes or an extra wrapper")
				require.Empty(t, choices["0"].tools)
				require.True(t, writer.outputProcessed)
			} else {
				body := []byte(`{"choices":[{"index":0,"finish_reason":"tool_calls","message":{"role":"assistant","tool_calls":[{"id":"lookup","type":"function","function":{"name":"lookup","arguments":"{}"}}]}}]}`)
				result, err := writer.processResponse(body, false)
				require.NoError(t, err)
				require.Equal(t, body, result, "business calls must reach the client unchanged")
				require.False(t, writer.outputProcessed)
			}
		})
	}
}

func TestRelayCoreExportedPreset(t *testing.T) {
	path := os.Getenv("NEW_API_CORE_CHANNEL")
	if path == "" {
		t.Skip("set NEW_API_CORE_CHANNEL to a locally prepared relay-core channel export")
	}
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var channel struct {
		Settings string `json:"settings"`
	}
	require.NoError(t, common.Unmarshal(data, &channel))
	var settings dto.ChannelOtherSettings
	require.NoError(t, common.UnmarshalJsonStr(channel.Settings, &settings))
	request := &dto.GeneralOpenAIRequest{Model: "gemini-3.8-flash", Messages: []dto.Message{{Role: "user", Content: "What time does the library open?"}}}
	if path := os.Getenv("NEW_API_EXPORTED_REQUEST"); path != "" {
		data, err = os.ReadFile(path)
		require.NoError(t, err)
		require.NoError(t, common.Unmarshal(data, request))
	}
	before, err := cloneGeneralRequest(request)
	require.NoError(t, err)
	filtered, err := ApplyRequestTextRegex(settings.ResponseTextFilter, settings.SillyTavernPreset, request, request.Model)
	require.NoError(t, err)
	compiled, trace, err := CompileWorldbookFrom(settings.Worldbook, settings.SillyTavernPreset, filtered.(*dto.GeneralOpenAIRequest), before, SillyTavernContext{})
	require.NoError(t, err)
	require.NotNil(t, trace.ScriptResponse)
	require.True(t, trace.ScriptResponse.requestHookExecuted)
	require.NotEmpty(t, trace.ScriptResponse.ownedTools)
	require.NotNil(t, trace.ScriptResponse.generationRequest)
	require.True(t, settings.SillyTavernPreset.ReplyAdapter.RequireContainer)
	require.Equal(t, "client_format", settings.SillyTavernPreset.ReplyAdapter.FormattedOutput)
	require.True(t, trace.ScriptResponse.settings.OutputPreprocessing.ConsumeToolCalls)
	require.True(t, trace.ScriptResponse.settings.OutputPreprocessing.ToolCallFormatter.Enabled)
	requirements, err := SnapshotClientRequirements(before)
	require.NoError(t, err)
	require.NoError(t, SealClientRequirements(requirements, compiled, settings.ResponseTextFilter, trace.ScriptResponse))
	if settings.Worldbook == nil {
		for _, message := range before.Messages {
			require.Contains(t, compiled.Messages, message)
		}
	}
	t.Run("worldbook anchors survive the actual request hook", func(t *testing.T) {
		client := &dto.GeneralOpenAIRequest{Model: request.Model, Messages: []dto.Message{
			{Role: "user", Content: "first"}, {Role: "assistant", Content: "reply"}, {Role: "user", Content: "worldbook-position"},
		}}
		lore := &dto.WorldbookConfig{Enabled: true, SemanticsVersion: 2, Books: []dto.Worldbook{{ID: "placement", Enabled: true, Entries: []dto.WorldbookEntry{
			{ID: "prelude", Enabled: true, Constant: true, Content: "prelude-position", InjectionMode: dto.WorldbookInjectionSystemPrelude},
			{ID: "near-low", Enabled: true, Constant: true, Content: "worldbook-position", Priority: 1, InjectionMode: dto.WorldbookInjectionBeforeLastUser},
			{ID: "near-high", Enabled: true, Constant: true, Content: "higher-position", Priority: 2, InjectionMode: dto.WorldbookInjectionBeforeLastUser},
		}}}}
		compiled, trace, err := CompileWorldbook(lore, settings.SillyTavernPreset, client, SillyTavernContext{})
		require.NoError(t, err)
		prelude := slices.IndexFunc(compiled.Messages, func(m dto.Message) bool { return m.Role == "system" && m.Content == "prelude-position" })
		first := slices.IndexFunc(compiled.Messages, func(m dto.Message) bool { return m.Role == "user" && m.Content == "first" })
		require.GreaterOrEqual(t, prelude, 0)
		require.Less(t, prelude, first)
		last := slices.IndexFunc(compiled.Messages, func(m dto.Message) bool { return m.Role == "user" && m.Content == "worldbook-position" })
		require.Greater(t, last, 0, "identical client text is not treated as a worldbook duplicate")
		require.Equal(t, "system", compiled.Messages[last-1].Role)
		require.Equal(t, "worldbook-position\n\nhigher-position", compiled.Messages[last-1].Content)
		require.True(t, trace.ScriptResponse.requestHookExecuted)
	})
	// Exercise this exact asset's actual output hook and formatter for both
	// delivery modes. Compilation alone cannot establish that restoration works.
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("restore-stream-%t", stream), func(t *testing.T) {
			client := &dto.GeneralOpenAIRequest{Model: request.Model, Stream: new(stream), ToolChoice: "none",
				Messages:       []dto.Message{{Role: "user", Content: "Return the exact nested JSON requested by the client."}},
				ResponseFormat: &dto.ResponseFormat{Type: "json_object"}}
			compiled, trace, err := CompileWorldbook(settings.Worldbook, settings.SillyTavernPreset, client, SillyTavernContext{})
			require.NoError(t, err)
			requirements, err := SnapshotClientRequirements(client)
			require.NoError(t, err)
			require.NoError(t, SealClientRequirements(requirements, compiled, settings.ResponseTextFilter, trace.ScriptResponse))
			expected := `{"nested":[null,{"path":"C:\\notes","literal":"\\n","markup":"<game>keep</game>"}]}`
			args, err := common.Marshal(map[string]string{"think_nya": "auxiliary, not the result", "game_content": expected})
			require.NoError(t, err)
			call := map[string]any{"id": "reply-actual", "type": "function", "function": map[string]string{"name": "soliumbra_reply", "arguments": string(args)}}
			field := "message"
			if stream {
				field, call["index"] = "delta", 0
			}
			body, err := common.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "finish_reason": "tool_calls", field: map[string]any{"role": "assistant", "tool_calls": []any{call}}}}})
			require.NoError(t, err)
			if stream {
				body = []byte("data: " + string(body) + "\n\ndata: [DONE]\n\n")
			}
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx.Set(ClientRequirementsKey, requirements)
			AttachPresetScriptResponse(ctx, trace)
			writer := BeginResponseTextFilterWithPreset(ctx, settings.ResponseTextFilter, settings.SillyTavernPreset, client.Model)
			require.NotNil(t, writer)
			result, err := writer.processResponse(body, stream)
			require.NoError(t, err)
			choices, err := responseChoices(result, stream)
			require.NoError(t, err)
			require.Equal(t, expected, choices["0"].text.String())
			require.Empty(t, choices["0"].tools)
			require.True(t, writer.outputProcessed)
		})
	}
}

// Exported channel settings contain no credentials and remain local. This
// reproduces compilation without transmitting private preset prompts upstream.
func TestRelayExportedChannelCompilation(t *testing.T) {
	path := os.Getenv("NEW_API_EXPORTED_CHANNEL_SETTINGS")
	if path == "" {
		t.Skip("set NEW_API_EXPORTED_CHANNEL_SETTINGS to a local settings export")
	}
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var channels map[string]struct {
		Settings struct {
			Preset    *dto.SillyTavernPresetConfig `json:"sillytavern_preset"`
			Worldbook *dto.WorldbookConfig         `json:"worldbook"`
			Filter    *dto.ResponseTextFilter      `json:"response_text_filter"`
		} `json:"settings"`
	}
	require.NoError(t, common.Unmarshal(data, &channels))
	for id, channel := range channels {
		t.Run(id, func(t *testing.T) {
			request := &dto.GeneralOpenAIRequest{Model: "gemini-3.8-flash", Messages: []dto.Message{{Role: "user", Content: "Hello. Please describe the weather."}}}
			if requestPath := os.Getenv("NEW_API_EXPORTED_REQUEST"); requestPath != "" {
				body, readErr := os.ReadFile(requestPath)
				require.NoError(t, readErr)
				require.NoError(t, common.Unmarshal(body, request))
			}
			variants := []string{"plain"}
			if selected := os.Getenv("NEW_API_EXPORTED_VARIANTS"); selected != "" {
				variants = strings.Split(selected, ",")
			}
			for _, variant := range variants {
				t.Run(variant, func(t *testing.T) {
					candidate, copyErr := cloneGeneralRequest(request)
					require.NoError(t, copyErr)
					if slices.Contains([]string{"none", "auto", "required", "named", "history", "history-none"}, variant) {
						candidate.ToolChoice = variant
						candidate.Tools = []dto.ToolCallRequest{{Type: "function", Function: dto.FunctionRequest{Name: "lookup", Parameters: []byte(`{"type":"object"}`)}}}
					}
					if variant == "named" {
						candidate.ToolChoice = map[string]any{"type": "function", "function": map[string]string{"name": "lookup"}}
					}
					if variant == "schema" {
						candidate.ToolChoice = "none"
						candidate.ResponseFormat = &dto.ResponseFormat{Type: "json_schema", JsonSchema: []byte(`{"name":"hours","strict":true,"schema":{"type":"object","properties":{"opens_at":{"type":"integer","const":9}},"required":["opens_at"],"additionalProperties":false}}`)}
					}
					if variant == "history" || variant == "history-none" {
						candidate.ToolChoice = "auto"
						if variant == "history-none" {
							candidate.ToolChoice = "none"
						}
						candidate.Messages = append(candidate.Messages, dto.Message{Role: "assistant", ToolCalls: []byte(`[{"id":"lookup_1","type":"function","function":{"name":"lookup","arguments":"{}"}}]`)}, dto.Message{Role: "tool", ToolCallId: "lookup_1", Content: `{"found":true}`}, dto.Message{Role: "user", Content: "Continue."})
					}
					if variant == "parts" {
						candidate.Messages = []dto.Message{{Role: "user", Content: []any{map[string]any{"type": "text", "text": "Hello."}}}}
					}
					if variant == "large" {
						candidate.Messages = []dto.Message{{Role: "user", Content: strings.Repeat("ordinary text ", 75000)}}
					}
					compiled, trace, compileErr := CompileWorldbookFrom(channel.Settings.Worldbook, channel.Settings.Preset, candidate, candidate, SillyTavernContext{})
					for cause := compileErr; cause != nil; cause = errors.Unwrap(cause) {
						t.Logf("compilation cause: %v", cause)
					}
					require.NoError(t, compileErr)
					require.NotNil(t, trace.ScriptResponse)
					require.True(t, trace.ScriptResponse.requestHookExecuted)
					requirements, err := SnapshotClientRequirements(candidate)
					require.NoError(t, err)
					require.NoError(t, SealClientRequirements(requirements, compiled, channel.Settings.Filter, trace.ScriptResponse))
					if slices.Contains([]string{"auto", "required", "named", "history"}, variant) {
						require.True(t, samePresetValue(candidate.ToolChoice, compiled.ToolChoice))
						return
					}
					for _, stream := range []bool{false, true} {
						expected := "The library opens at nine."
						if variant == "schema" {
							expected = `{"opens_at":9}`
						}
						args, err := common.Marshal(map[string]string{"think_nya": "Ready.", "game_content": expected})
						require.NoError(t, err)
						call := map[string]any{"id": "reply_1", "type": "function", "function": map[string]string{"name": trace.ScriptResponse.protocol.contents[0], "arguments": string(args)}}
						field := "message"
						if stream {
							field, call["index"] = "delta", 0
						}
						body, err := common.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "finish_reason": "tool_calls", field: map[string]any{"role": "assistant", "tool_calls": []any{call}}}}})
						require.NoError(t, err)
						if responsePath := os.Getenv("NEW_API_EXPORTED_UPSTREAM_RESPONSE"); responsePath != "" {
							captured, readErr := os.ReadFile(responsePath)
							require.NoError(t, readErr)
							var envelope map[string]any
							require.NoError(t, common.Unmarshal(captured, &envelope))
							choices := envelope["choices"].([]any)
							require.Len(t, choices, 1)
							choice := choices[0].(map[string]any)
							message := choice["message"].(map[string]any)
							tools := message["tool_calls"].([]any)
							require.Len(t, tools, 1)
							tool := tools[0].(map[string]any)
							function := tool["function"].(map[string]any)
							var arguments map[string]string
							require.NoError(t, common.Unmarshal([]byte(function["arguments"].(string)), &arguments))
							expected = arguments["game_content"]
							require.NotEmpty(t, expected)
							if stream {
								tool["index"] = 0
								choice["delta"] = message
								delete(choice, "message")
							}
							body, err = common.Marshal(envelope)
							require.NoError(t, err)
						}
						if stream {
							body = []byte("data: " + string(body) + "\n\ndata: [DONE]\n\n")
						}
						ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
						ctx.Set(ClientRequirementsKey, requirements)
						AttachPresetScriptResponse(ctx, trace)
						writer := BeginResponseTextFilterWithPreset(ctx, channel.Settings.Filter, channel.Settings.Preset, candidate.Model)
						require.NotNil(t, writer)
						result, err := writer.processResponse(body, stream)
						require.NoError(t, err)
						choices, err := responseChoices(result, stream)
						require.NoError(t, err)
						require.Equal(t, expected, strings.TrimSpace(choices["0"].text.String()))
						require.Empty(t, choices["0"].tools)
						require.True(t, writer.outputProcessed)
					}
				})
			}
		})
	}
}

// The optional browser bridge keeps authentication in an already logged-in
// browser. By default this uses a neutral fixture. NEW_API_CORE_CHANNEL opts in
// to the user's locally prepared preset/channel edition; it never updates a
// production channel or exports authentication through the local bridge.
func TestRelayLiveContractAcceptance(t *testing.T) {
	if os.Getenv("NEW_API_ACCEPTANCE_LIVE") != "1" {
		t.Skip("set NEW_API_ACCEPTANCE_LIVE=1 and connect the authenticated browser bridge")
	}
	type exchangeResult struct {
		Status int    `json:"status"`
		Body   string `json:"body"`
	}
	pending := make(chan []byte)
	returned := make(chan exchangeResult, 1)
	deadline, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			select {
			case body := <-pending:
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write(body)
			case <-r.Context().Done():
			case <-deadline.Done():
				w.WriteHeader(http.StatusGone)
			}
		case http.MethodPost:
			var result exchangeResult
			if err := common.DecodeJson(io.LimitReader(r.Body, 4<<20), &result); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			select {
			case returned <- result:
				w.WriteHeader(http.StatusNoContent)
			case <-deadline.Done():
				w.WriteHeader(http.StatusGone)
			}
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer func() { cancel(); server.Close() }()
	t.Logf("LIVE_BRIDGE=%s", server.URL)
	config := scriptPresetConfig(t, map[string]any{
		"ToolBindings": map[string]any{"reply": map[string]any{"enabled": true,
			"code": `return {name:"preset_reply",description:"Return the completed answer to the user, in body. Use this for final replies, not business actions.",parameters:{type:"object",properties:{body:{type:"string"}},required:["body"]}};`}},
		"FinalRequestProcessing": map[string]any{"enabled": true, "script": `({request,client}) => {
			request.messages.unshift({role:"system",content:"You are a library assistant. Use preset_reply to deliver completed answers. Keep requested JSON solely inside its body field. Use lookup only when the task asks for live data and no tool result has been provided. Do not invent tool results."});
			request.tool_choice = client.tools?.length && client.tool_choice !== "none" ? (client.tool_choice ?? "auto") : {type:"function",function:{name:"preset_reply"}};
			return request;
		}`},
		"OutputPreprocessing": map[string]any{"enabled": true, "consumeToolCalls": true,
			"script":            `({buffer}) => {if(!buffer.startsWith("<answer>")) throw Error("formatter was skipped");return {output:buffer,state:{}};}`,
			"toolCallFormatter": map[string]any{"enabled": true, "script": `({calls}) => "<answer>"+calls[0].arguments.body+"</answer>"`}},
	})
	config.ReplyAdapter = &dto.ReplyAdapter{Version: 2, StructuredOutput: true,
		Containers: []dto.ReplyContainer{{Name: "preset_reply", Formatter: true, OutputKind: "text"}}}
	lore := &dto.WorldbookConfig{Enabled: true, SemanticsVersion: 2, Books: []dto.Worldbook{{ID: "library", Enabled: true,
		Entries: []dto.WorldbookEntry{{ID: "code", Enabled: true, Keys: []string{"library"}, Content: "The library access code is MAPLE-7."}}}}}
	filter := &dto.ResponseTextFilter{Mode: "rules", Rules: []dto.TextRegexRule{{ID: "answer", Stage: "receive", Action: "extract",
		Pattern: `/<answer>([\s\S]*?)<\/answer>/g`, Replacement: "$1", MissingMatch: "error"}}}
	if path := os.Getenv("NEW_API_CORE_CHANNEL"); path != "" {
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		var channel struct {
			Settings string `json:"settings"`
		}
		require.NoError(t, common.Unmarshal(data, &channel))
		var settings dto.ChannelOtherSettings
		require.NoError(t, common.UnmarshalJsonStr(channel.Settings, &settings))
		config, filter = settings.SillyTavernPreset, settings.ResponseTextFilter
		require.NotNil(t, config.ReplyAdapter)
		require.True(t, config.ReplyAdapter.RequireContainer)
		// Keep the actual channel books, then add a bounded benign fixture for
		// deterministic activation. No acceptance entry is saved to the channel.
		if settings.Worldbook != nil && settings.Worldbook.Enabled {
			settings.Worldbook.Books = append(settings.Worldbook.Books, lore.Books...)
			lore = settings.Worldbook
		}
		t.Logf("actual relay edition acceptance: channel_sha256=%x", sha256.Sum256(data))
	}
	run := func(request *dto.GeneralOpenAIRequest) *responseChoice {
		t.Helper()
		compiled, trace, err := CompileWorldbook(lore, config, request, SillyTavernContext{})
		require.NoError(t, err)
		require.True(t, trace.ScriptResponse.requestHookExecuted)
		require.True(t, trace.ScriptResponse.requestMessagesChanged)
		require.NotEmpty(t, trace.ScriptResponse.ownedTools)
		requirements, err := SnapshotClientRequirements(request)
		require.NoError(t, err)
		require.NoError(t, SealClientRequirements(requirements, compiled, filter, trace.ScriptResponse))
		body, err := common.Marshal(compiled)
		require.NoError(t, err)
		select {
		case pending <- body:
		case <-deadline.Done():
			t.Fatal("browser did not collect the live request")
		}
		var response exchangeResult
		select {
		case response = <-returned:
		case <-deadline.Done():
			t.Fatal("browser did not return the model response")
		}
		require.Equal(t, http.StatusOK, response.Status, "upstream failed: %s", response.Body)
		ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
		ctx.Set(ClientRequirementsKey, requirements)
		AttachPresetScriptResponse(ctx, trace)
		writer := BeginResponseTextFilterWithPreset(ctx, filter, config, request.Model)
		require.NotNil(t, writer)
		stream := request.Stream != nil && *request.Stream
		upstream, err := responseChoices([]byte(response.Body), stream)
		require.NoError(t, err)
		require.NotEmpty(t, upstream["0"].tools, "live acceptance must prove tools were actually used, not just a successful text answer")
		processed, err := writer.processResponse([]byte(response.Body), stream)
		require.NoError(t, err)
		choices, err := responseChoices(processed, stream)
		require.NoError(t, err)
		require.Len(t, choices, 1)
		choice := choices["0"]
		require.NotNil(t, choice)
		if len(choice.tools) == 0 {
			require.True(t, writer.outputProcessed, "the output hook must run, not just request preprocessing")
		}
		return choice
	}
	newRequest := func(prompt string) *dto.GeneralOpenAIRequest {
		return &dto.GeneralOpenAIRequest{Model: "gemini-3.8-flash", ToolChoice: "none", Messages: []dto.Message{{Role: "user", Content: prompt}}}
	}
	if os.Getenv("NEW_API_ACCEPTANCE_CASES") != "ending" {
		plain := newRequest("At the library, my favorite color is blue. Reply with only the access code.")
		first := run(plain)
		require.Equal(t, "MAPLE-7", strings.TrimSpace(first.text.String()))
		plain.Messages = append(plain.Messages, dto.Message{Role: "assistant", Content: first.text.String()}, dto.Message{Role: "user", Content: "What color did I give you? Reply with one word."})
		require.Equal(t, "blue", strings.ToLower(strings.TrimSpace(run(plain).text.String())))
		structured := newRequest(`Return {"opens_at":9,"note":"C:\\notes\\new.txt"} exactly.`)
		structured.Stream = new(true)
		structured.ResponseFormat = &dto.ResponseFormat{Type: "json_schema", JsonSchema: []byte(`{"name":"hours","schema":{"type":"object","properties":{"opens_at":{"const":9},"note":{"const":"C:\\notes\\new.txt"}},"required":["opens_at","note"],"additionalProperties":false}}`)}
		require.JSONEq(t, `{"opens_at":9,"note":"C:\\notes\\new.txt"}`, run(structured).text.String())
		variable := newRequest("The client state is {\"count\":2}. Return one JSON Patch replace operation setting count to 3, and nothing else.")
		variable.ResponseFormat = &dto.ResponseFormat{Type: "json_schema", JsonSchema: []byte(`{"name":"patch","schema":{"type":"array","minItems":1,"maxItems":1,"items":{"type":"object","properties":{"op":{"const":"replace"},"path":{"const":"/count"},"value":{"const":3}},"required":["op","path","value"],"additionalProperties":false}}}`)}
		var patch []struct {
			Op    string `json:"op"`
			Path  string `json:"path"`
			Value int    `json:"value"`
		}
		require.NoError(t, common.Unmarshal([]byte(run(variable).text.String()), &patch))
		require.Len(t, patch, 1)
		require.Equal(t, "replace", patch[0].Op)
		require.Equal(t, "/count", patch[0].Path)
		clientState := map[string]int{"count": 2}
		clientState["count"] = patch[0].Value
		require.Equal(t, 3, clientState["count"], "the client must be able to apply the returned value")
		for _, selection := range []any{"auto", "required", map[string]any{"type": "function", "function": map[string]string{"name": "lookup"}}} {
			business := newRequest("Call lookup to get the live library opening hour for place library. Do not answer from memory.")
			business.Tools = []dto.ToolCallRequest{{Type: "function", Function: dto.FunctionRequest{Name: "lookup", Description: "Get the live opening hour for a place.", Parameters: []byte(`{"type":"object","properties":{"place":{"type":"string"}},"required":["place"]}`)}}}
			business.ToolChoice = selection
			businessReply := run(business)
			require.Len(t, businessReply.tools, 1)
			call := businessReply.tools["0"]
			require.NotNil(t, call)
			require.Equal(t, "lookup", call.name.String())
			require.JSONEq(t, `{"place":"library"}`, call.arguments.String())
			if selection != "auto" {
				continue
			}
			calls, err := common.Marshal([]any{map[string]any{"id": call.id, "type": "function", "function": map[string]string{"name": call.name.String(), "arguments": call.arguments.String()}}})
			require.NoError(t, err)
			business.Messages = append(business.Messages, dto.Message{Role: "assistant", ToolCalls: calls}, dto.Message{Role: "tool", ToolCallId: call.id, Content: `{"opens_at":11}`}, dto.Message{Role: "user", Content: "Use the tool result. Reply with only its opening-hour integer."})
			business.ToolChoice = "none"
			require.Equal(t, "11", strings.TrimSpace(run(business).text.String()))
		}
	}
	if os.Getenv("NEW_API_CORE_CHANNEL") != "" {
		literal := newRequest(`Return exactly this text with no explanation: <game>literal \\n C:\notes</game>`)
		assert.Equal(t, `<game>literal \\n C:\notes</game>`, strings.TrimSpace(run(literal).text.String()), "verbatim model accuracy is checked independently; do not hide this failure or skip the remaining business cases")
		variable := newRequest(`The client state is {"credits":10,"items":[]}. Spend 2 credits and add "map". Return exactly the resulting state as JSON, not a story or explanation.`)
		variable.ResponseFormat = &dto.ResponseFormat{Type: "json_schema", JsonSchema: []byte(`{"name":"state","schema":{"type":"object","properties":{"credits":{"const":8},"items":{"type":"array","items":{"const":"map"},"minItems":1,"maxItems":1}},"required":["credits","items"],"additionalProperties":false}}`)}
		require.JSONEq(t, `{"credits":8,"items":["map"]}`, run(variable).text.String())
		unchanged := newRequest(`The client state is {"credits":8,"items":["map"]}. Nothing happened. Return a JSON Patch array describing necessary changes, and nothing else. Do not invent a change.`)
		unchanged.ResponseFormat = &dto.ResponseFormat{Type: "json_schema", JsonSchema: []byte(`{"name":"no_changes","schema":{"type":"array","maxItems":0}}`)}
		require.JSONEq(t, `[]`, run(unchanged).text.String())
		creative := newRequest("Write two short sentences about SoliUmbra finding a blue door in a library. Only the story, no notes or summary.")
		story := run(creative).text.String()
		require.NotEmpty(t, story)
		require.NotContains(t, story, "<think_nya")
		require.NotContains(t, story, "<game>")
	}
}

// This opt-in check runs the imported data hooks unchanged, offline. Prompt
// material and browser scripts are deliberately not replayed or sent to a model.
// Passing it is not acceptance of the complete private preset.
func TestRelayImportedCoreOffline(t *testing.T) {
	path := os.Getenv("NEW_API_ACCEPTANCE_PRESET")
	if path == "" {
		t.Skip("set NEW_API_ACCEPTANCE_PRESET to run local imported-core checks")
	}
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var envelope struct {
		Extensions struct {
			SPreset map[string]any `json:"SPreset"`
		} `json:"extensions"`
	}
	require.NoError(t, common.Unmarshal(data, &envelope))
	bindings, ok := envelope.Extensions.SPreset["ToolBindings"].(map[string]any)
	require.True(t, ok)
	identifiers := make([]string, 0, len(bindings))
	for identifier := range bindings {
		identifiers = append(identifiers, identifier)
	}
	slices.Sort(identifiers)
	require.NotEmpty(t, identifiers)
	prompts := []any{map[string]any{"identifier": "chatHistory", "marker": true}}
	order := []any{}
	for _, identifier := range identifiers {
		prompts = append(prompts, map[string]any{"identifier": identifier, "role": "system", "content": "<run_tool_replacement>"})
		order = append(order, map[string]any{"identifier": identifier, "enabled": true})
	}
	order = append(order, map[string]any{"identifier": "chatHistory", "enabled": true})
	fixture, err := common.Marshal(map[string]any{
		"prompts": prompts, "prompt_order": []any{map[string]any{"order": order}},
		"extensions": map[string]any{"SPreset": envelope.Extensions.SPreset},
	})
	require.NoError(t, err)
	config := &dto.SillyTavernPresetConfig{Preset: fixture, ScriptRuntime: "spreset-v1"}
	// Source discovery uses the same compiler, without a client delivery override.
	request := &dto.GeneralOpenAIRequest{Model: "m", Messages: []dto.Message{{Role: "user", Content: "Remember that the library opens at nine."}}}
	compiled, trace, err := compileSillyTavernPreset(config, request, SillyTavernContext{})
	require.NoError(t, err)
	require.NotNil(t, trace.ScriptResponse)
	require.True(t, trace.ScriptResponse.requestHookExecuted)
	require.True(t, trace.ScriptResponse.requestMessagesChanged)
	require.Len(t, trace.ScriptResponse.replySources, 1)
	for _, source := range trace.ScriptResponse.replySources {
		config.ReplyAdapter = &dto.ReplyAdapter{Version: 2, Containers: []dto.ReplyContainer{source}}
	}
	request.ToolChoice = "none"
	compiled, trace, err = CompileSillyTavernPreset(config, request, SillyTavernContext{})
	require.NoError(t, err)
	require.NotNil(t, trace.ScriptResponse.generationRequest)
	require.NotEmpty(t, presetToolChoiceName(compiled.ToolChoice))
	requirements, err := SnapshotClientRequirements(request)
	require.NoError(t, err)
	require.NoError(t, SealClientRequirements(requirements, compiled, nil, trace.ScriptResponse))
	writer := &ResponseTextFilterWriter{requirements: requirements, scriptResponse: trace.ScriptResponse}
	name := presetToolChoiceName(compiled.ToolChoice)
	body := []byte(fmt.Sprintf(`{"choices":[{"index":0,"finish_reason":"tool_calls","message":{"role":"assistant","tool_calls":[{"id":"reply","type":"function","function":{"name":%q,"arguments":"{\"think_nya\":\"Ready.\",\"game_content\":\"The library opens at nine.\"}"}}]}}]}`, name))
	result, err := writer.processResponse(body, false)
	require.NoError(t, err)
	choices, err := responseChoices(result, false)
	require.NoError(t, err)
	require.Equal(t, "<think_nya>\nReady.\n</think_nya>\n<game>\nThe library opens at nine.\n</game>", choices["0"].text.String())
	require.Empty(t, choices["0"].tools)
	require.True(t, writer.outputProcessed, "the enabled imported output hook must actually run")
	t.Run("schema through unchanged formatter and output hook", func(t *testing.T) {
		configured := *config
		adapter := *config.ReplyAdapter
		adapter.StructuredOutput = true
		configured.ReplyAdapter = &adapter
		structured, err := cloneGeneralRequest(request)
		require.NoError(t, err)
		structured.ResponseFormat = &dto.ResponseFormat{Type: "json_schema", JsonSchema: []byte(`{"name":"hours","strict":true,"schema":{"type":"object","properties":{"opens_at":{"type":"integer","const":9}},"required":["opens_at"],"additionalProperties":false}}`)}
		compiled, trace, err := CompileSillyTavernPreset(&configured, structured, SillyTavernContext{})
		require.NoError(t, err)
		require.True(t, trace.ScriptResponse.requestHookExecuted)
		require.Nil(t, compiled.ResponseFormat)
		require.Contains(t, compiled.Messages[len(compiled.Messages)-1].Content, `"opens_at"`)
		filter := &dto.ResponseTextFilter{Mode: "rules", Rules: []dto.TextRegexRule{
			{ID: "reply", Stage: "receive", Action: "extract", Pattern: `/<game>\s*([\s\S]*?)\s*<\/game>/g`, Replacement: "$1", MissingMatch: "error"},
		}}
		requirements, err := SnapshotClientRequirements(structured)
		require.NoError(t, err)
		require.NoError(t, SealClientRequirements(requirements, compiled, filter, trace.ScriptResponse))
		ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
		ctx.Set(ClientRequirementsKey, requirements)
		AttachPresetScriptResponse(ctx, trace)
		writer := BeginResponseTextFilterWithPreset(ctx, filter, &configured, "m")
		require.NotNil(t, writer)
		args, err := common.Marshal(map[string]string{"think_nya": "Ready.", "game_content": `{"opens_at":9}`})
		require.NoError(t, err)
		body, err := common.Marshal(map[string]any{"choices": []any{map[string]any{
			"index": 0, "finish_reason": "tool_calls", "message": map[string]any{"role": "assistant", "tool_calls": []any{
				map[string]any{"id": "reply", "type": "function", "function": map[string]string{"name": name, "arguments": string(args)}},
			}},
		}}})
		require.NoError(t, err)
		result, err := writer.processResponse(body, false)
		require.NoError(t, err)
		choices, err := responseChoices(result, false)
		require.NoError(t, err)
		require.JSONEq(t, `{"opens_at":9}`, choices["0"].text.String())
		require.Empty(t, choices["0"].tools)
		require.True(t, writer.outputProcessed)
	})
	continued, err := cloneGeneralRequest(request)
	require.NoError(t, err)
	continued.Messages = append(continued.Messages,
		dto.Message{Role: "assistant", ToolCalls: []byte(`[{"id":"library_1","type":"function","function":{"name":"lookup","arguments":"{}"}}]`)},
		dto.Message{Role: "tool", ToolCallId: "library_1", Content: `{"opens_at":9}`},
		dto.Message{Role: "user", Content: "Use that result to answer."})
	_, _, err = CompileSillyTavernPreset(config, continued, SillyTavernContext{})
	require.ErrorContains(t, err, "tool history was removed, reordered or modified",
		"the imported hook rebuilds history without preserving business-call associations; do not fabricate them")
	t.Run("existing auto control does not repair tool history", func(t *testing.T) {
		var controlled map[string]any
		require.NoError(t, common.Unmarshal(fixture, &controlled))
		controlled["prompts"] = append(controlled["prompts"].([]any), map[string]any{
			"identifier": "tool-selection", "role": "system", "content": "<<<|catgodml|autotool>>>",
		})
		controlOrder := controlled["prompt_order"].([]any)[0].(map[string]any)
		controlOrder["order"] = append([]any{map[string]any{"identifier": "tool-selection", "enabled": true}}, controlOrder["order"].([]any)...)
		preset, err := common.Marshal(controlled)
		require.NoError(t, err)
		configured := *config
		configured.Preset = preset
		business, err := cloneGeneralRequest(request)
		require.NoError(t, err)
		business.ToolChoice = "auto"
		business.Tools = []dto.ToolCallRequest{{Type: "function", Function: dto.FunctionRequest{Name: "lookup", Parameters: []byte(`{"type":"object"}`)}}}
		compiled, trace, err := CompileSillyTavernPreset(&configured, business, SillyTavernContext{})
		require.NoError(t, err)
		require.Equal(t, "auto", compiled.ToolChoice)
		require.True(t, trace.ScriptResponse.requestHookExecuted)
		require.NotEmpty(t, trace.ScriptResponse.protocol.visible)
		requirements, err := SnapshotClientRequirements(business)
		require.NoError(t, err)
		require.NoError(t, SealClientRequirements(requirements, compiled, nil, trace.ScriptResponse))
		writer := &ResponseTextFilterWriter{requirements: requirements, scriptResponse: trace.ScriptResponse}
		body := []byte(`{"choices":[{"index":0,"finish_reason":"tool_calls","message":{"role":"assistant","tool_calls":[{"id":"lookup_1","type":"function","function":{"name":"lookup","arguments":"{}"}}]}}]}`)
		result, err := writer.processResponse(body, false)
		require.NoError(t, err)
		require.Equal(t, body, result, "the first business call can be delivered without disabling the request hook")
		business.Messages = continued.Messages
		_, _, err = CompileSillyTavernPreset(&configured, business, SillyTavernContext{})
		require.ErrorContains(t, err, "tool history was removed, reordered or modified")
	})
	parsed, err := config.ParseAndValidate()
	require.NoError(t, err)
	business, err := cloneGeneralRequest(request)
	require.NoError(t, err)
	business.ToolChoice = "required"
	business.Messages = append([]dto.Message{{Role: "system", Content: "<run_tool_replacement>"}}, business.Messages...)
	business.Tools = []dto.ToolCallRequest{{Type: "function", Function: dto.FunctionRequest{Name: "lookup", Parameters: []byte(`{"type":"object"}`)}}}
	_, _, err = preparePresetScripts(config, parsed, business, nil, nil)
	require.NoError(t, err)
	require.Equal(t, name, presetToolChoiceName(business.ToolChoice), "imported request hook forces its reply source instead of the client's business tool")
	core, err := common.Marshal(envelope.Extensions.SPreset)
	require.NoError(t, err)
	t.Logf("offline core only: preset_sha256=%x core_sha256=%x request_hook=%v formatter=%v output_hook=%v history_tools=%d", sha256.Sum256(data), sha256.Sum256(core), trace.ScriptResponse.requestHookExecuted, trace.ScriptResponse.settings.OutputPreprocessing.ToolCallFormatter.Enabled, trace.ScriptResponse.settings.OutputPreprocessing.Enabled, len(trace.ScriptResponse.historyTools))
	t.Log("capability gaps: default selection forces the internal source; the existing auto control permits a first business call but still removes native business history; P4 is not accepted")
}

func TestRelayUpgradedImportedCoreOffline(t *testing.T) {
	path := os.Getenv("NEW_API_ACCEPTANCE_UPGRADED_PRESET")
	if path == "" {
		t.Skip("set NEW_API_ACCEPTANCE_UPGRADED_PRESET to check the private protocol upgrade offline")
	}
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var imported struct {
		Extensions struct {
			SPreset map[string]any `json:"SPreset"`
		} `json:"extensions"`
	}
	require.NoError(t, common.Unmarshal(data, &imported))
	bindings, ok := imported.Extensions.SPreset["ToolBindings"].(map[string]any)
	require.True(t, ok)
	identifiers := make([]string, 0, len(bindings))
	for identifier := range bindings {
		identifiers = append(identifiers, identifier)
	}
	slices.Sort(identifiers)
	prompts := []any{
		map[string]any{"identifier": "chatHistory", "marker": true},
		map[string]any{"identifier": "history-start", "role": "system", "content": "Library background: the library opens at nine.\n<<<|catgodml|historyStart>>>"},
		map[string]any{"identifier": "history-end", "role": "system", "content": "<<<|catgodml|historyEnd>>>"},
	}
	order := []any{}
	for _, identifier := range identifiers {
		prompts = append(prompts, map[string]any{"identifier": identifier, "role": "system", "content": "<run_tool_replacement>"})
		order = append(order, map[string]any{"identifier": identifier, "enabled": true})
	}
	for _, identifier := range []string{"history-start", "chatHistory", "history-end"} {
		order = append(order, map[string]any{"identifier": identifier, "enabled": true})
	}
	fixture, err := common.Marshal(map[string]any{"prompts": prompts, "prompt_order": []any{map[string]any{"order": order}},
		"extensions": map[string]any{"SPreset": imported.Extensions.SPreset}})
	require.NoError(t, err)
	config := &dto.SillyTavernPresetConfig{Preset: fixture, ScriptRuntime: "spreset-v1"}
	initial := &dto.GeneralOpenAIRequest{Model: "m", Messages: []dto.Message{{Role: "user", Content: "Find the library opening time."}},
		Tools: []dto.ToolCallRequest{{Type: "function", Function: dto.FunctionRequest{Name: "lookup", Parameters: []byte(`{"type":"object","properties":{"place":{"type":"string"}},"required":["place"]}`)}}}, ToolChoice: "auto"}
	lore := &dto.WorldbookConfig{Enabled: true, SemanticsVersion: 2, InjectionMode: dto.WorldbookInjectionSystemPrelude,
		Books: []dto.Worldbook{{ID: "library", Enabled: true, Entries: []dto.WorldbookEntry{
			{ID: "access", Enabled: true, Keys: []string{"library"}, Content: "Library access code: MAPLE-7."},
		}}}}
	_, discovery, err := compileSillyTavernPreset(config, initial, SillyTavernContext{})
	require.NoError(t, err)
	require.Len(t, discovery.ScriptResponse.replySources, 1)
	for _, source := range discovery.ScriptResponse.replySources {
		config.ReplyAdapter = &dto.ReplyAdapter{Version: 2, StructuredOutput: true, Containers: []dto.ReplyContainer{source}}
	}
	filter := &dto.ResponseTextFilter{Mode: "rules", Rules: []dto.TextRegexRule{{ID: "reply", Stage: "receive", Action: "extract",
		Pattern: `/<game>\s*([\s\S]*?)\s*<\/game>/g`, Replacement: "$1", MissingMatch: "error"}}}
	for _, selection := range []any{"auto", "required", map[string]any{"type": "function", "function": map[string]string{"name": "lookup"}}} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("choice=%v/stream=%v", selection, stream), func(t *testing.T) {
				request, err := cloneGeneralRequest(initial)
				require.NoError(t, err)
				request.ToolChoice = selection
				compiled, trace, err := CompileWorldbook(lore, config, request, SillyTavernContext{})
				require.NoError(t, err)
				require.NotNil(t, trace.Worldbook)
				require.True(t, trace.Worldbook.Entries[0].Inserted)
				require.Contains(t, fmt.Sprint(compiled.Messages), "MAPLE-7", "matched lore must reach the request script")
				require.True(t, trace.ScriptResponse.requestHookExecuted)
				require.True(t, trace.ScriptResponse.requestMessagesChanged)
				require.True(t, samePresetValue(selection, compiled.ToolChoice))
				requirements, err := SnapshotClientRequirements(request)
				require.NoError(t, err)
				require.NoError(t, SealClientRequirements(requirements, compiled, filter, trace.ScriptResponse))
				call := map[string]any{"id": "lookup_1", "type": "function", "function": map[string]string{"name": "lookup", "arguments": `{"place":"library"}`}}
				message := map[string]any{"role": "assistant", "tool_calls": []any{call}}
				field := "message"
				if stream {
					field = "delta"
					call["index"] = 0
				}
				body, err := common.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "finish_reason": "tool_calls", field: message}}})
				require.NoError(t, err)
				if stream {
					body = []byte("data: " + string(body) + "\n\ndata: [DONE]\n\n")
				}
				writer := &ResponseTextFilterWriter{requirements: requirements, scriptResponse: trace.ScriptResponse}
				result, err := writer.processResponse(body, stream)
				require.NoError(t, err)
				require.Equal(t, body, result, "genuine business calls must not be formatted as preset replies")
				choices, err := responseChoices(result, stream)
				require.NoError(t, err)
				returnedCall := choices["0"].tools["0"]
				require.NotNil(t, returnedCall)
				calls, err := common.Marshal([]any{map[string]any{"id": returnedCall.id, "type": "function", "function": map[string]string{
					"name": returnedCall.name.String(), "arguments": returnedCall.arguments.String(),
				}}})
				require.NoError(t, err)
				// Each case continues with a different current-round selection.
				// Historical business calls remain valid even when no longer callable.
				request.ToolChoice = "auto"
				if selection == "required" {
					request.ToolChoice = "none"
				} else if _, named := selection.(map[string]any); named {
					request.ToolChoice, request.Tools = nil, nil
				}
				request.Messages = append(request.Messages, dto.Message{Role: "assistant", ToolCalls: calls},
					dto.Message{Role: "tool", ToolCallId: returnedCall.id, Content: `{"opens_at":9,"note":"Line one\nLine two: \"quoted\""}`},
					dto.Message{Role: "user", Content: "Use the tool result and return the opening time as JSON."})
				request.ResponseFormat = &dto.ResponseFormat{Type: "json_schema", JsonSchema: []byte(`{"name":"hours","strict":true,"schema":{"type":"object","properties":{"opens_at":{"const":9}},"required":["opens_at"],"additionalProperties":false}}`)}
				before, err := common.Marshal(request)
				require.NoError(t, err)
				compiled, trace, err = CompileWorldbook(lore, config, request, SillyTavernContext{})
				require.NoError(t, err)
				require.True(t, trace.ScriptResponse.requestHookExecuted)
				require.True(t, trace.ScriptResponse.requestMessagesChanged)
				require.Equal(t, request.Messages, compiled.Messages[4:4+len(request.Messages)], "native dialogue must remain complete, ordered and present exactly once")
				require.Contains(t, compiled.Messages[3].Content, "the library opens at nine", "background compilation must remain active")
				require.Contains(t, compiled.Messages[3].Content, "MAPLE-7", "worldbook must survive the native-history branch")
				require.Len(t, compiled.Messages, 4+len(request.Messages)+1, "no compressed replay or synthetic final user turn")
				after, err := common.Marshal(request)
				require.NoError(t, err)
				require.Equal(t, before, after)
				requirements, err = SnapshotClientRequirements(request)
				require.NoError(t, err)
				require.NoError(t, SealClientRequirements(requirements, compiled, filter, trace.ScriptResponse))
				ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
				ctx.Set(ClientRequirementsKey, requirements)
				AttachPresetScriptResponse(ctx, trace)
				writer = BeginResponseTextFilterWithPreset(ctx, filter, config, "m")
				require.NotNil(t, writer)
				var name string
				for candidate := range trace.ScriptResponse.replySources {
					name = candidate
				}
				args, err := common.Marshal(map[string]string{"think_nya": "Ready.", "game_content": `{"opens_at":9}`})
				require.NoError(t, err)
				call = map[string]any{"id": "final", "type": "function", "function": map[string]string{"name": name, "arguments": string(args)}}
				if stream {
					call["index"] = 0
				}
				body, err = common.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "finish_reason": "tool_calls", field: map[string]any{"role": "assistant", "tool_calls": []any{call}}}}})
				require.NoError(t, err)
				if stream {
					body = []byte("data: " + string(body) + "\n\ndata: [DONE]\n\n")
				}
				result, err = writer.processResponse(body, stream)
				require.NoError(t, err)
				choices, err = responseChoices(result, stream)
				require.NoError(t, err)
				require.JSONEq(t, `{"opens_at":9}`, choices["0"].text.String())
				require.Empty(t, choices["0"].tools)
				require.True(t, writer.outputProcessed)
			})
		}
	}
	t.Logf("offline upgraded core only: preset_sha256=%x; no model or browser execution", sha256.Sum256(data))
}

func TestPresetRequestHookClientView(t *testing.T) {
	settings := map[string]any{"FinalRequestProcessing": map[string]any{"enabled": true, "script": `({request,client,preset}) => {
		if (!Object.isFrozen(client) || !Object.isFrozen(client.messages) || !Object.isFrozen(client.tools[0].function)) throw Error("mutable client view");
		if (!Object.isFrozen(preset) || !Object.isFrozen(preset.prompt_order[0].order)) throw Error("mutable preset view");
		if (preset.prompt_order[0].order.find(p=>p.identifier==="reply").enabled !== false) throw Error("entry override missing");
		if (client.messages.length !== 1 || client.messages[0].content !== "effective history") throw Error("wrong history");
		if (client.tools.length !== 1 || client.tools[0].function.name !== "lookup") throw Error("wrong client tools");
		if (request.messages.length < client.messages.length) throw Error("preset assembly missing");
		request.messages.push({role:"system",content:"client-view-verified"}); return request;
	}`}}
	config := scriptPresetConfig(t, settings)
	config.EntryOverrides = map[string]bool{"reply": false}
	request := &dto.GeneralOpenAIRequest{Model: "m", Messages: []dto.Message{{Role: "user", Content: "raw history"}},
		Tools: []dto.ToolCallRequest{{Type: "function", Function: dto.FunctionRequest{Name: "lookup", Parameters: []byte(`{"type":"object"}`)}}}, ToolChoice: "auto"}
	before, err := common.Marshal(request)
	require.NoError(t, err)
	compiled, trace, err := CompileSillyTavernPreset(config, request, SillyTavernContext{ChatHistory: []dto.Message{{Role: "user", Content: "effective history"}}})
	require.NoError(t, err)
	require.True(t, trace.ScriptResponse.requestHookExecuted)
	require.Equal(t, "client-view-verified", compiled.Messages[len(compiled.Messages)-1].Content)
	after, err := common.Marshal(request)
	require.NoError(t, err)
	require.Equal(t, before, after)
	for _, mutation := range []string{`client.messages[0].content="changed"`, `client.tools.push({})`, `client.tools[0].function.name="claimed"`, `preset.prompt_order[0].order.push({})`, `preset.prompts[0].content="changed"`} {
		t.Run(mutation, func(t *testing.T) {
			settings["FinalRequestProcessing"].(map[string]any)["script"] = `({request,client,preset})=>{` + mutation + `;return request;}`
			_, _, err := CompileSillyTavernPreset(scriptPresetConfig(t, settings), request, SillyTavernContext{})
			require.ErrorContains(t, err, "preset script processing failed")
			after, err := common.Marshal(request)
			require.NoError(t, err)
			require.Equal(t, before, after)
		})
	}
	t.Run("original delivery controls survive internal compilation", func(t *testing.T) {
		settings := map[string]any{
			"ToolBindings": map[string]any{"reply": map[string]any{"enabled": true, "code": `return {name:"preset_reply",parameters:{type:"object",properties:{body:{type:"string"}}}};`}},
			"FinalRequestProcessing": map[string]any{"enabled": true, "script": `({request,client})=>{
				if(client.tool_choice!=="none" || client.response_format.type!=="json_object" || client.tools[0].function.name!=="lookup") throw Error("client controls lost");
				if(request.response_format || request.tools.some(t=>t.function.name==="lookup")) throw Error("generation not detached");
				request.tool_choice={type:"function",function:{name:"preset_reply"}};return request;
			}`},
		}
		configured := scriptPresetConfig(t, settings)
		configured.ReplyAdapter = &dto.ReplyAdapter{Version: 2, StructuredOutput: true, Containers: []dto.ReplyContainer{{Name: "preset_reply", ArgumentPointer: new("/body"), OutputKind: "text"}}}
		client, err := cloneGeneralRequest(request)
		require.NoError(t, err)
		client.ToolChoice = "none"
		client.ResponseFormat = &dto.ResponseFormat{Type: "json_object"}
		compiled, trace, err := CompileSillyTavernPreset(configured, client, SillyTavernContext{})
		require.NoError(t, err)
		require.True(t, trace.ScriptResponse.requestHookExecuted)
		require.Equal(t, "preset_reply", presetToolChoiceName(compiled.ToolChoice))
		require.Nil(t, compiled.ResponseFormat)
	})
}

func TestRelayNoneKeepsInternalReplyPipeline(t *testing.T) {
	preset := scriptPresetConfig(t, map[string]any{
		"ToolBindings": map[string]any{"reply": map[string]any{"enabled": true,
			"code": `return {name:"preset_reply",parameters:{type:"object",properties:{body:{type:"string"}}}};`}},
		"FinalRequestProcessing": map[string]any{"enabled": true, "script": `({request}) => {
			request.messages = request.messages.map(m => typeof m.content === "string" ? {...m,content:m.content.replaceAll("RAW","NORMALIZED")} : m);
			request.tool_choice = {type:"function",function:{name:"preset_reply"}}; return request;
		}`},
		"OutputPreprocessing": map[string]any{"enabled": true, "consumeToolCalls": true,
			"script":            `({buffer}) => { if (!buffer.startsWith("<answer>")) throw Error("formatter missing"); return {output:buffer.replace("raw-reply","processed-reply"),state:{}}; }`,
			"toolCallFormatter": map[string]any{"enabled": true, "script": `({calls}) => "<answer>"+calls[0].arguments.body+"</answer>"`}},
	})
	preset.ReplyAdapter = &dto.ReplyAdapter{Version: 2, Containers: []dto.ReplyContainer{{Name: "preset_reply", Formatter: true, OutputKind: "text"}}}
	t.Run("structured conversion requires confirmation", func(t *testing.T) {
		_, _, err := CompileSillyTavernPreset(preset, &dto.GeneralOpenAIRequest{
			Model: "m", ToolChoice: "none", ResponseFormat: &dto.ResponseFormat{Type: "json_object"},
			Messages: []dto.Message{{Role: "user", Content: "Return the library opening time as JSON."}},
		}, SillyTavernContext{})
		require.ErrorContains(t, err, "confirmed reply-source format conversion")
	})
	for _, variant := range []string{"plain", "same-name", "history", "legacy", "escaped"} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%v", variant, stream), func(t *testing.T) {
				request := &dto.GeneralOpenAIRequest{Model: "m", ToolChoice: "none", Tools: []dto.ToolCallRequest{
					{Type: "function", Function: dto.FunctionRequest{Name: "lookup"}},
				}, Messages: []dto.Message{{Role: "user", Content: "RAW"}}}
				if variant == "same-name" {
					request.Tools[0].Function.Name = "preset_reply"
				}
				if variant == "legacy" {
					request.Tools, request.ToolChoice = nil, nil
					request.Functions = []byte(`[{"name":"lookup","parameters":{"type":"object"}}]`)
					request.FunctionCall = []byte(`"none"`)
				}
				if variant == "history" {
					request.Messages = append(request.Messages,
						dto.Message{Role: "assistant", ToolCalls: []byte(`[{"id":"lookup_1","type":"function","function":{"name":"lookup","arguments":"{}"}}]`)},
						dto.Message{Role: "tool", ToolCallId: "lookup_1", Content: `{"found":true}`},
						dto.Message{Role: "user", Content: "RAW continuation"})
				}
				before, err := common.Marshal(request)
				require.NoError(t, err)
				compiled, trace, err := CompileSillyTavernPreset(preset, request, SillyTavernContext{})
				require.NoError(t, err)
				require.Equal(t, "preset_reply", presetToolChoiceName(compiled.ToolChoice))
				require.Len(t, compiled.Tools, 1)
				require.Equal(t, "preset_reply", compiled.Tools[0].Function.Name)
				require.NotNil(t, trace.ScriptResponse.generationRequest)
				require.Contains(t, compiled.Messages[1].Content, "NORMALIZED")
				if variant == "history" {
					require.Equal(t, request.Messages[1:3], compiled.Messages[2:4])
				}
				after, err := common.Marshal(request)
				require.NoError(t, err)
				require.Equal(t, before, after)
				ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
				requirements, err := SnapshotClientRequirements(request)
				require.NoError(t, err)
				require.NoError(t, SealClientRequirements(requirements, compiled, nil, trace.ScriptResponse))
				mutated, err := cloneGeneralRequest(compiled)
				require.NoError(t, err)
				mutated.ToolChoice = "none"
				require.Error(t, SealClientRequirements(requirements, mutated, nil, trace.ScriptResponse))
				require.Error(t, SealClientRequirements(nil, compiled, nil, trace.ScriptResponse))
				ctx.Set(ClientRequirementsKey, requirements)
				AttachPresetScriptResponse(ctx, trace)
				if variant == "plain" && !stream {
					guard := PresetGenerationGuardFromContext(ctx)
					require.NotNil(t, guard)
					require.NoError(t, guard.ValidateRequest(compiled))
					require.Error(t, guard.ValidateRequest(mutated))
					for _, provider := range []string{"openai", "gemini"} {
						t.Run(provider+"/final-overrides", func(t *testing.T) {
							var envelope map[string]any
							encoded, err := common.Marshal(compiled)
							require.NoError(t, err)
							require.NoError(t, common.UnmarshalPreservingNumbers(encoded, &envelope))
							choiceField, messagesField := "tool_choice", "messages"
							if provider == "gemini" {
								envelope = map[string]any{"contents": []any{map[string]any{"role": "user", "parts": []any{map[string]any{"text": "NORMALIZED"}}}},
									"tools":      []any{map[string]any{"functionDeclarations": []any{map[string]any{"name": "preset_reply"}}}},
									"toolConfig": map[string]any{"functionCallingConfig": map[string]any{"mode": "ANY", "allowedFunctionNames": []any{"preset_reply"}}}}
								choiceField, messagesField = "toolConfig", "contents"
							}
							baseline, err := common.DeepCopy(&envelope)
							require.NoError(t, err)
							envelope["temperature"] = 0.4
							encoded, err = common.Marshal(envelope)
							require.NoError(t, err)
							require.NoError(t, guard.ValidateOverrides(*baseline, encoded))
							for _, field := range []string{choiceField, messagesField, "tools"} {
								changed, err := common.DeepCopy(baseline)
								require.NoError(t, err)
								(*changed)[field] = nil
								encoded, err := common.Marshal(*changed)
								require.NoError(t, err)
								require.Error(t, guard.ValidateOverrides(*baseline, encoded))
							}
						})
					}
				}
				filter := &dto.ResponseTextFilter{Mode: "rules", Rules: []dto.TextRegexRule{
					{ID: "unwrap", Stage: "receive", Action: "replace", Pattern: `/<\/?answer>/g`},
				}}
				writer := BeginResponseTextFilterWithPreset(ctx, filter, preset, "m")
				require.NotNil(t, writer)
				if variant == "plain" && !stream {
					_, err := writer.processResponse([]byte(`{"choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":"not an internal reply"}}]}`), false)
					require.ErrorContains(t, err, "omitted the required tool call")
					refusal := []byte(`{"choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","refusal":"Request declined.","content":"Request declined."}}]}`)
					result, err := writer.processResponse(refusal, false)
					require.NoError(t, err)
					require.JSONEq(t, string(refusal), string(result))
				}
				field := "message"
				if stream {
					field = "delta"
				}
				reply, expected := "raw-reply", "processed-reply"
				if variant == "escaped" {
					reply = `C:\notes\new.txt; literal \n; "quotes"` + "\nreal newline"
					expected = reply
				}
				arguments, err := common.Marshal(map[string]string{"body": reply})
				require.NoError(t, err)
				body, err := common.Marshal(map[string]any{"choices": []any{map[string]any{
					"index": 0, "finish_reason": "tool_calls", field: map[string]any{"role": "assistant", "tool_calls": []any{
						map[string]any{"index": 0, "id": "reply", "type": "function", "function": map[string]any{"name": "preset_reply", "arguments": string(arguments)}},
					}},
				}}})
				require.NoError(t, err)
				if stream {
					body = []byte("data: " + string(body) + "\n\ndata: [DONE]\n\n")
				}
				result, err := writer.processResponse(body, stream)
				require.NoError(t, err)
				choices, err := responseChoices(result, stream)
				require.NoError(t, err)
				require.Equal(t, expected, choices["0"].text.String())
				require.Empty(t, choices["0"].tools)
				require.Equal(t, "stop", choices["0"].finish)
				leaked := []byte(strings.ReplaceAll(string(body), "preset_reply", "lookup"))
				_, err = writer.processResponse(leaked, stream)
				require.Error(t, err)
			})
		}
	}
}

func TestRelayClientRequirements(t *testing.T) {
	t.Run("repeated streaming function-name fragments are not dropped", func(t *testing.T) {
		requirements, err := SnapshotClientRequirements(&dto.GeneralOpenAIRequest{
			ToolChoice: "required", Tools: []dto.ToolCallRequest{{Type: "function", Function: dto.FunctionRequest{Name: "foofoo"}}},
		})
		require.NoError(t, err)
		writer := &ResponseTextFilterWriter{requirements: requirements}
		body := []byte("data: " + `{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"foo","arguments":"{"}}]}}]}` + "\n\ndata: " +
			`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"name":"foo","arguments":"}"}}]},"finish_reason":"tool_calls"}]}` + "\n\ndata: [DONE]\n\n")
		require.NoError(t, writer.validateDelivery(body, true))
	})
	t.Run("undeclared transport cannot replace client controls", func(t *testing.T) {
		original := &dto.GeneralOpenAIRequest{Tools: []dto.ToolCallRequest{{Type: "function", Function: dto.FunctionRequest{Name: "lookup"}}}, ToolChoice: "none"}
		compiled := &dto.GeneralOpenAIRequest{Tools: append(append([]dto.ToolCallRequest{}, original.Tools...), dto.ToolCallRequest{Type: "function", Function: dto.FunctionRequest{Name: "internal_reply"}}), ToolChoice: "required"}
		_, _, err := buildPresetProtocolPlan(original, compiled, &dto.SillyTavernPresetConfig{ToolProtocol: &dto.SillyTavernToolProtocol{Enabled: true}}, &PresetScriptResponse{ownedTools: map[string]bool{"internal_reply": true}}, false)
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
	plan, _, err := buildPresetProtocolPlan(original, compiled, config, response, false)
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
		_, _, err = buildPresetProtocolPlan(tools, compiled, config, response, false)
		require.ErrorContains(t, err, "conflicts with client tools")
	}
	_, _, err = buildPresetProtocolPlan(original, compiled, &dto.SillyTavernPresetConfig{}, response, false)
	require.ErrorContains(t, err, "no declared reply transport")
	for _, policy := range []*dto.SillyTavernToolProtocol{
		{Enabled: true, ClientFinal: "finish"},
		{Enabled: true, Auxiliary: []string{"lookup"}},
		{Enabled: true, Action: []string{"save"}},
		{Enabled: true, PhasePolicy: "auto"},
	} {
		_, _, err = buildPresetProtocolPlan(original, compiled, &dto.SillyTavernPresetConfig{ToolProtocol: policy}, response, false)
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

func TestRelayClientBusinessCallsKeepPresetPipeline(t *testing.T) {
	config := scriptPresetConfig(t, map[string]any{
		"ToolBindings": map[string]any{"reply": map[string]any{"enabled": true,
			"code": `return {name:"preset_reply",parameters:{type:"object",properties:{body:{type:"string"}}}};`}},
		"FinalRequestProcessing": map[string]any{"enabled": true, "script": `({request}) => {
			request.messages = request.messages.map(m =>
				typeof m.content === "string" && !["tool","function"].includes(m.role) && !m.tool_calls
					? {...m,content:m.content.replaceAll("RAW","NORMALIZED")} : m);
			if (request.tools.length === 1) request.tool_choice = {type:"function",function:{name:"preset_reply"}};
			return request;
		}`},
		"OutputPreprocessing": map[string]any{"enabled": true, "consumeToolCalls": true,
			"script": `({buffer}) => {
				if (!buffer.startsWith("<answer>")) throw Error("formatter missing");
				return {output:buffer.replace("raw-reply","processed-reply"),state:{}};
			}`,
			"toolCallFormatter": map[string]any{"enabled": true, "script": `({calls}) => "<answer>"+calls[0].arguments.body+"</answer>"`}},
	})
	config.ReplyAdapter = &dto.ReplyAdapter{Version: 2, StructuredOutput: true, Containers: []dto.ReplyContainer{
		{Name: "preset_reply", Formatter: true, OutputKind: "text"},
	}}
	for _, scenario := range []struct {
		selection string
		json      bool
	}{{"auto", false}, {"required", false}, {"named", false}, {"auto", true}, {"required", true}, {"named", true}} {
		selection := scenario.selection
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/json=%v/stream=%v", selection, scenario.json, stream), func(t *testing.T) {
				request := &dto.GeneralOpenAIRequest{Model: "m", ToolChoice: selection,
					ParallelTooCalls: common.GetPointer(false),
					Tools:            []dto.ToolCallRequest{{Type: "function", Function: dto.FunctionRequest{Name: "lookup", Parameters: []byte(`{"type":"object"}`)}}},
					Messages:         []dto.Message{{Role: "user", Content: "RAW current task"}},
				}
				if scenario.json {
					request.ResponseFormat = &dto.ResponseFormat{Type: "json_schema", JsonSchema: []byte(`{"name":"answer","schema":{"type":"object","properties":{"answer":{"const":"processed-reply"}},"required":["answer"],"additionalProperties":false}}`)}
				}
				if selection == "named" {
					request.ToolChoice = map[string]any{"type": "function", "function": map[string]any{"name": "lookup"}}
				}
				for round := range 2 {
					original, err := common.Marshal(request)
					require.NoError(t, err)
					compiled, trace, err := CompileSillyTavernPreset(config, request, SillyTavernContext{})
					require.NoError(t, err)
					require.Equal(t, request.ToolChoice, compiled.ToolChoice)
					require.Equal(t, request.ParallelTooCalls, compiled.ParallelTooCalls)
					require.True(t, trace.ScriptResponse.requestHookExecuted)
					require.Contains(t, compiled.Messages[1].Content, "NORMALIZED")
					if selection == "auto" {
						require.Len(t, compiled.Tools, 2)
					} else {
						require.Equal(t, request.Tools, compiled.Tools, "required/named can only choose a client business tool")
					}
					if round == 1 {
						require.Equal(t, request.Messages[1:3], compiled.Messages[2:4], "native history retains its exact chronological position and IDs")
						require.Equal(t, "NORMALIZED continuation", compiled.Messages[4].Content)
					}
					unchanged, err := common.Marshal(request)
					require.NoError(t, err)
					require.Equal(t, original, unchanged)
					requirements, err := SnapshotClientRequirements(request)
					require.NoError(t, err)
					require.NoError(t, SealClientRequirements(requirements, compiled, nil, trace.ScriptResponse))
					ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
					ctx.Set(ClientRequirementsKey, requirements)
					AttachPresetScriptResponse(ctx, trace)
					filter := &dto.ResponseTextFilter{Mode: "rules", Rules: []dto.TextRegexRule{
						{ID: "unwrap", Stage: "receive", Action: "replace", Pattern: `/<\/?answer>/g`},
					}}
					writer := BeginResponseTextFilterWithPreset(ctx, filter, config, "m")
					require.NotNil(t, writer)
					field := "message"
					if stream {
						field = "delta"
					}
					name, args := "lookup", "{}"
					wantText := "processed-reply"
					if round == 1 && selection == "auto" {
						name, args = "preset_reply", `{"body":"raw-reply"}`
						if scenario.json {
							args = `{"body":"{\"answer\":\"raw-reply\"}"}`
							wantText = `{"answer":"processed-reply"}`
						}
					}
					sideText := "ADDITIONAL_TEXT"
					if name == "lookup" {
						// Mixed business replies still contain preset-authored
						// text. It must pass through the required output hook.
						sideText = "<answer>raw-reply</answer>"
					}
					body, err := common.Marshal(map[string]any{"choices": []any{map[string]any{
						"index": 0, "finish_reason": "tool_calls", field: map[string]any{"role": "assistant", "content": sideText, "tool_calls": []any{
							map[string]any{"index": 0, "id": "native_1", "type": "function", "function": map[string]any{"name": name, "arguments": args}},
						}},
					}}})
					require.NoError(t, err)
					if stream {
						body = []byte("data: " + string(body) + "\n\ndata: [DONE]\n\n")
					}
					result, err := writer.processResponse(body, stream)
					require.NoError(t, err)
					choices, err := responseChoices(result, stream)
					require.NoError(t, err)
					if name == "preset_reply" {
						require.Equal(t, wantText, choices["0"].text.String())
						require.Empty(t, choices["0"].tools)
						require.True(t, writer.outputProcessed)
					} else {
						require.Equal(t, "processed-reply", choices["0"].text.String())
						require.Equal(t, "native_1", choices["0"].tools["0"].id)
						require.Equal(t, "lookup", choices["0"].tools["0"].name.String())
						require.Equal(t, "{}", choices["0"].tools["0"].arguments.String())
						require.Equal(t, "tool_calls", choices["0"].finish)
						require.True(t, writer.outputProcessed)
					}
					if round == 0 {
						request.Messages = append(request.Messages,
							dto.Message{Role: "assistant", ToolCalls: []byte(`[{"id":"native_1","type":"function","function":{"name":"lookup","arguments":"{}"}}]`)},
							dto.Message{Role: "tool", ToolCallId: "native_1", Content: `{"value":"C:\\notes\\new.txt","literal":"\\n"}`},
							dto.Message{Role: "user", Content: "RAW continuation"})
					}
				}
			})
		}
	}
}

func TestRelayStructuredReplyRunsCompletePipeline(t *testing.T) {
	config := scriptPresetConfig(t, map[string]any{
		"ToolBindings": map[string]any{"reply": map[string]any{"enabled": true,
			"code": `return {name:"preset_reply",parameters:{type:"object",properties:{body:{type:"string"}}}};`}},
		"FinalRequestProcessing": map[string]any{"enabled": true, "script": `({request}) => {
			request.tool_choice = {type:"function",function:{name:"preset_reply"}}; return request;
		}`},
		"OutputPreprocessing": map[string]any{"enabled": true, "consumeToolCalls": true,
			"script": `({buffer}) => {
				const m = window.SillyTavern.getContext().chatMetadata;
				if (m.processed || !buffer.startsWith("<answer>")) throw Error("invalid processing order");
				m.processed = true; return {output:buffer.replace("nine", "9"),state:{}};
			}`,
			"toolCallFormatter": map[string]any{"enabled": true, "script": `({calls}) => "<answer>"+calls[0].arguments.body+"</answer>"`}},
	})
	config.ReplyAdapter = &dto.ReplyAdapter{Version: 2, StructuredOutput: true,
		Containers: []dto.ReplyContainer{{Name: "preset_reply", Formatter: true, OutputKind: "text"}}}
	format := &dto.ResponseFormat{Type: "json_schema", JsonSchema: []byte(`{"name":"hours","strict":true,"schema":{"type":"object","properties":{"opens_at":{"$ref":"#/$defs/hour"},"note":{"type":"string"}},"required":["opens_at","note"],"additionalProperties":false,"$defs":{"hour":{"type":"integer","minimum":0,"maximum":23}}}}`)}
	for _, stream := range []bool{false, true} {
		for _, reply := range []struct{ name, text, wantError string }{
			{"valid", `{"opens_at":nine,"note":"C:\\notes\\new.txt"}`, ""},
			{"schema-mismatch", `{"opens_at":24,"note":"closed"}`, "does not satisfy"},
			{"missing-field", `{"opens_at":9}`, "does not satisfy"},
			{"extra-field", `{"opens_at":9,"note":"open","extra":true}`, "does not satisfy"},
			{"wrong-type", `{"opens_at":"9","note":"open"}`, "does not satisfy"},
			{"invalid-json", `{"opens_at":`, "not valid JSON"},
		} {
			t.Run(fmt.Sprintf("%s/stream=%v", reply.name, stream), func(t *testing.T) {
				request := &dto.GeneralOpenAIRequest{Model: "m", ToolChoice: "none", ResponseFormat: format,
					Messages: []dto.Message{{Role: "user", Content: "Return the library opening time and note."}}}
				compiled, trace, err := CompileSillyTavernPreset(config, request, SillyTavernContext{})
				require.NoError(t, err)
				require.Nil(t, compiled.ResponseFormat, "approved conversion uses the internal tool transport")
				require.Equal(t, format, request.ResponseFormat, "client constraints stay immutable")
				require.Contains(t, compiled.Messages[len(compiled.Messages)-1].Content, `"maximum":23`)
				requirements, err := SnapshotClientRequirements(request)
				require.NoError(t, err)
				ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
				ctx.Set(ClientRequirementsKey, requirements)
				AttachPresetScriptResponse(ctx, trace)
				filter := &dto.ResponseTextFilter{Mode: "rules", Rules: []dto.TextRegexRule{
					{ID: "unwrap", Stage: "receive", Action: "replace", Pattern: `/<\/?answer>/g`},
				}}
				require.NoError(t, SealClientRequirements(requirements, compiled, filter, trace.ScriptResponse))
				writer := BeginResponseTextFilterWithPreset(ctx, filter, config, "m")
				require.NotNil(t, writer)
				args, err := common.Marshal(map[string]any{"body": reply.text})
				require.NoError(t, err)
				field := "message"
				if stream {
					field = "delta"
				}
				finish := any("tool_calls")
				if stream {
					finish = nil
				}
				body, err := common.Marshal(map[string]any{"choices": []any{map[string]any{
					"index": 0, "finish_reason": finish, field: map[string]any{"role": "assistant", "content": "SIDE_TEXT_MUST_NOT_BECOME_JSON", "tool_calls": []any{
						map[string]any{"index": 0, "id": "reply", "type": "function", "function": map[string]any{"name": "preset_reply", "arguments": string(args)}},
					}},
				}}})
				require.NoError(t, err)
				if stream {
					body = []byte("data: " + `{"choices":[{"index":0,"delta":{"content":"PREFIX_MUST_NOT_BECOME_JSON"}}]}` + "\n\n" +
						"data: " + string(body) + "\n\n" +
						"data: " + `{"choices":[{"index":0,"delta":{"content":"SUFFIX_MUST_NOT_BECOME_JSON"},"finish_reason":"tool_calls"}]}` + "\n\ndata: [DONE]\n\n")
				}
				result, err := writer.processResponse(body, stream)
				require.True(t, writer.outputProcessed, "the original output hook must actually run")
				if reply.wantError != "" {
					require.ErrorContains(t, err, reply.wantError)
					require.Nil(t, result, "invalid output must never be delivered or repaired")
					return
				}
				require.NoError(t, err)
				choices, err := responseChoices(result, stream)
				require.NoError(t, err)
				require.Equal(t, `{"opens_at":9,"note":"C:\\notes\\new.txt"}`, choices["0"].text.String())
				require.Empty(t, choices["0"].tools)
			})
		}
	}
	for _, schema := range []string{
		`{"name":"bad","schema":{"type":"not-a-type"}}`,
		`{"name":"remote","schema":{"$ref":"https://example.com/private-schema"}}`,
		`{"name":"local","schema":{"$ref":"file:///etc/passwd"}}`,
	} {
		_, err := SnapshotClientRequirements(&dto.GeneralOpenAIRequest{ResponseFormat: &dto.ResponseFormat{Type: "json_schema", JsonSchema: []byte(schema)}})
		require.Error(t, err, "invalid or external schemas must fail before generation")
	}
	array, err := SnapshotClientRequirements(&dto.GeneralOpenAIRequest{ResponseFormat: &dto.ResponseFormat{Type: "json_schema", JsonSchema: []byte(`{"name":"items","schema":{"type":"array","items":{"type":"integer"}}}`)}})
	require.NoError(t, err)
	require.NoError(t, array.validateText(`[1,2]`))
	require.Error(t, array.validateText(`["1"]`))
	composite, err := SnapshotClientRequirements(&dto.GeneralOpenAIRequest{ResponseFormat: &dto.ResponseFormat{Type: "json_schema", JsonSchema: []byte(`{"name":"values","schema":{"type":"object","properties":{"values":{"type":"array","minItems":1,"items":{"oneOf":[{"type":"integer","minimum":0},{"type":"string","enum":["pending"]}]}}},"required":["values"],"additionalProperties":false}}`)}})
	require.NoError(t, err)
	require.NoError(t, composite.validateText(`{"values":[0,"pending"]}`))
	for _, invalid := range []string{`{"values":[]}`, `{"values":[false]}`, `{"values":["other"]}`} {
		require.Error(t, composite.validateText(invalid))
	}
	precise, err := SnapshotClientRequirements(&dto.GeneralOpenAIRequest{ResponseFormat: &dto.ResponseFormat{Type: "json_schema", JsonSchema: []byte(`{"name":"precise","schema":{"const":9007199254740993}}`)}})
	require.NoError(t, err)
	require.NoError(t, precise.validateText(`9007199254740993`))
	require.Error(t, precise.validateText(`9007199254740992`))
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
	for _, ending := range []string{"\r\n", "\r"} {
		t.Run(fmt.Sprintf("SSE ending %q", ending), func(t *testing.T) {
			normalized := strings.ReplaceAll(strings.ReplaceAll(stream, "data: [DONE]", ": finished\ndata:[DONE]"), "\n", ending)
			require.NoError(t, writer.validateDelivery([]byte(normalized), true))
		})
	}
	require.Error(t, writer.validateDelivery([]byte(strings.ReplaceAll(stream, "data: [DONE]\n\n", "")), true))
	changedID := strings.Replace(stream, `"delta":{},"finish_reason"`, `"delta":{"tool_calls":[{"index":0,"id":"b","function":{"arguments":""}}]},"finish_reason"`, 1)
	require.ErrorContains(t, writer.validateDelivery([]byte(changedID), true), "ID changed")
	secondChoice := strings.Replace(stream, `{"index":0,"delta":{},"finish_reason":"tool_calls"}`, `{"index":0,"delta":{},"finish_reason":"tool_calls"},{"index":1,"delta":{"content":"not a tool"},"finish_reason":"stop"}`, 1)
	require.ErrorContains(t, writer.validateDelivery([]byte(secondChoice), true), "omitted a required")
	require.Error(t, writer.validateDelivery([]byte(stream+`data: {"choices":[]}`+"\n\n"), true))
	afterFinish := strings.Replace(stream, "data: [DONE]", `data: {"choices":[{"index":0,"delta":{"content":"late"}}]}`+"\n\ndata: [DONE]", 1)
	require.ErrorContains(t, writer.validateDelivery([]byte(afterFinish), true), "after its finish")
	for _, test := range []struct {
		name   string
		body   string
		stream bool
		err    string
	}{
		{"duplicate JSON choices", `{"choices":[{"index":0,"message":{"content":"{"}},{"index":0,"message":{"content":"}"}}]}`, false, "duplicate choice"},
		{"duplicate SSE choices", "data: " + `{"choices":[{"index":0,"delta":{"content":"{"}},{"index":0,"delta":{"content":"}"}}]}` + "\n\ndata:[DONE]\n\n", true, "duplicate choice"},
		{"noninteger choice", `{"choices":[{"index":0.5,"message":{"content":"{}"}}]}`, false, "index is invalid"},
		{"string choice", `{"choices":[{"index":"0","message":{"content":"{}"}}]}`, false, "must be an integer"},
		{"duplicate tool indexes", "data: " + `{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"a","function":{"name":"lookup","arguments":"{"}},{"index":0,"function":{"arguments":"}"}}]},"finish_reason":"tool_calls"}]}` + "\n\ndata:[DONE]\n\n", true, "duplicate tool indexes"},
		{"duplicate tool IDs", `{"choices":[{"index":0,"message":{"tool_calls":[{"id":"a","function":{"name":"lookup","arguments":"{}"}},{"id":"a","function":{"name":"lookup","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`, false, "duplicate tool call IDs"},
		{"mixed tool protocols", `{"choices":[{"index":0,"message":{"function_call":{"name":"lookup","arguments":"{}"},"tool_calls":[{"id":"a","function":{"name":"lookup","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`, false, "mixes legacy and modern"},
		{"legacy instead of modern", `{"choices":[{"index":0,"message":{"function_call":{"name":"lookup","arguments":"{}"}},"finish_reason":"function_call"}]}`, false, "changed the client's tool protocol"},
	} {
		t.Run(test.name, func(t *testing.T) {
			require.ErrorContains(t, writer.validateDelivery([]byte(test.body), test.stream), test.err)
		})
	}
	// Two real calls must retain independent IDs; an empty continuation ID is
	// not a request to discard the ID sent in the first event.
	emptyID := strings.Replace(stream, `"delta":{},"finish_reason"`, `"delta":{"tool_calls":[{"index":0,"id":"","function":{"arguments":""}}]},"finish_reason"`, 1)
	require.NoError(t, writer.validateDelivery([]byte(emptyID), true))
	parallel := `{"choices":[{"index":0,"message":{"tool_calls":[{"id":"a","function":{"name":"lookup","arguments":"{}"}},{"id":"b","function":{"name":"lookup","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`
	require.NoError(t, writer.validateDelivery([]byte(parallel), false))
	legacyRequirements, err := SnapshotClientRequirements(&dto.GeneralOpenAIRequest{Functions: []byte(`[{"name":"lookup","parameters":{"type":"object"}}]`), FunctionCall: []byte(`"auto"`)})
	require.NoError(t, err)
	legacyWriter := &ResponseTextFilterWriter{requirements: legacyRequirements}
	legacy := `{"choices":[{"index":0,"message":{"function_call":{"name":"lookup","arguments":"{}"}},"finish_reason":"function_call"}]}`
	require.NoError(t, legacyWriter.validateDelivery([]byte(legacy), false))
	legacyStream := "data: " + strings.Replace(legacy, `"message":`, `"delta":`, 1) + "\n\ndata:[DONE]\n\n"
	require.NoError(t, legacyWriter.validateDelivery([]byte(legacyStream), true))
	require.ErrorContains(t, legacyWriter.validateDelivery([]byte(parallel), false), "changed the client's tool protocol")
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

func TestRelayOverflowCancelsOnlyCurrentProviderAttempt(t *testing.T) {
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest("POST", "/", nil)
	parent := ctx.Request.Context()
	writer := BeginResponseTextFilter(ctx, &dto.ResponseTextFilter{Mode: "regex_extract", Pattern: "(.*)"}, "m")
	require.NoError(t, writer.Admit(ctx))
	defer func() {
		if writer.admitted {
			_ = writer.Finish(ctx, false)
		}
	}()
	provider := ctx.Request.Context()
	_, err := writer.Write(make([]byte, maxFilteredResponseBytes+1))
	require.Error(t, err)
	require.ErrorIs(t, provider.Err(), context.Canceled)
	require.NoError(t, parent.Err())
	require.Error(t, writer.Finish(ctx, false))
	require.Equal(t, parent, ctx.Request.Context(), "retry must not inherit a canceled attempt context")
	require.False(t, writer.admitted)
}

func TestRelayReplySourceDoesNotConsumeOtherChoices(t *testing.T) {
	config := &dto.SillyTavernToolText{Enabled: true, Name: "reply", Argument: "body"}
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%v", stream), func(t *testing.T) {
			field := "message"
			if stream {
				field = "delta"
			}
			body := []byte(fmt.Sprintf(`{"id":"response-1","choices":[
				{"index":0,"%s":{"role":"assistant","content":"SIDE_TEXT","reasoning_content":"separate reasoning","tool_calls":[{"index":0,"id":"r","type":"function","function":{"name":"reply","arguments":"{\"body\":\"selected reply\"}"}}]},"finish_reason":"tool_calls"},
				{"index":1,"%s":{"role":"assistant","content":"client explanation","tool_calls":[{"index":0,"id":"c","type":"function","function":{"name":"lookup","arguments":"{\"path\":\"C:\\\\notes\"}"}}]},"finish_reason":"tool_calls"},
				{"index":2,"%s":{"role":"assistant","content":"ordinary reply"},"finish_reason":"stop"}],"provider_metadata":{"retained":true}}`, field, field, field))
			var envelope map[string]any
			require.NoError(t, common.Unmarshal(body, &envelope))
			body, err := common.Marshal(envelope)
			require.NoError(t, err)
			if stream {
				body = []byte("data: " + string(body) + "\n\ndata: [DONE]\n\n")
			}
			result, err := restorePresetToolText(body, stream, config, time.Now().Add(time.Second))
			require.NoError(t, err)
			choices, err := responseChoices(result, stream)
			require.NoError(t, err)
			require.Len(t, choices, 3)
			require.Equal(t, "selected reply", choices["0"].text.String())
			require.Empty(t, choices["0"].tools)
			require.Equal(t, "stop", choices["0"].finish)
			require.Equal(t, "client explanation", choices["1"].text.String())
			require.Equal(t, "c", choices["1"].tools["0"].id)
			require.Equal(t, "lookup", choices["1"].tools["0"].name.String())
			require.Equal(t, `{"path":"C:\\notes"}`, choices["1"].tools["0"].arguments.String())
			require.Equal(t, "tool_calls", choices["1"].finish)
			require.Equal(t, "ordinary reply", choices["2"].text.String())
			require.Contains(t, string(result), `"reasoning_content":"separate reasoning"`)
			require.Contains(t, string(result), `"provider_metadata":{"retained":true}`)
			require.NotContains(t, string(result), "SIDE_TEXT")
			require.Contains(t, string(body), "SIDE_TEXT", "the captured upstream bytes remain intact")
		})
	}
}

func TestRelayReplySourceRejectsUnsafeRestoration(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, test := range []struct {
			name, arguments, finish, wantError string
		}{
			{"mixed-client-tool", `{}`, "tool_calls", "cannot mix preset replies with client tools"},
			{"duplicate-call-id", `{}`, "tool_calls", "duplicate tool call IDs"},
			{"truncated", `{}`, "length", "requires a complete"},
			{"filtered", `{}`, "content_filter", "requires a complete"},
			{"refused", `{}`, "tool_calls", "requires a complete"},
			{"invalid-arguments", `{"body":`, "tool_calls", "invalid tool text arguments"},
			{"structured-side-content", `{"body":"reply"}`, "tool_calls", "cannot replace structured content"},
		} {
			t.Run(fmt.Sprintf("%s/stream=%v", test.name, stream), func(t *testing.T) {
				message := map[string]any{"content": "side text", "tool_calls": []any{
					map[string]any{"index": 0, "id": "r", "type": "function", "function": map[string]any{"name": "reply", "arguments": test.arguments}},
				}}
				if test.name == "mixed-client-tool" || test.name == "duplicate-call-id" {
					name, id := "lookup", "c"
					if test.name == "duplicate-call-id" {
						name, id = "reply", "r"
					}
					message["tool_calls"] = append(message["tool_calls"].([]any), map[string]any{
						"index": 1, "id": id, "type": "function", "function": map[string]any{"name": name, "arguments": `{}`},
					})
				}
				if test.name == "refused" {
					message["refusal"] = "cannot complete"
				}
				if test.name == "structured-side-content" {
					message["content"] = []any{map[string]any{"type": "image", "url": "example"}}
				}
				field := "message"
				if stream {
					field = "delta"
				}
				body, err := common.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "finish_reason": test.finish, field: message}}})
				require.NoError(t, err)
				if stream {
					body = []byte("data: " + string(body) + "\n\ndata: [DONE]\n\n")
				}
				result, err := restorePresetToolText(body, stream, &dto.SillyTavernToolText{Enabled: true, Name: "reply", Argument: "body"}, time.Now().Add(time.Second))
				require.ErrorContains(t, err, test.wantError)
				require.Nil(t, result, "unsafe responses must not be partially rewritten")
			})
		}
	}
}

func TestRelayReplySourceBatchKeepsCallOrder(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%v", stream), func(t *testing.T) {
			body := []byte(`{"choices":[{"index":0,"message":{"content":"side text","tool_calls":[{"id":"a","function":{"name":"first","arguments":"{}"}},{"id":"b","function":{"name":"second","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`)
			if stream {
				// The second call completes first. Formatting still follows the
				// calls' first appearance, not their completion order.
				body = []byte("data: " + `{"choices":[{"index":0,"delta":{"content":"side text","tool_calls":[{"index":0,"id":"a","function":{"name":"first","arguments":"{"}},{"index":1,"id":"b","function":{"name":"second","arguments":"{"}}]}}]}` + "\n\n" +
					"data: " + `{"choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"function":{"arguments":"}"}}]}}]}` + "\n\n" +
					"data: " + `{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"}"}}]},"finish_reason":"tool_calls"}]}` + "\n\ndata: [DONE]\n\n")
			}
			result, err := restorePresetToolCalls(body, stream, map[string]bool{"first": true, "second": true}, true,
				func(calls []*presetToolTextCall) (string, error) {
					require.Len(t, calls, 2)
					require.Equal(t, "first", calls[0].name.String())
					require.Equal(t, "second", calls[1].name.String())
					for _, call := range calls {
						require.Equal(t, "{}", call.arguments.String())
					}
					return "first|second", nil
				}, time.Now().Add(time.Second))
			require.NoError(t, err)
			choices, err := responseChoices(result, stream)
			require.NoError(t, err)
			require.Equal(t, "first|second", choices["0"].text.String())
			require.Empty(t, choices["0"].tools)
		})
	}
}

func TestRelayReplySourceValidatesStreamBeforeFormatting(t *testing.T) {
	call := "data: " + `{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"r","function":{"name":"reply","arguments":"{}"}}]}}]}` + "\n\n"
	finish := "data: " + `{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}` + "\n\n"
	media := "data: " + `{"choices":[{"index":0,"delta":{"content":[{"type":"image","url":"example"}]}}]}` + "\n\n"
	for _, test := range []struct{ name, body, wantError string }{
		{"missing-finish", call + "data: [DONE]\n\n", "requires a complete"},
		{"missing-terminator", call + finish, "requires a complete"},
		{"media-before-call", media + call + finish + "data: [DONE]\n\n", "cannot replace structured content"},
		{"media-after-call", call + media + finish + "data: [DONE]\n\n", "cannot replace structured content"},
	} {
		t.Run(test.name, func(t *testing.T) {
			formatted := false
			result, err := restorePresetToolCalls([]byte(test.body), true, map[string]bool{"reply": true}, true,
				func([]*presetToolTextCall) (string, error) {
					formatted = true
					return "reply", nil
				}, time.Now().Add(time.Second))
			require.ErrorContains(t, err, test.wantError)
			require.Nil(t, result)
			require.False(t, formatted, "invalid envelopes must not run the preset formatter")
		})
	}
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
					// P failure restores R, then independent F still runs.
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
					require.Equal(t, "changed", choices["0"].text.String())
					require.Equal(t, second, choices["1"].text.String())
				} else {
					require.Equal(t, "one-processed", choices["0"].text.String())
					require.Equal(t, second+"-processed", choices["1"].text.String())
				}
			})
		}
	}
}

func TestRelayOptionalProcessingCannotInvalidateDeliveredSchema(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, outputHook := range []bool{false, true} {
			t.Run(fmt.Sprintf("stream=%v/outputHook=%v", stream, outputHook), func(t *testing.T) {
				requirements, err := SnapshotClientRequirements(&dto.GeneralOpenAIRequest{
					ResponseFormat: &dto.ResponseFormat{Type: "json_schema", JsonSchema: []byte(`{"name":"answer","schema":{"type":"object","properties":{"body":{"const":"valid"}},"required":["body"]}}`)},
				})
				require.NoError(t, err)
				ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
				ctx.Set(ClientRequirementsKey, requirements)
				var preset *dto.SillyTavernPresetConfig
				filter := &dto.ResponseTextFilter{Mode: "rules", FailurePolicy: "passthrough", JSONTextPointers: []string{"/body"}, Rules: []dto.TextRegexRule{
					{ID: "change", Stage: "receive", Action: "replace", Pattern: "valid", Replacement: "invalid"},
				}}
				if outputHook {
					preset = scriptPresetConfig(t, map[string]any{"OutputPreprocessing": map[string]any{"enabled": true, "script": `() => ({output: "not JSON"})`}})
					preset.FailurePolicy = &dto.SillyTavernFailurePolicy{OutputProcessing: "preserve"}
					_, trace, err := CompileSillyTavernPreset(preset, &dto.GeneralOpenAIRequest{Model: "m"}, SillyTavernContext{})
					require.NoError(t, err)
					AttachPresetScriptResponse(ctx, trace)
					filter = nil
				}
				writer := BeginResponseTextFilterWithPreset(ctx, filter, preset, "m")
				body := []byte(`{"choices":[{"index":0,"message":{"content":"{\"body\":\"valid\"}"},"finish_reason":"stop"}]}`)
				if stream {
					body = []byte("data: " + strings.Replace(string(body), `"message"`, `"delta"`, 1) + "\n\ndata: [DONE]\n\n")
				}
				result, err := writer.processResponse(body, stream)
				require.NoError(t, err)
				require.Equal(t, body, result, "optional transformations must roll back to the valid checkpoint")
			})
		}
	}
}

func TestRelayOutputFailureStillRunsMandatoryCleanup(t *testing.T) {
	preset := scriptPresetConfig(t, map[string]any{"OutputPreprocessing": map[string]any{"enabled": true, "script": `() => { throw new Error("failure"); }`}})
	preset.FailurePolicy = &dto.SillyTavernFailurePolicy{OutputProcessing: "preserve"}
	_, trace, err := CompileSillyTavernPreset(preset, &dto.GeneralOpenAIRequest{Model: "m"}, SillyTavernContext{})
	require.NoError(t, err)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	AttachPresetScriptResponse(ctx, trace)
	writer := BeginResponseTextFilterWithPreset(ctx, &dto.ResponseTextFilter{Mode: "regex_extract", Pattern: "<body>(.*?)</body>", MissingMatch: "error"}, preset, "m")
	result, err := writer.processResponse([]byte(`{"choices":[{"index":0,"message":{"content":"private <body>answer</body>"},"finish_reason":"stop"}]}`), false)
	require.NoError(t, err)
	choices, err := responseChoices(result, false)
	require.NoError(t, err)
	require.Equal(t, "answer", choices["0"].text.String())
}

func TestRelayMixedBusinessToolsStillCleanText(t *testing.T) {
	for _, stream := range []bool{false, true} {
		ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
		writer := BeginResponseTextFilter(ctx, &dto.ResponseTextFilter{Mode: "regex_extract", Pattern: "<body>(.*?)</body>", MissingMatch: "error"}, "m")
		body := []byte(`{"choices":[{"index":0,"message":{"content":"private <body>answer</body>","tool_calls":[{"index":0,"id":"business-1","type":"function","function":{"name":"lookup","arguments":"{\"path\":\"C:\\\\notes\"}"}}]},"finish_reason":"tool_calls"}]}`)
		if stream {
			body = []byte("data: " + strings.Replace(string(body), `"message"`, `"delta"`, 1) + "\n\ndata: [DONE]\n\n")
		}
		result, err := writer.processResponse(body, stream)
		require.NoError(t, err)
		choices, err := responseChoices(result, stream)
		require.NoError(t, err)
		require.Equal(t, "answer", choices["0"].text.String())
		require.Equal(t, "business-1", choices["0"].tools["0"].id)
		require.Equal(t, `{"path":"C:\\notes"}`, choices["0"].tools["0"].arguments.String())
		require.Equal(t, "tool_calls", choices["0"].finish)
	}
}
