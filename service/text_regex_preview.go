package service

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/relaykit/dto"
)

// TextRegexPreview runs the same bounded regex executor as live traffic, but
// only on the caller-provided sample. It never loads or edits a channel.
type TextRegexPreview struct {
	Output string                 `json:"output"`
	Steps  []TextRegexPreviewStep `json:"steps"`
}

type TextRegexPreviewStep struct {
	Name    string `json:"name"`
	Before  string `json:"before"`
	After   string `json:"after"`
	Changed bool   `json:"changed"`
}

func PreviewTextRegex(config *dto.ResponseTextFilter, preset *dto.SillyTavernPresetConfig, model, stage, role string, depth int, input string) (*TextRegexPreview, error) {
	if stage != "send" && stage != "receive" {
		return nil, fmt.Errorf("regex preview stage must be send or receive")
	}
	if !slices.Contains([]string{"user", "assistant", "system", "developer"}, role) || depth < 0 || depth > 100000 {
		return nil, fmt.Errorf("regex preview role or depth is invalid")
	}
	if len(input) > 16<<10 {
		return nil, fmt.Errorf("regex preview input exceeds 16 KiB")
	}
	result := &TextRegexPreview{Output: input, Steps: []TextRegexPreviewStep{}}
	if config != nil && (len(config.Models) == 0 || slices.Contains(config.Models, model)) {
		normalized, err := config.Normalized()
		if err != nil {
			return nil, err
		}
		config = normalized
	} else {
		config = nil
	}
	if stage == "receive" && config != nil && config.Mode != "rules" {
		match := regexp.MustCompile(config.Pattern).FindStringSubmatch(result.Output)
		before := result.Output
		if len(match) >= 2 {
			result.Output = match[1]
			if config.TrimCapture {
				result.Output = strings.TrimSpace(result.Output)
			}
		} else if config.MissingMatch == "empty" {
			result.Output = ""
		}
		result.Steps = append(result.Steps, TextRegexPreviewStep{Name: "extract", Before: before, After: result.Output, Changed: before != result.Output})
	}
	rules, err := compileChannelTextRegex(config, model, stage)
	if err != nil {
		return nil, err
	}
	names := make(map[string]string)
	if config != nil {
		for _, rule := range config.Rules {
			names[rule.ID] = rule.Name
		}
	}
	imported, warnings := compilePresetTextRegex(preset, model, stage == "send")
	if len(warnings) > 0 {
		return nil, fmt.Errorf("preset regex contains unsupported or invalid rules; check preset compatibility warnings")
	}
	if preset != nil && len(imported) > 0 {
		parsed, err := preset.ParseAndValidate()
		if err != nil {
			return nil, err
		}
		for _, script := range parsed.Extensions.RegexScripts {
			names[script.ID] = script.ScriptName
		}
	}
	rules = append(rules, imported...)
	deadline := time.Now().Add(2 * time.Second)
	for _, rule := range rules {
		if stage == "send" && (!slices.Contains(rule.roles, role) || rule.minDepth != nil && *rule.minDepth >= 0 && depth < *rule.minDepth || rule.maxDepth != nil && *rule.maxDepth >= 0 && depth > *rule.maxDepth) {
			continue
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("regex preview execution budget exceeded")
		}
		before := result.Output
		after, err := rule.replace(before)
		if err != nil {
			return nil, fmt.Errorf("regex preview rule %q failed or exceeded execution limits", rule.id)
		}
		name := names[rule.id]
		if name == "" {
			name = rule.id
		}
		result.Steps = append(result.Steps, TextRegexPreviewStep{Name: name, Before: before, After: after, Changed: before != after})
		result.Output = after
	}
	return result, nil
}
