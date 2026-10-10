package service

import (
	"cmp"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/big"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
)

// SillyTavernContext is optional request-local data. It is supplied as the
// top-level _sillytavern_context field and is never forwarded to a provider.
// A plain OpenAI client can omit it: incoming system messages remain generic
// chat history and are never guessed to be a particular SillyTavern marker.
type SillyTavernContext struct {
	User             string            `json:"user,omitempty"`
	Char             string            `json:"char,omitempty"`
	LastUserMessage  string            `json:"last_user_message,omitempty"`
	Markers          map[string]string `json:"markers,omitempty"`
	ChatHistory      []dto.Message     `json:"chat_history,omitempty"`
	DialogueExamples [][]dto.Message   `json:"dialogue_examples,omitempty"`
	Variables        map[string]string `json:"variables,omitempty"`
	GlobalVariables  map[string]string `json:"global_variables,omitempty"`
	MacroValues      map[string]string `json:"macro_values,omitempty"`
	ChatID           string            `json:"chat_id,omitempty"`
	Input            *string           `json:"input,omitempty"`
	Now              time.Time         `json:"-"`
}

type PresetMessageSource struct {
	Index      int    `json:"index"`
	Role       string `json:"role"`
	Source     string `json:"source"`
	Identifier string `json:"identifier,omitempty"`
}

type PresetCompileTrace struct {
	Name     string                `json:"name"`
	Mode     string                `json:"mode"`
	Messages []PresetMessageSource `json:"messages"`
	Warnings []string              `json:"warnings,omitempty"`
}

type presetMessage struct {
	message dto.Message
	source  string
	id      string
}

type presetMacroContext struct {
	user            string
	char            string
	model           string
	chatID          string
	input           string
	lastMessage     string
	lastUserMessage string
	lastCharMessage string
	chatCount       int
	rollsRemaining  int64
	markers         map[string]string
	vars            map[string]string
	globalVars      map[string]string
	values          map[string]string
	warnings        map[string]bool
	manual          map[string]string
	now             time.Time
}

var sillyTavernTrimMacro = regexp.MustCompile(`(?i)(?:\r?\n)*\{\{trim\}\}(?:\r?\n)*`)
var sillyTavernDice = regexp.MustCompile(`(?i)^(?:(\d+)?d(\d+)([+-]\d+)?|(\d+))$`)
var sillyTavernUTCTime = regexp.MustCompile(`(?i)^time_utc([+-]\d{1,2})$`)
var sillyTavernLegacyMacro = regexp.MustCompile(`(?i)<(USER|BOT|CHAR|CHARIFNOTGROUP|GROUP)>`)

const maxSillyTavernExpansionBytes = 8 << 20

