package service

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
)

const (
	presetProtocolPhaseAuxiliary = "auxiliary"
	presetProtocolPhaseFinal     = "final"

	presetProtocolContractText = "text"
	presetProtocolContractJSON = "json"
	presetProtocolContractTool = "tool"

	presetProtocolVisibilityClientVisible   = "client_visible"
	presetProtocolVisibilityGatewayInternal = "gateway_internal"
)

// PresetProtocolAudit is safe to expose in request traces. It deliberately
// contains only role names and diagnostics, never message or schema bodies.
type PresetProtocolAudit struct {
	Enabled          bool     `json:"enabled"`
	Eligible         bool     `json:"eligible"`
	Activated        bool     `json:"activated"`
	Reason           string   `json:"reason,omitempty"`
	Phase            string   `json:"phase,omitempty"`
	Contract         string   `json:"contract,omitempty"`
	ClientFinal      string   `json:"client_final,omitempty"`
	PresetFinal      string   `json:"preset_final,omitempty"`
	FinalTool        string   `json:"final_tool,omitempty"`
	Auxiliary        []string `json:"auxiliary,omitempty"`
	Actions          []string `json:"actions,omitempty"`
	Contents         []string `json:"contents,omitempty"`
	Visibility       string   `json:"visibility,omitempty"`
	ResponseArgument string   `json:"response_argument,omitempty"`
	Diagnostics      []string `json:"diagnostics,omitempty"`
	Fingerprint      string   `json:"fingerprint,omitempty"`
}

type presetProtocolPlan struct {
	audit            PresetProtocolAudit
	clientFinal      string
	presetFinal      string
	finalTool        string
	auxiliary        []string
	actions          []string
	contents         []string
	contract         string
	responseTarget   string
	responseArgument string
	phasePolicy      string
	validationMode   int
	phase            string
	terminal         dto.ToolCallRequest
	visible          map[string]bool
	containers       map[string]dto.ReplyContainer
	internal         map[string]bool
	requireContainer bool
}

// A declared reply adapter identifies the preset's delivery mechanism. It does
// not authorize replacing that mechanism or bypassing required data hooks.
func presetSupportsClientDelivery(config *dto.SillyTavernPresetConfig) bool {
	return config != nil && config.ScriptRuntime != "" && config.ReplyAdapter != nil &&
		config.ReplyAdapter.Version == 2 && len(config.ReplyAdapter.Containers) > 0 &&
		(config.ToolText == nil || !config.ToolText.Enabled)
}

