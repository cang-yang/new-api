package service

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/gin-gonic/gin"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

type responseChoice struct {
	text    strings.Builder
	tools   map[string]*presetToolTextCall
	finish  string
	refused bool
	legacy  bool
}

// Read event boundaries after buffering, not network Write boundaries. Keep
// each choice and each tool index independent throughout validation.
func responseChoices(body []byte, stream bool) (map[string]*responseChoice, error) {
	choices, _, err := parseResponseChoices(body, stream)
	return choices, err
}

func parseResponseChoices(body []byte, stream bool) (map[string]*responseChoice, bool, error) {
	choices := map[string]*responseChoice{}
	frames := []string{string(body)}
	if stream {
		frames = strings.Split(strings.ReplaceAll(strings.ReplaceAll(string(body), "\r\n", "\n"), "\r", "\n"), "\n\n")
	}
	done := false
	for _, frame := range frames {
		data := frame
		if stream {
			lines := []string{}
			for line := range strings.SplitSeq(frame, "\n") {
				field, value, _ := strings.Cut(line, ":")
				if field == "data" {
					lines = append(lines, strings.TrimPrefix(value, " "))
				}
			}
			data = strings.Join(lines, "\n")
			if strings.TrimSpace(data) == "" {
				continue
			}
			if done {
				return nil, false, fmt.Errorf("stream contains data after its terminator")
			}
			if strings.TrimSpace(data) == "[DONE]" {
				done = true
				continue
			}
		}
		var payload map[string]any
		if err := common.UnmarshalPreservingNumbers([]byte(data), &payload); err != nil || payload == nil {
			return nil, false, fmt.Errorf("response is not a complete JSON envelope")
		}
		if payload["error"] != nil {
			return nil, false, fmt.Errorf("upstream returned an error event")
		}
		items, _ := payload["choices"].([]any)
		frameChoices := map[string]bool{}
		for index, item := range items {
			choice, ok := item.(map[string]any)
			if !ok {
				return nil, false, fmt.Errorf("invalid response choice")
			}
			key := presetToolTextChoiceKey(choice, index)
			if rawIndex, exists := choice["index"]; exists {
				if _, ok := rawIndex.(json.Number); !ok {
					return nil, false, fmt.Errorf("response choice index must be an integer")
				}
				if number, err := strconv.Atoi(key); err != nil || number < 0 || strconv.Itoa(number) != key {
					return nil, false, fmt.Errorf("response choice index is invalid")
				}
			}
			if frameChoices[key] {
				return nil, false, fmt.Errorf("response contains duplicate choice indexes in one event")
			}
			frameChoices[key] = true
			state := choices[key]
			if state == nil {
				state = &responseChoice{tools: map[string]*presetToolTextCall{}}
				choices[key] = state
			} else if stream && state.finish != "" {
				return nil, false, fmt.Errorf("stream contains choice data after its finish")
			}
			if reason, ok := choice["finish_reason"].(string); ok {
				state.finish = reason
			}
			field := "message"
			if stream {
				field = "delta"
			}
			message, _ := choice[field].(map[string]any)
			if refusal, ok := message["refusal"].(string); ok && refusal != "" {
				state.refused = true
			}
			if text, ok := message["content"].(string); ok {
				state.text.WriteString(text)
			}
			calls, _ := message["tool_calls"].([]any)
			if old, ok := message["function_call"].(map[string]any); ok {
				if len(calls) > 0 || len(state.tools) > 0 && !state.legacy {
					return nil, false, fmt.Errorf("response mixes legacy and modern tool calls")
				}
				state.legacy = true
				calls = append(calls, map[string]any{"function": old, "index": 0})
			} else if len(calls) > 0 && state.legacy {
				return nil, false, fmt.Errorf("response mixes legacy and modern tool calls")
			}
			frameTools := map[string]bool{}
			for position, raw := range calls {
				call, ok := raw.(map[string]any)
				if !ok {
					return nil, false, fmt.Errorf("invalid tool call")
				}
				toolKey := strconv.Itoa(position)
				if stream {
					if call["index"] == nil {
						return nil, false, fmt.Errorf("streaming tool call requires an index")
					}
					toolKey = fmt.Sprint(call["index"])
					if !state.legacy {
						if _, ok := call["index"].(json.Number); !ok {
							return nil, false, fmt.Errorf("streaming tool index must be an integer")
						}
					}
					if number, err := strconv.Atoi(toolKey); err != nil || number < 0 || strconv.Itoa(number) != toolKey {
						return nil, false, fmt.Errorf("streaming tool index is invalid")
					}
				}
				if frameTools[toolKey] {
					return nil, false, fmt.Errorf("response contains duplicate tool indexes in one event")
				}
				frameTools[toolKey] = true
				assembled := state.tools[toolKey]
				if assembled == nil {
					assembled = &presetToolTextCall{}
					state.tools[toolKey] = assembled
				}
				fn, _ := call["function"].(map[string]any)
				if name, ok := fn["name"].(string); ok {
					assembled.name.WriteString(name)
				}
				if args, ok := fn["arguments"].(string); ok {
					assembled.arguments.WriteString(args)
				}
				if id, ok := call["id"].(string); ok {
					if assembled.id != "" && id != "" && assembled.id != id {
						return nil, false, fmt.Errorf("streaming tool ID changed")
					}
					if id != "" {
						assembled.id = id
					}
				}
			}
		}
	}
	return choices, done, nil
}

