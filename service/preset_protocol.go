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
	contract         string
	responseTarget   string
	responseArgument string
	validationMode   int
	phase            string
	terminal         dto.ToolCallRequest
}

// buildPresetProtocolPlan returns a plan only when the gateway can safely
// separate the final reply protocol from auxiliary tools. An inactive plan is
// returned for ambiguous configurations so callers can still expose the
// diagnostic without changing the request.
func buildPresetProtocolPlan(original, compiled *dto.GeneralOpenAIRequest, config *dto.SillyTavernPresetConfig, response *PresetScriptResponse) (*presetProtocolPlan, int, error) {
	if original == nil || compiled == nil || config == nil || response == nil {
		return nil, 0, nil
	}
	protocol := config.ToolProtocol
	if protocol == nil || !protocol.Enabled {
		return inactivePresetProtocolPlan(false, false, "tool_protocol_disabled"), 0, nil
	}
	ownedOrder := orderedOwnedToolNames(compiled, response.ownedTools)
	if protocol != nil {
		if protocol.ClientFinal != "" {
			if _, ok := findToolByName(original.Tools, protocol.ClientFinal); !ok {
				return nil, 0, fmt.Errorf("sillytavern_preset tool_protocol client_final %q is not a client tool", protocol.ClientFinal)
			}
		}
		if protocol.PresetFinal != "" && !response.ownedTools[protocol.PresetFinal] {
			return nil, 0, fmt.Errorf("sillytavern_preset tool_protocol preset_final %q is not a preset tool", protocol.PresetFinal)
		}
		for _, name := range protocol.Auxiliary {
			if _, ok := findToolByName(compiled.Tools, name); !ok {
				return nil, 0, fmt.Errorf("sillytavern_preset tool_protocol auxiliary tool %q is not present in the compiled request", name)
			}
		}
	}

	clientFinal := ""
	presetFinal := ""
	if protocol != nil {
		clientFinal = protocol.ClientFinal
		presetFinal = protocol.PresetFinal
	}
	if clientFinal == "" {
		clientFinal = inferClientFinalTool(original, compiled, response.ownedTools)
	}
	if presetFinal == "" {
		if name := presetToolChoiceName(compiled.ToolChoice); name != "" && response.ownedTools[name] {
			presetFinal = name
		} else if clientFinal == "" && len(ownedOrder) == 1 && len(original.Tools) == 0 {
			presetFinal = ownedOrder[0]
		} else if protocol != nil && len(protocol.Auxiliary) > 0 {
			candidates := make([]string, 0, 1)
			for _, name := range ownedOrder {
				if !slices.Contains(protocol.Auxiliary, name) {
					candidates = append(candidates, name)
				}
			}
			if len(candidates) == 1 {
				presetFinal = candidates[0]
			}
		}
	}
	if protocol != nil && protocol.PresetFinal != "" && clientFinal != "" {
		return nil, 0, fmt.Errorf("sillytavern_preset tool_protocol preset_final cannot replace a client response tool")
	}
	if clientFinal == "" && len(ownedOrder) == 0 {
		return inactivePresetProtocolPlan(true, true, "no_client_or_preset_contract"), 0, nil
	}
	if clientFinal == "" && presetFinal == "" {
		// A request that carries no client tool contract has nothing to
		// coordinate: the preset's tools are the only tools in play, and a
		// reply format expressed in prompt text is not machine readable. That
		// is a boundary, not an ambiguity the operator could resolve here.
		if !clientDeclaresToolContract(original) {
			return inactivePresetProtocolPlan(true, false, "no_client_tool_contract"), 0, nil
		}
		return inactivePresetProtocolPlan(true, true, "ambiguous_preset_final_tool"), 0, nil
	}

	finalTool := clientFinal
	presetOwnedFinal := false
	if finalTool == "" {
		finalTool = presetFinal
		presetOwnedFinal = true
	}
	if presetOwnedFinal && !response.ownedTools[finalTool] {
		return nil, 0, fmt.Errorf("sillytavern_preset protocol final tool %q is not owned by the preset", finalTool)
	}
	terminal, ok := findToolByName(compiled.Tools, finalTool)
	if !ok {
		return nil, 0, fmt.Errorf("sillytavern_preset protocol final tool %q is not present in the compiled request", finalTool)
	}

	auxiliary := make([]string, 0, len(ownedOrder))
	if protocol != nil && len(protocol.Auxiliary) > 0 {
		auxiliary = append(auxiliary, protocol.Auxiliary...)
	} else if clientFinal != "" {
		for _, tool := range original.Tools {
			if name := tool.Function.Name; name != "" && name != clientFinal {
				auxiliary = append(auxiliary, name)
			}
		}
		for _, name := range ownedOrder {
			if name != clientFinal {
				auxiliary = append(auxiliary, name)
			}
		}
	} else {
		for _, name := range ownedOrder {
			if name != presetFinal {
				auxiliary = append(auxiliary, name)
			}
		}
	}
	auxiliary = orderedToolNames(compiled.Tools, auxiliary)
	if slices.Contains(auxiliary, finalTool) {
		return nil, 0, fmt.Errorf("sillytavern_preset protocol auxiliary tools cannot include the final tool")
	}
	if len(auxiliary) == 0 && clientFinal == "" && presetFinal == "" {
		return nil, 0, nil
	}

	contract, safe, err := resolvePresetProtocolContract(original, clientFinal, protocol)
	if err != nil {
		return nil, 0, err
	}
	if !safe {
		return inactivePresetProtocolPlan(true, true, "response_projection_unsafe"), 0, nil
	}
	// A preset terminal tool cannot be silently projected into a client tool
	// whose schema was never exposed to the model.
	if contract == presetProtocolContractTool && presetOwnedFinal {
		return inactivePresetProtocolPlan(true, true, "client_and_preset_final_conflict"), 0, nil
	}
	if response.tavern != nil && presetOwnedFinal {
		return inactivePresetProtocolPlan(true, true, "response_projection_unsafe"), 0, nil
	}

	active := len(auxiliary) > 0 || contract == presetProtocolContractJSON || presetOwnedFinal
	if !active {
		return inactivePresetProtocolPlan(true, true, "no_projection_needed"), 0, nil
	}
	plan := &presetProtocolPlan{
		clientFinal:      clientFinal,
		presetFinal:      presetFinal,
		finalTool:        finalTool,
		auxiliary:        auxiliary,
		contract:         contract,
		responseTarget:   protocolResponseTarget(protocol),
		responseArgument: protocolResponseArgument(protocol),
		validationMode:   1,
		terminal:         terminal,
	}
	plan.audit = PresetProtocolAudit{
		Enabled:          true,
		Eligible:         true,
		Activated:        true,
		Reason:           "projection_planned",
		Contract:         contract,
		ClientFinal:      clientFinal,
		PresetFinal:      presetFinal,
		FinalTool:        finalTool,
		Auxiliary:        append([]string(nil), auxiliary...),
		ResponseArgument: protocolResponseArgument(protocol),
	}
	plan.audit.Fingerprint = plan.fingerprint()
	return plan, plan.validationMode, nil
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

func protocolResponseTarget(protocol *dto.SillyTavernToolProtocol) string {
	if protocol == nil || protocol.ResponseTarget == "" {
		return "auto"
	}
	return protocol.ResponseTarget
}

func protocolResponseArgument(protocol *dto.SillyTavernToolProtocol) string {
	if protocol == nil {
		return ""
	}
	return protocol.ResponseArgument
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
	return names
}

func orderedToolNames(tools []dto.ToolCallRequest, names []string) []string {
	wanted := make(map[string]bool, len(names))
	for _, name := range names {
		wanted[name] = true
	}
	ordered := make([]string, 0, len(names))
	seen := make(map[string]bool, len(names))
	for _, tool := range tools {
		name := tool.Function.Name
		if wanted[name] && !seen[name] {
			ordered = append(ordered, name)
			seen[name] = true
		}
	}
	for _, name := range names {
		if !seen[name] {
			ordered = append(ordered, name)
		}
	}
	return ordered
}

// clientDeclaresToolContract reports whether the incoming request carries any
// client-owned tool contract. A client that only asks for a reply format in
// prompt text declares nothing here, so the gateway must not invent a role
// from prose it cannot validate.
func clientDeclaresToolContract(request *dto.GeneralOpenAIRequest) bool {
	if request == nil {
		return false
	}
	return len(request.Tools) > 0 || len(request.Functions) > 0 ||
		request.ToolChoice != nil || len(request.FunctionCall) > 0
}

func inferClientFinalTool(original, compiled *dto.GeneralOpenAIRequest, owned map[string]bool) string {
	if original == nil {
		return ""
	}
	if name := presetToolChoiceName(original.ToolChoice); name != "" {
		if _, ok := findToolByName(original.Tools, name); ok {
			return name
		}
	}
	choice, _ := original.ToolChoice.(string)
	if choice == "required" && len(original.Tools) == 1 {
		return original.Tools[0].Function.Name
	}
	if choice == "required" && len(original.Tools) > 1 {
		if auxiliary := lastPresetProtocolToolResult(original.Messages); auxiliary != "" {
			candidate := ""
			for _, tool := range original.Tools {
				name := tool.Function.Name
				if name == auxiliary {
					continue
				}
				if candidate != "" {
					return ""
				}
				candidate = name
			}
			return candidate
		}
	}
	if len(original.Tools) == 1 && compiledHasOwnedToolAddition(original, compiled, owned) {
		compiledChoice, _ := compiled.ToolChoice.(string)
		if compiledChoice == "required" {
			return original.Tools[0].Function.Name
		}
	}
	return ""
}

func compiledHasOwnedToolAddition(original, compiled *dto.GeneralOpenAIRequest, owned map[string]bool) bool {
	if original == nil || compiled == nil || len(owned) == 0 {
		return false
	}
	for _, tool := range compiled.Tools {
		name := tool.Function.Name
		if owned[name] {
			if _, exists := findToolByName(original.Tools, name); !exists {
				return true
			}
		}
	}
	return false
}

func lastPresetProtocolToolResult(messages []dto.Message) string {
	callNames := make(map[string]string)
	lastUser := -1
	for index, message := range messages {
		if message.Role == "user" {
			lastUser = index
		}
		for _, call := range parsePresetProtocolToolCalls(message.ToolCalls) {
			callNames[call.ID] = call.Function.Name
		}
	}
	for index := len(messages) - 1; index > lastUser; index-- {
		message := messages[index]
		if message.Role != "tool" && message.Role != "function" {
			continue
		}
		if name := callNames[message.ToolCallId]; name != "" {
			return name
		}
		if message.Name != nil {
			return *message.Name
		}
	}
	return ""
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
		Contract         string
		ClientFinal      string
		PresetFinal      string
		FinalTool        string
		Auxiliary        []string
	}{
		Version:          "preset-protocol-v4",
		Enabled:          p.audit.Enabled,
		ResponseTarget:   p.responseTarget,
		ResponseArgument: p.responseArgument,
		Contract:         p.contract,
		ClientFinal:      p.clientFinal,
		PresetFinal:      p.presetFinal,
		FinalTool:        p.finalTool,
		Auxiliary:        p.auxiliary,
	})
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func (p *presetProtocolPlan) phaseForMessages(messages []dto.Message) string {
	if p == nil || len(p.auxiliary) == 0 {
		return presetProtocolPhaseFinal
	}
	auxiliary := make(map[string]bool, len(p.auxiliary))
	for _, name := range p.auxiliary {
		auxiliary[name] = true
	}
	callNames := make(map[string]string)
	lastUser := -1
	for index, message := range messages {
		if message.Role == "user" {
			lastUser = index
		}
		for _, call := range parsePresetProtocolToolCalls(message.ToolCalls) {
			callNames[call.ID] = call.Function.Name
		}
	}
	for index := len(messages) - 1; index > lastUser; index-- {
		message := messages[index]
		if message.Role != "tool" && message.Role != "function" {
			continue
		}
		name := callNames[message.ToolCallId]
		if name == "" && message.Name != nil {
			name = *message.Name
		}
		if auxiliary[name] {
			return presetProtocolPhaseFinal
		}
	}
	return presetProtocolPhaseAuxiliary
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
	p.phase = p.phaseForMessages(request.Messages)
	p.audit.Phase = p.phase
	selected := []string{p.finalTool}
	if p.phase == presetProtocolPhaseAuxiliary {
		selected = append([]string(nil), p.auxiliary...)
	}
	if len(selected) == 0 {
		return fmt.Errorf("preset protocol has no tools for phase %q", p.phase)
	}
	tools := make([]dto.ToolCallRequest, 0, len(selected))
	for _, name := range selected {
		tool, ok := findToolByName(request.Tools, name)
		if !ok {
			return fmt.Errorf("preset protocol tool %q is not present in the compiled request", name)
		}
		tools = append(tools, tool)
	}
	request.Tools = tools
	if p.phase == presetProtocolPhaseAuxiliary && len(selected) > 1 {
		request.ToolChoice = "required"
		return nil
	}
	request.ToolChoice = map[string]any{
		"type": "function",
		"function": map[string]any{
			"name": selected[0],
		},
	}
	return nil
}

