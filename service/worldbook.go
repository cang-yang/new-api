package service

import (
	"cmp"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
)

type WorldbookDecision struct {
	Eligible  bool   `json:"eligible"`
	Triggered bool   `json:"triggered"`
	Selected  bool   `json:"selected"`
	Inserted  bool   `json:"inserted"`
	BookID    string `json:"book_id"`
	ID        string `json:"id"`
	Name      string `json:"name"`
	Status    string `json:"status"`
	Reason    string `json:"reason"`
	Order     int    `json:"order,omitempty"`
	// Placement describes the assembled insertion, before preset postprocessing
	// and scripts may merge, move, or replace messages.
	Placement *dto.WorldbookPlacement `json:"placement,omitempty"`
}

type WorldbookPreview struct {
	Fingerprint    string                    `json:"fingerprint,omitempty"`
	DraftRevision  string                    `json:"draft_revision,omitempty"`
	Phase          string                    `json:"phase,omitempty"`
	PlacementPhase string                    `json:"placement_phase,omitempty"`
	BypassReason   string                    `json:"bypass_reason,omitempty"`
	FinalRequest   *dto.GeneralOpenAIRequest `json:"final_request,omitempty"`

	Text      string                  `json:"text"`
	Entries   []WorldbookDecision     `json:"entries"`
	Warnings  []string                `json:"warnings"`
	Placement *dto.WorldbookPlacement `json:"placement,omitempty"`
	expanded  bool
	groups    []worldbookInjectionGroup
}

type worldbookInjectionGroup struct {
	mode      string
	depth     int
	text      string
	decisions []int
	contents  []string
}

type worldbookCandidate struct {
	entry    dto.WorldbookEntry
	index    int
	required bool
}

func worldbookHasRequiredContent(config *dto.WorldbookConfig) bool {
	return config != nil && config.Enabled && slices.ContainsFunc(config.Books, func(book dto.Worldbook) bool {
		return book.Enabled && book.Required && slices.ContainsFunc(book.Entries, func(entry dto.WorldbookEntry) bool { return entry.Enabled })
	})
}

