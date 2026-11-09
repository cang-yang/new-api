package service

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
)

type responseChoice struct {
	text    strings.Builder
	tools   map[string]*presetToolTextCall
	finish  string
	refused bool
}

// Read event boundaries after buffering, not network Write boundaries. Keep
// each choice and each tool index independent throughout validation.
func responseChoices(body []byte, stream bool) (map[string]*responseChoice, error) {
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
				return nil, fmt.Errorf("stream contains data after its terminator")
			}
			if strings.TrimSpace(data) == "[DONE]" {
				done = true
				continue
			}
		}
		var payload map[string]any
		if err := common.UnmarshalPreservingNumbers([]byte(data), &payload); err != nil || payload == nil {
			return nil, fmt.Errorf("response is not a complete JSON envelope")
		}
		if payload["error"] != nil {
			return nil, fmt.Errorf("upstream returned an error event")
		}
		items, _ := payload["choices"].([]any)
		for index, item := range items {
			choice, ok := item.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("invalid response choice")
			}
			key := presetToolTextChoiceKey(choice, index)
			if stream {
				if number, err := strconv.Atoi(key); err != nil || number < 0 || strconv.Itoa(number) != key {
					return nil, fmt.Errorf("streaming choice index is invalid")
				}
			}
			state := choices[key]
			if state == nil {
				state = &responseChoice{tools: map[string]*presetToolTextCall{}}
				choices[key] = state
			} else if stream && state.finish != "" {
				return nil, fmt.Errorf("stream contains choice data after its finish")
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
				calls = append(calls, map[string]any{"function": old, "index": 0})
			}
			for position, raw := range calls {
				call, ok := raw.(map[string]any)
				if !ok {
					return nil, fmt.Errorf("invalid tool call")
				}
				toolKey := strconv.Itoa(position)
				if stream {
					if call["index"] == nil {
						return nil, fmt.Errorf("streaming tool call requires an index")
					}
					toolKey = fmt.Sprint(call["index"])
					if number, err := strconv.Atoi(toolKey); err != nil || number < 0 || strconv.Itoa(number) != toolKey {
						return nil, fmt.Errorf("streaming tool index is invalid")
					}
				}
				assembled := state.tools[toolKey]
				if assembled == nil {
					assembled = &presetToolTextCall{}
					state.tools[toolKey] = assembled
				}
				fn, _ := call["function"].(map[string]any)
				if name, ok := fn["name"].(string); ok {
					if assembled.name.String() != name {
						assembled.name.WriteString(name)
					}
				}
				if args, ok := fn["arguments"].(string); ok {
					assembled.arguments.WriteString(args)
				}
				if id, ok := call["id"].(string); ok {
					if assembled.id != "" && id != "" && assembled.id != id {
						return nil, fmt.Errorf("streaming tool ID changed")
					}
					assembled.id = id
				}
			}
		}
	}
	return choices, nil
}

func (w *ResponseTextFilterWriter) validateDelivery(body []byte, stream bool) error {
	choices, err := responseChoices(body, stream)
	if err != nil {
		return err
	}
	internal := w.internalToolNames()
	for _, choice := range choices {
		if len(internal) > 0 && len(choice.tools) > 0 {
			return fmt.Errorf("reply transport was not completely restored")
		}
		for _, call := range choice.tools {
			if internal[call.name.String()] {
				return fmt.Errorf("internal reply transport was not restored")
			}
		}
		if w.requirements == nil || w.requirements.request == nil {
			continue
		}
		request := w.requirements.request
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
		if len(choice.tools) == 0 && request.ResponseFormat != nil && (request.ResponseFormat.Type == "json_object" || request.ResponseFormat.Type == "json") {
			if _, err := presetProtocolJSONArgument(choice.text.String()); err != nil {
				return err
			}
		}
	}
	if len(choices) == 0 && w.requirements != nil {
		return fmt.Errorf("response omitted all choices")
	}
	if stream && w.requirements != nil {
		done := false
		for frame := range strings.SplitSeq(strings.ReplaceAll(string(body), "\r\n", "\n"), "\n\n") {
			if strings.TrimSpace(frame) == "data: [DONE]" {
				done = true
			}
		}
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
}

func SnapshotClientRequirements(request *dto.GeneralOpenAIRequest) (*ClientRequirements, error) {
	copy, err := cloneGeneralRequest(request)
	return &ClientRequirements{request: copy}, err
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

// Seal checks original protocol fields after all preset request hooks ran.
func SealClientRequirements(requirements *ClientRequirements, compiled *dto.GeneralOpenAIRequest, filter *dto.ResponseTextFilter) error {
	if requirements == nil || requirements.request == nil {
		return nil
	}
	if err := validatePresetClientToolContractWithMode(requirements.request, compiled, 0); err != nil {
		return newResponseStageError(FailureStageToolResolution, err)
	}
	if requirements.request.ResponseFormat != nil && requirements.request.ResponseFormat.Type == "json_schema" && filter != nil && len(filter.JSONTextPointers) > 0 {
		return newResponseStageError(FailureStagePreflight, fmt.Errorf("JSON schema requests cannot use JSON text-field processing"))
	}
	return nil
}
