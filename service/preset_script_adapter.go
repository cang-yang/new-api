package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/gin-gonic/gin"
)

type presetScriptHook struct {
	Enabled bool   `json:"enabled"`
	Script  string `json:"script"`
}

// SPreset is an adapter for a plugin configuration contract, not a preset name.
// Browser script bodies are retained in the original JSON and never evaluated.
type spresetSettings struct {
	ChatSquash             presetChatSquash `json:"ChatSquash"`
	FinalRequestProcessing presetScriptHook `json:"FinalRequestProcessing"`
	OutputPreprocessing    struct {
		presetScriptHook
		ConsumeToolCalls  bool             `json:"consumeToolCalls"`
		ToolCallFormatter presetScriptHook `json:"toolCallFormatter"`
	} `json:"OutputPreprocessing"`
	ToolBindings map[string]struct {
		Enabled bool   `json:"enabled"`
		Code    string `json:"code"`
	} `json:"ToolBindings"`
}

// The response side receives only this request's registered tool ownership.
// It must never infer ownership from the provider's response.
type PresetScriptResponse struct {
	generationRequest        *dto.GeneralOpenAIRequest
	generationClientControls []byte
	textToClientFormat       bool
	requestHookExecuted      bool
	requestMessagesChanged   bool
	requestInputMessages     int
	requestOutputMessages    int
	settings                 *spresetSettings
	ownedTools               map[string]bool
	fingerprint              [32]byte
	tavern                   *presetTavernSession
	protocol                 *presetProtocolPlan
	resolvedPlan             *AdaptationResolvedPlan
	dataHost                 *presetDataHost
	boundTools               map[string]bool
	historyTools             map[string]bool
	replySources             map[string]dto.ReplyContainer
}

func AttachPresetScriptResponse(c *gin.Context, trace PresetCompileTrace) {
	c.Set("preset_script_response", trace.ScriptResponse)
}

func presetScriptFingerprint(config *dto.SillyTavernPresetConfig) [32]byte {
	// Receive regex switches/model scopes may legitimately differ on a retry.
	// The controller independently checks whether outbound settings are reusable.
	// Ownership here belongs to the exact script definition and active entries.
	data, _ := common.Marshal(struct {
		Preset       json.RawMessage
		Runtime      string
		Entries      map[string]bool
		Modules      map[string]string
		ToolProtocol *dto.SillyTavernToolProtocol
		ReplyAdapter *dto.ReplyAdapter
		DeliveryMode string
	}{config.Preset, config.ScriptRuntime, config.EntryOverrides, config.ScriptModules, config.ToolProtocol, config.ReplyAdapter, config.DeliveryMode})
	return sha256.Sum256(data)
}

func parseSPresetSettings(config *dto.SillyTavernPresetConfig) (*spresetSettings, error) {
	if config == nil || config.ScriptRuntime == "" {
		return nil, nil
	}
	var envelope struct {
		Extensions struct {
			SPreset *spresetSettings `json:"SPreset"`
		} `json:"extensions"`
	}
	if err := common.Unmarshal(config.Preset, &envelope); err != nil {
		return nil, fmt.Errorf("invalid preset script settings")
	}
	if envelope.Extensions.SPreset == nil {
		return nil, fmt.Errorf("spreset-v1 requires extensions.SPreset; browser scripts are not executable hooks")
	}
	return envelope.Extensions.SPreset, nil
}