func (w *ResponseTextFilterWriter) validateDelivery(body []byte, stream bool) error {
	return w.validateResponseContract(body, stream, true)
}

// Restoration is an intermediate step: its text may still require the preset's
// output hook and mandatory cleanup. Protocol constraints apply immediately;
// the client's text/JSON constraints apply only to the delivered result.
func (w *ResponseTextFilterWriter) validateResponseContract(body []byte, stream, finalFormat bool) error {
	if w.scriptResponse != nil && w.scriptResponse.generationRequest != nil && (w.requirements == nil || w.requirements.request == nil) {
		return fmt.Errorf("client delivery requires saved client requirements")
	}
	choices, done, err := parseResponseChoices(body, stream)
	if err != nil {
		return err
	}
	internal := w.internalToolNames()
	for _, choice := range choices {
		seenIDs := map[string]bool{}
		for _, call := range choice.tools {
			if call.id != "" && seenIDs[call.id] {
				return fmt.Errorf("response contains duplicate tool call IDs")
			}
			seenIDs[call.id] = true
			if internal[call.name.String()] {
				return fmt.Errorf("internal reply transport was not restored")
			}
		}
		if finalFormat && len(choice.tools) == 0 && !choice.refused && choice.finish != "length" && choice.finish != "content_filter" &&
			w.scriptResponse != nil && w.scriptResponse.protocol != nil && w.scriptResponse.protocol.contract == presetProtocolContractJSON {
			if _, err := presetProtocolJSONArgument(choice.text.String()); err != nil {
				return err
			}
		}
		if w.requirements == nil || w.requirements.request == nil {
			continue
		}
		request := w.requirements.request
		if len(choice.tools) > 0 && (choice.legacy && len(request.Tools) > 0 || !choice.legacy && len(request.Tools) == 0 && len(request.Functions) > 0) {
			return fmt.Errorf("response changed the client's tool protocol")
		}
		if stream && choice.finish == "" {
			return fmt.Errorf("stream ended before the choice finished")
		}
		mode, _ := request.ToolChoice.(string)
		named := presetToolChoiceName(request.ToolChoice)
		if len(request.FunctionCall) > 0 {
			var legacy any
			if common.Unmarshal(request.FunctionCall, &legacy) == nil {
				if str, ok := legacy.(string); ok {
					mode = str
				} else if object, ok := legacy.(map[string]any); ok {
					named, _ = object["name"].(string)
				}
			}
		}
		for _, call := range choice.tools {
			if mode == "none" {
				return fmt.Errorf("response violated the client's no-tool choice")
			}
			found := false
			for _, tool := range request.Tools {
				if tool.Function.Name == call.name.String() {
					found = true
				}
			}
			var functions []dto.FunctionRequest
			_ = common.Unmarshal(request.Functions, &functions)
			for _, tool := range functions {
				if tool.Name == call.name.String() {
					found = true
				}
			}
			if !found || named != "" && named != call.name.String() {
				return fmt.Errorf("response contains a tool not permitted by the client")
			}
			if len(request.Functions) == 0 && call.id == "" {
				return fmt.Errorf("client tool call is missing its ID")
			}
			if choice.finish != "length" && !choice.refused {
				var arguments map[string]any
				if common.UnmarshalPreservingNumbers([]byte(call.arguments.String()), &arguments) != nil || arguments == nil {
					return fmt.Errorf("client tool arguments must be a JSON object")
				}
			}
		}
		if request.ParallelTooCalls != nil && !*request.ParallelTooCalls && len(choice.tools) > 1 {
			return fmt.Errorf("response violated parallel_tool_calls:false")
		}
		if choice.finish == "length" || choice.finish == "content_filter" || choice.refused {
			continue
		}
		if (mode == "required" || named != "") && len(choice.tools) == 0 {
			return fmt.Errorf("response omitted a required client tool call")
		}
		if finalFormat && len(choice.tools) == 0 {
			if err := w.requirements.validateText(choice.text.String()); err != nil {
				return err
			}
		}
	}
	if len(choices) == 0 && w.requirements != nil {
		return fmt.Errorf("response omitted all choices")
	}
	if stream && w.requirements != nil {
		if !done {
			return fmt.Errorf("stream ended without its terminator")
		}
	}
	return nil
}