// CompileSillyTavernPreset transforms a typed chat request before provider
// conversion. Original message objects (including multimodal and tool fields)
// are retained as whole objects; only preset-created messages contain strings.
func CompileSillyTavernPreset(config *dto.SillyTavernPresetConfig, request *dto.GeneralOpenAIRequest, context SillyTavernContext) (*dto.GeneralOpenAIRequest, PresetCompileTrace, error) {
	var trace PresetCompileTrace
	if config == nil {
		return request, trace, nil
	}
	if len(config.Models) > 0 && !slices.Contains(config.Models, request.Model) {
		return request, trace, nil
	}
	preset, err := config.ParseAndValidate()
	if err != nil {
		return nil, trace, err
	}
	compiled, err := common.DeepCopy(request)
	if err != nil {
		return nil, trace, err
	}
	trace.Name = preset.Name
	trace.Mode = "compatibility"
	for _, script := range preset.Extensions.TavernHelper.Scripts {
		if script.Type == "script" && script.Enabled {
			trace.Warnings = append(trace.Warnings, "Browser scripts are preserved but not executed; use native declarative features for server-side compatibility")
			break
		}
	}
	_, regexWarnings := compilePresetResponseRegex(config, request.Model)
	trace.Warnings = append(trace.Warnings, regexWarnings...)
	if config.ContextMode == "exact" {
		trace.Mode = "exact"
		if config.ReferenceSource == "" || config.ReferenceSource == "newapi" {
			return nil, trace, fmt.Errorf("exact SillyTavern mode requires an explicit reference_source")
		}
		if context.User == "" || context.Char == "" || context.Markers == nil || context.ChatHistory == nil || context.DialogueExamples == nil {
			return nil, trace, fmt.Errorf("exact SillyTavern mode requires user, char, markers, chat_history and dialogue_examples in _sillytavern_context")
		}
	}
	if trace.Name == "" {
		trace.Name = "SillyTavern preset"
	}
	if topA := strings.TrimSpace(string(preset.TopA)); topA != "" && topA != "0" && topA != "0.0" && topA != "null" {
		trace.Warnings = append(trace.Warnings, "top_a is not mapped by the generic preset compiler")
	}
	if context.Markers == nil {
		context.Markers = make(map[string]string)
	}
	if context.User == "" {
		context.User = config.User
	}
	if context.Char == "" {
		context.Char = config.Char
	}
	chat := context.ChatHistory
	if chat == nil {
		chat = compiled.Messages
	}
	lastUserMessage := context.LastUserMessage
	if lastUserMessage == "" {
		for index := len(chat) - 1; index >= 0; index-- {
			if chat[index].Role == "user" {
				if content, ok := chat[index].Content.(string); ok {
					lastUserMessage = content
				}
				break
			}
		}
	}
	now := context.Now
	if now.IsZero() {
		now = time.Now()
	}
	if config.TimeZone != "" {
		location, _ := time.LoadLocation(config.TimeZone) // validated by ParseAndValidate
		now = now.In(location)
	}
	lastMessage, lastCharMessage := "", ""
	foundLastMessage, foundLastCharMessage := false, false
	lastMessageID := ""
	for index := len(chat) - 1; index >= 0; index-- {
		content, ok := chat[index].Content.(string)
		if !ok {
			continue
		}
		if !foundLastMessage {
			lastMessage = content
			lastMessageID = strconv.Itoa(index)
			foundLastMessage = true
		}
		if !foundLastCharMessage && chat[index].Role == "assistant" {
			lastCharMessage = content
			foundLastCharMessage = true
		}
		if foundLastMessage && foundLastCharMessage {
			break
		}
	}
	macro := presetMacroContext{
		user: context.User, char: context.Char, model: request.Model, chatID: context.ChatID,
		input: lastUserMessage, lastMessage: lastMessage, lastUserMessage: lastUserMessage,
		lastCharMessage: lastCharMessage, chatCount: len(chat), markers: context.Markers,
		rollsRemaining: 10_000,
		vars:           make(map[string]string), globalVars: context.GlobalVariables,
		values: make(map[string]string), warnings: make(map[string]bool), manual: config.MacroValues, now: now,
	}
	if context.Input != nil {
		macro.input = *context.Input
	}
	macro.values["lastmessageid"] = lastMessageID
	// These are request-local limits, not inferred provider capacities.
	var limits struct {
		Context *int64 `json:"openai_max_context"`
	}
	if err := common.Unmarshal(config.Preset, &limits); err != nil {
		return nil, trace, err
	}
	responseLimit := request.MaxTokens
	if request.MaxCompletionTokens != nil {
		responseLimit = request.MaxCompletionTokens
	}
	if preset.MaxTokens != nil && (config.ParameterPolicy != "client" || responseLimit == nil) {
		responseLimit = preset.MaxTokens
	}
	if responseLimit != nil {
		macro.values["maxresponse"] = strconv.FormatUint(uint64(*responseLimit), 10)
		macro.values["maxresponsetokens"] = macro.values["maxresponse"]
	}
	if limits.Context != nil && *limits.Context > 0 {
		macro.values["maxcontext"] = strconv.FormatInt(*limits.Context, 10)
		macro.values["maxcontexttokens"] = macro.values["maxcontext"]
		if responseLimit != nil && uint64(*responseLimit) <= uint64(*limits.Context) {
			macro.values["maxprompt"] = strconv.FormatInt(*limits.Context-int64(*responseLimit), 10)
			macro.values["maxprompttokens"] = macro.values["maxprompt"]
		}
	}
	for key, value := range context.MacroValues {
		macro.values[strings.ToLower(key)] = value
	}
	for key, value := range context.Variables {
		macro.vars[key] = value
	}
	prompts := make(map[string]dto.SillyTavernPrompt, len(preset.Prompts))
	for _, prompt := range preset.Prompts {
		prompts[prompt.Identifier] = prompt
	}
	order := preset.PromptOrder[0].Order
	for _, candidate := range preset.PromptOrder {
		if candidate.CharacterID == 100001 {
			order = candidate.Order
			break
		}
	}
	chatMarkerEnabled := false
	ordered := make(map[string]bool, len(order))
	for _, entry := range order {
		ordered[entry.Identifier] = true
	}
	for _, prompt := range preset.Prompts {
		if !ordered[prompt.Identifier] && config.EntryOverrides[prompt.Identifier] {
			order = append(order, dto.SillyTavernPromptOrderEntry{Identifier: prompt.Identifier, Enabled: true})
		}
	}
	chatMarkerPresent := false
	for _, entry := range order {
		if entry.Identifier == "chatHistory" {
			chatMarkerPresent = true
		}
		prompt, exists := prompts[entry.Identifier]
		enabled := entry.Enabled
		if override, ok := config.EntryOverrides[entry.Identifier]; ok {
			enabled = override
		}
		if enabled && !exists {
			warning := fmt.Sprintf("enabled prompt_order identifier %q has no matching prompt", entry.Identifier)
			if trace.Mode == "exact" {
				return nil, trace, fmt.Errorf("%s", warning)
			}
			trace.Warnings = append(trace.Warnings, warning)
		}
		if !exists || !enabled {
			continue
		}
		if trace.Mode == "exact" && prompt.Marker && entry.Identifier != "chatHistory" && entry.Identifier != "dialogueExamples" {
			if _, exists := context.Markers[entry.Identifier]; !exists {
				return nil, trace, fmt.Errorf("exact SillyTavern mode is missing marker %q", entry.Identifier)
			}
		}
		if entry.Identifier == "chatHistory" {
			// Normal API requests cannot honor continue-only history. Reject
			// this configuration instead of silently sending only the preset.
			if len(prompt.InjectionTrigger) > 0 && !slices.Contains(prompt.InjectionTrigger, "normal") {
				return nil, trace, fmt.Errorf("chatHistory injection_trigger must include normal for API requests")
			}
			chatMarkerEnabled = true
		}
	}
	if trace.Mode == "compatibility" && context.ChatHistory == nil {
		for _, message := range chat {
			if message.Role == "system" {
				trace.Warnings = append(trace.Warnings, "incoming system messages remain generic chat history; they are not inferred as SillyTavern markers")
				break
			}
		}
	}
	var output []presetMessage
	var inChat []dto.SillyTavernPrompt
	const maxCompiledPresetTextBytes = 16 << 20
	compiledPresetTextBytes := 0
	for _, entry := range order {
		prompt, exists := prompts[entry.Identifier]
		enabled := entry.Enabled
		if override, ok := config.EntryOverrides[entry.Identifier]; ok {
			enabled = override
		}
		if !exists || !enabled {
			continue
		}
		if len(prompt.InjectionTrigger) > 0 && !slices.Contains(prompt.InjectionTrigger, "normal") {
			trace.Warnings = append(trace.Warnings, fmt.Sprintf("preset prompt %q is not applicable to normal generation", entry.Identifier))
			continue
		}
		if prompt.Identifier == "chatHistory" {
			output = append(output, presetMessage{source: "marker", id: "chatHistory"})
			continue
		}
		if prompt.Identifier == "dialogueExamples" {
			output = append(output, presetMessage{source: "marker", id: "dialogueExamples"})
			continue
		}
		content := prompt.Content
		if prompt.Marker {
			content = context.Markers[prompt.Identifier]
			switch prompt.Identifier {
			case "scenario":
				if content != "" && preset.ScenarioFormat != "" {
					content = preset.ScenarioFormat
				}
			case "charPersonality":
				if content != "" && preset.PersonalityFormat != "" {
					content = preset.PersonalityFormat
				}
			case "worldInfoBefore", "worldInfoAfter":
				if content != "" && strings.TrimSpace(preset.WorldInfoFormat) != "" {
					content = strings.ReplaceAll(preset.WorldInfoFormat, "{0}", content)
				}
			}
		}
		for _, patch := range config.Patches {
			if patch.Identifier == "" || patch.Identifier == prompt.Identifier {
				growth := len(patch.Replace) - len(patch.Find)
				if len(content) > maxSillyTavernExpansionBytes || (growth > 0 && strings.Count(content, patch.Find) > (maxSillyTavernExpansionBytes-len(content))/growth) {
					return nil, trace, fmt.Errorf("sillytavern_preset patch expansion exceeded its limit")
				}
				content = strings.ReplaceAll(content, patch.Find, patch.Replace)
			}
		}
		content = macro.expand(content, 0)
		if macro.warnings["macro expansion limit reached"] {
			return nil, trace, fmt.Errorf("sillytavern_preset macro expansion exceeded its limit")
		}
		compiledPresetTextBytes += len(content)
		if compiledPresetTextBytes > maxCompiledPresetTextBytes {
			return nil, trace, fmt.Errorf("sillytavern_preset compiled text exceeds 16 MiB")
		}
		if strings.TrimSpace(content) == "" {
			continue
		}
		prompt.Content = content
		if prompt.InjectionPosition == 1 {
			inChat = append(inChat, prompt)
			continue
		}
		role := prompt.Role
		if role == "" {
			role = "system"
		}
		output = append(output, presetMessage{message: dto.Message{Role: role, Content: content}, source: "preset", id: prompt.Identifier})
	}
	processedChat := make([]dto.Message, len(chat))
	copy(processedChat, chat)
	for index := len(processedChat) - 1; index >= 0; index-- {
		if content, ok := processedChat[index].Content.(string); ok {
			content = macro.expand(content, 0)
			processedChat[index].Content = content
			if macro.warnings["macro expansion limit reached"] || len(processedChat[index].Content.(string)) > maxCompiledPresetTextBytes {
				return nil, trace, fmt.Errorf("sillytavern_preset chat macro expansion exceeded its limit")
			}
		}
	}
	chat = processedChat
	// SillyTavern groups equal depth/order/role injections and reverses their
	// priority while rebuilding chronological chat history.
	byDepth := make(map[int][]dto.SillyTavernPrompt)
	for _, prompt := range inChat {
		depth := min(prompt.InjectionDepth, len(chat))
		byDepth[depth] = append(byDepth[depth], prompt)
	}
	var history []presetMessage
	for boundary := 0; boundary <= len(chat); boundary++ {
		depth := len(chat) - boundary
		group := byDepth[depth]
		slices.SortStableFunc(group, func(a, b dto.SillyTavernPrompt) int {
			if a.InjectionOrder != b.InjectionOrder {
				return cmp.Compare(a.InjectionOrder, b.InjectionOrder)
			}
			roleOrder := func(role string) int {
				switch role {
				case "assistant":
					return 0
				case "user":
					return 1
				default:
					return 2
				}
			}
			return roleOrder(a.Role) - roleOrder(b.Role)
		})
		lastOrder := 0
		lastRole := ""
		for index, prompt := range group {
			role := prompt.Role
			if role == "" {
				role = "system"
			}
			if index > 0 && prompt.InjectionOrder == lastOrder && role == lastRole {
				previous := &history[len(history)-1]
				previous.message.Content = previous.message.Content.(string) + "\n" + prompt.Content
				previous.id += "," + prompt.Identifier
				continue
			}
			history = append(history, presetMessage{message: dto.Message{Role: role, Content: prompt.Content}, source: "in_chat", id: prompt.Identifier})
			lastOrder = prompt.InjectionOrder
			lastRole = role
		}
		if boundary < len(chat) {
			history = append(history, presetMessage{message: chat[boundary], source: "chat_history"})
		}
	}
	if newChat := macro.expand(preset.NewChatPrompt, 0); strings.TrimSpace(newChat) != "" {
		history = append([]presetMessage{{message: dto.Message{Role: "system", Content: newChat}, source: "preset", id: "newMainChat"}}, history...)
	}
	var final []presetMessage
	for _, item := range output {
		if item.source != "marker" {
			final = append(final, item)
			continue
		}
		if item.id == "chatHistory" {
			final = append(final, history...)
		} else {
			for _, example := range context.DialogueExamples {
				if delimiter := macro.expand(preset.NewExampleChatPrompt, 0); strings.TrimSpace(delimiter) != "" {
					final = append(final, presetMessage{message: dto.Message{Role: "system", Content: delimiter}, source: "preset", id: "newExampleChat"})
				}
				for _, message := range example {
					if _, ok := message.Content.(string); ok {
						message.Role = "system"
					}
					final = append(final, presetMessage{message: message, source: "dialogue_example", id: "dialogueExamples"})
				}
			}
		}
	}
	if !chatMarkerEnabled && !chatMarkerPresent {
		final = append(final, history...)
		trace.Warnings = append(trace.Warnings, "chatHistory marker absent; client history appended for compatibility")
	}
	if preset.SquashSystemMessages {
		var squashed []presetMessage
		for _, item := range final {
			content, text := item.message.Content.(string)
			if len(squashed) > 0 && text && item.message.Role == "system" && item.message.Name == nil &&
				item.source != "dialogue_example" && item.id != "newMainChat" && item.id != "newExampleChat" &&
				squashed[len(squashed)-1].source != "dialogue_example" && squashed[len(squashed)-1].id != "newMainChat" && squashed[len(squashed)-1].id != "newExampleChat" &&
				squashed[len(squashed)-1].message.Role == "system" && squashed[len(squashed)-1].message.Name == nil {
				if previous, ok := squashed[len(squashed)-1].message.Content.(string); ok {
					squashed[len(squashed)-1].message.Content = previous + "\n" + content
					squashed[len(squashed)-1].id += "," + item.id
					continue
				}
			}
			squashed = append(squashed, item)
		}
		final = squashed
	}
	switch normalizeSillyTavernPostProcessing(config.PostProcessing) {
	case "merge":
		err = validateSillyTavernTextMessages(final, "merge")
		if err == nil {
			final = mergeAdjacentSillyTavernRoles(final)
		}
	case "semi_strict":
		final, err = semiStrictSillyTavernPostProcess(final)
	case "strict":
		final, err = strictSillyTavernPostProcess(final)
	case "single_user":
		final, err = singleUserSillyTavernPostProcess(final, context.User, context.Char)
	}
	if err != nil {
		return nil, trace, err
	}
	compiled.Messages = make([]dto.Message, 0, len(final))
	createdTextBytes := 0
	for index, item := range final {
		if item.source == "preset" || item.source == "in_chat" {
			if content, ok := item.message.Content.(string); ok {
				createdTextBytes += len(content)
				if createdTextBytes > maxCompiledPresetTextBytes {
					return nil, trace, fmt.Errorf("sillytavern_preset compiled text exceeds 16 MiB")
				}
			}
		}
		compiled.Messages = append(compiled.Messages, item.message)
		trace.Messages = append(trace.Messages, PresetMessageSource{Index: index, Role: item.message.Role, Source: item.source, Identifier: item.id})
	}
	if len(compiled.Messages) == 0 {
		return nil, trace, fmt.Errorf("sillytavern_preset compiled an empty message list")
	}
	applySillyTavernParameters(config, preset, compiled)
	if config.ToolText != nil && config.ToolText.Enabled {
		if compiled.ToolChoice != nil {
			return nil, trace, fmt.Errorf("preset tool_text cannot override an explicit client tool_choice")
		}
		for _, tool := range compiled.Tools {
			if tool.Function.Name == config.ToolText.Name {
				return nil, trace, fmt.Errorf("preset tool_text tool name conflicts with a client tool")
			}
		}
		compiled.Tools = append(compiled.Tools, dto.ToolCallRequest{
			Type: "function",
			Function: dto.FunctionRequest{
				Name:        config.ToolText.Name,
				Description: "Return the assistant's user-visible response text. This transport does not execute an external action.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{config.ToolText.Argument: map[string]any{
						"type": "string", "description": "The complete user-visible response text.",
					}},
					"required": []string{config.ToolText.Argument},
				},
			},
		})
	}
	for warning := range macro.warnings {
		trace.Warnings = append(trace.Warnings, warning)
	}
	slices.Sort(trace.Warnings)
	if trace.Mode == "exact" && len(macro.warnings) > 0 {
		return nil, trace, fmt.Errorf("exact SillyTavern mode cannot resolve every preset macro: %s", strings.Join(trace.Warnings, "; "))
	}
	return compiled, trace, nil
}

