package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/gin-gonic/gin"
)

const maxFilteredResponseBytes = 8 << 20

// ResponseTextFilterWriter filters the client-facing JSON/SSE after the adaptor
// has accounted for the original upstream response. A bounded buffer lets a
// regex match span arbitrary SSE chunks and keeps protocol metadata intact.
type ResponseTextFilterWriter struct {
	gin.ResponseWriter
	config        dto.ResponseTextFilter
	pattern       *regexp.Regexp
	regexes       []presetResponseRegex
	hasFilter     bool
	strictFailure bool
	status        int
	buffer        bytes.Buffer
	tooLarge      bool
	blocked       bool
	warned        bool
	transformErr  error
	deadline      time.Time
}

func BeginResponseTextFilter(c *gin.Context, config *dto.ResponseTextFilter, model string) *ResponseTextFilterWriter {
	return BeginResponseTextFilterWithPreset(c, config, nil, model)
}

// BeginResponseTextFilterWithPreset applies an optional explicit extractor and
// the enabled preset's receive-side regex scripts to assistant text only.
func BeginResponseTextFilterWithPreset(c *gin.Context, config *dto.ResponseTextFilter, preset *dto.SillyTavernPresetConfig, model string) *ResponseTextFilterWriter {
	if c == nil || c.Writer == nil {
		return nil
	}
	if config != nil && len(config.Models) > 0 {
		matched := false
		for _, name := range config.Models {
			if name == model {
				matched = true
				break
			}
		}
		if !matched {
			config = nil
		}
	}
	normalized, setupErr := config.Normalized()
	if setupErr == nil {
		config = normalized
	}
	regexes, warnings := compilePresetResponseRegex(preset, model)
	channelRules, compileErr := compileChannelTextRegex(config, model, "receive")
	if compileErr != nil {
		setupErr = compileErr
	}
	regexes = append(channelRules, regexes...)
	if len(warnings) > 0 {
		logger.LogWarn(c, "Some preset response regex rules were skipped during compilation; inspect preset compatibility warnings")
	}
	hasFilter := config != nil && config.Mode != "rules"
	if !hasFilter && len(regexes) == 0 && len(warnings) == 0 && setupErr == nil {
		return nil
	}
	w := &ResponseTextFilterWriter{ResponseWriter: c.Writer, regexes: regexes, hasFilter: hasFilter, status: c.Writer.Status()}
	w.strictFailure = textRegexRequiresStrictFailure(config, preset, model, false)
	if len(warnings) > 0 {
		w.transformErr = fmt.Errorf("preset regex compatibility failure")
	}
	if setupErr != nil {
		w.transformErr = fmt.Errorf("response regex configuration failure")
	}
	if config != nil {
		w.config = *config
	}
	if hasFilter && setupErr == nil {
		w.pattern = regexp.MustCompile(config.Pattern)
	}
	c.Writer = w
	return w
}

func (w *ResponseTextFilterWriter) WriteHeader(code int) {
	if code > 0 {
		w.status = code
	}
}
func (w *ResponseTextFilterWriter) WriteHeaderNow() {}
func (w *ResponseTextFilterWriter) Status() int     { return w.status }
func (w *ResponseTextFilterWriter) Written() bool {
	return w.buffer.Len() > 0 || w.ResponseWriter.Written()
}

func (w *ResponseTextFilterWriter) Write(data []byte) (int, error) {
	if w.blocked {
		return len(data), nil
	}
	if w.tooLarge {
		return w.ResponseWriter.Write(data)
	}
	if bytes.Equal(data, []byte(": PING\n\n")) {
		return w.ResponseWriter.Write(data)
	}
	if w.buffer.Len()+len(data) > maxFilteredResponseBytes {
		if w.strictFailure {
			w.blocked = true
			w.buffer.Reset()
			return len(data), nil
		}
		w.tooLarge = true
		common.SysError("Response regex exceeded buffer limit; original response retained, text omitted")
		w.ResponseWriter.WriteHeader(w.status)
		if _, err := w.ResponseWriter.Write(w.buffer.Bytes()); err != nil {
			return 0, err
		}
		w.buffer.Reset()
		return w.ResponseWriter.Write(data)
	}
	return w.buffer.Write(data)
}

