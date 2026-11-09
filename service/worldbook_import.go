package service

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
)

// ImportWorldbook treats all imported fields as data. Unsupported behavioral
// restrictions remain blocking and their original JSON is kept for inspection.
func ImportWorldbook(name string, data []byte) (dto.Worldbook, error) {
	book := dto.Worldbook{ID: common.GetUUID(), Name: strings.TrimSpace(name), Enabled: true, Entries: []dto.WorldbookEntry{}}
	if len(data) > dto.MaxWorldbookBytes {
		return book, fmt.Errorf("worldbook exceeds 6 MiB")
	}
	var root map[string]json.RawMessage
	if err := common.Unmarshal(data, &root); err != nil {
		return book, fmt.Errorf("invalid worldbook JSON")
	}
	if nested, ok := root["character_book"]; ok {
		if err := common.Unmarshal(nested, &root); err != nil {
			return book, fmt.Errorf("invalid character book")
		}
	}
	if book.Name == "" {
		_ = common.Unmarshal(root["name"], &book.Name)
	}
	if book.Name == "" {
		book.Name = "Worldbook"
	}
	if value := root["enabled"]; value != nil {
		if err := common.Unmarshal(value, &book.Enabled); err != nil {
			return book, fmt.Errorf("invalid worldbook enabled flag")
		}
	}
	var bookRestrictions []dto.WorldbookNormalizedRule
	for field, value := range root {
		switch field {
		case "id", "name", "description", "enabled", "entries", "warnings":
		case "extensions":
			var extensions map[string]json.RawMessage
			if err := common.Unmarshal(value, &extensions); err != nil {
				bookRestrictions = append(bookRestrictions, worldbookIgnoredRule("book.extensions", "invalid book extensions were ignored"))
				continue
			}
			for key, setting := range extensions {
				if worldbookBehaviorActive(setting) {
					bookRestrictions = append(bookRestrictions, worldbookIgnoredRule("book.extensions."+key, "book extensions are not used by the gateway"))
				}
			}
		default:
			if worldbookBehaviorActive(value) {
				bookRestrictions = append(bookRestrictions, worldbookIgnoredRule("book."+field, "book-level override is not used by the gateway"))
			}
		}
	}
	rawEntries, ok := root["entries"]
	if !ok {
		return book, fmt.Errorf("worldbook requires entries")
	}
	var entries []json.RawMessage
	if strings.HasPrefix(strings.TrimSpace(string(rawEntries)), "[") {
		if err := common.Unmarshal(rawEntries, &entries); err != nil {
			return book, fmt.Errorf("invalid worldbook entries")
		}
	} else {
		var object map[string]json.RawMessage
		if err := common.Unmarshal(rawEntries, &object); err != nil {
			return book, fmt.Errorf("invalid worldbook entries")
		}
		keys := make([]string, 0, len(object))
		for key := range object {
			keys = append(keys, key)
		}
		slices.SortFunc(keys, func(a, b string) int {
			ai, ae := strconv.Atoi(a)
			bi, be := strconv.Atoi(b)
			if ae == nil && be == nil {
				return ai - bi
			}
			return strings.Compare(a, b)
		})
		for _, key := range keys {
			entries = append(entries, object[key])
		}
	}
	if len(entries) > 1000 {
		return book, fmt.Errorf("worldbook supports at most 1000 entries")
	}
	canonical := root["id"] != nil && root["enabled"] != nil
	if canonical {
		if err := common.Unmarshal(rawEntries, &book.Entries); err != nil {
			return book, fmt.Errorf("invalid canonical worldbook")
		}
		if warnings := root["warnings"]; warnings != nil {
			_ = common.Unmarshal(warnings, &book.Warnings)
		}
	} else {
		book.Warnings = append(book.Warnings, "Supported insertion positions are imported; unsupported behavior requires review")
		for i, raw := range entries {
			var fields map[string]json.RawMessage
			if err := common.Unmarshal(raw, &fields); err != nil || fields == nil {
				return book, fmt.Errorf("invalid worldbook entry")
			}
			entry := dto.WorldbookEntry{ID: strconv.Itoa(i), Enabled: true, SecondaryLogic: "none", Keys: []string{}, SecondaryKeys: []string{}, Source: raw}
			if id := fields["uid"]; id != nil {
				entry.ID = strings.Trim(string(id), "\"")
			}
			if id := fields["id"]; id != nil {
				entry.ID = strings.Trim(string(id), "\"")
			}
			_ = common.Unmarshal(fields["comment"], &entry.Name)
			if entry.Name == "" {
				_ = common.Unmarshal(fields["name"], &entry.Name)
			}
			_ = common.Unmarshal(fields["content"], &entry.Content)
			if value := fields["injection_mode"]; value != nil {
				if err := common.Unmarshal(value, &entry.InjectionMode); err != nil {
					return book, fmt.Errorf("invalid worldbook entry injection mode")
				}
			}
			if value := fields["injection_depth"]; value != nil {
				if err := common.Unmarshal(value, &entry.InjectionDepth); err != nil {
					return book, fmt.Errorf("invalid worldbook entry injection depth")
				}
			}
			_ = common.Unmarshal(fields["key"], &entry.Keys)
			if fields["keys"] != nil {
				_ = common.Unmarshal(fields["keys"], &entry.Keys)
			}
			_ = common.Unmarshal(fields["keysecondary"], &entry.SecondaryKeys)
			if fields["secondary_keys"] != nil {
				_ = common.Unmarshal(fields["secondary_keys"], &entry.SecondaryKeys)
			}
			_ = common.Unmarshal(fields["constant"], &entry.Constant)
			_ = common.Unmarshal(fields["enabled"], &entry.Enabled)
			var disabled bool
			_ = common.Unmarshal(fields["disable"], &disabled)
			entry.Enabled = entry.Enabled && !disabled
			_ = common.Unmarshal(fields["caseSensitive"], &entry.CaseSensitive)
			if fields["case_sensitive"] != nil {
				_ = common.Unmarshal(fields["case_sensitive"], &entry.CaseSensitive)
			}
			_ = common.Unmarshal(fields["matchWholeWords"], &entry.WholeWords)
			if fields["whole_words"] != nil {
				_ = common.Unmarshal(fields["whole_words"], &entry.WholeWords)
			}
			_ = common.Unmarshal(fields["order"], &entry.Priority)
			if fields["insertion_order"] != nil {
				_ = common.Unmarshal(fields["insertion_order"], &entry.Priority)
			}
			if value := fields["preventRecursion"]; value != nil && string(value) != "null" {
				var preventRecursion bool
				if err := common.Unmarshal(value, &preventRecursion); err != nil {
					entry.Issues = append(entry.Issues, "preventRecursion")
				} else {
					entry.PreventRecursion = preventRecursion
				}
			}
			var selective bool
			_ = common.Unmarshal(fields["selective"], &selective)
			for _, field := range []string{"constant", "enabled", "disable", "caseSensitive", "case_sensitive", "matchWholeWords", "whole_words", "selective"} {
				if value := fields[field]; value != nil && string(value) != "null" {
					var flag bool
					if err := common.Unmarshal(value, &flag); err != nil {
						entry.Issues = append(entry.Issues, field)
					}
				}
			}
			for _, field := range []string{"order", "insertion_order", "selectiveLogic"} {
				if value := fields[field]; value != nil && string(value) != "null" {
					var number int
					if err := common.Unmarshal(value, &number); err != nil {
						entry.Issues = append(entry.Issues, field)
					}
				}
			}
			if selective {
				var logic int
				_ = common.Unmarshal(fields["selectiveLogic"], &logic)
				switch logic {
				case 0:
					entry.SecondaryLogic = "any"
				case 1:
					entry.SecondaryLogic = "not_all"
				case 2:
					entry.SecondaryLogic = "not_any"
				case 3:
					entry.SecondaryLogic = "all"
				default:
					entry.Issues = append(entry.Issues, "selectiveLogic")
				}
			}
			for _, key := range append(slices.Clone(entry.Keys), entry.SecondaryKeys...) {
				if _, err := dto.CompileWorldbookKey(key, entry.CaseSensitive); err != nil {
					entry.Issues = append(entry.Issues, "regex")
					break
				}
			}
			entry.Issues = compactWorldbookStrings(entry.Issues)
			if len(entry.Issues) > 0 {
				book.Warnings = append(book.Warnings, fmt.Sprintf("Entry %s has invalid data: %s", entry.ID, strings.Join(entry.Issues, ", ")))
			}
			book.Entries = append(book.Entries, entry)
		}
	}
	// ST entry identifiers are only unique inside their source book. Give
	// each imported identity a new request-wide ID, retaining duplicate IDs
	// within the same source so normal deduplication still applies.
	identities := map[string]string{}
	riskyEntries := 0
	for i := range book.Entries {
		var fields map[string]json.RawMessage
		if err := common.Unmarshal(entries[i], &fields); err != nil || fields == nil {
			return book, fmt.Errorf("invalid worldbook entry")
		}
		for _, field := range []string{"key", "keys", "keysecondary", "secondary_keys"} {
			value := fields[field]
			if value == nil || strings.TrimSpace(string(value)) == "null" {
				continue
			}
			var keys []json.RawMessage
			if err := common.Unmarshal(value, &keys); err != nil {
				return book, fmt.Errorf("invalid worldbook key list: %s", field)
			}
			for _, key := range keys {
				if !strings.HasPrefix(strings.TrimSpace(string(key)), "\"") {
					return book, fmt.Errorf("invalid worldbook key list: %s", field)
				}
			}
		}
		entry := &book.Entries[i]
		sourceFields := fields
		if canonical && len(entry.Source) > 0 {
			if common.Unmarshal(entry.Source, &sourceFields) != nil || sourceFields == nil {
				return book, fmt.Errorf("invalid canonical worldbook source")
			}
		} else if !canonical {
			mapWorldbookImportedPlacement(fields, entry)
		}
		if len(entry.Source) == 0 {
			entry.Source = entries[i]
		}
		normalizedRules := worldbookImportedEntryNormalization(sourceFields)
		for index := range normalizedRules {
			if normalizedRules[index].Action == "risk" {
				normalizedRules[index].Fingerprint = worldbookRiskFingerprint(book.Entries[i], normalizedRules[index])
			}
		}
		if canonical {
			for _, alias := range []string{"key", "keysecondary", "disable", "caseSensitive", "matchWholeWords", "order", "insertion_order", "selective", "selectiveLogic"} {
				if worldbookBehaviorActive(fields[alias]) {
					normalizedRules = append(normalizedRules, worldbookIgnoredRule(alias, "canonical entry repeats a SillyTavern alias"))
				}
			}
		}
		// Refresh from preserved source instead of retaining old, missing
		// fingerprints or erased confirmation records. Edited supported fields
		// and insertion choices are never overwritten by a review refresh.
		book.Entries[i].NormalizedRules = normalizedRules
		if len(normalizedRules) > 0 {
			if len(book.Entries[i].Source) == 0 {
				book.Entries[i].Source = entries[i]
			}
			fields := make([]string, 0, len(normalizedRules))
			for _, rule := range normalizedRules {
				fields = append(fields, rule.Field)
			}
			book.Warnings = append(book.Warnings, fmt.Sprintf("Entry %s was normalized: %s", book.Entries[i].ID, strings.Join(compactWorldbookStrings(fields), ", ")))
		}
		if riskFields := worldbookRiskFields(normalizedRules); len(riskFields) > 0 {
			// Behavior that cannot be reproduced changes when or how often an
			// entry triggers, so it is never enabled silently. The user
			// reviews and confirms the simplification once per entry.
			book.Entries[i].Enabled = false
			riskyEntries++
			if len(book.Entries[i].Source) == 0 {
				book.Entries[i].Source = entries[i]
			}
			book.Warnings = append(book.Warnings, fmt.Sprintf("Entry %s needs review before it can run: %s", book.Entries[i].ID, strings.Join(compactWorldbookStrings(riskFields), ", ")))
		}
		if book.Entries[i].Keys == nil {
			book.Entries[i].Keys = []string{}
		}
		if book.Entries[i].SecondaryKeys == nil {
			book.Entries[i].SecondaryKeys = []string{}
		}
		book.Entries[i].NormalizedRules = append(book.Entries[i].NormalizedRules, bookRestrictions...)
		book.Entries[i].NormalizedRules = compactWorldbookNormalizedRules(book.Entries[i].NormalizedRules)
		book.Entries[i].Issues = compactWorldbookStrings(book.Entries[i].Issues)
		original := book.Entries[i].ID
		if original == "" {
			original = strconv.Itoa(i)
		}
		if identities[original] == "" {
			identities[original] = common.GetUUID()
		}
		book.Entries[i].ID = identities[original]
	}
	if book.Entries == nil {
		book.Entries = []dto.WorldbookEntry{}
	}
	if len(bookRestrictions) > 0 {
		fields := make([]string, 0, len(bookRestrictions))
		for _, rule := range bookRestrictions {
			fields = append(fields, rule.Field)
		}
		book.Warnings = append(book.Warnings, "Book was normalized: "+strings.Join(compactWorldbookStrings(fields), ", "))
	}
	if riskyEntries > 0 {
		book.Warnings = append(book.Warnings, fmt.Sprintf("%d imported entries need review before they can run", riskyEntries))
	}
	config := dto.WorldbookConfig{Books: []dto.Worldbook{book}}
	if err := config.Validate(); err != nil {
		return book, err
	}
	return book, nil
}