func preparePresetScripts(config *dto.SillyTavernPresetConfig, preset *dto.SillyTavernPreset, request *dto.GeneralOpenAIRequest, macro *presetMacroContext, clientRequest *dto.GeneralOpenAIRequest) (*PresetScriptResponse, []string, error) {
	if config.ScriptRuntime == "tavern-v1" {
		clientTools := append([]dto.ToolCallRequest(nil), request.Tools...)
		response, warnings, err := prepareTavernScripts(config, request, macro)
		if err != nil {
			return nil, nil, err
		}
		response.ownedTools = map[string]bool{}
		if err := validatePresetScriptTools(request.Tools, clientTools, response.ownedTools); err != nil {
			response.tavern.Close()
			return nil, nil, err
		}
		return response, warnings, nil
	}
	settings, err := parseSPresetSettings(config)
	if err != nil || settings == nil {
		return nil, nil, err
	}
	host, err := newPresetDataHost(config)
	if err != nil {
		return nil, nil, err
	}
	// The compiler supplies the effective client history before preset assembly.
	// This separate view is for cooperating hooks; it never restores messages
	// that a hook dropped or silently changes the final upstream request.
	if clientRequest == nil {
		clientRequest = request
	}
	host.Client, err = common.Marshal(map[string]any{
		"messages": clientRequest.Messages, "tools": clientRequest.Tools, "tool_choice": clientRequest.ToolChoice,
		"functions": clientRequest.Functions, "function_call": clientRequest.FunctionCall,
		"parallel_tool_calls": clientRequest.ParallelTooCalls, "response_format": clientRequest.ResponseFormat,
	})
	if err != nil {
		return nil, nil, err
	}
	response := &PresetScriptResponse{settings: settings, ownedTools: map[string]bool{}, boundTools: map[string]bool{}, historyTools: map[string]bool{}, replySources: map[string]dto.ReplyContainer{}, dataHost: host, fingerprint: presetScriptFingerprint(config)}
	clientTools := append([]dto.ToolCallRequest(nil), request.Tools...)
	order := preset.PromptOrder[0].Order
	for _, group := range preset.PromptOrder {
		if group.CharacterID == 100001 {
			order = group.Order
			break
		}
	}
	enabled := map[string]bool{}
	for _, entry := range order {
		enabled[entry.Identifier] = entry.Enabled
	}
	for identifier, value := range config.EntryOverrides {
		enabled[identifier] = value
	}
	deadline := time.Now().Add(time.Second)
	if err := applyPresetChatSquash(settings.ChatSquash, request, macro, deadline); err != nil {
		return nil, nil, err
	}
	for _, prompt := range preset.Prompts {
		binding, exists := settings.ToolBindings[prompt.Identifier]
		if !exists || !binding.Enabled || !enabled[prompt.Identifier] ||
			len(prompt.InjectionTrigger) > 0 && !slices.Contains(prompt.InjectionTrigger, "normal") {
			continue
		}
		var definition dto.FunctionRequest
		if err := runPresetScriptWithHost("() => {\n"+binding.Code+"\n}", nil, &definition, deadline, host); err != nil {
			return nil, nil, fmt.Errorf("ToolBindings: %w", err)
		}
		for _, tool := range request.Tools {
			if tool.Function.Name == definition.Name {
				return nil, nil, fmt.Errorf("ToolBindings has a duplicate or conflicting tool name")
			}
		}
		request.Tools = append(request.Tools, dto.ToolCallRequest{Type: "function", Function: definition})
		response.boundTools[definition.Name] = true
		if settings.OutputPreprocessing.ConsumeToolCalls && settings.OutputPreprocessing.ToolCallFormatter.Enabled {
			response.replySources[definition.Name] = dto.ReplyContainer{Name: definition.Name, Formatter: true, OutputKind: "text", SourcePromptID: prompt.Identifier}
		}
	}
	beforeScript, err := common.DeepCopy(request)
	if err != nil {
		return nil, nil, err
	}
	if settings.FinalRequestProcessing.Enabled {
		if err := applyPresetRequestScriptWithHost(settings.FinalRequestProcessing.Script, request, deadline, host); err != nil {
			return nil, nil, err
		}
		response.requestHookExecuted = true
	}
	response.requestInputMessages = len(beforeScript.Messages)
	response.requestOutputMessages = len(request.Messages)
	response.requestMessagesChanged = !samePresetValue(beforeScript.Messages, request.Messages)
	if err := validatePresetScriptTools(request.Tools, clientTools, response.ownedTools); err != nil {
		return nil, nil, err
	}
	if err := classifyPresetHistoryTools(beforeScript, request, response); err != nil {
		return nil, nil, err
	}
	for name, source := range response.replySources {
		definition, _ := findToolByName(request.Tools, name)
		data, err := common.Marshal([]any{source.SourcePromptID, settings.ToolBindings[source.SourcePromptID].Code, definition, settings.OutputPreprocessing.ToolCallFormatter})
		if err != nil {
			return nil, nil, err
		}
		sum := sha256.Sum256(data)
		source.SourceFingerprint = hex.EncodeToString(sum[:])
		response.replySources[name] = source
	}
	warnings := []string{"SPreset data hooks run in an isolated server runtime; browser APIs, remote scripts, tool actions and log uploads are not executed"}
	// Do not pretend that other enabled plugin features are supported.
	var raw struct {
		Extensions struct {
			SPreset map[string]json.RawMessage `json:"SPreset"`
		} `json:"extensions"`
	}
	if err := common.Unmarshal(config.Preset, &raw); err != nil {
		return nil, nil, err
	}
	for key, value := range raw.Extensions.SPreset {
		if slices.Contains([]string{"ToolBindings", "FinalRequestProcessing", "OutputPreprocessing", "RegexBinding", "ChatSquash"}, key) {
			continue
		}
		var feature struct {
			Enabled bool `json:"enabled"`
		}
		_ = common.Unmarshal(value, &feature)
		if key == "ForcedPostProcessing" && feature.Enabled {
			var processing struct {
				Mode string `json:"mode"`
			}
			_ = common.Unmarshal(value, &processing)
			if processing.Mode == "" || processing.Mode == "none" {
				continue
			}
		}
		if feature.Enabled || string(value) == "true" {
			warnings = append(warnings, fmt.Sprintf("SPreset feature %s is preserved but not executed by spreset-v1", key))
		}
		if key == "MessageInjections" {
			var entries map[string]struct {
				Enabled bool `json:"enabled"`
			}
			_ = common.Unmarshal(value, &entries)
			for _, entry := range entries {
				if entry.Enabled {
					warnings = append(warnings, "SPreset MessageInjections are preserved but not executed by spreset-v1")
					break
				}
			}
		}
	}
	return response, warnings, nil
}