func (w *ResponseTextFilterWriter) WriteString(data string) (int, error) {
	return w.Write([]byte(data))
}

// Flush must not release an incomplete SSE frame before extraction is known.
func (w *ResponseTextFilterWriter) Flush() {
	if w.tooLarge {
		w.ResponseWriter.Flush()
	}
}

func (w *ResponseTextFilterWriter) Finish(c *gin.Context, success bool) error {
	if w == nil {
		return nil
	}
	c.Writer = w.ResponseWriter
	if !success && !w.tooLarge {
		// The controller owns errors/retries. Never concatenate a buffered failed
		// attempt with a later successful response or the controller's error JSON.
		w.buffer.Reset()
		return nil
	}
	if w.blocked {
		w.Header().Del("Content-Length")
		return fmt.Errorf("response text filter exceeded %d bytes", maxFilteredResponseBytes)
	}
	if w.tooLarge {
		return nil
	}
	body := w.buffer.Bytes()
	if len(body) == 0 {
		return nil
	}
	if success && w.status < 400 {
		w.deadline = time.Now().Add(2 * time.Second)
		var filtered []byte
		var err error
		if w.transformErr != nil {
			err = w.transformErr
		} else if strings.Contains(w.Header().Get("Content-Type"), "text/event-stream") {
			filtered, err = w.filterSSE(body)
		} else {
			filtered, err = w.filterJSON(body)
		}
		if w.transformErr != nil {
			err = w.transformErr
		}
		if err == nil {
			body = filtered
		} else if w.strictFailure {
			w.Header().Del("Content-Length")
			return fmt.Errorf("response text filter could not safely parse or transform the response")
		} else {
			logger.LogWarn(c, "Response text filter bypassed an unsupported or malformed response")
		}
	}
	w.Header().Del("Content-Length")
	w.ResponseWriter.WriteHeader(w.status)
	_, err := w.ResponseWriter.Write(body)
	return err
}

type responseTextSlot struct {
	parent map[string]any
	field  string
	group  string
}

func (w *ResponseTextFilterWriter) transform(text string) (string, bool) {
	original := text
	matched := false
	if w.hasFilter {
		match := w.pattern.FindStringSubmatch(text)
		if len(match) >= 2 {
			matched = true
			text = match[1]
			if w.config.TrimCapture {
				text = strings.TrimSpace(text)
			}
		}
	}
	if !matched && w.hasFilter && w.config.MissingMatch == "empty" {
		text = ""
	}
	for _, script := range w.regexes {
		if !w.deadline.IsZero() && time.Now().After(w.deadline) {
			w.transformErr = fmt.Errorf("response regex execution budget exceeded")
			return original, false
		}
		replaced, err := script.replace(text)
		if err != nil || len(replaced) > maxFilteredResponseBytes {
			if !w.warned {
				common.SysError("Preset response regex skipped a rule after an execution/output limit; response text and patterns omitted")
				w.warned = true
			}
			w.transformErr = fmt.Errorf("response regex execution failed or exceeded output limit")
			return original, false
		}
		text = replaced
	}
	return text, text != original
}