// Inspect active fields on both canonical and ST entries. Gateway-only
// normalization keeps imported entries runnable while making every ignored
// behavior visible in the editor.
func worldbookImportedEntryNormalization(fields map[string]json.RawMessage) []dto.WorldbookNormalizedRule {
	var rules []dto.WorldbookNormalizedRule
	for key, value := range fields {
		switch key {
		case "uid", "id", "name", "comment", "content", "key", "keys", "keysecondary", "secondary_keys", "constant", "enabled", "disable", "caseSensitive", "case_sensitive", "matchWholeWords", "whole_words", "order", "insertion_order", "selective", "selectiveLogic", "priority", "secondary_logic", "normalized_rules", "issues", "prevent_recursion", "source", "injection_mode", "injection_depth":
		case "extensions":
			var extensions map[string]json.RawMessage
			if err := common.Unmarshal(value, &extensions); err != nil {
				rules = append(rules, worldbookIgnoredRule(key, "invalid extensions were ignored"))
				continue
			}
			for extension, setting := range extensions {
				switch extension {
				case "display_index":
					continue
				case "position", "depth":
					if !worldbookSupportedPlacement(fields) {
						rules = append(rules, worldbookRiskRule("extensions."+extension, "the original Tavern placement cannot be reproduced; choose an insertion position instead"))
					}
					continue
				}
				if worldbookBehaviorActive(setting) {
					rules = append(rules, worldbookIgnoredRule("extensions."+extension, "extension behavior is not used by the gateway"))
				}
			}
		case "probability":
			if worldbookProbabilityChangesActivation(value) {
				rules = append(rules, worldbookRiskRule(key, "random activation cannot be reproduced; matching always includes the entry"))
			}
		case "useProbability":
			if worldbookBehaviorActive(value) {
				rules = append(rules, worldbookIgnoredRule(key, "probability is treated as always eligible after matching"))
			}
		case "characterFilter":
			if worldbookCharacterFilterActive(value) {
				rules = append(rules, worldbookRiskRule(key, "character or scenario filtering cannot be reproduced; the entry would match every request"))
			}
		case "group", "groupWeight", "groupScoring":
			if worldbookBehaviorActive(value) {
				rules = append(rules, worldbookRiskRule(key, "entry grouping cannot be reproduced"))
			}
		case "sticky", "cooldown", "delay":
			if worldbookBehaviorActive(value) {
				rules = append(rules, worldbookRiskRule(key, "activation timing cannot be reproduced; the entry would trigger on every keyword match"))
			}
		case "role":
			if worldbookBehaviorActive(value) && string(value) != `"system"` && string(value) != "0" {
				rules = append(rules, worldbookRiskRule(key, "per-entry role override cannot be reproduced"))
			}
		case "displayIndex":
			continue
		case "position", "depth":
			if !worldbookSupportedPlacement(fields) {
				rules = append(rules, worldbookRiskRule(key, "the original Tavern placement cannot be reproduced; choose an insertion position instead"))
			}
		case "addMemo", "memo":
			if worldbookBehaviorActive(value) {
				rules = append(rules, worldbookIgnoredRule(key, "memo data is not used by the gateway"))
			}
		case "unsupported":
			if worldbookBehaviorActive(value) {
				rules = append(rules, worldbookIgnoredRule(key, "legacy unsupported metadata no longer blocks execution"))
			}
		default:
			if worldbookBehaviorActive(value) {
				rules = append(rules, worldbookIgnoredRule(key, "unknown imported behavior is ignored"))
			}
		}
	}
	return compactWorldbookNormalizedRules(rules)
}