func normalizeSillyTavernPostProcessing(mode string) string {
	switch mode {
	case "claude":
		return "merge"
	case "semi":
		return "semi_strict"
	case "single":
		return "single_user"
	default:
		return mode
	}
}

func validateSillyTavernTextMessages(items []presetMessage, mode string) error {
	for index, item := range items {
		if _, ok := item.message.Content.(string); !ok || item.message.Name != nil ||
			len(item.message.ToolCalls) > 0 || item.message.ToolCallId != "" || len(item.message.Tools) > 0 ||
			(item.message.Role != "system" && item.message.Role != "user" && item.message.Role != "assistant") {
			return fmt.Errorf("SillyTavern %s post-processing cannot safely reproduce structured content, named messages, or tools at message[%d]", mode, index)
		}
	}
	return nil
}

// mergeAdjacentSillyTavernRoles merges adjacent text messages with the same
// role. Callers validate structured messages before using this helper when the
// selected mode requires a text-only representation.
func mergeAdjacentSillyTavernRoles(items []presetMessage) []presetMessage {
	merged := make([]presetMessage, 0, len(items))
	for _, item := range items {
		content, ok := item.message.Content.(string)
		if len(merged) > 0 && ok && content != "" && merged[len(merged)-1].message.Role == item.message.Role {
			previous := &merged[len(merged)-1]
			if previousContent, previousOK := previous.message.Content.(string); previousOK {
				previous.message.Content = previousContent + "\n\n" + content
				previous.id += "," + item.id
				previous.source = "post_processing"
				continue
			}
		}
		merged = append(merged, item)
	}
	return merged
}

