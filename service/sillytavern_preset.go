package service

import (
	"cmp"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
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
	lastUserMessage string
	markers         map[string]string
	vars            map[string]string
	warnings        map[string]bool
	manual          map[string]string
	now             time.Time
}

var sillyTavernTrimMacro = regexp.MustCompile(`(?i)(?:\r?\n)*\{\{trim\}\}(?:\r?\n)*`)

const maxSillyTavernExpansionBytes = 8 << 20

// CompileSillyTavernPreset transforms a typed chat request before provider
// conversion. Original message objects (including multimodal and tool fields)
// are retained as whole objects; only preset-created messages contain strings.
func CompileSillyTavernPreset(config *dto.SillyTavernPresetConfig, request *dto.GeneralOpenAIRequest, context SillyTavernContext) (*dto.GeneralOpenAIRequest, PresetCompileTrace, error) {
	var trace PresetCompileTrace
	if config == nil {
		return request, trace, nil
	}
	preset, err := config.ParseAndValidate()
	if err != nil {
		return nil, trace, err
	}
	if len(config.Models) > 0 && !slices.Contains(config.Models, request.Model) {
		return request, trace, nil
	}
	compiled, err := common.DeepCopy(request)
	if err != nil {
		return nil, trace, err
	}
	trace.Name = preset.Name
	trace.Mode = "compatibility"
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
	macro := presetMacroContext{user: context.User, char: context.Char, lastUserMessage: lastUserMessage, markers: context.Markers, vars: make(map[string]string), warnings: make(map[string]bool), manual: config.MacroValues, now: now}
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
		if !exists || !enabled {
			continue
		}
		if trace.Mode == "exact" && prompt.Marker && entry.Identifier != "chatHistory" && entry.Identifier != "dialogueExamples" {
			if _, exists := context.Markers[entry.Identifier]; !exists {
				return nil, trace, fmt.Errorf("exact SillyTavern mode is missing marker %q", entry.Identifier)
			}
		}
		if entry.Identifier == "chatHistory" {
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
		if !exists || !enabled || (len(prompt.InjectionTrigger) > 0 && !slices.Contains(prompt.InjectionTrigger, "normal")) {
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
	if config.PostProcessing == "strict" {
		final, err = strictSillyTavernPostProcess(final)
		if err != nil {
			return nil, trace, err
		}
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
	for warning := range macro.warnings {
		trace.Warnings = append(trace.Warnings, warning)
	}
	slices.Sort(trace.Warnings)
	if trace.Mode == "exact" && len(macro.warnings) > 0 {
		return nil, trace, fmt.Errorf("exact SillyTavern mode cannot resolve every preset macro: %s", strings.Join(trace.Warnings, "; "))
	}
	return compiled, trace, nil
}

// strictSillyTavernPostProcess mirrors SillyTavern's strict, no-tools
// postProcessPrompt: merge identical adjacent roles with two newlines, turn
// non-leading system messages into user messages, insert a user placeholder
// when needed, and merge again. Structured media and tools are rejected until
// their exact SillyTavern flatten/restore behavior can be reproduced.
func strictSillyTavernPostProcess(items []presetMessage) ([]presetMessage, error) {
	for index, item := range items {
		if _, ok := item.message.Content.(string); !ok || item.message.Name != nil ||
			len(item.message.ToolCalls) > 0 || item.message.ToolCallId != "" || len(item.message.Tools) > 0 ||
			(item.message.Role != "system" && item.message.Role != "user" && item.message.Role != "assistant") {
			return nil, fmt.Errorf("SillyTavern strict post-processing cannot safely reproduce structured content, named messages, or tools at message[%d]", index)
		}
	}
	merge := func(input []presetMessage) []presetMessage {
		merged := make([]presetMessage, 0, len(input))
		for _, item := range input {
			content := item.message.Content.(string)
			if len(merged) > 0 && merged[len(merged)-1].message.Role == item.message.Role && content != "" {
				previous := &merged[len(merged)-1]
				previous.message.Content = previous.message.Content.(string) + "\n\n" + content
				previous.id += "," + item.id
				previous.source = "post_processing"
				continue
			}
			merged = append(merged, item)
		}
		return merged
	}
	merged := merge(items)
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
	return merge(merged), nil
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
	if preset.N != nil && *preset.N > 1 && (!preferClient || request.N == nil) {
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
		name, args, _ := strings.Cut(body, "::")
		if value, ok := m.manual[strings.TrimSpace(name)]; ok && args == "" {
			write(value)
			continue
		}
		switch strings.ToLower(strings.TrimSpace(name)) {
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
		case "isodate", "date":
			write(m.now.Format("2006-01-02"))
		case "isotime", "time":
			write(m.now.Format("15:04"))
		case "weekday":
			write(m.now.Weekday().String())
		case "scenario":
			write(m.markers["scenario"])
		case "personality":
			write(m.markers["charPersonality"])
		case "setvar":
			key, value, ok := strings.Cut(args, "::")
			if ok {
				m.vars[key] = m.expand(value, depth+1)
			}
		case "addvar":
			key, value, ok := strings.Cut(args, "::")
			if ok {
				value = m.expand(value, depth+1)
				current := m.vars[key]
				currentNumber, currentError := strconv.ParseFloat(current, 64)
				valueNumber, valueError := strconv.ParseFloat(value, 64)
				if current == "" {
					currentNumber, currentError = 0, nil
				}
				if currentError == nil && valueError == nil {
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
			write(m.vars[args])
		case "random":
			choices := strings.Split(m.expand(args, depth+1), "::")
			if len(choices) > 0 {
				index, err := rand.Int(rand.Reader, big.NewInt(int64(len(choices))))
				if err == nil {
					write(choices[index.Int64()])
				}
			}
		case "trim":
			write("{{trim}}")
		case "//":
		default:
			if strings.HasPrefix(body, "//") {
				break
			}
			m.warnings["unsupported macro: "+name] = true
			write("{{")
			write(body)
			write("}}")
		}
	}
	return sillyTavernTrimMacro.ReplaceAllString(result.String(), "")
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
