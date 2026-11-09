package service

import (
	"encoding/json"
	"fmt"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
)

func newPresetDataHost(config *dto.SillyTavernPresetConfig) (*presetDataHost, error) {
	var settings map[string]json.RawMessage
	if err := common.Unmarshal(config.Preset, &settings); err != nil {
		return nil, err
	}
	var orders []map[string]json.RawMessage
	if err := common.Unmarshal(settings["prompt_order"], &orders); err != nil {
		return nil, err
	}
	for i := range orders {
		var entries []map[string]json.RawMessage
		if err := common.Unmarshal(orders[i]["order"], &entries); err != nil {
			return nil, err
		}
		for _, entry := range entries {
			var identifier string
			if err := common.Unmarshal(entry["identifier"], &identifier); err != nil {
				return nil, err
			}
			if enabled, exists := config.EntryOverrides[identifier]; exists {
				entry["enabled"], _ = common.Marshal(enabled)
			}
		}
		orders[i]["order"], _ = common.Marshal(entries)
	}
	settings["prompt_order"], _ = common.Marshal(orders)
	encoded, err := common.Marshal(settings)
	if err != nil {
		return nil, err
	}
	return &presetDataHost{Settings: encoded, Metadata: json.RawMessage(`{}`)}, nil
}

// Only this hook's newly generated, complete call/result pairs establish a
// history-only role. A provider response can never grant itself this role.
func classifyPresetHistoryTools(before, after *dto.GeneralOpenAIRequest, response *PresetScriptResponse) error {
	originalIDs := map[string]bool{}
	for _, message := range before.Messages {
		var calls []struct {
			ID string `json:"id"`
		}
		if len(message.ToolCalls) > 0 && common.Unmarshal(message.ToolCalls, &calls) != nil {
			return fmt.Errorf("preset input tool history is invalid")
		}
		for _, call := range calls {
			originalIDs[call.ID] = true
		}
		if message.ToolCallId != "" {
			originalIDs[message.ToolCallId] = true
		}
	}
	counts, results := map[string]int{}, map[string]int{}
	owners, pending, used := map[string]string{}, map[string]bool{}, map[string]bool{}
	for _, message := range after.Messages {
		if message.Role != "tool" && len(pending) > 0 {
			return fmt.Errorf("preset generated an incomplete tool history block")
		}
		var calls []struct {
			ID       string `json:"id"`
			Type     string `json:"type"`
			Function struct {
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			} `json:"function"`
		}
		if len(message.ToolCalls) > 0 && common.Unmarshal(message.ToolCalls, &calls) != nil {
			return fmt.Errorf("preset generated invalid tool history")
		}
		for _, call := range calls {
			counts[call.ID]++
			if originalIDs[call.ID] {
				continue
			}
			if message.Role != "assistant" || call.ID == "" || call.Type != "function" || !response.ownedTools[call.Function.Name] || common.GetJsonType([]byte(call.Function.Arguments)) != "object" {
				return fmt.Errorf("preset generated an undeclared or invalid history tool call")
			}
			owners[call.ID], pending[call.ID] = call.Function.Name, true
		}
		if message.ToolCallId != "" {
			results[message.ToolCallId]++
			if originalIDs[message.ToolCallId] {
				continue
			}
			if message.Role != "tool" || !pending[message.ToolCallId] {
				return fmt.Errorf("preset generated an unmatched tool result")
			}
			used[owners[message.ToolCallId]] = true
			delete(pending, message.ToolCallId)
		}
	}
	if len(pending) > 0 {
		return fmt.Errorf("preset generated incomplete tool history")
	}
	for id := range owners {
		if counts[id] != 1 || results[id] != 1 {
			return fmt.Errorf("preset generated duplicate tool history IDs")
		}
	}
	for name := range used {
		if !response.boundTools[name] {
			response.historyTools[name] = true
		}
	}
	return nil
}