// strictSillyTavernPostProcess mirrors SillyTavern's strict, no-tools
// postProcessPrompt: merge identical adjacent roles with two newlines, turn
// non-leading system messages into user messages, insert a user placeholder
// when needed, and merge again. Structured media and tools are rejected until
// their exact SillyTavern flatten/restore behavior can be reproduced.
func strictSillyTavernPostProcess(items []presetMessage) ([]presetMessage, error) {
	if err := validateSillyTavernTextMessages(items, "strict"); err != nil {
		return nil, err
	}
	merged := mergeAdjacentSillyTavernRoles(items)
	if len(merged) == 0 {
		merged = []presetMessage{{message: dto.Message{Role: "user", Content: "[Start a new chat]"}, source: "post_processing"}}
	}
	for index := 1; index < len(merged); index++ {
		if merged[index].message.Role == "system" {
			merged[index].message.Role = "user"
		}
	}
	if merged[0].message.Role == "system" {
		if len(merged) == 1 || merged[1].message.Role != "user" {
			merged = slices.Insert(merged, 1, presetMessage{message: dto.Message{Role: "user", Content: "[Start a new chat]"}, source: "post_processing"})
		}
	} else if merged[0].message.Role != "user" {
		merged = slices.Insert(merged, 0, presetMessage{message: dto.Message{Role: "user", Content: "[Start a new chat]"}, source: "post_processing"})
	}
	return mergeAdjacentSillyTavernRoles(merged), nil
}

