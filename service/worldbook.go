package service

import (
	"cmp"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
)

type WorldbookDecision struct {
	BookID string `json:"book_id"`
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
	Reason string `json:"reason"`
}

type WorldbookPreview struct {
	Text     string              `json:"text"`
	Entries  []WorldbookDecision `json:"entries"`
	Warnings []string            `json:"warnings"`
}

type worldbookCandidate struct {
	entry dto.WorldbookEntry
	index int
}

type worldbookMatchBudget struct {
	remaining int
	exhausted bool
}

// MatchWorldbook is shared by preview and the relay. All state belongs to this
// invocation, including recursive matches, deduplication and the character budget.
func MatchWorldbook(config *dto.WorldbookConfig, request *dto.GeneralOpenAIRequest) (WorldbookPreview, error) {
	result := WorldbookPreview{Entries: []WorldbookDecision{}, Warnings: []string{}}
	if err := config.Validate(); err != nil {
		return result, err
	}
	if config == nil {
		return result, nil
	}
	depth, budget := config.Limits()
	var parts []string
	size := 0
	selected := 0
	for i := len(request.Messages) - 1; i >= 0 && selected < depth; i-- {
		message := request.Messages[i]
		if message.Role != "user" && message.Role != "assistant" {
			continue
		}
		selected++
		remaining := (256 << 10) - size
		text, truncated := worldbookMessageText(message.Content, remaining)
		if truncated {
			result.Warnings = append(result.Warnings, "Worldbook scan text was limited to 256 KiB")
		}
		if text != "" {
			parts = append(parts, text)
			size += len(text) + 1
		}
		if size >= 256<<10 {
			break
		}
	}
	slices.Reverse(parts)
	scan := strings.Join(parts, "\n")
	var candidates []worldbookCandidate
	for _, book := range config.Books {
		for _, entry := range book.Entries {
			decision := WorldbookDecision{BookID: book.ID, ID: entry.ID, Name: entry.Name, Status: "skipped", Reason: "no_match"}
			if !config.Enabled || !book.Enabled || !entry.Enabled {
				decision.Reason = "disabled"
			} else if len(entry.Unsupported) > 0 {
				decision.Reason = "unsupported"
			}
			index := len(result.Entries)
			result.Entries = append(result.Entries, decision)
			if decision.Reason == "no_match" {
				candidates = append(candidates, worldbookCandidate{entry: entry, index: index})
			}
		}
	}
	slices.SortStableFunc(candidates, func(a, b worldbookCandidate) int { return cmp.Compare(b.entry.Priority, a.entry.Priority) })
	seenIDs, seenContent := map[string]bool{}, map[string]bool{}
	work := worldbookMatchBudget{remaining: 32 << 20}
	var included []string
	used := 0
	passes := 1
	if config.Recursive {
		passes = 3
	}
	for range passes {
		var association []string
		for _, candidate := range candidates {
			decision := &result.Entries[candidate.index]
			if decision.Reason != "no_match" {
				continue
			}
			entry := candidate.entry
			if !worldbookEntryMatches(entry, scan, &work) {
				continue
			}
			if seenIDs[entry.ID] || seenContent[entry.Content] {
				decision.Reason = "duplicate"
				continue
			}
			cost := utf8.RuneCountInString(entry.Content)
			if len(included) > 0 {
				cost += 2
			}
			if cost == 0 || used+cost > budget {
				decision.Reason = "budget"
				continue
			}
			decision.Status = "included"
			decision.Reason = "included"
			seenIDs[entry.ID] = true
			seenContent[entry.Content] = true
			used += cost
			included = append(included, entry.Content)
			association = append(association, entry.Content)
		}
		if len(association) == 0 || work.exhausted {
			break
		}
		// Association only uses included text, and stays within the same bounded
		// scan input. At most two additional passes can discover related entries.
		scan += "\n" + strings.Join(association, "\n")
		if len(scan) > 256<<10 {
			scan = scan[:256<<10]
			for !utf8.ValidString(scan) {
				scan = scan[:len(scan)-1]
			}
		}
	}
	// Association discovers entries in passes, while final placement follows
	// the same stable priority order regardless of when a match was discovered.
	included = included[:0]
	for _, candidate := range candidates {
		if result.Entries[candidate.index].Status == "included" {
			included = append(included, candidate.entry.Content)
		}
	}
	result.Text = strings.Join(included, "\n\n")
	if work.exhausted {
		result.Warnings = append(result.Warnings, "Worldbook matching stopped after its work limit")
	}
	return result, nil
}

