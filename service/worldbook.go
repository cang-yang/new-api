package service

import (
	"cmp"
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
	Order  int    `json:"order,omitempty"`
}

type WorldbookPreview struct {
	Text      string                  `json:"text"`
	Entries   []WorldbookDecision     `json:"entries"`
	Warnings  []string                `json:"warnings"`
	Placement *dto.WorldbookPlacement `json:"placement,omitempty"`
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
	return matchWorldbook(config, request)
}

func matchWorldbook(config *dto.WorldbookConfig, request *dto.GeneralOpenAIRequest) (WorldbookPreview, error) {
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
		if !worldbookScanMessageRole(message.Role) {
			continue
		}
		selected++
		remaining := (256 << 10) - size
		text, truncated := worldbookMessageScanText(message, remaining)
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
			} else if len(entry.Issues) > 0 {
				decision.Reason = "invalid"
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
			if !entry.PreventRecursion {
				association = append(association, entry.Content)
			}
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
	// Association discovers entries in passes. Final placement puts higher
	// priority entries later so they are closer to the end of the block.
	orderedIncluded := make([]worldbookCandidate, 0, len(included))
	for _, candidate := range candidates {
		if result.Entries[candidate.index].Status == "included" {
			orderedIncluded = append(orderedIncluded, candidate)
		}
	}
	slices.SortStableFunc(orderedIncluded, func(a, b worldbookCandidate) int {
		return cmp.Compare(a.entry.Priority, b.entry.Priority)
	})
	included = included[:0]
	for index, candidate := range orderedIncluded {
		result.Entries[candidate.index].Order = index + 1
		included = append(included, candidate.entry.Content)
	}
	result.Text = strings.Join(included, "\n\n")
	placement := resolveWorldbookPlacement(config, request.Messages)
	result.Placement = &placement
	if work.exhausted {
		result.Warnings = append(result.Warnings, "Worldbook matching stopped after its work limit")
	}
	return result, nil
}