// semiStrictSillyTavernPostProcess keeps an optional leading system message,
// merges adjacent roles, and normalizes later system messages to user messages.
// It intentionally does not insert a placeholder or force the first role.
func semiStrictSillyTavernPostProcess(items []presetMessage) ([]presetMessage, error) {
	if err := validateSillyTavernTextMessages(items, "semi_strict"); err != nil {
		return nil, err
	}
	merged := mergeAdjacentSillyTavernRoles(items)
	for index := 1; index < len(merged); index++ {
		if merged[index].message.Role == "system" {
			merged[index].message.Role = "user"
		}
	}
	return mergeAdjacentSillyTavernRoles(merged), nil
}

// singleUserSillyTavernPostProcess flattens all textual messages into one
// user message. Structured content and tools fail closed instead of being
// silently stringified and losing semantics.
func singleUserSillyTavernPostProcess(items []presetMessage, user, char string) ([]presetMessage, error) {
	if err := validateSillyTavernTextMessages(items, "single_user"); err != nil {
		return nil, err
	}
	parts := make([]string, 0, len(items))
	for _, item := range items {
		if content := item.message.Content.(string); content != "" {
			name := ""
			if item.message.Role == "user" {
				name = user
			} else if item.message.Role == "assistant" {
				name = char
			}
			if name != "" && !strings.HasPrefix(content, name+": ") {
				content = name + ": " + content
			}
			parts = append(parts, content)
		}
	}
	if len(parts) == 0 {
		parts = append(parts, "[Start a new chat]")
	}
	return []presetMessage{{
		message: dto.Message{Role: "user", Content: strings.Join(parts, "\n\n")},
		source:  "post_processing",
		id:      "single_user",
	}}, nil
}

func applySillyTavernParameters(config *dto.SillyTavernPresetConfig, preset *dto.SillyTavernPreset, request *dto.GeneralOpenAIRequest) {
	preferClient := config.ParameterPolicy == "client"
	// SillyTavern's Custom and OpenAI sources never add these sampler fields
	// to their generated request. They are source-specific fields, not generic
	// Chat Completion preset parameters (openai.js createGenerationParameters).
	allowExtendedSamplers := config.ReferenceSource != "custom" && config.ReferenceSource != "openai"
	if preset.Temperature != nil && (!preferClient || request.Temperature == nil) {
		request.Temperature = preset.Temperature
	}
	if preset.TopP != nil && (!preferClient || request.TopP == nil) {
		request.TopP = preset.TopP
	}
	if allowExtendedSamplers && preset.TopK != nil && *preset.TopK > 0 && (!preferClient || request.TopK == nil) {
		request.TopK = preset.TopK
	}
	if raw := strings.TrimSpace(string(preset.MinP)); allowExtendedSamplers && raw != "" && raw != "0" && raw != "0.0" && raw != "null" && (!preferClient || len(request.MinP) == 0) {
		request.MinP = preset.MinP
	}
	if raw := strings.TrimSpace(string(preset.RepetitionPenalty)); allowExtendedSamplers && raw != "" && raw != "1" && raw != "1.0" && raw != "null" && (!preferClient || len(request.RepetitionPenalty) == 0) {
		request.RepetitionPenalty = preset.RepetitionPenalty
	}
	if preset.FrequencyPenalty != nil && (!preferClient || request.FrequencyPenalty == nil) {
		request.FrequencyPenalty = preset.FrequencyPenalty
	}
	if preset.PresencePenalty != nil && (!preferClient || request.PresencePenalty == nil) {
		request.PresencePenalty = preset.PresencePenalty
	}
	if preset.Seed != nil && *preset.Seed >= 0 && (!preferClient || request.Seed == nil) {
		request.Seed = preset.Seed
	}
	if preset.MaxTokens != nil && (!preferClient || (request.MaxTokens == nil && request.MaxCompletionTokens == nil)) {
		request.MaxTokens = preset.MaxTokens
		if !preferClient {
			request.MaxCompletionTokens = nil
		}
	}
	// Preserve SillyTavern's omission of the default n=1 when no client n
	// exists, but do not let a client multi-candidate value survive preset
	// priority when the preset explicitly selects a single candidate.
	if preset.N != nil && (*preset.N > 1 || request.N != nil) && (!preferClient || request.N == nil) {
		request.N = preset.N
	}
	allowReasoning := config.ReferenceSource != "custom" || strings.HasPrefix(request.Model, "gpt-5") || strings.HasPrefix(request.Model, "o1") || strings.HasPrefix(request.Model, "o3") || strings.HasPrefix(request.Model, "o4") || strings.HasPrefix(request.Model, "koboldcpp/")
	if allowReasoning && preset.ReasoningEffort != "" && preset.ReasoningEffort != "auto" && (!preferClient || request.ReasoningEffort == "") {
		request.ReasoningEffort = preset.ReasoningEffort
	}
	allowVerbosity := config.ReferenceSource != "custom" || strings.HasPrefix(request.Model, "gpt-5")
	if allowVerbosity && len(preset.Verbosity) > 0 && strings.TrimSpace(string(preset.Verbosity)) != `"auto"` && (!preferClient || len(request.Verbosity) == 0) {
		request.Verbosity = preset.Verbosity
	}
}