type worldbookTextTail struct {
	bytes     []byte
	limit     int
	started   bool
	truncated bool
}

// add visits text parts from newest to oldest. Only the bounded suffix is
// copied; large media text is never joined into an intermediate full string.
func (tail *worldbookTextTail) add(text string) {
	if tail.started {
		if len(tail.bytes) < tail.limit {
			tail.bytes = append(tail.bytes, '\n')
		} else {
			tail.truncated = true
		}
	}
	tail.started = true
	take := min(len(text), tail.limit-len(tail.bytes))
	if take < len(text) {
		tail.truncated = true
	}
	for index := len(text) - 1; index >= len(text)-take; index-- {
		tail.bytes = append(tail.bytes, text[index])
	}
}

func worldbookMessageText(content any, limit int) (string, bool) {
	tail := worldbookTextTail{limit: max(0, limit)}
	switch value := content.(type) {
	case string:
		tail.add(value)
	case []dto.MediaContent:
		for index := len(value) - 1; index >= 0; index-- {
			part := value[index]
			if part.Type == "text" || part.Type == "input_text" {
				tail.add(part.Text)
			}
		}
	case []any:
		for index := len(value) - 1; index >= 0; index-- {
			part := value[index]
			if item, ok := part.(map[string]any); ok && (item["type"] == "text" || item["type"] == "input_text") {
				if text, ok := item["text"].(string); ok {
					tail.add(text)
				}
			}
		}
	}
	slices.Reverse(tail.bytes)
	for len(tail.bytes) > 0 && !utf8.RuneStart(tail.bytes[0]) {
		tail.bytes = tail.bytes[1:]
	}
	return string(tail.bytes), tail.truncated
}

func worldbookEntryMatches(entry dto.WorldbookEntry, text string, work *worldbookMatchBudget) bool {
	if entry.Constant {
		return true
	}
	primary := false
	for _, key := range entry.Keys {
		if worldbookKeyMatches(key, text, entry, work) {
			primary = true
			break
		}
		if work != nil && work.exhausted {
			return false
		}
	}
	if !primary {
		return false
	}
	if entry.SecondaryLogic == "" || entry.SecondaryLogic == "none" {
		return true
	}
	matches := 0
	for _, key := range entry.SecondaryKeys {
		if worldbookKeyMatches(key, text, entry, work) {
			matches++
		}
		if work != nil && work.exhausted {
			return false
		}
	}
	switch entry.SecondaryLogic {
	case "any":
		return matches > 0
	case "all":
		return matches == len(entry.SecondaryKeys)
	case "not_any":
		return matches == 0
	case "not_all":
		return matches < len(entry.SecondaryKeys)
	default:
		return false
	}
}

func worldbookKeyMatches(key, text string, entry dto.WorldbookEntry, work *worldbookMatchBudget) bool {
	if strings.TrimSpace(key) == "" {
		return false
	}
	if work != nil {
		cost := max(1, len(text))
		if work.exhausted || cost > work.remaining {
			work.exhausted = true
			return false
		}
		work.remaining -= cost
	}
	expression, err := dto.CompileWorldbookKey(key, entry.CaseSensitive)
	if err != nil {
		return false
	}
	if expression != nil {
		if entry.WholeWords {
			// RE2 has no Unicode lookaround. Consuming an adjacent non-word
			// character expresses existence of a bounded match in one scan.
			expression, err = regexp.Compile(`(?:^|[^\p{L}\p{N}\p{M}_])(?:` + expression.String() + `)(?:$|[^\p{L}\p{N}\p{M}_])`)
			if err != nil {
				return false
			}
		}
		return expression.MatchString(text)
	}
	if !entry.CaseSensitive {
		key = strings.ToLower(key)
		text = strings.ToLower(text)
	}
	offset := 0
	for offset <= len(text) {
		index := strings.Index(text[offset:], key)
		if index < 0 {
			return false
		}
		index += offset
		if !entry.WholeWords {
			return true
		}
		before, _ := utf8.DecodeLastRuneInString(text[:index])
		after, _ := utf8.DecodeRuneInString(text[index+len(key):])
		beforeWord := unicode.IsLetter(before) || unicode.IsNumber(before) || unicode.IsMark(before) || before == '_'
		afterWord := unicode.IsLetter(after) || unicode.IsNumber(after) || unicode.IsMark(after) || after == '_'
		if !beforeWord && !afterWord {
			return true
		}
		offset = index + len(key)
	}
	return false
}

