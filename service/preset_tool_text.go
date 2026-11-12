package service

import (
	"bytes"
	"fmt"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
)

// Configured reply tools own the delivered text of their choice. Side text is
// not part of that reply; client tools, reasoning and other choices are separate.
type presetToolTextCall struct {
	name      strings.Builder
	arguments strings.Builder
	last      map[string]any
	lastFrame int
	text      string
	choice    string
	id        string
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
	return restorePresetToolCalls(body, stream, map[string]bool{config.Name: true}, false,
		func(calls []*presetToolTextCall) (string, error) {
			var arguments map[string]any
			if err := common.UnmarshalPreservingNumbers([]byte(calls[0].arguments.String()), &arguments); err != nil {
				return "", fmt.Errorf("invalid tool text arguments")
			}
			text, ok := arguments[config.Argument].(string)
			if !ok {
				return "", fmt.Errorf("tool text argument must be a string")
			}
			return text, nil
		}, deadline)
}

// Batch formatters see all owned calls in a choice, in first-appearance order.
// Non-batched declarative transports retain their original emission behavior.
func restorePresetToolCalls(body []byte, stream bool, owned map[string]bool, batch bool, format func([]*presetToolTextCall) (string, error), deadline time.Time) ([]byte, error) {
	states, done, err := parseResponseChoices(body, stream)
	if err != nil {
		return nil, err
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
	var callOrder []string
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
					call = &presetToolTextCall{choice: choiceKey}
					calls[key] = call
					callOrder = append(callOrder, key)
				}
				if id, ok := tool["id"].(string); ok && id != "" {
					call.id = id
				}
				function, _ := tool["function"].(map[string]any)
				if name, ok := function["name"].(string); ok {
					// Names are deltas too: an intermediate prefix may itself
					// name another owned tool. Resolve ownership after assembly.
					// The older single-tool transport explicitly tolerates
					// providers repeating its full name; retain that behavior.
					if batch {
						call.name.WriteString(name)
					} else if name != "" && call.name.String() != name {
						if owned[call.name.String()] {
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
	targetedCalls := map[string]int{}
	for _, call := range calls {
		if owned[call.name.String()] {
			targetedCalls[call.choice]++
		}
	}
	for choiceKey, state := range states {
		targeted := targetedCalls[choiceKey]
		ids := map[string]bool{}
		for _, call := range state.tools {
			if call.id != "" && ids[call.id] {
				return nil, fmt.Errorf("response contains duplicate tool call IDs")
			}
			ids[call.id] = true
			if state.legacy && owned[call.name.String()] {
				return nil, fmt.Errorf("reply transport requires modern tool calls")
			}
		}
		if targeted == 0 {
			continue
		}
		if len(state.tools) != targeted {
			return nil, fmt.Errorf("reply transport cannot mix preset replies with client tools")
		}
		if !batch && targeted != 1 {
			return nil, fmt.Errorf("reply transport requires exactly one configured call")
		}
		if state.finish == "length" || state.finish == "content_filter" || state.refused || stream && (!done || state.finish == "") {
			return nil, fmt.Errorf("reply transport requires a complete, non-refused response")
		}
		choiceHasTransport[choiceKey] = true
	}
	changed := false
	// Decode all targeted calls before changing any payload. A malformed transport
	// cannot leak half-transformed internal tool arguments to the client.
	groups := map[string][]*presetToolTextCall{}
	var groupOrder []string
	for _, key := range callOrder {
		call := calls[key]
		if !deadline.IsZero() && time.Now().After(deadline) {
			return nil, fmt.Errorf("tool text transformation timed out")
		}
		if !owned[call.name.String()] {
			continue
		}
		groupKey := key
		if batch {
			groupKey = call.choice
		}
		if len(groups[groupKey]) == 0 {
			groupOrder = append(groupOrder, groupKey)
		}
		groups[groupKey] = append(groups[groupKey], call)
		changed = true
	}
	if !changed {
		return body, nil
	}
	// Reject unsupported side payloads before invoking any formatter. Only plain
	// text can be superseded by the configured source; never discard media.
	for _, frame := range frames {
		choices, _ := frame.payload["choices"].([]any)
		for position, value := range choices {
			choice, _ := value.(map[string]any)
			if !choiceHasTransport[presetToolTextChoiceKey(choice, position)] {
				continue
			}
			field := "message"
			if stream {
				field = "delta"
			}
			message, _ := choice[field].(map[string]any)
			if content := message["content"]; content != nil {
				if _, ok := content.(string); !ok {
					return nil, fmt.Errorf("tool text cannot replace structured content")
				}
			}
		}
	}
	for _, key := range groupOrder {
		group := groups[key]
		text, err := format(group)
		if err != nil {
			return nil, err
		}
		if len(text) > maxFilteredResponseBytes {
			return nil, fmt.Errorf("tool formatter exceeded response limit")
		}
		last := group[0]
		for _, call := range group[1:] {
			if call.lastFrame >= last.lastFrame {
				last = call
			}
		}
		last.text = text
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
			if choiceHasTransport[choiceKey] {
				// The explicitly configured reply source is authoritative. Clear
				// side text from every event, including before/after tool deltas;
				// otherwise it can leak into JSON or duplicate the final reply.
				delete(message, "content")
			}
			tools, _ := message["tool_calls"].([]any)
			retained := make([]any, 0, len(tools))
			for position, toolValue := range tools {
				tool := toolValue.(map[string]any)
				key := presetToolTextCallKey(choiceKey, tool, position, frameIndex, stream)
				call := calls[key]
				if !owned[call.name.String()] {
					retained = append(retained, toolValue)
					choiceHasClientTools[choiceKey] = true
					continue
				}
				choiceHasTransport[choiceKey] = true
				// Emit at the final fragment, after the complete arguments exist.
				if !emitted[key] && call.lastFrame == frameIndex {
					message["content"] = call.text
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
