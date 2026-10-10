package service

import (
	"bytes"
	"fmt"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
)

// The transport owns one explicitly configured function only. Client tools,
// reasoning, usage and choice indexes must survive unchanged.
type presetToolTextCall struct {
	name      strings.Builder
	arguments strings.Builder
	last      map[string]any
	lastFrame int
	text      string
}

type presetToolTextFrame struct {
	raw     string
	lines   []string
	payload map[string]any
}

func restorePresetToolText(body []byte, stream bool, config *dto.SillyTavernToolText, deadline time.Time) ([]byte, error) {
	if config == nil || !config.Enabled {
		return body, nil
	}
	var frames []presetToolTextFrame
	if !stream {
		var payload map[string]any
		if err := common.UnmarshalPreservingNumbers(body, &payload); err != nil {
			return nil, err
		}
		frames = append(frames, presetToolTextFrame{payload: payload})
	} else {
		normalized := strings.ReplaceAll(strings.ReplaceAll(string(body), "\r\n", "\n"), "\r", "\n")
		for raw := range strings.SplitSeq(normalized, "\n\n") {
			if raw == "" {
				continue
			}
			frame := presetToolTextFrame{raw: raw, lines: strings.Split(raw, "\n")}
			var data []string
			for _, line := range frame.lines {
				field, value, _ := strings.Cut(line, ":")
				if field == "data" {
					data = append(data, strings.TrimPrefix(value, " "))
				}
			}
			value := strings.Join(data, "\n")
			if value != "" && strings.TrimSpace(value) != "[DONE]" {
				if err := common.UnmarshalPreservingNumbers([]byte(value), &frame.payload); err != nil {
					return nil, fmt.Errorf("invalid tool text SSE event")
				}
			}
			frames = append(frames, frame)
		}
	}
	calls := map[string]*presetToolTextCall{}
	choiceHasClientTools := map[string]bool{}
	choiceHasTransport := map[string]bool{}
	for frameIndex, frame := range frames {
		if !deadline.IsZero() && time.Now().After(deadline) {
			return nil, fmt.Errorf("tool text transformation timed out")
		}
		choices, _ := frame.payload["choices"].([]any)
		for choicePosition, value := range choices {
			choice, ok := value.(map[string]any)
			if !ok {
				continue
			}
			choiceKey := presetToolTextChoiceKey(choice, choicePosition)
			field := "message"
			if stream {
				field = "delta"
			}
			message, _ := choice[field].(map[string]any)
			tools, _ := message["tool_calls"].([]any)
			for position, toolValue := range tools {
				tool, ok := toolValue.(map[string]any)
				if !ok {
					return nil, fmt.Errorf("invalid tool call")
				}
				if stream {
					if _, ok := tool["index"]; !ok {
						return nil, fmt.Errorf("streaming tool calls require an index")
					}
				}
				key := presetToolTextCallKey(choiceKey, tool, position, frameIndex, stream)
				call := calls[key]
				if call == nil {
					if len(calls) >= 128 {
						return nil, fmt.Errorf("tool text call limit exceeded")
					}
					call = &presetToolTextCall{}
					calls[key] = call
				}
				function, _ := tool["function"].(map[string]any)
				if name, ok := function["name"].(string); ok {
					if name != "" && call.name.String() != name {
						if call.name.String() == config.Name {
							return nil, fmt.Errorf("tool text name changed during streaming")
						}
						call.name.WriteString(name)
					}
				}
				if arguments, ok := function["arguments"].(string); ok {
					call.arguments.WriteString(arguments)
				}
				call.last = message
				call.lastFrame = frameIndex
			}
		}
	}
	changed := false
	// Decode all targeted calls before changing any payload. A malformed transport
	// cannot leak half-transformed internal tool arguments to the client.
	for _, call := range calls {
		if !deadline.IsZero() && time.Now().After(deadline) {
			return nil, fmt.Errorf("tool text transformation timed out")
		}
		if call.name.String() != config.Name {
			continue
		}
		var arguments map[string]any
		if err := common.UnmarshalPreservingNumbers([]byte(call.arguments.String()), &arguments); err != nil {
			return nil, fmt.Errorf("invalid tool text arguments")
		}
		text, ok := arguments[config.Argument].(string)
		if !ok {
			return nil, fmt.Errorf("tool text argument must be a string")
		}
		if content, exists := call.last["content"]; exists && content != nil {
			if _, ok := content.(string); !ok {
				return nil, fmt.Errorf("tool text cannot replace structured content")
			}
		}
		call.text = text
		changed = true
	}
	if !changed {
		return body, nil
	}
	// Walk in wire order, not map order, so multiple text calls remain ordered.
	emitted := map[string]bool{}
	for frameIndex, frame := range frames {
		choices, _ := frame.payload["choices"].([]any)
		for choicePosition, value := range choices {
			choice, ok := value.(map[string]any)
			if !ok {
				continue
			}
			choiceKey := presetToolTextChoiceKey(choice, choicePosition)
			field := "message"
			if stream {
				field = "delta"
			}
			message, _ := choice[field].(map[string]any)
			tools, _ := message["tool_calls"].([]any)
			retained := make([]any, 0, len(tools))
			for position, toolValue := range tools {
				tool := toolValue.(map[string]any)
				key := presetToolTextCallKey(choiceKey, tool, position, frameIndex, stream)
				call := calls[key]
				if call.name.String() != config.Name {
					retained = append(retained, toolValue)
					choiceHasClientTools[choiceKey] = true
					continue
				}
				choiceHasTransport[choiceKey] = true
				// Emit at the final fragment, after the complete arguments exist.
				if !emitted[key] && call.lastFrame == frameIndex {
					content, _ := message["content"].(string)
					message["content"] = content + call.text
					emitted[key] = true
				}
			}
			if len(tools) > 0 {
				if len(retained) == 0 {
					delete(message, "tool_calls")
				} else {
					message["tool_calls"] = retained
				}
			}
		}
	}
	for _, frame := range frames {
		choices, _ := frame.payload["choices"].([]any)
		for position, value := range choices {
			choice, ok := value.(map[string]any)
			key := presetToolTextChoiceKey(choice, position)
			if ok && choice["finish_reason"] == "tool_calls" && choiceHasTransport[key] && !choiceHasClientTools[key] {
				choice["finish_reason"] = "stop"
			}
		}
	}
	if !stream {
		result, err := common.Marshal(frames[0].payload)
		if len(result) > maxFilteredResponseBytes {
			return nil, fmt.Errorf("tool text response size limit exceeded")
		}
		return result, err
	}
	var output bytes.Buffer
	for _, frame := range frames {
		if frame.payload == nil {
			output.WriteString(frame.raw)
			output.WriteString("\n\n")
			continue
		}
		data, err := common.Marshal(frame.payload)
		if err != nil {
			return nil, err
		}
		written := false
		for _, line := range frame.lines {
			field, _, _ := strings.Cut(line, ":")
			if field == "data" {
				if written {
					continue
				}
				output.WriteString("data: ")
				output.Write(data)
				written = true
			} else {
				output.WriteString(line)
			}
			output.WriteByte('\n')
		}
		output.WriteByte('\n')
		if output.Len() > maxFilteredResponseBytes {
			return nil, fmt.Errorf("tool text response size limit exceeded")
		}
	}
	return output.Bytes(), nil
}

func presetToolTextChoiceKey(choice map[string]any, position int) string {
	if index, ok := choice["index"]; ok {
		return fmt.Sprint(index)
	}
	return fmt.Sprint(position)
}

func presetToolTextCallKey(choice string, tool map[string]any, position, frame int, stream bool) string {
	if stream {
		if index, ok := tool["index"]; ok {
			return fmt.Sprintf("%s/%v", choice, index)
		}
	}
	return fmt.Sprintf("%s/%d/%d", choice, frame, position)
}