// A client's no-tool result contract does not disable the preset's private
// reply transport. Only the registered reply sources may generate calls here;
// the writer still validates the restored result against the original request.
func compilePresetWithInternalReply(config *dto.SillyTavernPresetConfig, original *dto.GeneralOpenAIRequest, context SillyTavernContext) (*dto.GeneralOpenAIRequest, PresetCompileTrace, error) {
	structured := original.ResponseFormat != nil && original.ResponseFormat.Type != "" && original.ResponseFormat.Type != "text"
	if structured && !config.ReplyAdapter.StructuredOutput {
		return nil, PresetCompileTrace{}, newResponseStageError(FailureStagePreflight, fmt.Errorf("client structured output requires a confirmed reply-source format conversion"))
	}
	// A response format constrains completed text, not genuine business calls.
	// Keep current business controls for compatible hooks; only explicit none
	// (or no business tools) makes this an internal-only generation request.
	internalOnly := clientForbidsToolCalls(original) || len(original.Tools) == 0 && len(original.Functions) == 0
	if structured {
		if _, err := SnapshotClientRequirements(original); err != nil {
			return nil, PresetCompileTrace{}, err
		}
	}
	input, err := cloneGeneralRequest(original)
	if err != nil {
		return nil, PresetCompileTrace{}, err
	}
	if internalOnly {
		input.Tools, input.ToolChoice = nil, nil
		input.Functions, input.FunctionCall = nil, nil
	}
	if structured {
		input.ResponseFormat = nil
	}
	context.internalReplyOnly = internalOnly
	context.scriptClient = original
	compiled, trace, err := compileSillyTavernPreset(config, input, context)
	if err != nil {
		return nil, trace, err
	}
	delivered := false
	defer func() {
		if !delivered && trace.ScriptResponse != nil && trace.ScriptResponse.tavern != nil {
			trace.ScriptResponse.tavern.Close()
		}
	}()
	response := trace.ScriptResponse
	if response == nil || response.protocol == nil || !response.protocol.audit.Activated {
		return nil, trace, newResponseStageError(FailureStageToolResolution, fmt.Errorf("internal reply requires a confirmed reply transport"))
	}
	if structured {
		format, err := common.Marshal(original.ResponseFormat)
		if err != nil {
			return nil, trace, err
		}
		if len(compiled.Messages) >= 4096 {
			return nil, trace, newResponseStageError(FailureStagePreflight, fmt.Errorf("reply format instruction exceeded the message limit"))
		}
		// This is the approved conversion contract, not a replay of the raw
		// business task. The original formatter and processing chain still run.
		compiled.Messages = append(compiled.Messages, dto.Message{Role: "system", Content: "The client requires the following final reply format after the configured preset formatter and cleanup. " +
			"For a completed text answer, use the preset reply transport as configured. This does not replace any required client business tool call. " +
			"Put the requested final result in the reply content; do not add Markdown fences around the JSON result. " +
			"This is a converted format with local validation, not native provider schema enforcement. Final reply format: " + string(format)})
		trace.Messages = append(trace.Messages, PresetMessageSource{Index: len(compiled.Messages) - 1, Role: "system", Source: "client_format_conversion"})
		response.textToClientFormat = response.protocol.contract == presetProtocolContractText && config.ReplyAdapter.FormattedOutput != "client_format"
		response.protocol.clientFinal = original.ResponseFormat.Type
		response.protocol.actions = append(response.protocol.actions, "client_format_conversion")
		response.protocol.audit.ClientFinal = original.ResponseFormat.Type
		response.protocol.audit.Actions = append(response.protocol.audit.Actions, "client_format_conversion")
		response.protocol.audit.Fingerprint = response.protocol.fingerprint()
	}
	// Keep history-only definitions when the script selected a reply source.
	// Some providers require definitions for history; the named selection and
	// restoration guard keep those tools from becoming callable reply sources.
	tools := make([]dto.ToolCallRequest, 0, len(response.protocol.containers))
	for _, tool := range compiled.Tools {
		if _, declared := response.protocol.containers[tool.Function.Name]; !internalOnly || declared || response.historyTools[tool.Function.Name] {
			tools = append(tools, tool)
		}
	}
	if len(tools) == 0 {
		return nil, trace, newResponseStageError(FailureStageToolResolution, fmt.Errorf("internal reply has no callable reply transport"))
	}
	compiled.Tools = tools
	if internalOnly && compiled.ToolChoice == "none" {
		return nil, trace, newResponseStageError(FailureStageToolResolution, fmt.Errorf("preset disabled its required reply transport"))
	}
	if name := presetToolChoiceName(compiled.ToolChoice); internalOnly && name != "" {
		if _, declared := response.protocol.containers[name]; !declared {
			return nil, trace, newResponseStageError(FailureStageToolResolution, fmt.Errorf("preset selected a tool that is not a reply source"))
		}
	}
	if err := response.freezeGenerationContract(original, compiled); err != nil {
		return nil, trace, err
	}
	delivered = true
	return compiled, trace, nil
}

func clientForbidsToolCalls(request *dto.GeneralOpenAIRequest) bool {
	if request.ToolChoice == "none" {
		return true
	}
	var legacy string
	return len(request.FunctionCall) > 0 && common.Unmarshal(request.FunctionCall, &legacy) == nil && legacy == "none"
}