const ClientRequirementsKey = "adaptation_client_requirements"

// AdaptationSummary is the only plan shape permitted in audit JSON.
type AdaptationSummary struct {
	Version            int    `json:"version"`
	Contract           string `json:"contract"`
	ProfileFingerprint string `json:"profile_fingerprint"`
	Fingerprint        string `json:"fingerprint"`
}

// ClientRequirements preserves orthogonal client constraints. It is runtime
// state, never serialized into the audit metadata.
type ClientRequirements struct {
	request *dto.GeneralOpenAIRequest
	schema  *jsonschema.Schema
}

func SnapshotClientRequirements(request *dto.GeneralOpenAIRequest) (*ClientRequirements, error) {
	copy, err := cloneGeneralRequest(request)
	if err != nil {
		return nil, err
	}
	result := &ClientRequirements{request: copy}
	if copy == nil || copy.ResponseFormat == nil || copy.ResponseFormat.Type != "json_schema" {
		return result, nil
	}
	var format struct {
		Schema any `json:"schema"`
	}
	if len(copy.ResponseFormat.JsonSchema) > 256<<10 || common.UnmarshalPreservingNumbers(copy.ResponseFormat.JsonSchema, &format) != nil || format.Schema == nil {
		return nil, newResponseStageError(FailureStagePreflight, fmt.Errorf("client JSON schema is missing, invalid or exceeds 256 KiB"))
	}
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	// Inline $defs/$ref work normally. Validation must never read a server file
	// or fetch an external URL supplied in an untrusted client schema.
	compiler.UseLoader(inlineSchemaLoader{})
	if err := compiler.AddResource("https://gateway.invalid/client-schema", format.Schema); err != nil {
		return nil, newResponseStageError(FailureStagePreflight, fmt.Errorf("client JSON schema is invalid"))
	}
	result.schema, err = compiler.Compile("https://gateway.invalid/client-schema")
	if err != nil {
		return nil, newResponseStageError(FailureStagePreflight, fmt.Errorf("client JSON schema is invalid or requires an external resource"))
	}
	return result, nil
}

type inlineSchemaLoader struct{}

func (inlineSchemaLoader) Load(string) (any, error) {
	return nil, fmt.Errorf("external schema resources are disabled")
}

func (r *ClientRequirements) validateText(text string) error {
	if r == nil || r.request == nil || r.request.ResponseFormat == nil {
		return nil
	}
	switch r.request.ResponseFormat.Type {
	case "json", "json_object":
		_, err := presetProtocolJSONArgument(text)
		return err
	case "json_schema":
		if r.schema == nil {
			return fmt.Errorf("client JSON schema was not prepared")
		}
		var value any
		if common.UnmarshalPreservingNumbers([]byte(text), &value) != nil {
			return fmt.Errorf("reply is not valid JSON")
		}
		if r.schema.Validate(value) != nil {
			// Library diagnostics include instance values; do not copy chat or
			// schema contents into public errors or execution metadata.
			return fmt.Errorf("reply does not satisfy the client JSON schema")
		}
	}
	return nil
}

// Runtime-only state cannot be included in metadata JSON.
func (r *ClientRequirements) MarshalJSON() ([]byte, error) {
	return nil, fmt.Errorf("client requirements are runtime-only")
}

func samePresetValue(a, b any) bool {
	x, err := common.Marshal(a)
	if err != nil {
		return false
	}
	y, err := common.Marshal(b)
	return err == nil && samePresetJSON(x, y)
}

// AdaptationApplies classifies only explicit protocol fields, never prose,
// user-agent, tool names or the presumed phase of a conversation.
func AdaptationApplies(scope string, request dto.Request) (bool, string) {
	if scope != "plain_chat_only" {
		return true, "all_supported"
	}
	chat, ok := request.(*dto.GeneralOpenAIRequest)
	if !ok {
		return false, "other_protocol"
	}
	if clientDeclaresToolContract(chat) {
		return false, "client_tools"
	}
	if chat.ResponseFormat != nil && chat.ResponseFormat.Type != "" && chat.ResponseFormat.Type != "text" {
		return false, "structured_output"
	}
	for _, m := range chat.Messages {
		if len(m.ToolCalls) > 0 || len(m.FunctionCall) > 0 || len(m.Tools) > 0 || m.Role == "tool" || m.Role == "function" || m.ToolCallId != "" {
			return false, "tool_history"
		}
		if m.Content != nil {
			if _, ok := m.Content.(string); !ok {
				return false, "non_text_content"
			}
		}
	}
	return true, "plain_chat"
}