func validatePresetScriptTools(tools, clientTools []dto.ToolCallRequest, owned map[string]bool) error {
	if len(tools) > 128 {
		return fmt.Errorf("preset scripts allow at most 128 tools")
	}
	seen := map[string]bool{}
	for _, tool := range tools {
		original := slices.IndexFunc(clientTools, func(client dto.ToolCallRequest) bool {
			return client.Function.Name == tool.Function.Name && client.Type == tool.Type
		})
		if original >= 0 {
			left, _ := common.Marshal(clientTools[original])
			right, _ := common.Marshal(tool)
			if !samePresetJSON(left, right) {
				return fmt.Errorf("preset scripts cannot modify client tools")
			}
		} else {
			if tool.Type != "function" || !presetScriptToolName.MatchString(tool.Function.Name) ||
				len(tool.Function.Parameters) == 0 || common.GetJsonType(tool.Function.Parameters) != "object" {
				return fmt.Errorf("preset scripts must declare named function tools with object parameters")
			}
			owned[tool.Function.Name] = true
		}
		key := tool.Type + ":" + tool.Function.Name
		if seen[key] {
			return fmt.Errorf("preset scripts returned duplicate tools")
		}
		seen[key] = true
	}
	for _, client := range clientTools {
		if !seen[client.Type+":"+client.Function.Name] {
			return fmt.Errorf("preset scripts cannot remove client tools")
		}
	}
	return nil
}

var presetScriptToolName = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// validatePresetClientToolContract checks only the current request. It never
// reconstructs a conversation or executes tools on behalf of the client.
func validatePresetClientToolContract(original, compiled *dto.GeneralOpenAIRequest) error {
	return validatePresetClientToolContractWithMode(original, compiled, 0)
}