// CompileWorldbook compiles the preset before applying lore, so scripts and
// macros cannot execute imported worldbook content.
func CompileWorldbook(config *dto.WorldbookConfig, preset *dto.SillyTavernPresetConfig, request *dto.GeneralOpenAIRequest, context SillyTavernContext) (*dto.GeneralOpenAIRequest, PresetCompileTrace, error) {
	context.RetainWorldbookMarkers = config != nil && config.Enabled
	compiled, trace, err := CompileSillyTavernPreset(preset, request, context)
	if err != nil {
		return compiled, trace, err
	}
	source := request
	if context.ChatHistory != nil {
		copy := *request
		copy.Messages = context.ChatHistory
		source = &copy
	}
	return ApplyWorldbook(config, source, compiled, trace)
}

// ApplyWorldbook builds from a worldbook-free preset result. Retries can reuse
// that base without rerunning stateful preset scripts or accumulating lore.
func ApplyWorldbook(config *dto.WorldbookConfig, source, base *dto.GeneralOpenAIRequest, trace PresetCompileTrace) (*dto.GeneralOpenAIRequest, PresetCompileTrace, error) {
	preview, err := MatchWorldbook(config, source)
	if err != nil {
		preview.Warnings = append(preview.Warnings, fmt.Sprintf("Worldbook was skipped: %v", err))
		preview.Text = ""
	}
	compiled := base
	clone := preview.Text != ""
	for _, message := range trace.Messages {
		if (message.Identifier == "worldInfoBefore" || message.Identifier == "worldInfoAfter") && message.Index >= 0 && message.Index < len(base.Messages) && base.Messages[message.Index].Content == "" {
			clone = true
		}
	}
	if clone {
		compiled, err = common.DeepCopy(base)
		if err != nil {
			return base, trace, nil
		}
	}
	trace.Messages = slices.Clone(trace.Messages)
	if preview.Text != "" {
		inserted := false
		for _, message := range trace.Messages {
			if (message.Identifier == "worldInfoBefore" || message.Identifier == "worldInfoAfter") && message.Index >= 0 && message.Index < len(compiled.Messages) {
				if content, ok := compiled.Messages[message.Index].Content.(string); ok {
					if content != "" {
						content += "\n\n"
					}
					compiled.Messages[message.Index].Content = content + preview.Text
					inserted = true
					break
				}
			}
		}
		if !inserted {
			index := 0
			for index < len(compiled.Messages) && (compiled.Messages[index].Role == "system" || compiled.Messages[index].Role == "developer") {
				index++
			}
			compiled.Messages = insertWorldbookMessage(compiled.Messages, preview.Text)
			for i := range trace.Messages {
				if trace.Messages[i].Index >= index {
					trace.Messages[i].Index++
				}
			}
			trace.Messages = append(trace.Messages, PresetMessageSource{Index: index, Role: "system", Source: "worldbook"})
		}
	}
	// Empty markers were retained only to locate a possible insertion point.
	// Remove unused ones from the outgoing request without altering the retry base.
	if clone {
		empty := map[int]bool{}
		for _, message := range trace.Messages {
			if (message.Identifier == "worldInfoBefore" || message.Identifier == "worldInfoAfter") && message.Index >= 0 && message.Index < len(compiled.Messages) && compiled.Messages[message.Index].Content == "" {
				empty[message.Index] = true
			}
		}
		if len(empty) > 0 {
			remap := map[int]int{}
			messages := make([]dto.Message, 0, len(compiled.Messages)-len(empty))
			for index, message := range compiled.Messages {
				if !empty[index] {
					remap[index] = len(messages)
					messages = append(messages, message)
				}
			}
			compiled.Messages = messages
			provenance := make([]PresetMessageSource, 0, len(trace.Messages))
			for _, message := range trace.Messages {
				if index, ok := remap[message.Index]; ok {
					message.Index = index
					provenance = append(provenance, message)
				}
			}
			trace.Messages = provenance
		}
	}
	if config != nil {
		trace.Worldbook = &WorldbookPreview{Entries: preview.Entries, Warnings: preview.Warnings}
	}
	return compiled, trace, nil
}

func insertWorldbookMessage(messages []dto.Message, text string) []dto.Message {
	index := 0
	for index < len(messages) && (messages[index].Role == "system" || messages[index].Role == "developer") {
		index++
	}
	result := make([]dto.Message, 0, len(messages)+1)
	result = append(result, messages[:index]...)
	result = append(result, dto.Message{Role: "system", Content: text})
	return append(result, messages[index:]...)
}