func textSlots(payload map[string]any, stream bool) []responseTextSlot {
	var slots []responseTextSlot
	add := func(parent map[string]any, field, group string) {
		if value, ok := parent[field].(string); ok && value != "" {
			slots = append(slots, responseTextSlot{parent, field, group})
		}
	}
	if event, _ := payload["type"].(string); stream && event != "" {
		switch event {
		case "response.output_text.delta":
			add(payload, "delta", "responses:"+fmt.Sprint(payload["output_index"])+":"+fmt.Sprint(payload["content_index"]))
		case "response.output_text.done":
			add(payload, "text", "responses_done:"+fmt.Sprint(payload["output_index"])+":"+fmt.Sprint(payload["content_index"]))
		case "response.content_part.added", "response.content_part.done":
			if part, ok := payload["part"].(map[string]any); ok && part["type"] == "output_text" {
				add(part, "text", event+":"+fmt.Sprint(payload["output_index"])+":"+fmt.Sprint(payload["content_index"]))
			}
		case "response.output_item.added", "response.output_item.done":
			if item, ok := payload["item"].(map[string]any); ok {
				snapshots := textSlots(map[string]any{"output": []any{item}}, false)
				for i := range snapshots {
					snapshots[i].group = event + ":" + fmt.Sprint(payload["output_index"]) + ":" + snapshots[i].group
				}
				slots = append(slots, snapshots...)
			}
		case "content_block_delta":
			if delta, ok := payload["delta"].(map[string]any); ok && delta["type"] == "text_delta" {
				add(delta, "text", "claude:"+fmt.Sprint(payload["index"]))
			}
		case "content_block_start":
			if block, ok := payload["content_block"].(map[string]any); ok && block["type"] == "text" {
				add(block, "text", "claude:"+fmt.Sprint(payload["index"]))
			}
		}
		if event == "response.completed" || event == "response.done" {
			if response, ok := payload["response"].(map[string]any); ok {
				snapshots := textSlots(response, false)
				for i := range snapshots {
					snapshots[i].group = event + ":" + snapshots[i].group
				}
				return append(slots, snapshots...)
			}
		}
		return slots
	}
	if choices, ok := payload["choices"].([]any); ok {
		for i, raw := range choices {
			choice, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			group := "choice:" + strconv.Itoa(i)
			if index, ok := choice["index"].(json.Number); ok {
				group = "choice:" + index.String()
			}
			add(choice, "text", group)
			for _, key := range []string{"message", "delta"} {
				message, ok := choice[key].(map[string]any)
				if !ok {
					continue
				}
				add(message, "content", group)
				if parts, ok := message["content"].([]any); ok {
					for _, rawPart := range parts {
						part, ok := rawPart.(map[string]any)
						if ok && (part["type"] == "text" || part["type"] == "output_text") {
							add(part, "text", group)
						}
					}
				}
			}
		}
	}
	if output, ok := payload["output"].([]any); ok {
		for i, raw := range output {
			item, ok := raw.(map[string]any)
			if !ok || item["type"] != "message" {
				continue
			}
			if parts, ok := item["content"].([]any); ok {
				for j, rawPart := range parts {
					part, ok := rawPart.(map[string]any)
					if ok && part["type"] == "output_text" {
						add(part, "text", "output:"+strconv.Itoa(i)+":"+strconv.Itoa(j))
					}
				}
			}
		}
	}
	if content, ok := payload["content"].([]any); ok {
		for i, raw := range content {
			part, ok := raw.(map[string]any)
			if ok && part["type"] == "text" {
				add(part, "text", "claude:"+strconv.Itoa(i))
			}
		}
	}
	if candidates, ok := payload["candidates"].([]any); ok {
		for i, raw := range candidates {
			candidate, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			group := "candidate:" + strconv.Itoa(i)
			if index, ok := candidate["index"].(json.Number); ok {
				group = "candidate:" + index.String()
			}
			content, ok := candidate["content"].(map[string]any)
			if !ok {
				continue
			}
			parts, ok := content["parts"].([]any)
			if !ok {
				continue
			}
			for _, rawPart := range parts {
				part, ok := rawPart.(map[string]any)
				if ok && part["thought"] != true {
					add(part, "text", group)
				}
			}
		}
	}
	return slots
}