// validatePresetClientToolContractWithMode validates the client-visible
// contract before an active gateway protocol plan is allowed to project the
// provider-visible tools and tool_choice. Mode 1 permits internal reply
// selection, but still protects history, legacy controls and tool definitions.
func validatePresetClientToolContractWithMode(original, compiled *dto.GeneralOpenAIRequest, mode int) error {
	if original.ResponseFormat != nil {
		before, _ := common.Marshal(original.ResponseFormat)
		after, _ := common.Marshal(compiled.ResponseFormat)
		if !samePresetJSON(before, after) {
			return fmt.Errorf("preset conflicts with the client response_format")
		}
	}
	if !samePresetValue(original.ParallelTooCalls, compiled.ParallelTooCalls) {
		return fmt.Errorf("preset conflicts with client parallel_tool_calls")
	}
	protected := len(original.Tools) > 0 || len(original.Functions) > 0 || original.ToolChoice != nil || len(original.FunctionCall) > 0
	var history []dto.Message
	for _, message := range original.Messages {
		if len(message.ToolCalls) > 0 || len(message.FunctionCall) > 0 || message.ToolCallId != "" || message.Role == "tool" || message.Role == "function" || len(message.Tools) > 0 {
			history = append(history, message)
		}
	}
	if !protected && len(history) == 0 {
		return nil
	}
	beforeChoice, afterChoice := original.ToolChoice, compiled.ToolChoice
	if beforeChoice == nil {
		beforeChoice = "auto"
	}
	if afterChoice == nil {
		afterChoice = "auto"
	}
	if mode == 0 {
		before, _ := common.Marshal(beforeChoice)
		after, _ := common.Marshal(afterChoice)
		if !samePresetJSON(before, after) {
			return fmt.Errorf("preset conflicts with client tools: tool_choice was changed")
		}
	}
	for _, pair := range [][2]any{{original.Functions, compiled.Functions}, {original.FunctionCall, compiled.FunctionCall}} {
		before, _ := common.Marshal(pair[0])
		after, _ := common.Marshal(pair[1])
		if !samePresetJSON(before, after) {
			return fmt.Errorf("preset conflicts with client tools: legacy function controls were changed")
		}
	}
	owned := map[string]bool{}
	for _, tool := range original.Tools {
		before, _ := common.Marshal(tool)
		if !slices.ContainsFunc(compiled.Tools, func(candidate dto.ToolCallRequest) bool {
			after, _ := common.Marshal(candidate)
			return samePresetJSON(before, after)
		}) {
			return fmt.Errorf("preset conflicts with client tools: tool definition was removed or modified")
		}
	}
	for _, tool := range compiled.Tools {
		if !slices.ContainsFunc(original.Tools, func(candidate dto.ToolCallRequest) bool {
			return candidate.Type == tool.Type && candidate.Function.Name == tool.Function.Name
		}) {
			owned[tool.Function.Name] = true
		}
	}
	if mode == 0 {
		if protected && len(owned) > 0 {
			return fmt.Errorf("preset conflicts with client tools: required tool selection was expanded")
		}
	}
	// Preserve complete tool messages and their order, including opaque result
	// strings and provider metadata. Extra preset demonstrations may not reuse IDs.
	callCounts, resultCounts := map[string]int{}, map[string]int{}
	for _, message := range compiled.Messages {
		if message.ToolCallId != "" {
			resultCounts[message.ToolCallId]++
		}
		var calls []struct {
			ID string `json:"id"`
		}
		if len(message.ToolCalls) > 0 {
			if err := common.Unmarshal(message.ToolCalls, &calls); err != nil {
				return fmt.Errorf("preset returned invalid tool history")
			}
		}
		for _, call := range calls {
			callCounts[call.ID]++
		}
	}
	remaining := compiled.Messages
	for _, message := range history {
		encoded, _ := common.Marshal(message)
		index := slices.IndexFunc(remaining, func(candidate dto.Message) bool {
			value, _ := common.Marshal(candidate)
			return samePresetJSON(encoded, value)
		})
		if index < 0 {
			return fmt.Errorf("preset conflicts with client tools: tool history was removed, reordered or modified")
		}
		remaining = remaining[index+1:]
		if message.ToolCallId != "" && resultCounts[message.ToolCallId] != 1 {
			return fmt.Errorf("preset conflicts with client tools: tool result ID was reused")
		}
		var calls []struct {
			ID       string `json:"id"`
			Function struct {
				Name string `json:"name"`
			} `json:"function"`
		}
		if len(message.ToolCalls) > 0 {
			if err := common.Unmarshal(message.ToolCalls, &calls); err != nil {
				return fmt.Errorf("client tool history is invalid")
			}
		}
		for _, call := range calls {
			if owned[call.Function.Name] && mode == 0 {
				return fmt.Errorf("preset conflicts with client tools: tool history name was claimed by the preset")
			}
			if callCounts[call.ID] != 1 {
				return fmt.Errorf("preset conflicts with client tools: tool call ID was reused")
			}
		}
	}
	return nil
}