// buildPresetProtocolPlan accepts only explicitly declared reply containers.
// Client business tools and history are protected, not assigned gateway roles.
func buildPresetProtocolPlan(original, compiled *dto.GeneralOpenAIRequest, config *dto.SillyTavernPresetConfig, response *PresetScriptResponse, internalReplyOnly bool) (*presetProtocolPlan, int, error) {
	if original == nil || compiled == nil || config == nil || response == nil {
		return nil, 0, nil
	}
	validationMode := 0
	clientTools := len(original.Tools) > 0 && !clientForbidsToolCalls(original)
	if clientTools {
		// Adding a reply source must not override a business-call decision.
		// Only hooks that preserve the client's selection can use this branch.
		beforeChoice, afterChoice := original.ToolChoice, compiled.ToolChoice
		if beforeChoice == nil {
			beforeChoice = "auto"
		}
		if afterChoice == nil {
			afterChoice = "auto"
		}
		if !samePresetValue(beforeChoice, afterChoice) {
			return nil, 0, fmt.Errorf("preset conflicts with client tools: tool_choice was changed")
		}
		validationMode = 1
	}
	if internalReplyOnly {
		// Only compilePresetWithInternalReply can detach current business
		// controls. History remains protected even when selection is internal.
		if len(original.Tools) > 0 || original.ToolChoice != nil || len(original.Functions) > 0 || len(original.FunctionCall) > 0 {
			return nil, 0, fmt.Errorf("internal reply compilation still contains current client tool controls")
		}
		validationMode = 1
	}
	if err := validatePresetClientToolContractWithMode(original, compiled, validationMode); err != nil {
		return nil, 0, err
	}
	if err := config.ReplyAdapter.Validate(); err != nil {
		return nil, 0, err
	}
	protocol := config.ToolProtocol
	if protocol != nil && protocol.Enabled && (protocol.ClientFinal != "" || len(protocol.Auxiliary) > 0 || len(protocol.Action) > 0 || protocol.PhasePolicy != "" && protocol.PhasePolicy != "disabled") {
		return nil, 0, fmt.Errorf("legacy tool orchestration requires review; declare reply transports instead")
	}
	containers := []dto.ReplyContainer(nil)
	if config.ReplyAdapter != nil {
		containers = config.ReplyAdapter.Containers
	}
	// Lossless migration is limited to an explicit content transport plus a field
	// or the preset's explicitly enabled formatter. No history or schema guessing.
	if len(containers) == 0 && protocol != nil && protocol.Enabled {
		names := append([]string(nil), protocol.Content...)
		if protocol.PresetFinal != "" && !slices.Contains(names, protocol.PresetFinal) {
			names = append(names, protocol.PresetFinal)
		}
		for _, name := range names {
			kind := "text"
			if protocol.ResponseTarget == "json" {
				kind = "json_object"
			}
			container := dto.ReplyContainer{Name: name, OutputKind: kind}
			if protocol.ResponseArgument != "" {
				for _, segment := range strings.Split(protocol.ResponseArgument, ".") {
					if segment == "" || strings.ContainsAny(segment, "/[]~") || segment[0] >= '0' && segment[0] <= '9' {
						return nil, 0, fmt.Errorf("legacy reply field is ambiguous; choose a JSON Pointer")
					}
				}
				pointer := "/" + strings.ReplaceAll(strings.ReplaceAll(protocol.ResponseArgument, "~", "~0"), ".", "/")
				container.ArgumentPointer = &pointer
			} else {
				container.Formatter = presetProtocolHasResponseFormatter(response)
			}
			if container.ArgumentPointer == nil && !container.Formatter {
				return nil, 0, fmt.Errorf("reply transport requires a reply field or the preset formatter")
			}
			containers = append(containers, container)
		}
	}
	owned := orderedOwnedToolNames(compiled, response.ownedTools)
	candidates := make([]string, 0, len(owned))
	for _, name := range owned {
		if !response.historyTools[name] {
			candidates = append(candidates, name)
		}
	}
	if len(containers) == 0 && len(candidates) == 1 && presetProtocolHasResponseFormatter(response) && response.settings.OutputPreprocessing.ConsumeToolCalls {
		if container, exists := response.replySources[candidates[0]]; exists {
			containers = append(containers, container)
		}
	}
	if len(owned) == 0 {
		return inactivePresetProtocolPlan(false, false, "no_internal_transport"), 0, nil
	}
	if len(candidates) == 0 {
		return nil, 0, fmt.Errorf("preset generated history tools without a reply source")
	}
	if !internalReplyOnly && len(original.Functions) > 0 {
		return nil, 0, fmt.Errorf("legacy client functions cannot be combined with an internal reply transport")
	}
	if original.ResponseFormat != nil && original.ResponseFormat.Type == "json_schema" {
		return nil, 0, fmt.Errorf("reply transport cannot transform a client JSON schema")
	}
	declared := map[string]dto.ReplyContainer{}
	for _, container := range containers {
		if container.SourceFingerprint != "" {
			source := response.replySources[container.Name]
			if source.SourceFingerprint != container.SourceFingerprint || source.SourcePromptID != container.SourcePromptID {
				return nil, 0, fmt.Errorf("detected reply source has changed; check the preset and confirm its reply source again")
			}
		}
		if !response.ownedTools[container.Name] || response.historyTools[container.Name] {
			return nil, 0, fmt.Errorf("reply source must refer to a current preset reply tool")
		}
		declared[container.Name] = container
	}
	for _, name := range owned {
		if _, ok := declared[name]; !ok && !response.historyTools[name] {
			return nil, 0, fmt.Errorf("preset tool %q has no declared reply transport", name)
		}
	}
	if len(response.historyTools) > 0 && compiled.ToolChoice != "none" && !clientTools {
		if _, exists := declared[presetToolChoiceName(compiled.ToolChoice)]; !exists {
			return nil, 0, fmt.Errorf("preset must select a reply tool without allowing history-only tools")
		}
	}
	plan := &presetProtocolPlan{phase: presetProtocolPhaseFinal, contents: candidates, internal: map[string]bool{}, visible: map[string]bool{}, containers: declared}
	plan.requireContainer = config.ReplyAdapter != nil && config.ReplyAdapter.RequireContainer
	if clientTools {
		for _, tool := range original.Tools {
			plan.visible[tool.Function.Name] = true
		}
	}
	for _, name := range owned {
		plan.internal[name] = true
		if response.historyTools[name] {
			continue
		}
		container := declared[name]
		contract := "text"
		if container.OutputKind == "json_object" {
			contract = "json"
		}
		if original.ResponseFormat != nil && original.ResponseFormat.Type != "" && original.ResponseFormat.Type != "text" && contract != "json" {
			return nil, 0, fmt.Errorf("reply transport output does not satisfy the client's JSON format")
		}
		if plan.contract != "" && plan.contract != contract {
			return nil, 0, fmt.Errorf("reply transports must declare the same output kind")
		}
		plan.contract = contract
	}
	plan.audit = PresetProtocolAudit{Enabled: true, Eligible: true, Activated: true, Reason: "explicit_reply_transport", Contents: candidates, Contract: plan.contract}
	plan.audit.Fingerprint = plan.fingerprint()
	return plan, validationMode, nil
}

