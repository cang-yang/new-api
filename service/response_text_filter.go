package service

import (
	"bytes"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/gin-gonic/gin"
)

const maxFilteredResponseBytes = 8 << 20

// ResponseTextFilterWriter filters the client-facing JSON/SSE after the adaptor
// has accounted for the original upstream response. A bounded buffer lets a
// regex match span arbitrary SSE chunks and keeps protocol metadata intact.
type ResponseTextFilterWriter struct {
	gin.ResponseWriter
	config   dto.ResponseTextFilter
	pattern  *regexp.Regexp
	status   int
	buffer   bytes.Buffer
	tooLarge bool
	blocked  bool
}

func BeginResponseTextFilter(c *gin.Context, config *dto.ResponseTextFilter, model string) *ResponseTextFilterWriter {
	if c == nil || config == nil || c.Writer == nil || config.Validate() != nil {
		return nil
	}
	if len(config.Models) > 0 {
		matched := false
		for _, name := range config.Models {
			if name == model {
				matched = true
				break
			}
		}
		if !matched {
			return nil
		}
	}
	w := &ResponseTextFilterWriter{ResponseWriter: c.Writer, config: *config, status: c.Writer.Status()}
	if config.Mode == "regex_extract" {
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
		if w.config.MissingMatch == "empty" {
			w.blocked = true
			w.buffer.Reset()
			return len(data), nil
		}
		w.tooLarge = true
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
		var filtered []byte
		var err error
		if strings.Contains(w.Header().Get("Content-Type"), "text/event-stream") {
			filtered, err = w.filterSSE(body)
		} else {
			filtered, err = w.filterJSON(body)
		}
		if err == nil {
			body = filtered
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

func (w *ResponseTextFilterWriter) extract(text string) (string, bool) {
	if w.config.Mode == "tag_extract" {
		_, after, found := strings.Cut(text, w.config.StartTag)
		if !found {
			return "", false
		}
		content, _, found := strings.Cut(after, w.config.EndTag)
		return strings.TrimSpace(content), found
	}
	match := w.pattern.FindStringSubmatch(text)
	if len(match) < 2 {
		return "", false
	}
	return match[1], true
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
				return append(slots, textSlots(response, false)...)
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
			if index, ok := choice["index"].(float64); ok {
				group = "choice:" + strconv.Itoa(int(index))
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
				for _, rawPart := range parts {
					part, ok := rawPart.(map[string]any)
					if ok && part["type"] == "output_text" {
						add(part, "text", "output:"+strconv.Itoa(i))
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
			if index, ok := candidate["index"].(float64); ok {
				group = "candidate:" + strconv.Itoa(int(index))
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
	if err := common.Unmarshal(body, &payload); err != nil {
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
		if extracted, found := w.extract(original.String()); found || w.config.MissingMatch == "empty" {
			group[0].parent[group[0].field] = extracted
			for _, slot := range group[1:] {
				slot.parent[slot.field] = ""
			}
			changed = true
		}
	}
	if !changed {
		return body, nil
	}
	return common.Marshal(payload)
}

func (w *ResponseTextFilterWriter) filterSSE(body []byte) ([]byte, error) {
	type frame struct {
		prefix  string
		payload map[string]any
		raw     []byte
		slots   []responseTextSlot
	}
	var frames []frame
	groups := map[string][]responseTextSlot{}
	for _, raw := range bytes.Split(body, []byte("\n\n")) {
		if len(raw) == 0 {
			continue
		}
		item := frame{raw: raw}
		for _, line := range strings.Split(string(raw), "\n") {
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			data := strings.TrimPrefix(line, "data: ")
			if data == "[DONE]" {
				break
			}
			var payload map[string]any
			if err := common.Unmarshal([]byte(data), &payload); err != nil {
				break
			}
			item.payload = payload
			item.prefix = strings.SplitN(string(raw), "data: ", 2)[0]
			item.slots = textSlots(payload, true)
			for _, slot := range item.slots {
				groups[slot.group] = append(groups[slot.group], slot)
			}
			break
		}
		frames = append(frames, item)
	}
	changed := false
	for _, group := range groups {
		var original strings.Builder
		for _, slot := range group {
			original.WriteString(slot.parent[slot.field].(string))
		}
		if extracted, found := w.extract(original.String()); found || w.config.MissingMatch == "empty" {
			group[0].parent[group[0].field] = extracted
			for _, slot := range group[1:] {
				slot.parent[slot.field] = ""
			}
			changed = true
		}
	}
	if !changed {
		return body, nil
	}
	var out bytes.Buffer
	for _, item := range frames {
		if item.payload == nil || len(item.slots) == 0 {
			out.Write(item.raw)
		} else {
			data, err := common.Marshal(item.payload)
			if err != nil {
				return body, nil
			}
			out.WriteString(item.prefix)
			out.WriteString("data: ")
			out.Write(data)
		}
		out.WriteString("\n\n")
	}
	return out.Bytes(), nil
}