func worldbookIgnoredRule(field, detail string) dto.WorldbookNormalizedRule {
	return dto.WorldbookNormalizedRule{Field: field, Action: "ignored", Detail: detail}
}

func mapWorldbookImportedPlacement(fields map[string]json.RawMessage, entry *dto.WorldbookEntry) {
	var extensions map[string]json.RawMessage
	_ = common.Unmarshal(fields["extensions"], &extensions)
	position := fields["position"]
	if position == nil {
		position = extensions["position"]
	}
	var name string
	if common.Unmarshal(position, &name) == nil {
		switch name {
		case "before_char":
			entry.InjectionMode = dto.WorldbookInjectionBeforeDefinition
		case "after_char":
			entry.InjectionMode = dto.WorldbookInjectionAfterDefinition
		}
		return
	}
	var code int
	if position == nil || common.Unmarshal(position, &code) != nil {
		return
	}
	switch code {
	case 0:
		entry.InjectionMode = dto.WorldbookInjectionBeforeDefinition
	case 1:
		entry.InjectionMode = dto.WorldbookInjectionAfterDefinition
	case 4:
		entry.InjectionMode = dto.WorldbookInjectionAtDepth
		depth := fields["depth"]
		if depth == nil {
			depth = extensions["depth"]
		}
		_ = common.Unmarshal(depth, &entry.InjectionDepth)
	}
}