func replyPointer(value any, pointer string) (any, error) {
	if err := dto.ValidateJSONPointer(pointer); err != nil {
		return nil, err
	}
	if pointer == "" {
		return value, nil
	}
	for _, part := range strings.Split(pointer[1:], "/") {
		part = strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
		switch item := value.(type) {
		case map[string]any:
			var exists bool
			value, exists = item[part]
			if !exists {
				return nil, fmt.Errorf("reply field is missing")
			}
		case []any:
			index, err := strconv.Atoi(part)
			if err != nil || strconv.Itoa(index) != part || index < 0 || index >= len(item) {
				return nil, fmt.Errorf("reply array index is invalid")
			}
			value = item[index]
		default:
			return nil, fmt.Errorf("reply field crosses a non-container value")
		}
	}
	return value, nil
}

func (w *ResponseTextFilterWriter) transformContent(text string) (string, bool) {
	if !w.structured || len(w.config.JSONTextPointers) == 0 {
		return w.transform(text)
	}
	var object map[string]any
	if common.UnmarshalPreservingNumbers([]byte(text), &object) != nil || object == nil {
		w.transformErr = newResponseStageError(FailureStageRegex, fmt.Errorf("JSON field processing requires an object"))
		return text, false
	}
	for _, path := range w.config.JSONTextPointers {
		value, err := replyPointer(object, path)
		field, ok := value.(string)
		if err != nil || !ok {
			w.transformErr = newResponseStageError(FailureStageRegex, fmt.Errorf("selected JSON text field is missing or is not text"))
			return text, false
		}
		result, changed := w.transform(field)
		if w.transformErr != nil {
			return text, false
		}
		if changed {
			var parentPath, key string
			last := strings.LastIndex(path, "/")
			if last < 0 {
				w.transformErr = fmt.Errorf("JSON root is not a text field")
				return text, false
			}
			parentPath, key = path[:last], path[last+1:]
			parent, err := replyPointer(object, parentPath)
			if err != nil {
				w.transformErr = err
				return text, false
			}
			key = strings.ReplaceAll(strings.ReplaceAll(key, "~1", "/"), "~0", "~")
			switch item := parent.(type) {
			case map[string]any:
				item[key] = result
			case []any:
				index, _ := strconv.Atoi(key)
				item[index] = result
			}
		}
	}
	encoded, err := common.Marshal(object)
	if err != nil {
		w.transformErr = err
		return text, false
	}
	return string(encoded), string(encoded) != text
}

// Transport restoration also needs choice-wide visibility before mutation.
func mustResponseChoices(body []byte, stream bool) map[string]*responseChoice {
	choices, _ := responseChoices(body, stream)
	return choices
}

// clientProtocolControls excludes message text, which send rules and presets
// may legitimately transform. Business tool definitions and delivery controls
// remain bound to the original client, independently of internal generation.
func clientProtocolControls(request *dto.GeneralOpenAIRequest) ([]byte, error) {
	return common.Marshal(map[string]any{
		"tools": request.Tools, "tool_choice": request.ToolChoice,
		"functions": request.Functions, "function_call": request.FunctionCall,
		"response_format": request.ResponseFormat, "parallel_tool_calls": request.ParallelTooCalls,
	})
}