func inactivePresetProtocolPlan(enabled, eligible bool, reason string) *presetProtocolPlan {
	plan := &presetProtocolPlan{audit: PresetProtocolAudit{
		Enabled:     enabled,
		Eligible:    eligible,
		Reason:      reason,
		Diagnostics: []string{reason},
	}}
	plan.audit.Fingerprint = plan.fingerprint()
	return plan
}

func (p *presetProtocolPlan) InternalToolNames() map[string]bool {
	if p == nil {
		return nil
	}
	return p.internal
}

func presetProtocolHasResponseFormatter(response *PresetScriptResponse) bool {
	return response != nil && response.settings != nil &&
		response.settings.OutputPreprocessing.ToolCallFormatter.Enabled
}

func orderedOwnedToolNames(request *dto.GeneralOpenAIRequest, owned map[string]bool) []string {
	if request == nil {
		return nil
	}
	names := make([]string, 0, len(owned))
	seen := make(map[string]bool, len(owned))
	for _, tool := range request.Tools {
		name := tool.Function.Name
		if owned[name] && !seen[name] {
			names = append(names, name)
			seen[name] = true
		}
	}
	for name := range owned {
		if !seen[name] {
			names = append(names, name)
		}
	}
	slices.Sort(names[len(seen):])
	return names
}

func clientDeclaresToolContract(request *dto.GeneralOpenAIRequest) bool {
	if request == nil {
		return false
	}
	if len(request.Tools) > 0 || len(request.Functions) > 0 || request.ToolChoice != nil || len(request.FunctionCall) > 0 {
		return true
	}
	for _, message := range request.Messages {
		if clientToolConversationMessage(message) {
			return true
		}
	}
	return false
}

func clientToolConversationMessage(message dto.Message) bool {
	return len(message.ToolCalls) > 0 || len(message.FunctionCall) > 0 || len(message.Tools) > 0 ||
		message.ToolCallId != "" || message.Role == "tool" || message.Role == "function"
}

func presetToolChoiceName(choice any) string {
	if _, ok := choice.(string); ok {
		return ""
	}
	var value map[string]any
	encoded, err := common.Marshal(choice)
	if err != nil || common.UnmarshalPreservingNumbers(encoded, &value) != nil {
		return ""
	}
	if name, ok := value["name"].(string); ok {
		return name
	}
	function, _ := value["function"].(map[string]any)
	name, _ := function["name"].(string)
	return name
}

func findToolByName(tools []dto.ToolCallRequest, name string) (dto.ToolCallRequest, bool) {
	for _, tool := range tools {
		if tool.Type == "function" && tool.Function.Name == name {
			return tool, true
		}
	}
	return dto.ToolCallRequest{}, false
}