func applyPresetRequestScript(script string, request *dto.GeneralOpenAIRequest, deadline time.Time) error {
	return applyPresetRequestScriptWithHost(script, request, deadline, nil)
}

func applyPresetRequestScriptWithHost(script string, request *dto.GeneralOpenAIRequest, deadline time.Time, host *presetDataHost) error {
	encoded, err := common.Marshal(request)
	if err != nil {
		return err
	}
	var after map[string]json.RawMessage
	source := strings.TrimRight(strings.TrimSpace(script), "; \r\n\t")
	source = "input => { if (input.client) freezePresetDataView(input.client); if (input.preset) freezePresetDataView(input.preset); const result = (" + source + ")(input); return result === undefined ? input.request : result; }"
	var client, preset json.RawMessage
	if host != nil {
		client = host.Client
		preset = host.Settings
	}
	err = runPresetScriptWithHost(source, map[string]any{
		"request": json.RawMessage(encoded), "model": request.Model,
		"client": client,
		"preset": preset,
		"source": "openai", "stream": request.Stream != nil && *request.Stream, "preview": false,
		"provider": map[string]string{"adapter": "openai", "model": request.Model},
	}, &after, deadline, host)
	if err != nil {
		return fmt.Errorf("FinalRequestProcessing: %w", err)
	}
	return applyPresetRequestChanges(encoded, after, request)
}