func (m *presetMacroContext) expand(input string, depth int) string {
	if depth > 16 || len(input) > 8<<20 {
		m.warnings["macro expansion limit reached"] = true
		return input
	}
	group := m.char
	if value, ok := m.markers["group"]; ok {
		group = value
	}
	legacyValues := map[string]string{"<USER>": m.user, "<BOT>": m.char, "<CHAR>": m.char, "<GROUP>": group, "<CHARIFNOTGROUP>": group}
	expandedBytes := len(input)
	for _, index := range sillyTavernLegacyMacro.FindAllStringIndex(input, -1) {
		expandedBytes += len(legacyValues[strings.ToUpper(input[index[0]:index[1]])]) - (index[1] - index[0])
		if expandedBytes > maxSillyTavernExpansionBytes {
			m.warnings["macro expansion limit reached"] = true
			return input
		}
	}
	input = sillyTavernLegacyMacro.ReplaceAllStringFunc(input, func(token string) string {
		return legacyValues[strings.ToUpper(token)]
	})
	rawInput := input
	var result strings.Builder
	write := func(value string) {
		if len(value) > maxSillyTavernExpansionBytes-result.Len() {
			m.warnings["macro expansion limit reached"] = true
			return
		}
		result.WriteString(value)
	}
	for len(input) > 0 {
		if result.Len() > 8<<20 {
			m.warnings["macro expansion limit reached"] = true
			return result.String()
		}
		start := strings.Index(input, "{{")
		if start < 0 {
			write(input)
			break
		}
		write(input[:start])
		offset := len(rawInput) - len(input) + start
		input = input[start+2:]
		level := 1
		end := -1
		for i := 0; i+1 < len(input); i++ {
			if input[i:i+2] == "{{" {
				level++
				i++
			} else if input[i:i+2] == "}}" {
				level--
				if level == 0 {
					end = i
					break
				}
				i++
			}
		}
		if end < 0 {
			write("{{")
			write(input)
			break
		}
		body := input[:end]
		input = input[end+2:]
		name, args, hasSeparator := strings.Cut(body, "::")
		if !hasSeparator {
			trimmedBody := strings.TrimSpace(body)
			if fields := strings.Fields(trimmedBody); len(fields) > 1 && !strings.Contains(fields[0], ":") {
				name, args = fields[0], strings.TrimSpace(strings.TrimPrefix(trimmedBody, fields[0]))
			} else if colon := strings.IndexByte(trimmedBody, ':'); colon > 0 {
				name, args = trimmedBody[:colon], trimmedBody[colon+1:]
			} else {
				name = trimmedBody
			}
		}
		if value, ok := m.manual[strings.TrimSpace(name)]; ok && args == "" {
			write(value)
			continue
		}
		macroName := strings.ToLower(strings.TrimSpace(name))
		if value, ok := m.values[macroName]; ok && args == "" {
			write(value)
			continue
		}
		if utcMatch := sillyTavernUTCTime.FindStringSubmatch(macroName); len(utcMatch) == 2 {
			offset, err := strconv.Atoi(utcMatch[1])
			if err == nil && offset >= -23 && offset <= 23 {
				location := time.FixedZone("UTC", offset*60*60)
				write(m.now.In(location).Format("15:04"))
				continue
			}
		}
		switch macroName {
		case "user":
			if m.user == "" {
				m.warnings["{{user}} has no value; set the channel user or request context"] = true
			}
			write(m.user)
		case "char":
			if m.char == "" {
				m.warnings["{{char}} has no value; set the channel character or request context"] = true
			}
			write(m.char)
		case "lastusermessage":
			if m.lastUserMessage == "" {
				m.warnings["{{lastUserMessage}} has no plain-text user message in the request context"] = true
			}
			write(m.lastUserMessage)
		case "lastmessage":
			write(m.lastMessage)
		case "lastcharmessage":
			write(m.lastCharMessage)
		case "input":
			write(m.input)
		case "model":
			write(m.model)
		case "group", "charifnotgroup":
			if group, ok := m.markers["group"]; ok {
				write(group)
			} else {
				write(m.char)
			}
		case "lastgenerationtype":
			write("normal") // This compiler only handles normal chat generation.
		case "allchatrange":
			if m.chatCount > 0 {
				write(fmt.Sprintf("0-%d", m.chatCount-1))
			}
		case "isodate", "date":
			write(m.now.Format("2006-01-02"))
		case "isotime", "time":
			write(m.now.Format("15:04"))
		case "datetimeformat":
			if format := strings.TrimSpace(args); format != "" {
				write(formatSillyTavernDate(m.now, format))
			}
		case "timediff":
			first, second, ok := strings.Cut(m.expand(args, depth+1), "::")
			time1, ok1 := parseSillyTavernDate(first, m.now.Location())
			time2, ok2 := parseSillyTavernDate(second, m.now.Location())
			if ok && ok1 && ok2 {
				write(humanizeSillyTavernDuration(time1.Sub(time2)))
			} else {
				m.warnings["timeDiff requires two valid ISO dates or date/time values"] = true
				write("{{" + body + "}}")
			}
		case "weekday":
			write(m.now.Weekday().String())
		case "newline":
			write("\n")
		case "noop":
			// Intentionally expands to an empty string.
		case "uuid":
			var id [16]byte
			if _, err := rand.Read(id[:]); err != nil {
				m.warnings["random number source unavailable"] = true
				write("{{" + body + "}}")
				break
			}
			id[6] = (id[6] & 0x0f) | 0x40
			id[8] = (id[8] & 0x3f) | 0x80
			write(fmt.Sprintf("%x-%x-%x-%x-%x", id[:4], id[4:6], id[6:8], id[8:10], id[10:]))
		case "reverse":
			write(reverseSillyTavernString(m.expand(args, depth+1)))
		case "scenario", "description", "personality", "persona", "charprompt", "charjailbreak", "charinstruction", "charversion", "char_version", "chardepthprompt", "creatornotes", "mesexamples", "mesexamplesraw":
			key := map[string]string{
				"scenario": "scenario", "description": "charDescription", "personality": "charPersonality",
				"persona": "personaDescription", "charprompt": "charPrompt", "charjailbreak": "charJailbreak",
				"charinstruction": "charJailbreak", "charversion": "charVersion", "char_version": "charVersion",
				"chardepthprompt": "charDepthPrompt", "creatornotes": "creatorNotes",
				"mesexamples": "mesExamples", "mesexamplesraw": "mesExamplesRaw",
			}[macroName]
			if value, ok := m.markers[key]; ok {
				write(value)
			} else {
				m.warnings["macro requires request context marker: "+key] = true
				write("{{" + body + "}}")
			}
		case "setvar":
			key, value, ok := strings.Cut(args, "::")
			if ok {
				m.vars[strings.TrimSpace(key)] = m.expand(value, depth+1)
			}
		case "addvar":
			key, value, ok := strings.Cut(args, "::")
			if ok {
				value = m.expand(value, depth+1)
				key = strings.TrimSpace(key)
				current := m.vars[key]
				var array []any
				if common.Unmarshal([]byte(current), &array) == nil && array != nil {
					array = append(array, value)
					encoded, err := common.Marshal(array)
					if err != nil || len(encoded) > maxSillyTavernExpansionBytes {
						m.warnings["macro expansion limit reached"] = true
						return result.String()
					}
					m.vars[key] = string(encoded)
					break
				}
				currentNumber, currentError := strconv.ParseFloat(current, 64)
				valueNumber, valueError := strconv.ParseFloat(value, 64)
				if current == "" {
					currentNumber, currentError = 0, nil
				}
				if currentError == nil && valueError == nil && !math.IsNaN(currentNumber+valueNumber) && !math.IsInf(currentNumber+valueNumber, 0) {
					m.vars[key] = strconv.FormatFloat(currentNumber+valueNumber, 'f', -1, 64)
				} else {
					if len(current) > maxSillyTavernExpansionBytes || len(value) > maxSillyTavernExpansionBytes-len(current) {
						m.warnings["macro expansion limit reached"] = true
						return result.String()
					}
					m.vars[key] = current + value
				}
			}
		case "getvar":
			write(m.vars[strings.TrimSpace(args)])
		case "incvar", "decvar":
			key := strings.TrimSpace(args)
			current := float64(0)
			if value := m.vars[key]; value != "" {
				var err error
				current, err = strconv.ParseFloat(value, 64)
				if err != nil || math.IsNaN(current) || math.IsInf(current, 0) {
					m.warnings["increment/decrement requires a numeric variable: "+key] = true
					write("{{" + body + "}}")
					break
				}
			}
			if macroName == "incvar" {
				current++
			} else {
				current--
			}
			m.vars[key] = strconv.FormatFloat(current, 'f', -1, 64)
			write(m.vars[key])
		case "getglobalvar":
			if value, ok := m.globalVars[strings.TrimSpace(args)]; ok {
				write(value)
			} else {
				m.warnings["global variable requires explicit request context: "+strings.TrimSpace(args)] = true
				write("{{" + body + "}}")
			}
		case "random":
			choices := splitSillyTavernMacroList(m.expand(args, depth+1))
			if len(choices) > 0 {
				index, err := rand.Int(rand.Reader, big.NewInt(int64(len(choices))))
				if err == nil {
					write(choices[index.Int64()])
				} else {
					m.warnings["random number source unavailable"] = true
					write("{{" + body + "}}")
				}
			}
		case "pick":
			choices := splitSillyTavernMacroList(m.expand(args, depth+1))
			if len(choices) > 0 {
				// Stable choices are supported, but the gateway cannot reproduce
				// a browser's cached seedrandom chat hash or its PRNG sequence.
				m.warnings["pick is deterministic in New API but does not reproduce SillyTavern's seedrandom sequence"] = true
				if m.chatID == "" {
					m.warnings["pick uses content-only seed; supply chat_id for chat-scoped stable choices"] = true
				}
				seed := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%d", m.chatID, rawInput, offset)))
				index := binary.BigEndian.Uint64(seed[:8]) % uint64(len(choices))
				write(choices[index])
			}
		case "roll", "dice":
			if value, ok := rollSillyTavernDice(m.expand(strings.TrimSpace(args), depth+1), &m.rollsRemaining); ok {
				write(strconv.FormatInt(value, 10))
			} else {
				if m.rollsRemaining == 0 {
					m.warnings["dice macro execution budget exceeded"] = true
				}
				m.warnings["unsupported dice macro: "+strings.TrimSpace(args)] = true
				write("{{")
				write(body)
				write("}}")
			}
		case "trim":
			write("{{trim}}")
		case "//":
		default:
			if strings.HasPrefix(body, "//") {
				break
			}
			// Explicit context markers also support extension/instruct macros,
			// without exposing server-global state or executing preset code.
			if args == "" {
				found := false
				for key, value := range m.markers {
					if strings.EqualFold(key, macroName) {
						write(value)
						found = true
						break
					}
				}
				if found {
					break
				}
			}
			m.warnings["unsupported macro: "+name] = true
			write("{{")
			write(body)
			write("}}")
		}
	}
	return sillyTavernTrimMacro.ReplaceAllString(result.String(), "")
}