func resolvePresetProtocolContract(original *dto.GeneralOpenAIRequest, clientFinal string, protocol *dto.SillyTavernToolProtocol) (string, bool, error) {
	explicit := ""
	if protocol != nil {
		explicit = protocol.ResponseTarget
	}
	if clientFinal != "" {
		if explicit != "" && explicit != "auto" && explicit != presetProtocolContractTool {
			return "", false, fmt.Errorf("sillytavern_preset tool_protocol.response_target cannot replace a client_final tool contract")
		}
		return presetProtocolContractTool, true, nil
	}
	if original != nil && original.ResponseFormat != nil {
		switch strings.ToLower(strings.TrimSpace(original.ResponseFormat.Type)) {
		case "json_schema":
			return "", false, nil
		case "json", "json_object":
			if explicit != "" && explicit != "auto" && explicit != "json" {
				return "", false, fmt.Errorf("channel reply target conflicts with client JSON format")
			}
			return presetProtocolContractJSON, true, nil
		}
	}
	if explicit != "" && explicit != "auto" {
		if explicit == presetProtocolContractTool {
			return "", false, nil
		}
		return explicit, true, nil
	}
	if original != nil && original.ResponseFormat != nil {
		switch strings.ToLower(strings.TrimSpace(original.ResponseFormat.Type)) {
		case "json", "json_object":
			return presetProtocolContractJSON, true, nil
		}
	}
	return presetProtocolContractText, true, nil
}