func applyPresetRequestChanges(encoded []byte, after map[string]json.RawMessage, request *dto.GeneralOpenAIRequest) error {
	var before map[string]json.RawMessage
	if err := common.Unmarshal(encoded, &before); err != nil {
		return err
	}
	if after == nil {
		return fmt.Errorf("FinalRequestProcessing must return a request object")
	}
	// ST's final-fetch hook can contain provider transport settings. Accept
	// only configurations exactly represented by the normalized OpenAI fields.
	if value, exists := after["toolConfig"]; exists {
		var config struct {
			FunctionCallingConfig struct {
				Mode    string   `json:"mode"`
				Allowed []string `json:"allowedFunctionNames"`
			} `json:"functionCallingConfig"`
		}
		var tools []dto.ToolCallRequest
		var choice string
		if common.Unmarshal(value, &config) != nil || common.Unmarshal(after["tools"], &tools) != nil ||
			common.Unmarshal(after["tool_choice"], &choice) != nil {
			return fmt.Errorf("FinalRequestProcessing toolConfig cannot be represented by tool_choice")
		}
		normalizedConfig, err := common.Marshal(config)
		if err != nil || !presetScriptJSONPreserved(value, normalizedConfig) {
			return fmt.Errorf("FinalRequestProcessing toolConfig contains unsupported options")
		}
		mode := config.FunctionCallingConfig.Mode
		if !(mode == "ANY" && choice == "required" || mode == "AUTO" && choice == "auto" || mode == "NONE" && choice == "none") {
			return fmt.Errorf("FinalRequestProcessing toolConfig conflicts with tool_choice")
		}
		if len(config.FunctionCallingConfig.Allowed) > 0 {
			names := config.FunctionCallingConfig.Allowed
			seen := map[string]bool{}
			for _, name := range names {
				if _, exists := findToolByName(tools, name); !exists || seen[name] {
					return fmt.Errorf("FinalRequestProcessing toolConfig references an unknown or duplicate tool")
				}
				seen[name] = true
			}
			if mode == "ANY" && len(names) == 1 {
				after["tool_choice"], _ = common.Marshal(map[string]any{"type": "function", "function": map[string]string{"name": names[0]}})
			} else if mode != "NONE" && len(names) != len(tools) {
				return fmt.Errorf("FinalRequestProcessing restricted toolConfig cannot be represented without changing tool selection")
			}
		}
		delete(after, "toolConfig")
	}
	if value, exists := after["custom_include_body"]; exists {
		var body, effort string
		if common.Unmarshal(value, &body) != nil || common.Unmarshal(after["reasoning_effort"], &effort) != nil ||
			strings.TrimSpace(body) != "reasoning_effort: "+effort {
			return fmt.Errorf("FinalRequestProcessing custom_include_body only supports a matching reasoning_effort")
		}
		delete(after, "custom_include_body")
	}
	// A preset is not allowed to change routing, streaming, billing limits, or
	// inject arbitrary provider fields after the gateway's request validation.
	allowed := []string{"messages", "tools", "tool_choice", "response_format", "stop", "reasoning_effort"}
	for key := range before {
		if !slices.Contains(allowed, key) && !samePresetJSON(before[key], after[key]) {
			return fmt.Errorf("FinalRequestProcessing cannot change request field %q", key)
		}
	}
	for key := range after {
		if !slices.Contains(allowed, key) && !samePresetJSON(before[key], after[key]) {
			return fmt.Errorf("FinalRequestProcessing returned unsupported request field %q", key)
		}
	}
	encoded, err := common.Marshal(after)
	if err != nil {
		return err
	}
	var changed dto.GeneralOpenAIRequest
	if err := common.Unmarshal(encoded, &changed); err != nil {
		return fmt.Errorf("FinalRequestProcessing returned invalid request fields")
	}
	// A successful unmarshal is not proof of compatibility: typed DTOs ignore
	// unknown nested properties. Reject data loss before sending upstream.
	normalized, err := common.Marshal(changed)
	if err != nil {
		return fmt.Errorf("FinalRequestProcessing returned invalid request fields")
	}
	var represented map[string]json.RawMessage
	if err := common.Unmarshal(normalized, &represented); err != nil {
		return err
	}
	for _, field := range allowed {
		if value, exists := after[field]; exists && !presetScriptJSONPreserved(value, represented[field]) {
			return fmt.Errorf("FinalRequestProcessing contains unsupported nested data in %q", field)
		}
	}
	if len(changed.Messages) == 0 || len(changed.Messages) > 4096 {
		return fmt.Errorf("FinalRequestProcessing requires 1 to 4096 messages")
	}
	for _, message := range changed.Messages {
		if !slices.Contains([]string{"system", "developer", "user", "assistant", "tool", "function"}, message.Role) {
			return fmt.Errorf("FinalRequestProcessing returned an unsupported message role")
		}
	}
	// Apply only the permitted fields; round-tripping the complete request would
	// discard provider metadata in DTO fields marked json:\"-\".
	request.Messages, request.Tools, request.ToolChoice = changed.Messages, changed.Tools, changed.ToolChoice
	request.ResponseFormat, request.Stop, request.ReasoningEffort = changed.ResponseFormat, changed.Stop, changed.ReasoningEffort
	return nil
}

func presetScriptJSONPreserved(input, output json.RawMessage) bool {
	var source, result any
	if common.UnmarshalPreservingNumbers(input, &source) != nil {
		return false
	}
	if len(output) == 0 {
		// Known optional top-level fields may normalize an explicit empty value.
		return source == nil || source == ""
	}
	if common.UnmarshalPreservingNumbers(output, &result) != nil {
		return false
	}
	var contains func(any, any) bool
	contains = func(a, b any) bool {
		switch value := a.(type) {
		case map[string]any:
			target, ok := b.(map[string]any)
			if !ok {
				return false
			}
			for key, item := range value {
				other, exists := target[key]
				if !exists || !contains(item, other) {
					return false
				}
			}
			return true
		case []any:
			target, ok := b.([]any)
			if !ok || len(value) != len(target) {
				return false
			}
			for i, item := range value {
				if !contains(item, target[i]) {
					return false
				}
			}
			return true
		default:
			return reflect.DeepEqual(a, b)
		}
	}
	return contains(source, result)
}

func samePresetJSON(a, b json.RawMessage) bool {
	if len(a) == 0 || len(b) == 0 {
		return len(a) == len(b)
	}
	var left, right any
	return common.UnmarshalPreservingNumbers(a, &left) == nil &&
		common.UnmarshalPreservingNumbers(b, &right) == nil && reflect.DeepEqual(left, right)
}
