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
	var bookRestrictions []string
	for field, value := range root {
		switch field {
		case "id", "name", "description", "enabled", "entries", "warnings":
		case "extensions":
			var extensions map[string]json.RawMessage
			if err := common.Unmarshal(value, &extensions); err != nil {
				bookRestrictions = append(bookRestrictions, "book.extensions")
				continue
			}
			for key, setting := range extensions {
				if worldbookBehaviorActive(setting) {
					bookRestrictions = append(bookRestrictions, "book.extensions."+key)
				}
			}
		default:
			if worldbookBehaviorActive(value) {
				bookRestrictions = append(bookRestrictions, "book."+field)
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
		book.Warnings = append(book.Warnings, "Imported placement is normalized to one background insertion; advanced SillyTavern behavior is not supported")
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
			var selective bool
			_ = common.Unmarshal(fields["selective"], &selective)
			for _, field := range []string{"constant", "enabled", "disable", "caseSensitive", "case_sensitive", "matchWholeWords", "whole_words", "selective"} {
				if value := fields[field]; value != nil && string(value) != "null" {
					var flag bool
					if err := common.Unmarshal(value, &flag); err != nil {
						entry.Unsupported = append(entry.Unsupported, field)
					}
				}
			}
			for _, field := range []string{"order", "insertion_order", "selectiveLogic"} {
				if value := fields[field]; value != nil && string(value) != "null" {
					var number int
					if err := common.Unmarshal(value, &number); err != nil {
						entry.Unsupported = append(entry.Unsupported, field)
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
					entry.Unsupported = append(entry.Unsupported, "selectiveLogic")
				}
			}
			for _, key := range append(slices.Clone(entry.Keys), entry.SecondaryKeys...) {
				if _, err := dto.CompileWorldbookKey(key, entry.CaseSensitive); err != nil {
					entry.Unsupported = append(entry.Unsupported, "regex")
					break
				}
			}
			slices.Sort(entry.Unsupported)
			entry.Unsupported = slices.Compact(entry.Unsupported)
			if len(entry.Unsupported) > 0 {
				book.Warnings = append(book.Warnings, fmt.Sprintf("Entry %s is blocked by unsupported rules: %s", entry.ID, strings.Join(entry.Unsupported, ", ")))
			}
			book.Entries = append(book.Entries, entry)
		}
	}
	// ST entry identifiers are only unique inside their source book. Give
	// each imported identity a new request-wide ID, retaining duplicate IDs
	// within the same source so normal deduplication still applies.
	identities := map[string]string{}
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
		restrictions := worldbookImportedEntryRestrictions(fields)
		if canonical {
			// A canonical DTO does not interpret ST aliases. Keep any active
			// aliases blocking rather than silently weakening their conditions.
			for _, alias := range []string{"key", "keysecondary", "disable", "caseSensitive", "matchWholeWords", "order", "insertion_order", "selective", "selectiveLogic"} {
				if worldbookBehaviorActive(fields[alias]) {
					restrictions = append(restrictions, alias)
				}
			}
		}
		book.Entries[i].Unsupported = append(book.Entries[i].Unsupported, restrictions...)
		if len(restrictions) > 0 {
			if len(book.Entries[i].Source) == 0 {
				book.Entries[i].Source = entries[i]
			}
			slices.Sort(restrictions)
			book.Warnings = append(book.Warnings, fmt.Sprintf("Entry %s is blocked by unsupported rules: %s", book.Entries[i].ID, strings.Join(restrictions, ", ")))
		}
		if book.Entries[i].Keys == nil {
			book.Entries[i].Keys = []string{}
		}
		if book.Entries[i].SecondaryKeys == nil {
			book.Entries[i].SecondaryKeys = []string{}
		}
		book.Entries[i].Unsupported = append(book.Entries[i].Unsupported, bookRestrictions...)
		slices.Sort(book.Entries[i].Unsupported)
		book.Entries[i].Unsupported = slices.Compact(book.Entries[i].Unsupported)
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
		slices.Sort(bookRestrictions)
		book.Warnings = append(book.Warnings, "Book is blocked by unsupported rules: "+strings.Join(bookRestrictions, ", "))
	}
	config := dto.WorldbookConfig{Books: []dto.Worldbook{book}}
	if err := config.Validate(); err != nil {
		return book, err
	}
	return book, nil
}

// Inspect active fields on both canonical and ST entries. The optional source
// is inspection data, so a user who explicitly simplified an entry is not
// reblocked merely because its original source retained unsupported fields.
func worldbookImportedEntryRestrictions(fields map[string]json.RawMessage) []string {
	var restrictions []string
	for key, value := range fields {
		switch key {
		case "uid", "id", "name", "comment", "content", "key", "keys", "keysecondary", "secondary_keys", "constant", "enabled", "disable", "caseSensitive", "case_sensitive", "matchWholeWords", "whole_words", "order", "insertion_order", "selective", "selectiveLogic", "position", "displayIndex", "addMemo", "memo", "priority", "secondary_logic", "unsupported", "source":
		case "extensions":
			var extensions map[string]json.RawMessage
			if err := common.Unmarshal(value, &extensions); err != nil {
				restrictions = append(restrictions, key)
				continue
			}
			for extension, setting := range extensions {
				switch extension {
				case "position", "depth", "display_index":
					continue
				}
				if worldbookBehaviorActive(setting) {
					restrictions = append(restrictions, "extensions."+extension)
				}
			}
		case "probability":
			var probability float64
			if err := common.Unmarshal(value, &probability); err != nil || probability < 100 {
				restrictions = append(restrictions, key)
			}
		case "useProbability":
		default:
			if worldbookBehaviorActive(value) {
				restrictions = append(restrictions, key)
			}
		}
	}
	return restrictions
}

func worldbookBehaviorActive(value json.RawMessage) bool {
	text := strings.TrimSpace(string(value))
	return text != "" && text != "null" && text != "false" && text != "0" && text != "0.0" && text != "\"\"" && text != "[]" && text != "{}"
}