func worldbookSupportedPlacement(fields map[string]json.RawMessage) bool {
	entry := dto.WorldbookEntry{}
	mapWorldbookImportedPlacement(fields, &entry)
	var extensions map[string]json.RawMessage
	_ = common.Unmarshal(fields["extensions"], &extensions)
	if entry.InjectionMode == "" {
		return fields["position"] == nil && extensions["position"] == nil
	}
	if fields["position"] != nil && extensions["position"] != nil && !samePresetJSON(fields["position"], extensions["position"]) {
		return false
	}
	if entry.InjectionMode == dto.WorldbookInjectionAtDepth && fields["depth"] != nil && extensions["depth"] != nil && !samePresetJSON(fields["depth"], extensions["depth"]) {
		return false
	}
	if entry.InjectionMode != dto.WorldbookInjectionAtDepth {
		return true
	}
	depth := fields["depth"]
	if depth == nil {
		depth = extensions["depth"]
	}
	if depth != nil && common.Unmarshal(depth, &entry.InjectionDepth) != nil {
		return false
	}
	return entry.InjectionDepth >= 0 && entry.InjectionDepth <= 20
}

func worldbookRiskRule(field, detail string) dto.WorldbookNormalizedRule {
	return dto.WorldbookNormalizedRule{Field: field, Action: "risk", Detail: detail}
}