func CanSkipWorldbook(config *dto.WorldbookConfig, preset *dto.SillyTavernPresetConfig) bool {
	return AdaptationFailureMode(preset, FailureStageWorldbook) == FailureSkip && !worldbookHasRequiredContent(config)
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

func matchWorldbook(config *dto.WorldbookConfig, request *dto.GeneralOpenAIRequest, expand ...func(string) string) (WorldbookPreview, error) {
	options := worldbookMatchOptions{}
	if len(expand) > 0 {
		options.expand = expand[0]
	}
	return matchWorldbookWith(config, request, options)
}

type worldbookMatchOptions struct {
	expand    func(string) string
	anchor    func(string, int) string
	available func(string, int) bool
}

// assemblePresetWorldbook commits no caller-owned messages or macro state.
// Matching, insertion and wrapper expansion share one working transaction.
func assemblePresetWorldbook(context SillyTavernContext, preset *dto.SillyTavernPreset, original []presetMessage, marker string, macro *presetMacroContext) ([]presetMessage, *WorldbookPreview, error) {
	final := slices.Clone(original)
	preview := context.worldbookPreview
	if context.worldbookSource != nil {
		matched, err := matchWorldbookWith(context.worldbookConfig, context.worldbookSource, worldbookMatchOptions{
			expand: func(text string) string { return macro.expand(text, 0) },
			anchor: func(mode string, depth int) string {
				placement, isMarker := worldbookGroupPlacement(context.worldbookConfig, mode, depth, final, -1)
				return fmt.Sprintf("%d:%t", placement.Index, isMarker)
			},
			available: func(mode string, depth int) bool {
				placement, _ := worldbookGroupPlacement(context.worldbookConfig, mode, depth, final, -1)
				return !placement.Fallback
			},
		})
		if err != nil {
			return nil, nil, err
		}
		preview = &matched
	}
	if macro.warnings["macro expansion limit reached"] {
		return nil, nil, fmt.Errorf("worldbook macro expansion exceeded its limit")
	}
	if preview != nil && preview.Text != "" {
		// Do not mutate a legacy preview supplied by the caller on failure.
		copy, err := common.DeepCopy(preview)
		if err != nil {
			return nil, nil, err
		}
		copy.groups, copy.expanded = preview.groups, preview.expanded
		preview = copy
		markerIndex := slices.IndexFunc(final, func(item presetMessage) bool {
			return marker != "" && slices.Contains(strings.Split(item.id, ","), marker)
		})
		final, err = injectWorldbookGroups(context.worldbookConfig, preview, final, markerIndex, macro)
		if err != nil {
			return nil, nil, err
		}
	}
	if context.worldbookConfig.SemanticsVersion >= 2 {
		for index := range final {
			item := &final[index]
			if item.id != "worldInfoBefore" && item.id != "worldInfoAfter" {
				continue
			}
			if content, ok := item.message.Content.(string); ok && strings.TrimSpace(content) != "" && strings.TrimSpace(preset.WorldInfoFormat) != "" {
				wrapper := macro.expand(preset.WorldInfoFormat, 0)
				size := int64(len(wrapper)) + int64(strings.Count(wrapper, "{0}"))*int64(len(content)-3)
				if macro.warnings["macro expansion limit reached"] || size > maxSillyTavernExpansionBytes {
					return nil, nil, fmt.Errorf("worldbook format exceeded its limit")
				}
				item.message.Content = strings.ReplaceAll(wrapper, "{0}", content)
			}
		}
	}
	return final, preview, nil
}

func matchWorldbookWith(config *dto.WorldbookConfig, request *dto.GeneralOpenAIRequest, options worldbookMatchOptions) (WorldbookPreview, error) {
	result := WorldbookPreview{Entries: []WorldbookDecision{}, Warnings: []string{}}
	if err := config.Validate(); err != nil {
		return result, err
	}
	if config == nil || request == nil {
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
			} else if !worldbookRisksAccepted(entry, config) {
				decision.Reason = "needs_review"
			}
			decision.Eligible = decision.Reason == "no_match"
			if book.Required && (decision.Reason == "invalid" || decision.Reason == "needs_review") {
				return result, fmt.Errorf("required worldbook contains an entry that needs correction or review")
			}
			index := len(result.Entries)
			result.Entries = append(result.Entries, decision)
			if decision.Reason == "no_match" {
				candidates = append(candidates, worldbookCandidate{entry: entry, index: index, required: book.Required})
			}
		}
	}
	slices.SortStableFunc(candidates, func(a, b worldbookCandidate) int {
		if a.required != b.required {
			if a.required {
				return -1
			}
			return 1
		}
		return cmp.Compare(b.entry.Priority, a.entry.Priority)
	})
	seenIDs, seenContent := map[string]bool{}, map[string]bool{}
	work := worldbookMatchBudget{remaining: 32 << 20}
	var included []string
	budgetBlocks := map[string]bool{}
	used := 0
	passes := 1
	if config.Recursive {
		passes = 3
	}
	for range passes {
		var association []string
		for candidateIndex := range candidates {
			candidate := &candidates[candidateIndex]
			decision := &result.Entries[candidate.index]
			if decision.Reason != "no_match" {
				continue
			}
			entry := candidate.entry
			if !worldbookEntryMatches(entry, scan, &work) {
				continue
			}
			decision.Triggered = true
			mode, entryDepth := entry.InjectionMode, entry.InjectionDepth
			if mode == "" || mode == dto.WorldbookInjectionFollowGlobal {
				mode, entryDepth = config.InjectionMode, config.InjectionDepth
			}
			if options.available != nil && config.PlacementFallback != "" && !options.available(mode, entryDepth) {
				if config.PlacementFallback == "fail" || config.PlacementFallback == "skip" && candidate.required {
					return result, fmt.Errorf("required worldbook insertion position is unavailable")
				}
				if config.PlacementFallback == "skip" {
					decision.Reason = "position_unavailable"
					continue
				}
			}
			if config.SemanticsVersion >= 2 && options.expand != nil {
				entry.Content = options.expand(entry.Content)
				candidate.entry.Content = entry.Content
				result.expanded = true
			}
			identity := decision.BookID + "\x00" + entry.ID
			if seenIDs[identity] || config.SemanticsVersion < 2 && seenContent[entry.Content] {
				decision.Reason = "duplicate"
				continue
			}
			cost := utf8.RuneCountInString(entry.Content)
			if cost == 0 {
				if candidate.required {
					return result, fmt.Errorf("triggered required worldbook entry expanded to empty content")
				}
				decision.Reason = "empty"
				continue
			}
			block := mode
			if mode == dto.WorldbookInjectionAtDepth {
				block += strconv.Itoa(entryDepth)
			}
			if options.anchor != nil {
				block = options.anchor(mode, entryDepth)
			}
			if config.SemanticsVersion < 2 && len(included) > 0 || config.SemanticsVersion >= 2 && budgetBlocks[block] {
				cost += 2
			}
			if cost == 0 || used+cost > budget {
				if candidate.required {
					return result, fmt.Errorf("required worldbook content exceeds the configured character budget")
				}
				decision.Reason = "budget"
				continue
			}
			decision.Status = "included"
			decision.Reason = "included"
			seenIDs[identity] = true
			decision.Selected = true
			seenContent[entry.Content] = true
			used += cost
			budgetBlocks[block] = true
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
		return cmp.Or(cmp.Compare(a.entry.Priority, b.entry.Priority), cmp.Compare(a.index, b.index))
	})
	included = included[:0]
	for index, candidate := range orderedIncluded {
		result.Entries[candidate.index].Order = index + 1
		included = append(included, candidate.entry.Content)
		mode, depth := candidate.entry.InjectionMode, candidate.entry.InjectionDepth
		if mode == "" || mode == dto.WorldbookInjectionFollowGlobal {
			mode, depth = config.InjectionMode, config.InjectionDepth
		}
		if mode == "" {
			mode = dto.WorldbookInjectionInherit
		}
		groupIndex := slices.IndexFunc(result.groups, func(group worldbookInjectionGroup) bool {
			return group.mode == mode && (mode != dto.WorldbookInjectionAtDepth || group.depth == depth)
		})
		if groupIndex < 0 {
			result.groups = append(result.groups, worldbookInjectionGroup{mode: mode, depth: depth})
			groupIndex = len(result.groups) - 1
		}
		group := &result.groups[groupIndex]
		if len(group.decisions) > 0 {
			group.text += "\n\n"
		}
		group.text += candidate.entry.Content
		group.decisions = append(group.decisions, candidate.index)
		group.contents = append(group.contents, candidate.entry.Content)
		placement := resolveWorldbookPlacement(&dto.WorldbookConfig{InjectionMode: mode, InjectionDepth: depth}, request.Messages)
		placement.Fallback = placement.Fallback || mode == dto.WorldbookInjectionInherit
		result.Entries[candidate.index].Placement = &placement
	}
	result.Text = strings.Join(included, "\n\n")
	placement := resolveWorldbookPlacement(config, request.Messages)
	result.Placement = &placement
	if work.exhausted {
		if worldbookHasRequiredContent(config) {
			return result, fmt.Errorf("required worldbook matching exceeded its work limit")
		}
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

// CompileWorldbookFrom receives effective history after send rules, separate
// from preset instructions. Matching and placement share the compiler's macro
// context and run before any preset request scripts.
func CompileWorldbookFrom(config *dto.WorldbookConfig, preset *dto.SillyTavernPresetConfig, source, base *dto.GeneralOpenAIRequest, context SillyTavernContext) (*dto.GeneralOpenAIRequest, PresetCompileTrace, error) {
	if source == nil || base == nil {
		return nil, PresetCompileTrace{}, newResponseStageError(FailureStagePreflight, fmt.Errorf("worldbook requires a request"))
	}
	if context.ChatHistory != nil {
		historySource := *source
		historySource.Messages = context.ChatHistory
		source = &historySource
	}
	if preset == nil || len(preset.Models) > 0 && !slices.Contains(preset.Models, base.Model) {
		if config != nil && config.SemanticsVersion >= 2 {
			macro, err := newPresetMacroContext(nil, nil, source, context, source.Messages)
			if err != nil {
				return nil, PresetCompileTrace{}, newResponseStageError(FailureStageWorldbook, err)
			}
			final := make([]presetMessage, len(base.Messages))
			for index, message := range base.Messages {
				final[index] = presetMessage{message: message, source: "chat_history", historyID: strconv.Itoa(index)}
			}
			preview, err := matchWorldbookWith(config, source, worldbookMatchOptions{
				expand: func(text string) string { return macro.expand(text, 0) },
				anchor: func(mode string, depth int) string {
					placement, marker := worldbookGroupPlacement(config, mode, depth, final, -1)
					return fmt.Sprintf("%d:%t", placement.Index, marker)
				},
				available: func(mode string, depth int) bool {
					placement, _ := worldbookGroupPlacement(config, mode, depth, final, -1)
					return !placement.Fallback
				},
			})
			if err != nil {
				return nil, PresetCompileTrace{}, newResponseStageError(FailureStageWorldbook, err)
			}
			if macro.warnings["macro expansion limit reached"] {
				return nil, PresetCompileTrace{}, newResponseStageError(FailureStageWorldbook, fmt.Errorf("worldbook macro expansion exceeded its limit"))
			}
			return applyWorldbookPreview(config, preview, base, PresetCompileTrace{})
		}
		return ApplyWorldbook(config, source, base, PresetCompileTrace{})
	}
	if context.Now.IsZero() {
		context.Now = time.Now()
	}
	parsed, err := preset.ParseAndValidate()
	if err != nil {
		return nil, PresetCompileTrace{}, newResponseStageError(FailureStagePreflight, err)
	}
	if config != nil && config.SemanticsVersion >= 2 {
		// Matching uses effective history, but placement needs the assembled
		// anchors. Both run inside one compiler/macro context, before scripts.
		context.worldbookConfig = config
		context.worldbookSource = source
		return CompileSillyTavernPreset(preset, base, context)
	}
	macro, err := newPresetMacroContext(preset, parsed, source, context, source.Messages)
	if err != nil {
		return nil, PresetCompileTrace{}, newResponseStageError(FailureStageWorldbook, err)
	}
	preview, err := matchWorldbook(config, source, func(text string) string { return macro.expand(text, 0) })
	if err == nil && macro.warnings["macro expansion limit reached"] {
		err = fmt.Errorf("worldbook macro expansion exceeded its limit")
	}
	if err != nil {
		if CanSkipWorldbook(config, preset) {
			compiled, trace, compileErr := CompileSillyTavernPreset(preset, base, context)
			trace.Warnings = append(trace.Warnings, "worldbook skipped by the configured failure policy")
			trace.Worldbook = &WorldbookPreview{Phase: "skipped", BypassReason: "failure_policy", Entries: []WorldbookDecision{}, Warnings: []string{}}
			return compiled, trace, compileErr
		}
		return nil, PresetCompileTrace{}, newResponseStageError(FailureStageWorldbook, err)
	}
	context.worldbookConfig, context.worldbookPreview = config, &preview
	compiled, trace, err := CompileSillyTavernPreset(preset, base, context)
	if config != nil && trace.Worldbook == nil {
		trace.Worldbook = &preview
	}
	return compiled, trace, err
}

// PreviewWorldbook executes the same worldbook-before-preset pipeline used by
// live requests while returning only the matching/audit data for the channel UI.
func PreviewWorldbook(config *dto.WorldbookConfig, preset *dto.SillyTavernPresetConfig, request *dto.GeneralOpenAIRequest) (WorldbookPreview, error) {
	return PreviewWorldbookDraft(config, preset, nil, request, SillyTavernContext{}, "", false)
}

func PreviewWorldbookDraft(config *dto.WorldbookConfig, preset *dto.SillyTavernPresetConfig, sendRules *dto.ResponseTextFilter, request *dto.GeneralOpenAIRequest, context SillyTavernContext, revision string, finalRequest bool, scopes ...string) (WorldbookPreview, error) {
	if config == nil {
		return WorldbookPreview{Entries: []WorldbookDecision{}, Warnings: []string{}}, nil
	}
	profile := NewAdaptationProfile(preset, config, sendRules, sendRules)
	fingerprint, err := AdaptationProfileFingerprint(profile)
	if err != nil {
		return WorldbookPreview{}, err
	}
	effective, err := cloneGeneralRequest(request)
	if err != nil {
		return WorldbookPreview{}, err
	}
	if effective == nil {
		return WorldbookPreview{}, fmt.Errorf("preview requires a request")
	}
	if len(scopes) > 0 {
		if scopes[0] != "" && scopes[0] != "all_supported" && scopes[0] != "plain_chat_only" {
			return WorldbookPreview{}, fmt.Errorf("invalid adaptation scope")
		}
		if applies, reason := AdaptationApplies(scopes[0], effective); !applies {
			preview := WorldbookPreview{DraftRevision: revision, Fingerprint: fingerprint, Phase: "scope_bypass", BypassReason: reason, Entries: []WorldbookDecision{}, Warnings: []string{}}
			if finalRequest {
				preview.FinalRequest = effective
			}
			return preview, nil
		}
	}
	if context.ChatHistory != nil {
		effective.Messages = context.ChatHistory
	}
	transformed, err := ApplyRequestTextRegex(sendRules, preset, effective, effective.Model)
	if err != nil {
		return WorldbookPreview{}, err
	}
	effective = transformed.(*dto.GeneralOpenAIRequest)
	if context.ChatHistory != nil {
		context.ChatHistory = effective.Messages
	}
	originalPreset := preset
	if preset != nil {
		copied, err := common.DeepCopy(preset)
		if err != nil {
			return WorldbookPreview{}, err
		}
		if !finalRequest {
			copied.ScriptRuntime = ""
		}
		if !finalRequest {
			copied.ToolProtocol = nil
		}
		if !finalRequest {
			copied.ReplyAdapter = nil
		}
		if !finalRequest {
			copied.ToolText = nil
		}
		preset = copied
	}
	compiled, trace, err := CompileWorldbook(config, preset, effective, context)
	if trace.ScriptResponse != nil && trace.ScriptResponse.tavern != nil {
		defer trace.ScriptResponse.tavern.Close()
	}
	if err != nil {
		return WorldbookPreview{}, err
	}
	if trace.Worldbook != nil {
		preview := *trace.Worldbook
		preview.Fingerprint = fingerprint
		preview.DraftRevision = revision
		preview.Phase = "before_scripts"
		if finalRequest {
			if requirements, err := SnapshotClientRequirements(request); err != nil {
				return WorldbookPreview{}, err
			} else if err := SealClientRequirements(requirements, compiled, sendRules, trace.ScriptResponse); err != nil {
				return WorldbookPreview{}, err
			}
			preview.FinalRequest = compiled
			preview.Phase = "final_request"
		}
		if !finalRequest && originalPreset != nil && originalPreset.ScriptRuntime != "" {
			preview.PlacementPhase = "before_scripts"
			preview.Warnings = append(preview.Warnings, "Positions shown before preset scripts; scripts were not executed")
		}
		return preview, nil
	}
	return MatchWorldbook(config, request)
}

// ApplyWorldbook inserts matched lore into a message base. Callers that need
// preset processing should use CompileWorldbook so insertion happens first.
func ApplyWorldbook(config *dto.WorldbookConfig, source, base *dto.GeneralOpenAIRequest, trace PresetCompileTrace) (*dto.GeneralOpenAIRequest, PresetCompileTrace, error) {
	final := make([]presetMessage, len(base.Messages))
	for index, message := range base.Messages {
		final[index] = presetMessage{message: message, source: "chat_history", historyID: strconv.Itoa(index)}
	}
	preview, err := matchWorldbookWith(config, source, worldbookMatchOptions{
		available: func(mode string, depth int) bool {
			placement, _ := worldbookGroupPlacement(config, mode, depth, final, -1)
			return !placement.Fallback
		},
	})
	if err != nil {
		return nil, trace, newResponseStageError(FailureStageWorldbook, err)
	}
	return applyWorldbookPreview(config, preview, base, trace)
}

func applyWorldbookPreview(config *dto.WorldbookConfig, preview WorldbookPreview, base *dto.GeneralOpenAIRequest, trace PresetCompileTrace) (*dto.GeneralOpenAIRequest, PresetCompileTrace, error) {
	if preview.Text != "" {
		compiled, err := common.DeepCopy(base)
		if err != nil {
			return nil, trace, err
		}
		messages := make([]presetMessage, len(compiled.Messages))
		for index, message := range compiled.Messages {
			messages[index] = presetMessage{message: message, source: "chat_history", historyID: strconv.Itoa(index)}
		}
		messages, err = injectWorldbookGroups(config, &preview, messages, -1, nil)
		if err != nil {
			return nil, trace, err
		}
		compiled.Messages = make([]dto.Message, len(messages))
		for index, message := range messages {
			compiled.Messages[index] = message.message
		}
		base = compiled
	}
	if config != nil {
		trace.Worldbook = &WorldbookPreview{Text: preview.Text, Entries: preview.Entries, Warnings: preview.Warnings, Placement: preview.Placement}
	}
	return base, trace, nil
}

// Resolve every group against the same assembled history, then account for
// inserted blocks when reporting actual message indices. Matching and budgets
// have already run once; placement never scans or selects entries again.
func injectWorldbookGroups(config *dto.WorldbookConfig, preview *WorldbookPreview, final []presetMessage, markerIndex int, macro *presetMacroContext) ([]presetMessage, error) {
	messages := make([]dto.Message, len(final))
	for index, item := range final {
		messages[index] = item.message
	}
	global := resolveWorldbookPlacement(config, messages)
	globalInherit := config.InjectionMode == "" || config.InjectionMode == dto.WorldbookInjectionInherit
	if globalInherit && markerIndex >= 0 {
		global = dto.WorldbookPlacement{Mode: dto.WorldbookInjectionInherit, Index: markerIndex, Role: messages[markerIndex].Role}
	} else {
		global.Fallback = global.Fallback || globalInherit
	}
	type insertion struct {
		placement dto.WorldbookPlacement
		group     worldbookInjectionGroup
		marker    bool
	}
	insertions := make([]insertion, 0, len(preview.groups))
	for _, group := range preview.groups {
		placement, marker := worldbookGroupPlacement(config, group.mode, group.depth, final, markerIndex)
		if placement.Fallback {
			if config.PlacementFallback == "fail" {
				return nil, fmt.Errorf("worldbook insertion position is unavailable")
			}
			if config.PlacementFallback == "skip" {
				for _, index := range group.decisions {
					decision := &preview.Entries[index]
					required := slices.ContainsFunc(config.Books, func(book dto.Worldbook) bool {
						return book.ID == decision.BookID && book.Required
					})
					if required {
						return nil, fmt.Errorf("required worldbook insertion position is unavailable")
					}
					decision.Status, decision.Reason, decision.Selected = "skipped", "position_unavailable", false
					decision.Placement = nil
				}
				continue
			}
			preview.Warnings = append(preview.Warnings, "Worldbook insertion position unavailable; background placed before the conversation")
		}
		if marker {
			if config.SemanticsVersion >= 2 {
				for index, decision := range group.decisions {
					insertions = append(insertions, insertion{placement: placement, marker: true, group: worldbookInjectionGroup{text: group.contents[index], decisions: []int{decision}}})
				}
			} else {
				insertions = append(insertions, insertion{placement: placement, group: group, marker: true})
			}
		} else {
			for index, decision := range group.decisions {
				insertions = append(insertions, insertion{placement: placement, group: worldbookInjectionGroup{text: group.contents[index], decisions: []int{decision}}})
			}
		}
	}
	slices.SortStableFunc(insertions, func(a, b insertion) int {
		if a.placement.Index != b.placement.Index {
			return cmp.Compare(a.placement.Index, b.placement.Index)
		}
		if a.marker != b.marker {
			if a.marker {
				return 1
			}
			return -1
		}
		return cmp.Compare(preview.Entries[a.group.decisions[0]].Order, preview.Entries[b.group.decisions[0]].Order)
	})
	offset := 0
	globalIndex := global.Index
	for start := 0; start < len(insertions); {
		item := insertions[start]
		placement := item.placement
		placement.Index += offset
		end := start + 1
		if item.marker && config.SemanticsVersion >= 2 {
			texts := []string{item.group.text}
			for end < len(insertions) && insertions[end].marker && insertions[end].placement.Index == item.placement.Index {
				texts = append(texts, insertions[end].group.text)
				end++
			}
			previous, _ := final[placement.Index].message.Content.(string)
			final[placement.Index].message.Content = strings.TrimSpace(previous + "\n\n" + strings.Join(texts, "\n\n"))
		} else if !item.marker {
			texts := []string{item.group.text}
			for end < len(insertions) && !insertions[end].marker && insertions[end].placement.Index == item.placement.Index {
				texts = append(texts, insertions[end].group.text)
				end++
			}
			text := strings.Join(texts, "\n\n")
			if macro != nil && !preview.expanded {
				text = macro.expand(text, 0)
				if macro.warnings["macro expansion limit reached"] || len(text) > 16<<20 {
					return nil, fmt.Errorf("worldbook macro expansion exceeded its limit")
				}
			}
			final = slices.Insert(final, placement.Index, presetMessage{message: dto.Message{Role: "system", Content: text}, source: "worldbook"})
			offset++
			if item.placement.Index < globalIndex || global.Mode == dto.WorldbookInjectionInherit && item.placement.Index == globalIndex {
				global.Index++
			}
		}
		for _, grouped := range insertions[start:end] {
			for _, index := range grouped.group.decisions {
				actual := grouped.placement
				actual.Index = placement.Index
				preview.Entries[index].Placement = &actual
				preview.Entries[index].Inserted = true
				final[placement.Index].worldbookEntries = append(final[placement.Index].worldbookEntries, index)
			}
		}
		start = end
	}
	// The combined legacy placement continues to describe the global setting.
	preview.Placement = &global
	if config.PlacementFallback == "skip" {
		var inserted []string
		for _, item := range insertions {
			inserted = append(inserted, item.group.text)
		}
		preview.Text = strings.Join(inserted, "\n\n")
	}
	return final, nil
}

// Matching, budget admission and insertion use this same physical anchor.
func worldbookGroupPlacement(config *dto.WorldbookConfig, mode string, depth int, final []presetMessage, legacyMarker int) (dto.WorldbookPlacement, bool) {
	if mode == "" {
		mode = dto.WorldbookInjectionInherit
	}
	messages := make([]dto.Message, len(final))
	for index, item := range final {
		messages[index] = item.message
	}
	placement := resolveWorldbookPlacement(&dto.WorldbookConfig{InjectionMode: mode, InjectionDepth: depth}, messages)
	markerIndex := legacyMarker
	isMarkerMode := mode == dto.WorldbookInjectionInherit || mode == dto.WorldbookInjectionBeforeDefinition || mode == dto.WorldbookInjectionAfterDefinition
	if config.SemanticsVersion >= 2 {
		if !isMarkerMode {
			placement := resolveWorldbookHistoryPlacement(mode, depth, final)
			if mode == dto.WorldbookInjectionAtDepth && config.PlacementFallback != "" {
				historyCount := 0
				for _, item := range final {
					if item.source == "chat_history" && item.historyID != "" && worldbookScanMessageRole(item.message.Role) {
						historyCount++
					}
				}
				if depth > historyCount {
					placement = dto.WorldbookPlacement{Mode: dto.WorldbookInjectionSystemPrelude, Role: "system", Index: worldbookSystemPreludeIndex(messages), Fallback: true}
				}
			}
			return placement, false
		}
		desired := "worldInfoBefore"
		if mode == dto.WorldbookInjectionAfterDefinition {
			desired = "worldInfoAfter"
		}
		markerIndex = slices.IndexFunc(final, func(item presetMessage) bool { return item.id == desired })
		if markerIndex < 0 && mode == dto.WorldbookInjectionInherit {
			markerIndex = slices.IndexFunc(final, func(item presetMessage) bool { return item.id == "worldInfoAfter" })
		}
	}
	if isMarkerMode && markerIndex >= 0 {
		return dto.WorldbookPlacement{Mode: mode, Index: markerIndex, Role: messages[markerIndex].Role}, true
	}
	placement.Fallback = placement.Fallback || isMarkerMode
	if isMarkerMode {
		placement.Mode = dto.WorldbookInjectionSystemPrelude
	}
	return placement, false
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

// Source history IDs exist only in the assembler. Preset-created user/system
// messages never change the meaning of a conversation depth in v2.
func resolveWorldbookHistoryPlacement(mode string, depth int, final []presetMessage) dto.WorldbookPlacement {
	messages := make([]dto.Message, len(final))
	indices := []int{}
	for index, item := range final {
		messages[index] = item.message
		if item.source == "chat_history" && item.historyID != "" && worldbookScanMessageRole(item.message.Role) {
			indices = append(indices, index)
		}
	}
	placement := dto.WorldbookPlacement{Mode: mode, Role: "system", Index: worldbookSystemPreludeIndex(messages)}
	if mode == dto.WorldbookInjectionBeforeLastUser {
		found := false
		for i := len(indices) - 1; i >= 0; i-- {
			if messages[indices[i]].Role == "user" {
				placement.Index = indices[i]
				found = true
				break
			}
		}
		if !found {
			placement.Fallback = true
			placement.Mode = dto.WorldbookInjectionSystemPrelude
		}
	} else if mode == dto.WorldbookInjectionAtDepth {
		if len(indices) == 0 {
			placement.Fallback = true
			placement.Mode = dto.WorldbookInjectionSystemPrelude
		} else if depth == 0 {
			placement.Index = indices[len(indices)-1] + 1
		} else {
			placement.Index = indices[max(0, len(indices)-depth)]
		}
	}
	for placement.Index > 0 && placement.Index < len(messages) && (messages[placement.Index].Role == "tool" || messages[placement.Index].Role == "function") {
		placement.Index--
	}
	return placement
}
