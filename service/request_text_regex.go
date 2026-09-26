package service

import (
	"fmt"
	"reflect"
	"slices"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
)

// ApplyRequestTextRegex processes a private typed request before preset assembly
// and token estimation. It never rewrites tools, images, reasoning, or raw JSON.
func ApplyRequestTextRegex(config *dto.ResponseTextFilter, preset *dto.SillyTavernPresetConfig, request dto.Request, model string) (dto.Request, error) {
	result, err := applyRequestTextRegex(config, preset, request, model)
	if err != nil && !textRegexRequiresStrictFailure(config, preset, model, true) {
		common.SysError("Send regex bypassed after a processing failure; original request retained, text omitted")
		return request, nil
	}
	return result, err
}

func applyRequestTextRegex(config *dto.ResponseTextFilter, preset *dto.SillyTavernPresetConfig, request dto.Request, model string) (dto.Request, error) {
	rules, err := compileChannelTextRegex(config, model, "send")
	if err != nil {
		return nil, err
	}
	imported, warnings := compilePresetTextRegex(preset, model, true)
	if len(warnings) > 0 {
		return nil, fmt.Errorf("send-side preset regex has unsupported or invalid rules; resolve compatibility warnings before enabling")
	}
	rules = append(rules, imported...)
	if len(rules) == 0 {
		return request, nil
	}
	switch request.(type) {
	case *dto.GeneralOpenAIRequest, *dto.OpenAIResponsesRequest, *dto.ClaudeRequest, *dto.GeminiChatRequest:
	default:
		return nil, fmt.Errorf("send-side regex supports chat, responses, Claude messages and Gemini content requests only")
	}
	raw, err := common.Marshal(request)
	if err != nil {
		return nil, err
	}
	if len(raw) > 16<<20 {
		return nil, fmt.Errorf("send-side regex request exceeds 16 MiB")
	}
	var payload map[string]any
	if err = common.UnmarshalPreservingNumbers(raw, &payload); err != nil {
		return nil, err
	}
	deadline := time.Now().Add(2 * time.Second)
	remainingText := 16 << 20
	apply := func(parent map[string]any, field, role string, depth int) error {
		text, ok := parent[field].(string)
		if !ok || text == "" {
			return nil
		}
		for _, rule := range rules {
			if !slices.Contains(rule.roles, role) || rule.minDepth != nil && *rule.minDepth >= 0 && depth < *rule.minDepth || rule.maxDepth != nil && *rule.maxDepth >= 0 && depth > *rule.maxDepth {
				continue
			}
			if time.Now().After(deadline) {
				return fmt.Errorf("send-side regex execution budget exceeded")
			}
			text, err = rule.replace(text)
			if err != nil {
				return fmt.Errorf("send-side regex failed: %w", err)
			}
		}
		remainingText -= len(text)
		if remainingText < 0 {
			return fmt.Errorf("send-side regex total text exceeds 16 MiB")
		}
		parent[field] = text
		return nil
	}
	// Only explicitly supported text part types are writable. Tool results and
	// model-internal thinking blocks are excluded even if they contain text keys.
	parts := func(parent map[string]any, field, role string, depth int, gemini bool) error {
		if err := apply(parent, field, role, depth); err != nil {
			return err
		}
		if list, ok := parent[field].([]any); ok {
			for _, rawPart := range list {
				part, ok := rawPart.(map[string]any)
				if !ok {
					continue
				}
				kind, _ := part["type"].(string)
				if kind == "text" || kind == "input_text" || kind == "output_text" || gemini && kind == "" && part["thought"] != true {
					if err := apply(part, "text", role, depth); err != nil {
						return err
					}
				}
			}
		}
		return nil
	}
	for _, field := range []string{"messages", "input", "contents"} {
		if field == "input" {
			if err := apply(payload, field, "user", 0); err != nil {
				return nil, err
			}
		}
		list, ok := payload[field].([]any)
		if !ok {
			continue
		}
		for i, item := range list {
			message, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if kind, ok := message["type"].(string); ok && kind != "message" {
				continue
			}
			role, _ := message["role"].(string)
			if field == "contents" && role == "model" {
				role = "assistant"
			}
			if role == "" && (field == "input" || field == "contents") {
				role = "user"
			}
			if !slices.Contains([]string{"user", "assistant", "system", "developer"}, role) {
				continue
			}
			contentField := "content"
			if field == "contents" {
				contentField = "parts"
			}
			if err := parts(message, contentField, role, len(list)-1-i, field == "contents"); err != nil {
				return nil, err
			}
		}
	}
	for _, field := range []string{"system", "instructions"} {
		if err := parts(payload, field, "system", 0, false); err != nil {
			return nil, err
		}
	}
	if system, ok := payload["systemInstruction"].(map[string]any); ok {
		if err := parts(system, "parts", "system", 0, true); err != nil {
			return nil, err
		}
	}
	raw, err = common.Marshal(payload)
	if err != nil {
		return nil, err
	}
	if len(raw) > 16<<20 {
		return nil, fmt.Errorf("send-side regex request exceeds 16 MiB")
	}
	copyRequest := reflect.New(reflect.TypeOf(request).Elem()).Interface().(dto.Request)
	if err := common.Unmarshal(raw, copyRequest); err != nil {
		return nil, err
	}
	return copyRequest, nil
}