func worldbookScanMessageRole(role string) bool {
	switch strings.ToLower(role) {
	case "user", "assistant", "tool", "function":
		return true
	default:
		return false
	}
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

func worldbookMessageScanText(message dto.Message, limit int) (string, bool) {
	text, truncated := worldbookMessageText(message.Content, limit)
	calls := message.ParseToolCalls()
	parts := make([]string, 0, 1+len(calls)*2)
	if text != "" {
		parts = append(parts, text)
	}
	for _, call := range calls {
		if call.Function.Name != "" {
			parts = append(parts, call.Function.Name)
		}
		if call.Function.Arguments != "" {
			parts = append(parts, call.Function.Arguments)
		}
	}
	if len(parts) == 0 {
		return "", truncated
	}
	combined := strings.Join(parts, "\n")
	if len(combined) > limit {
		combined = combined[len(combined)-limit:]
		truncated = true
		for len(combined) > 0 && !utf8.ValidString(combined) {
			combined = combined[1:]
		}
	}
	return combined, truncated
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
	// SillyTavern applies secondary logic only when secondary keys exist.
	// Imported entries commonly retain a selective operator with an empty list.
	if len(entry.SecondaryKeys) == 0 || entry.SecondaryLogic == "" || entry.SecondaryLogic == "none" {
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

// CompileWorldbook matches the incoming history, then places lore during
// preset assembly, before post-processing and request scripts.
func CompileWorldbook(config *dto.WorldbookConfig, preset *dto.SillyTavernPresetConfig, request *dto.GeneralOpenAIRequest, context SillyTavernContext) (*dto.GeneralOpenAIRequest, PresetCompileTrace, error) {
	return CompileWorldbookFrom(config, preset, request, request, context)
}

// CompileWorldbookFrom separates the raw client scan source from the request
// that is later compiled by the preset. This keeps keyword matching on the
// client's original input while still inserting lore before preset scripts run.
func CompileWorldbookFrom(config *dto.WorldbookConfig, preset *dto.SillyTavernPresetConfig, source, base *dto.GeneralOpenAIRequest, context SillyTavernContext) (*dto.GeneralOpenAIRequest, PresetCompileTrace, error) {
	if context.ChatHistory != nil {
		historySource := *source
		historySource.Messages = context.ChatHistory
		source = &historySource
	}
	if preset == nil || len(preset.Models) > 0 && !slices.Contains(preset.Models, base.Model) {
		return ApplyWorldbook(config, source, base, PresetCompileTrace{})
	}
	preview, err := MatchWorldbook(config, source)
	if err != nil {
		return nil, PresetCompileTrace{}, err
	}
	context.worldbookConfig, context.worldbookPreview = config, &preview
	compiled, trace, err := CompileSillyTavernPreset(preset, base, context)
	if config != nil {
		trace.Worldbook = &preview
	}
	return compiled, trace, err
}

// PreviewWorldbook executes the same worldbook-before-preset pipeline used by
// live requests while returning only the matching/audit data for the channel UI.
func PreviewWorldbook(config *dto.WorldbookConfig, preset *dto.SillyTavernPresetConfig, request *dto.GeneralOpenAIRequest) (WorldbookPreview, error) {
	if config == nil {
		return WorldbookPreview{Entries: []WorldbookDecision{}, Warnings: []string{}}, nil
	}
	_, trace, err := CompileWorldbook(config, preset, request, SillyTavernContext{})
	if trace.ScriptResponse != nil && trace.ScriptResponse.tavern != nil {
		defer trace.ScriptResponse.tavern.Close()
	}
	if err != nil {
		return WorldbookPreview{}, err
	}
	if trace.Worldbook != nil {
		return *trace.Worldbook, nil
	}
	return MatchWorldbook(config, request)
}

// ApplyWorldbook inserts matched lore into a message base. Callers that need
// preset processing should use CompileWorldbook so insertion happens first.
func ApplyWorldbook(config *dto.WorldbookConfig, source, base *dto.GeneralOpenAIRequest, trace PresetCompileTrace) (*dto.GeneralOpenAIRequest, PresetCompileTrace, error) {
	preview, err := MatchWorldbook(config, source)
	if err != nil {
		return nil, trace, err
	}
	if preview.Text != "" {
		compiled, err := common.DeepCopy(base)
		if err != nil {
			return nil, trace, err
		}
		placement := resolveWorldbookPlacement(config, compiled.Messages)
		if config.InjectionMode == "" || config.InjectionMode == dto.WorldbookInjectionInherit {
			placement.Fallback = true
		}
		index := max(0, min(placement.Index, len(compiled.Messages)))
		compiled.Messages = insertWorldbookMessageAt(compiled.Messages, index, preview.Text)
		placement.Index = index
		preview.Placement = &placement
		base = compiled
	}
	if config != nil {
		trace.Worldbook = &WorldbookPreview{Text: preview.Text, Entries: preview.Entries, Warnings: preview.Warnings, Placement: preview.Placement}
	}
	return base, trace, nil
}

func insertWorldbookMessageAt(messages []dto.Message, index int, text string) []dto.Message {
	index = max(0, min(index, len(messages)))
	result := make([]dto.Message, 0, len(messages)+1)
	result = append(result, messages[:index]...)
	result = append(result, dto.Message{Role: "system", Content: text})
	return append(result, messages[index:]...)
}

func resolveWorldbookPlacement(config *dto.WorldbookConfig, messages []dto.Message) dto.WorldbookPlacement {
	mode := dto.WorldbookInjectionSystemPrelude
	depth := 0
	if config != nil {
		mode = config.InjectionMode
		if mode == "" || mode == dto.WorldbookInjectionInherit {
			mode = dto.WorldbookInjectionSystemPrelude
		}
		depth = config.InjectionDepth
	}
	placement := dto.WorldbookPlacement{Mode: mode, Role: "system", Index: worldbookSystemPreludeIndex(messages)}
	switch mode {
	case dto.WorldbookInjectionBeforeLastUser:
		if index := worldbookLastRoleIndex(messages, "user"); index >= 0 {
			placement.Index = index
		} else {
			placement.Mode = dto.WorldbookInjectionSystemPrelude
			placement.Index = worldbookSystemPreludeIndex(messages)
			placement.Fallback = true
		}
	case dto.WorldbookInjectionAtDepth:
		if index := worldbookDepthIndex(messages, depth); index >= 0 {
			placement.Index = index
		} else {
			placement.Mode = dto.WorldbookInjectionSystemPrelude
			placement.Index = worldbookSystemPreludeIndex(messages)
			placement.Fallback = true
		}
	}
	// Keep tool call/results contiguous when a requested boundary falls inside one exchange.
	for placement.Index > 0 && placement.Index < len(messages) && messages[placement.Index].Role == "tool" {
		placement.Index--
	}
	return placement
}

func worldbookSystemPreludeIndex(messages []dto.Message) int {
	index := 0
	for index < len(messages) && (messages[index].Role == "system" || messages[index].Role == "developer") {
		index++
	}
	return index
}

func worldbookLastRoleIndex(messages []dto.Message, role string) int {
	for index := len(messages) - 1; index >= 0; index-- {
		if messages[index].Role == role {
			return index
		}
	}
	return -1
}

func worldbookDepthIndex(messages []dto.Message, depth int) int {
	depth = max(0, min(depth, 20))
	if depth > len(messages) {
		return -1
	}
	return len(messages) - depth
}