func rollSillyTavernDice(formula string, remaining *int64) (int64, bool) {
	match := sillyTavernDice.FindStringSubmatch(strings.ReplaceAll(formula, " ", ""))
	if match == nil {
		return 0, false
	}
	count := int64(1)
	if match[1] != "" {
		parsed, err := strconv.ParseInt(match[1], 10, 64)
		if err != nil || parsed < 1 || parsed > 1000 {
			return 0, false
		}
		count = parsed
	}
	sideText := match[2]
	if match[4] != "" {
		sideText = match[4] // {{roll N}} is shorthand for 1dN.
	}
	sides, err := strconv.ParseInt(sideText, 10, 64)
	if err != nil || sides < 1 || sides > 100_000_000 {
		return 0, false
	}
	modifier := int64(0)
	if match[3] != "" {
		modifier, err = strconv.ParseInt(match[3], 10, 64)
		if err != nil || (modifier > 0 && count*sides > math.MaxInt64-modifier) {
			return 0, false
		}
	}
	if remaining != nil {
		if count > *remaining {
			*remaining = 0
			return 0, false
		}
		*remaining -= count
	}
	total := modifier
	for range count {
		roll, err := rand.Int(rand.Reader, big.NewInt(sides))
		if err != nil {
			return 0, false
		}
		total += roll.Int64() + 1
	}
	return total, true
}