func (p *presetProtocolPlan) restoreTerminal(response *PresetScriptResponse, body []byte, stream bool, deadline time.Time) ([]byte, error) {
	if p == nil || !p.audit.Activated || p.phase != presetProtocolPhaseFinal || p.contract == presetProtocolContractTool {
		return body, nil
	}
	owned := map[string]bool{p.finalTool: true}
	return restorePresetToolCalls(body, stream, owned, true, func(calls []*presetToolTextCall) (string, error) {
		if len(calls) != 1 {
			return "", fmt.Errorf("preset protocol final response requires exactly one terminal tool call")
		}
		var decoded any
		if err := common.UnmarshalPreservingNumbers([]byte(calls[0].arguments.String()), &decoded); err != nil {
			return "", fmt.Errorf("preset protocol terminal arguments must be valid JSON")
		}
		// An explicit response_argument is an operator contract stored with the
		// preset, so it always wins over heuristics and text formatters.
		if p.responseArgument != "" {
			value, err := presetProtocolArgument(decoded, p.responseArgument)
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
			return presetTerminalText(value, p.terminal), nil
		}
		if p.contract == presetProtocolContractJSON {
			return encodePresetProtocolProjection(decoded)
		}
		// When no explicit contract is configured, detect a client-shaped JSON
		// payload embedded in any string argument of the preset's terminal tool.
		// Tool and argument names are never hardcoded here: the shape of the
		// payload decides, and ambiguity falls through to the text formatter.
		if p.contract == presetProtocolContractText && p.responseTarget == "auto" {
			if projected, ok := presetProtocolAutoJSONArgument(decoded); ok {
				return encodePresetProtocolProjection(projected)
			}
		}
		if p.contract == presetProtocolContractText && response != nil &&
			response.settings != nil && response.settings.OutputPreprocessing.ToolCallFormatter.Enabled {
			text, err := response.formatOwnedCalls(calls, stream, deadline)
			if err != nil {
				return "", err
			}
			return text, nil
		}
		return presetTerminalText(decoded, p.terminal), nil
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
	text, ok := value.(string)
	if !ok {
		return value, nil
	}
	var decoded any
	if err := common.UnmarshalPreservingNumbers([]byte(text), &decoded); err != nil {
		return nil, fmt.Errorf("preset protocol response_argument must contain valid JSON")
	}
	return decoded, nil
}

func presetProtocolAutoJSONArgument(value any) (any, bool) {
	object, ok := value.(map[string]any)
	if !ok || len(object) == 0 {
		return nil, false
	}
	var bare map[string]any
	bareFound := 0
	var wrapped map[string]any
	wrappedFound := 0
	body := ""
	bodyFound := 0
	for _, raw := range object {
		text, ok := raw.(string)
		if !ok {
			continue
		}
		if decoded, ok := presetProtocolClientJSONObject(text); ok {
			bare = decoded
			bareFound++
			continue
		}
		inner, wrappedOK := presetProtocolStripSingleTagWrapper(text)
		if !wrappedOK {
			continue
		}
		if decoded, ok := presetProtocolClientJSONObject(inner); ok {
			wrapped = decoded
			wrappedFound++
			continue
		}
		// A tag such as <game>...</game> can hold the reply body on its own
		// while the client envelope (emotion, options, ...) sits in another
		// argument. Keep the inner text so it can be merged below.
		if trimmed := strings.TrimSpace(inner); trimmed != "" {
			body = trimmed
			bodyFound++
		}
	}
	var candidate map[string]any
	switch {
	case bareFound == 1:
		candidate = bare
	case bareFound == 0 && wrappedFound == 1:
		candidate = wrapped
	default:
		return nil, false
	}
	textKey, hasText := presetProtocolResponseTextField(candidate)
	if hasText {
		return candidate, true
	}
	// The model split the reply: the envelope carries everything except the
	// body, which arrived wrapped in a tag. Rebuild one client JSON object
	// without hardcoding any preset, tool, or argument name.
	if bodyFound != 1 {
		return nil, false
	}
	merged := make(map[string]any, len(candidate)+1)
	for key, item := range candidate {
		merged[key] = item
	}
	merged[textKey] = body
	return merged, true
}

func presetProtocolClientJSONObject(text string) (map[string]any, bool) {
	trimmed := strings.TrimSpace(text)
	if !strings.HasPrefix(trimmed, "{") {
		return nil, false
	}
	var decoded map[string]any
	if common.UnmarshalPreservingNumbers([]byte(trimmed), &decoded) != nil {
		return nil, false
	}
	if !presetProtocolLooksLikeClientResponse(decoded) {
		return nil, false
	}
	return decoded, true
}

// presetProtocolStripSingleTagWrapper removes one surrounding XML-like tag
// whose name is not hardcoded, so any "<tag>payload</tag>" shape is handled the
// same way. Tags with attributes or nested markup are left untouched.
func presetProtocolStripSingleTagWrapper(text string) (string, bool) {
	trimmed := strings.TrimSpace(text)
	if !strings.HasPrefix(trimmed, "<") {
		return "", false
	}
	openEnd := strings.Index(trimmed, ">")
	if openEnd <= 1 {
		return "", false
	}
	name := trimmed[1:openEnd]
	if name == "" || strings.ContainsAny(name, "<> \t\r\n/") {
		return "", false
	}
	closeTag := "</" + name + ">"
	if !strings.HasSuffix(trimmed, closeTag) {
		return "", false
	}
	return strings.TrimSpace(trimmed[openEnd+1 : len(trimmed)-len(closeTag)]), true
}

// presetProtocolAutoJSONProjection inspects the preset-owned tool calls in a
// response that never reached an activated protocol plan and projects a
// client-shaped JSON payload embedded in one argument. It is deliberately
// conservative: exactly one owned call and exactly one JSON object carrying a
// response-like field, otherwise the caller keeps the existing pipeline.
func presetProtocolAutoJSONProjection(calls []*presetToolTextCall) (string, bool, error) {
	if len(calls) != 1 {
		return "", false, nil
	}
	var decoded any
	if err := common.UnmarshalPreservingNumbers([]byte(calls[0].arguments.String()), &decoded); err != nil {
		return "", false, nil
	}
	projected, ok := presetProtocolAutoJSONArgument(decoded)
	if !ok {
		return "", false, nil
	}
	text, err := encodePresetProtocolProjection(projected)
	if err != nil {
		return "", false, err
	}
	return text, true, nil
}

var presetProtocolTextFields = []string{"content", "message", "text", "output", "response", "reply"}

// presetProtocolEnvelopeFields identify a client reply object that carries
// structured metadata even when its text field is absent or empty, so the
// adapter can merge a separately wrapped body back into it.
var presetProtocolEnvelopeFields = []string{"emotion", "control", "observe", "intent", "opts", "options", "choices", "state", "mood"}

// presetProtocolResponseTextField returns the effective text field name and
// whether it already holds a non-empty string.
func presetProtocolResponseTextField(value map[string]any) (string, bool) {
	field := ""
	for _, key := range presetProtocolTextFields {
		raw, ok := value[key]
		if !ok {
			continue
		}
		if field == "" {
			field = key
		}
		if text, ok := raw.(string); ok && strings.TrimSpace(text) != "" {
			return key, true
		}
	}
	if field == "" {
		field = "content"
	}
	return field, false
}

func presetProtocolLooksLikeClientResponse(value map[string]any) bool {
	for _, key := range presetProtocolTextFields {
		if _, ok := value[key]; ok {
			return true
		}
	}
	for _, key := range presetProtocolEnvelopeFields {
		if _, ok := value[key]; ok {
			return true
		}
	}
	return false
}

func presetTerminalText(value any, terminal dto.ToolCallRequest) string {
	if text, ok := value.(string); ok {
		return text
	}
	object, ok := value.(map[string]any)
	if !ok {
		encoded, _ := common.Marshal(value)
		return string(encoded)
	}
	schema := struct {
		Required         []string `json:"required"`
		PropertyOrdering []string `json:"propertyOrdering"`
	}{}
	_ = common.Unmarshal(terminal.Function.Parameters, &schema)
	for index := len(schema.Required) - 1; index >= 0; index-- {
		name := schema.Required[index]
		if text, ok := object[name].(string); ok {
			return text
		}
	}
	for index := len(schema.PropertyOrdering) - 1; index >= 0; index-- {
		name := schema.PropertyOrdering[index]
		if text, ok := object[name].(string); ok {
			return text
		}
	}
	names := make([]string, 0, len(object))
	for name := range object {
		if _, ok := object[name].(string); ok {
			names = append(names, name)
		}
	}
	if len(names) == 1 {
		return object[names[0]].(string)
	}
	encoded, _ := common.Marshal(object)
	return string(encoded)
}