func (w *ResponseTextFilterWriter) filterJSON(body []byte) ([]byte, error) {
	var payload map[string]any
	if err := common.UnmarshalPreservingNumbers(body, &payload); err != nil {
		return nil, err
	}
	slots := textSlots(payload, false)
	groups := map[string][]responseTextSlot{}
	for _, slot := range slots {
		groups[slot.group] = append(groups[slot.group], slot)
	}
	changed := false
	for _, group := range groups {
		var original strings.Builder
		for _, slot := range group {
			original.WriteString(slot.parent[slot.field].(string))
		}
		if extracted, changedText := w.transform(original.String()); changedText {
			group[0].parent[group[0].field] = extracted
			for _, slot := range group[1:] {
				slot.parent[slot.field] = ""
			}
			changed = true
		}
	}
	if !changed {
		if w.transformErr != nil {
			return nil, w.transformErr
		}
		return body, nil
	}
	if w.transformErr != nil {
		return nil, w.transformErr
	}
	encoded, err := common.Marshal(payload)
	if len(encoded) > maxFilteredResponseBytes {
		return nil, fmt.Errorf("filtered JSON response exceeds size limit")
	}
	return encoded, err
}

func (w *ResponseTextFilterWriter) filterSSE(body []byte) ([]byte, error) {
	type frame struct {
		lines   []string
		payload map[string]any
		raw     string
		slots   []responseTextSlot
	}
	var frames []frame
	groups := map[string][]responseTextSlot{}
	// SSE accepts CRLF, CR, LF, data without a space, and multiple data lines.
	// Preserve event/id/retry/comment fields even when they follow the data.
	normalized := strings.ReplaceAll(strings.ReplaceAll(string(body), "\r\n", "\n"), "\r", "\n")
	for raw := range strings.SplitSeq(normalized, "\n\n") {
		if len(raw) == 0 {
			continue
		}
		item := frame{raw: raw, lines: strings.Split(raw, "\n")}
		var dataLines []string
		for _, line := range item.lines {
			field, value, _ := strings.Cut(line, ":")
			if field == "data" {
				dataLines = append(dataLines, strings.TrimPrefix(value, " "))
			}
		}
		data := strings.Join(dataLines, "\n")
		if data != "" && strings.TrimSpace(data) != "[DONE]" {
			if err := common.UnmarshalPreservingNumbers([]byte(data), &item.payload); err != nil {
				return nil, fmt.Errorf("invalid JSON in response SSE event")
			}
			item.slots = textSlots(item.payload, true)
			for _, slot := range item.slots {
				groups[slot.group] = append(groups[slot.group], slot)
			}
		}
		frames = append(frames, item)
	}
	changed := false
	for _, group := range groups {
		var original strings.Builder
		for _, slot := range group {
			original.WriteString(slot.parent[slot.field].(string))
		}
		if extracted, changedText := w.transform(original.String()); changedText {
			group[0].parent[group[0].field] = extracted
			for _, slot := range group[1:] {
				slot.parent[slot.field] = ""
			}
			changed = true
		}
	}
	if !changed {
		if w.transformErr != nil {
			return nil, w.transformErr
		}
		return body, nil
	}
	if w.transformErr != nil {
		return nil, w.transformErr
	}
	var out bytes.Buffer
	for _, item := range frames {
		if item.payload == nil || len(item.slots) == 0 {
			out.WriteString(item.raw)
			out.WriteByte('\n')
		} else {
			data, err := common.Marshal(item.payload)
			if err != nil {
				return nil, err
			}
			written := false
			for _, line := range item.lines {
				field, _, _ := strings.Cut(line, ":")
				if field == "data" {
					if written {
						continue
					}
					out.WriteString("data: ")
					out.Write(data)
					written = true
				} else {
					out.WriteString(line)
				}
				out.WriteByte('\n')
			}
		}
		out.WriteByte('\n')
		if out.Len() > maxFilteredResponseBytes {
			return nil, fmt.Errorf("filtered SSE response exceeds size limit")
		}
	}
	return out.Bytes(), nil
}