// Seal checks the two contracts after request hooks ran. An internal transport
// may differ from client controls only when compilation froze both contracts;
// arbitrary later mutations are not granted the same permission.
func SealClientRequirements(requirements *ClientRequirements, compiled *dto.GeneralOpenAIRequest, filter *dto.ResponseTextFilter, responses ...*PresetScriptResponse) error {
	if len(responses) > 1 {
		return newResponseStageError(FailureStagePreflight, fmt.Errorf("only one preset response contract may be sealed"))
	}
	var response *PresetScriptResponse
	if len(responses) == 1 {
		response = responses[0]
	}
	if response != nil && response.textToClientFormat && filter != nil && len(filter.JSONTextPointers) > 0 {
		return newResponseStageError(FailureStagePreflight, fmt.Errorf("text reply conversion cannot use JSON text-field processing before conversion"))
	}
	if requirements != nil && requirements.request != nil && requirements.request.ResponseFormat != nil && requirements.request.ResponseFormat.Type == "json_schema" && filter != nil && len(filter.JSONTextPointers) > 0 {
		return newResponseStageError(FailureStagePreflight, fmt.Errorf("JSON schema requests cannot use JSON text-field processing"))
	}
	if response != nil && response.generationRequest != nil {
		if requirements == nil || requirements.request == nil {
			return newResponseStageError(FailureStageToolResolution, fmt.Errorf("internal reply requires saved client requirements"))
		}
		controls, err := clientProtocolControls(requirements.request)
		if err != nil || !samePresetJSON(controls, response.generationClientControls) || !samePresetValue(compiled, response.generationRequest) {
			return newResponseStageError(FailureStageToolResolution, fmt.Errorf("request no longer matches the compiled internal reply contract"))
		}
		return nil
	}
	if requirements == nil || requirements.request == nil {
		return nil
	}
	if err := validatePresetClientToolContractWithMode(requirements.request, compiled, 0); err != nil {
		return newResponseStageError(FailureStageToolResolution, err)
	}
	return nil
}

// PresetGenerationGuard protects one compiled internal request, not the
// client's no-tool delivery controls. It is request-local and never serialized.
type PresetGenerationGuard struct {
	request *dto.GeneralOpenAIRequest
}

func PresetGenerationGuardFromContext(c *gin.Context) *PresetGenerationGuard {
	value, _ := c.Get("preset_script_response")
	response, _ := value.(*PresetScriptResponse)
	if response == nil || response.generationRequest == nil {
		return nil
	}
	return &PresetGenerationGuard{request: response.generationRequest}
}

// ValidateRequest allows model routing and ordinary generation parameters to
// change, but not tool ownership, delivery controls or script-produced history.
func (g *PresetGenerationGuard) ValidateRequest(request *dto.GeneralOpenAIRequest) error {
	if g == nil {
		return nil
	}
	if request == nil {
		return newResponseStageError(FailureStageToolResolution, fmt.Errorf("internal generation request is missing"))
	}
	expected, err := clientProtocolControls(g.request)
	actual, actualErr := clientProtocolControls(request)
	if err != nil || actualErr != nil || !samePresetJSON(expected, actual) || !samePresetValue(g.request.Messages, request.Messages) {
		return newResponseStageError(FailureStageToolResolution, fmt.Errorf("internal generation controls or messages changed after compilation"))
	}
	return nil
}

// ValidateOverrides compares the provider envelope before and after field
// filtering/parameter overrides. It does not claim to certify an arbitrary
// provider conversion; unsupported envelope shapes fail instead of bypassing it.
func (g *PresetGenerationGuard) ValidateOverrides(converted any, actual []byte) error {
	if g == nil {
		return nil
	}
	if request, ok := converted.(*dto.GeneralOpenAIRequest); ok {
		if err := g.ValidateRequest(request); err != nil {
			return err
		}
	}
	encoded, err := common.Marshal(converted)
	if err != nil {
		return newResponseStageError(FailureStageToolResolution, err)
	}
	var before, after map[string]any
	if common.UnmarshalPreservingNumbers(encoded, &before) != nil || common.UnmarshalPreservingNumbers(actual, &after) != nil || before == nil || after == nil {
		return newResponseStageError(FailureStageToolResolution, fmt.Errorf("internal generation requires a JSON provider envelope"))
	}
	if before["messages"] == nil && before["contents"] == nil && before["input"] == nil {
		return newResponseStageError(FailureStageToolResolution, fmt.Errorf("provider envelope is not supported by the internal generation guard"))
	}
	for _, field := range []string{
		"messages", "contents", "input", "system", "system_prompt", "systemInstruction", "system_instruction", "instructions",
		"tools", "functions", "tool_choice", "function_call", "toolConfig", "tool_config", "response_format", "parallel_tool_calls",
	} {
		if !samePresetValue(before[field], after[field]) {
			return newResponseStageError(FailureStageToolResolution, fmt.Errorf("parameter overrides changed protected internal generation field %q", field))
		}
	}
	for _, field := range []string{"generationConfig", "generation_config"} {
		beforeConfig, _ := before[field].(map[string]any)
		afterConfig, _ := after[field].(map[string]any)
		for _, key := range []string{"responseMimeType", "responseSchema", "responseJsonSchema", "response_mime_type", "response_schema", "response_json_schema"} {
			if !samePresetValue(beforeConfig[key], afterConfig[key]) {
				return newResponseStageError(FailureStageToolResolution, fmt.Errorf("parameter overrides changed protected internal generation field %q", field+"."+key))
			}
		}
	}
	return nil
}