func worldbookRiskFields(rules []dto.WorldbookNormalizedRule) []string {
	fields := make([]string, 0, len(rules))
	for _, rule := range rules {
		if rule.Action == "risk" {
			fields = append(fields, rule.Field)
		}
	}
	return fields
}

// worldbookProbabilityChangesActivation reports whether an imported
// probability field would change when the entry triggers. A probability of
// 100 is the SillyTavern default and behaves exactly like always eligible.
func worldbookProbabilityChangesActivation(value json.RawMessage) bool {
	if !worldbookBehaviorActive(value) {
		return false
	}
	var probability float64
	if err := common.Unmarshal(value, &probability); err != nil {
		return true
	}
	return probability < 100
}

// worldbookCharacterFilterActive reports whether a Tavern character filter
// actually restricts the entry. Exports include an inert empty filter on most
// entries, which must not be treated as unsupported behavior.
func worldbookCharacterFilterActive(value json.RawMessage) bool {
	if !worldbookBehaviorActive(value) {
		return false
	}
	var filter struct {
		Names     []string `json:"names"`
		IsExclude bool     `json:"isExclude"`
		Scenario  *string  `json:"scenario"`
	}
	if err := common.Unmarshal(value, &filter); err != nil {
		return true
	}
	if len(filter.Names) > 0 {
		return true
	}
	return filter.Scenario != nil && strings.TrimSpace(*filter.Scenario) != ""
}

func compactWorldbookStrings(values []string) []string {
	slices.Sort(values)
	return slices.Compact(values)
}

func compactWorldbookNormalizedRules(rules []dto.WorldbookNormalizedRule) []dto.WorldbookNormalizedRule {
	slices.SortFunc(rules, func(a, b dto.WorldbookNormalizedRule) int {
		if order := strings.Compare(a.Field, b.Field); order != 0 {
			return order
		}
		if order := strings.Compare(a.Action, b.Action); order != 0 {
			return order
		}
		return strings.Compare(a.Detail, b.Detail)
	})
	return slices.CompactFunc(rules, func(a, b dto.WorldbookNormalizedRule) bool {
		return a == b
	})
}

func worldbookBehaviorActive(value json.RawMessage) bool {
	text := strings.TrimSpace(string(value))
	return text != "" && text != "null" && text != "false" && text != "0" && text != "0.0" && text != "\"\"" && text != "[]" && text != "{}"
}
