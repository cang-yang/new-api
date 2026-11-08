package service

import (
	"fmt"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
)

func (p *PresetScriptResponse) restoreTools(body []byte, stream bool, deadline time.Time) ([]byte, error) {
	if p.tavern != nil {
		return body, nil
	}
	if p.protocol != nil && p.protocol.audit.Activated {
		if p.protocol.phase == presetProtocolPhaseAuxiliary || p.protocol.contract == presetProtocolContractTool {
			return body, nil
		}
		return p.protocol.restoreTerminal(p, body, stream, deadline)
	}
	settings := p.settings.OutputPreprocessing
	if !settings.ConsumeToolCalls || len(p.ownedTools) == 0 {
		return body, nil
	}
	return restorePresetToolCalls(body, stream, p.ownedTools, true, func(calls []*presetToolTextCall) (string, error) {
		return p.formatOwnedCalls(calls, stream, deadline)
	}, deadline)
}

func (p *PresetScriptResponse) formatOwnedCalls(calls []*presetToolTextCall, stream bool, deadline time.Time) (string, error) {
	if p == nil || p.settings == nil {
		return "", fmt.Errorf("preset script response settings are unavailable")
	}
	settings := p.settings.OutputPreprocessing
	arguments := make([]any, 0, len(calls))
	texts := make([]string, 0, len(calls))
	for _, call := range calls {
		var value any
		if err := common.UnmarshalPreservingNumbers([]byte(call.arguments.String()), &value); err != nil {
			return "", fmt.Errorf("toolCallFormatter requires complete JSON arguments")
		}
		arguments = append(arguments, map[string]any{
			"id": call.id, "name": call.name.String(), "arguments": value,
		})
		text := call.arguments.String()
		if object, ok := value.(map[string]any); ok && len(object) == 1 {
			for _, field := range object {
				if single, ok := field.(string); ok {
					text = single
				}
			}
		} else if single, ok := value.(string); ok {
			text = single
		}
		texts = append(texts, text)
	}
	defaultText := strings.Join(texts, "\n")
	if !settings.ToolCallFormatter.Enabled {
		return defaultText, nil
	}
	var result any
	if err := runPresetScript(settings.ToolCallFormatter.Script, map[string]any{
		"calls": arguments, "defaultText": defaultText, "stream": stream, "final": true, "channel": "main",
	}, &result, deadline); err != nil {
		return "", fmt.Errorf("toolCallFormatter: %w", err)
	}
	if object, ok := result.(map[string]any); ok {
		for _, key := range []string{"output", "text", "content"} {
			if value, exists := object[key]; exists {
				result = value
				break
			}
		}
	}
	text, ok := result.(string)
	if !ok {
		return "", fmt.Errorf("toolCallFormatter must return text")
	}
	return text, nil
}

func (p *PresetScriptResponse) processText(text string, deadline time.Time) (string, error) {
	if p.tavern != nil {
		var output string
		if !deadline.IsZero() && time.Now().After(deadline) {
			return "", fmt.Errorf("Tavern response processing deadline exceeded")
		}
		err := p.tavern.call("finish", text, &output)
		return output, err
	}
	settings := p.settings.OutputPreprocessing
	if !settings.Enabled {
		return text, nil
	}
	var result struct {
		Output *string `json:"output"`
		Hold   string  `json:"hold"`
		State  struct {
			Rejected bool `json:"rejected"`
		} `json:"state"`
	}
	if err := runPresetScript(settings.Script, map[string]any{
		"buffer": text, "final": true, "state": map[string]any{},
	}, &result, deadline); err != nil {
		return "", fmt.Errorf("OutputPreprocessing: %w", err)
	}
	// A gateway does not autonomously regenerate rejected responses.
	if result.Output == nil || result.Hold != "" || result.State.Rejected {
		return "", fmt.Errorf("OutputPreprocessing must accept a complete final response without held text")
	}
	return *result.Output, nil
}