func splitSillyTavernMacroList(value string) []string {
	if strings.Contains(value, "::") {
		return strings.Split(value, "::")
	}
	value = strings.ReplaceAll(value, `\,`, "\x00")
	parts := strings.Split(value, ",")
	for index := range parts {
		parts[index] = strings.TrimSpace(strings.ReplaceAll(parts[index], "\x00", ","))
	}
	return parts
}

func reverseSillyTavernString(value string) string {
	runes := []rune(value)
	for left, right := 0, len(runes)-1; left < right; left, right = left+1, right-1 {
		runes[left], runes[right] = runes[right], runes[left]
	}
	return string(runes)
}

// Format the common Moment tokens individually: converting a whole format to
// a Go layout would also reinterpret literal digits and bracketed text.
func formatSillyTavernDate(now time.Time, format string) string {
	tokens := []struct{ token, layout string }{
		{"YYYY", "2006"}, {"MMMM", "January"}, {"dddd", "Monday"},
		{"MMM", "Jan"}, {"ddd", "Mon"}, {"SSS", "000"},
		{"YY", "06"}, {"MM", "01"}, {"DD", "02"}, {"HH", "15"}, {"hh", "03"},
		{"mm", "04"}, {"ss", "05"}, {"ZZ", "-0700"}, {"SS", "00"},
		{"M", "1"}, {"D", "2"}, {"H", "15"}, {"h", "3"}, {"m", "4"},
		{"s", "5"}, {"A", "PM"}, {"a", "pm"}, {"Z", "-07:00"}, {"S", "0"},
	}
	var output strings.Builder
	for len(format) > 0 {
		if strings.HasPrefix(format, "[") {
			if literal, rest, ok := strings.Cut(format[1:], "]"); ok {
				output.WriteString(literal)
				format = rest
				continue
			}
		}
		if strings.HasPrefix(format, "X") {
			output.WriteString(strconv.FormatInt(now.Unix(), 10))
			format = format[1:]
			continue
		}
		if strings.HasPrefix(format, "x") {
			output.WriteString(strconv.FormatInt(now.UnixMilli(), 10))
			format = format[1:]
			continue
		}
		if strings.HasPrefix(format, "d") && !strings.HasPrefix(format, "ddd") {
			if strings.HasPrefix(format, "dd") {
				output.WriteString(now.Weekday().String()[:2])
				format = format[2:]
			} else {
				output.WriteString(strconv.Itoa(int(now.Weekday())))
				format = format[1:]
			}
			continue
		}
		matched := false
		for _, token := range tokens {
			if rest, ok := strings.CutPrefix(format, token.token); ok {
				if token.token == "H" {
					output.WriteString(strconv.Itoa(now.Hour()))
				} else if strings.HasPrefix(token.token, "S") {
					milliseconds := fmt.Sprintf("%03d", now.Nanosecond()/1_000_000)
					output.WriteString(milliseconds[:len(token.token)])
				} else {
					output.WriteString(now.Format(token.layout))
				}
				format = rest
				matched = true
				break
			}
		}
		if !matched {
			output.WriteByte(format[0])
			format = format[1:]
		}
	}
	return output.String()
}

func parseSillyTavernDate(value string, location *time.Location) (time.Time, bool) {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05", "2006-01-02 15:04", "2006-01-02", "2006-01-02T15:04:05", "2006-01-02T15:04"} {
		if parsed, err := time.ParseInLocation(layout, strings.TrimSpace(value), location); err == nil {
			return parsed, true
		}
	}
	return time.Time{}, false
}

// Moment's default English relative-time thresholds. The gateway deliberately
// uses a stable language instead of depending on the administrator's locale.
func humanizeSillyTavernDuration(duration time.Duration) string {
	seconds := math.Round(math.Abs(duration.Seconds()))
	minutes := math.Round(seconds / 60)
	hours := math.Round(seconds / 3600)
	days := math.Round(seconds / 86400)
	months := math.Round(days * 4800 / 146097)
	years := math.Round(days * 400 / 146097)
	var text string
	switch {
	case seconds < 45:
		text = "a few seconds"
	case minutes <= 1:
		text = "a minute"
	case minutes < 45:
		text = fmt.Sprintf("%.0f minutes", minutes)
	case hours <= 1:
		text = "an hour"
	case hours < 22:
		text = fmt.Sprintf("%.0f hours", hours)
	case days <= 1:
		text = "a day"
	case days < 26:
		text = fmt.Sprintf("%.0f days", days)
	case months <= 1:
		text = "a month"
	case months < 11:
		text = fmt.Sprintf("%.0f months", months)
	case years <= 1:
		text = "a year"
	default:
		text = fmt.Sprintf("%.0f years", years)
	}
	if duration > 0 {
		return "in " + text
	}
	return text + " ago"
}

// DecodeSillyTavernContext reads an optional structured context without
// adding it to an upstream DTO. Unknown client fields remain untouched.
func DecodeSillyTavernContext(reader io.Reader) (SillyTavernContext, error) {
	var envelope struct {
		Context json.RawMessage `json:"_sillytavern_context"`
	}
	var context SillyTavernContext
	if err := common.DecodeJson(reader, &envelope); err != nil {
		return context, err
	}
	if len(envelope.Context) > 0 {
		if err := common.Unmarshal(envelope.Context, &context); err != nil {
			return context, err
		}
	}
	return context, nil
}
