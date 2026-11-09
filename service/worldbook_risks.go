package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
)

func worldbookRiskFingerprint(entry dto.WorldbookEntry, rule dto.WorldbookNormalizedRule) string {
	var source map[string]any
	_ = common.UnmarshalPreservingNumbers(entry.Source, &source)
	var value any = source
	for part := range strings.SplitSeq(rule.Field, ".") {
		object, ok := value.(map[string]any)
		if !ok {
			value = nil
			break
		}
		value = object[part]
	}
	encoded, _ := common.Marshal([]any{rule.Field, rule.Detail, value})
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func worldbookRisksAccepted(entry dto.WorldbookEntry, configs ...*dto.WorldbookConfig) bool {
	rules := entry.NormalizedRules
	// Source cannot be edited through advanced JSON to bypass the server gate.
	var source map[string]json.RawMessage
	if len(entry.Source) > 0 && common.Unmarshal(entry.Source, &source) == nil {
		for _, rule := range worldbookImportedEntryNormalization(source) {
			if rule.Action == "risk" {
				found := false
				for _, existing := range rules {
					if existing.Field == rule.Field && existing.Detail == rule.Detail {
						found = true
					}
				}
				if !found {
					rules = append(rules, rule)
				}
			}
		}
	}
	for _, rule := range rules {
		if rule.Action != "risk" {
			continue
		}
		mode, depth, version := entry.InjectionMode, entry.InjectionDepth, 2
		if len(configs) > 0 && configs[0] != nil {
			config := configs[0]
			version = max(1, config.SemanticsVersion)
			if mode == "" || mode == dto.WorldbookInjectionFollowGlobal {
				mode, depth = config.InjectionMode, config.InjectionDepth
			}
		}
		if mode != dto.WorldbookInjectionAtDepth {
			depth = 0
		}
		key := fmt.Sprintf("%s|%s|%d|%d", worldbookRiskFingerprint(entry, rule), mode, depth, version)
		if entry.AcceptedRisks[rule.Field] != key {
			return false
		}
	}
	return true
}