func (p *presetProtocolPlan) fingerprint() string {
	if p == nil {
		return ""
	}
	data, err := common.Marshal(struct {
		Version          string
		Enabled          bool
		ResponseTarget   string
		ResponseArgument string
		PhasePolicy      string
		Contract         string
		ClientFinal      string
		PresetFinal      string
		FinalTool        string
		Auxiliary        []string
		Actions          []string
		Contents         []string
		Visibility       string
		Containers       map[string]dto.ReplyContainer
		ClientTools      map[string]bool
		RequireContainer bool
	}{
		Version:          "reply-adapter-v2",
		Containers:       p.containers,
		ClientTools:      p.visible,
		RequireContainer: p.requireContainer,
		Enabled:          p.audit.Enabled,
		ResponseTarget:   p.responseTarget,
		ResponseArgument: p.responseArgument,
		PhasePolicy:      p.phasePolicy,
		Contract:         p.contract,
		ClientFinal:      p.clientFinal,
		PresetFinal:      p.presetFinal,
		FinalTool:        p.finalTool,
		Auxiliary:        p.auxiliary,
		Actions:          p.actions,
		Contents:         p.contents,
		Visibility:       p.audit.Visibility,
	})
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func parsePresetProtocolToolCalls(raw []byte) []struct {
	ID       string `json:"id"`
	Function struct {
		Name string `json:"name"`
	} `json:"function"`
} {
	if len(raw) == 0 {
		return nil
	}
	var calls []struct {
		ID       string `json:"id"`
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
	}
	if common.Unmarshal(raw, &calls) != nil {
		return nil
	}
	return calls
}

func (p *presetProtocolPlan) applyRequest(request *dto.GeneralOpenAIRequest) error {
	if p == nil || !p.audit.Activated || request == nil {
		return nil
	}
	if len(p.visible) == 0 && !p.requireContainer {
		return nil
	}
	// Required/named means a client business call, never a hidden completion.
	// Auto may also complete via a declared reply container. Synthetic history
	// tools are context only and are not offered as callable actions.
	choice := request.ToolChoice
	businessOnly := len(p.visible) > 0 && (choice == "required" || presetToolChoiceName(choice) != "")
	request.Tools = slices.DeleteFunc(request.Tools, func(tool dto.ToolCallRequest) bool {
		if p.visible[tool.Function.Name] {
			return false
		}
		_, reply := p.containers[tool.Function.Name]
		return businessOnly || !reply
	})
	if p.requireContainer && !businessOnly {
		// A completed answer must use a confirmed transport. Requiring a call
		// over the union still lets auto clients choose a genuine business call.
		// This runs before the internal contract is frozen, never as an override.
		request.ToolChoice = "required"
		if len(p.visible) == 0 && len(p.containers) == 1 {
			for name := range p.containers {
				request.ToolChoice = map[string]any{"type": "function", "function": map[string]string{"name": name}}
			}
		}
		p.audit.Actions = append(p.audit.Actions, "required_reply_transport")
	}
	return nil
}

func (p *presetProtocolPlan) restoreTerminal(response *PresetScriptResponse, body []byte, stream bool, deadline time.Time) ([]byte, error) {
	if p == nil || !p.audit.Activated || p.phase != presetProtocolPhaseFinal || p.contract == presetProtocolContractTool {
		return body, nil
	}
	for _, choice := range mustResponseChoices(body, stream) {
		if response.generationRequest != nil && len(choice.tools) == 0 && !choice.refused && choice.finish != "length" && choice.finish != "content_filter" {
			selection := response.generationRequest.ToolChoice
			if selection == "required" || presetToolChoiceName(selection) != "" {
				return nil, fmt.Errorf("upstream omitted the required tool call")
			}
		}
		for _, call := range choice.tools {
			if response.generationRequest != nil {
				if _, offered := findToolByName(response.generationRequest.Tools, call.name.String()); !offered {
					return nil, fmt.Errorf("upstream called a tool outside the compiled generation contract")
				}
			}
			if _, declared := p.containers[call.name.String()]; !declared && !p.visible[call.name.String()] {
				return nil, fmt.Errorf("upstream returned an unknown or history-only tool instead of a reply source")
			}
		}
	}
	owned := p.internal
	if len(owned) == 0 {
		owned = map[string]bool{p.finalTool: true}
	}
	return restorePresetToolCalls(body, stream, owned, true, func(calls []*presetToolTextCall) (string, error) {
		if len(calls) != 1 {
			return "", fmt.Errorf("preset protocol final response requires exactly one terminal tool call")
		}
		var decoded any
		if err := common.UnmarshalPreservingNumbers([]byte(calls[0].arguments.String()), &decoded); err != nil || common.GetJsonType([]byte(calls[0].arguments.String())) != "object" {
			return "", fmt.Errorf("preset protocol terminal arguments must be a complete JSON object")
		}
		// An explicit response_argument is an operator contract stored with the
		// preset, so it always wins over heuristics and text formatters.
		container, declared := p.containers[calls[0].name.String()]
		if !declared {
			return "", fmt.Errorf("upstream returned a history-only tool instead of a reply")
		}
		if declared && container.ArgumentPointer != nil || p.responseArgument != "" {
			var value any
			var err error
			if declared {
				value, err = replyPointer(decoded, *container.ArgumentPointer)
			} else {
				value, err = presetProtocolArgument(decoded, p.responseArgument)
			}
			if err != nil {
				return "", err
			}
			if p.contract == presetProtocolContractJSON {
				value, err = presetProtocolJSONArgument(value)
				if err != nil {
					return "", err
				}
				return encodePresetProtocolProjection(value)
			}
			if text, ok := value.(string); ok {
				return text, nil
			}
			return "", fmt.Errorf("reply text field must be a string")
		}
		if presetProtocolHasResponseFormatter(response) {
			text, err := response.formatOwnedCalls(calls, stream, deadline)
			if err != nil {
				return "", err
			}
			if p.contract == presetProtocolContractJSON {
				var decoded any
				if err := common.UnmarshalPreservingNumbers([]byte(text), &decoded); err != nil {
					return "", fmt.Errorf("preset protocol toolCallFormatter must return valid JSON for a JSON contract")
				}
				if _, err := presetProtocolJSONArgument(decoded); err != nil {
					return "", err
				}
			}
			return text, nil
		}
		return "", fmt.Errorf("preset protocol response projection requires response_argument or toolCallFormatter")
	}, deadline)
}

func encodePresetProtocolProjection(value any) (string, error) {
	encoded, err := common.Marshal(value)
	if err != nil {
		return "", err
	}
	if len(encoded) > maxFilteredResponseBytes {
		return "", fmt.Errorf("preset protocol JSON projection exceeded response limit")
	}
	return string(encoded), nil
}

func presetProtocolArgument(value any, path string) (any, error) {
	current := value
	for segment := range strings.SplitSeq(path, ".") {
		object, ok := current.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("preset protocol response_argument %q does not resolve to an object", path)
		}
		next, exists := object[segment]
		if !exists {
			return nil, fmt.Errorf("preset protocol terminal arguments are missing response_argument %q", path)
		}
		current = next
	}
	return current, nil
}

func presetProtocolJSONArgument(value any) (any, error) {
	if text, ok := value.(string); ok {
		var decoded any
		if err := common.UnmarshalPreservingNumbers([]byte(text), &decoded); err != nil {
			return nil, fmt.Errorf("reply JSON field must contain one JSON object")
		}
		value = decoded
	}
	if _, ok := value.(map[string]any); !ok {
		return nil, fmt.Errorf("reply JSON must be an object, not an array, scalar or null")
	}
	return value, nil
}
