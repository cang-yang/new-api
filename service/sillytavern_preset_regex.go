package service

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/dlclark/regexp2/v2"
)

// presetResponseRegex is the shared bounded executor for both channel rules and
// preset scripts. Callers select explicit text slots and direction.
type presetResponseRegex struct {
	id           string
	pattern      *regexp2.Regexp
	global       bool
	replaceBy    string
	trimStrings  []string
	extract      bool
	missingEmpty bool
	trimCapture  bool
	roles        []string
	minDepth     *int
	maxDepth     *int
}

var presetRegexCapture = regexp.MustCompile(`\$(\d+)|\$<([^>]+)>`)
var presetRegexMatchMacro = regexp.MustCompile(`(?i)\{\{match\}\}`)

// A strict active source must not be weakened by another source's fallback.
// Absent policies preserve legacy behavior instead of silently changing it.
func textRegexRequiresStrictFailure(config *dto.ResponseTextFilter, preset *dto.SillyTavernPresetConfig, model string, send bool) bool {
	if config != nil && (len(config.Models) == 0 || slices.Contains(config.Models, model)) {
		active := !send && config.Mode != "rules"
		for _, rule := range config.Rules {
			if !rule.Disabled && ((send && config.EnableSend && rule.Stage == "send") || (!send && rule.Stage == "receive" && (rule.MinDepth == nil || *rule.MinDepth <= 0))) {
				active = true
			}
		}
		if active && (config.FailurePolicy == "error" || config.FailurePolicy == "" && (config.Mode == "rules" || config.MissingMatch == "empty")) {
			return true
		}
	}
	if preset != nil && (len(preset.Models) == 0 || slices.Contains(preset.Models, model)) && ((send && preset.EnableSendRegex) || (!send && preset.EnableEmbeddedRegex)) {
		rules, warnings := compilePresetTextRegex(preset, model, send)
		return (len(rules) > 0 || len(warnings) > 0) && preset.RegexFailurePolicy != "passthrough"
	}
	return false
}

func compilePresetResponseRegex(config *dto.SillyTavernPresetConfig, model string) ([]presetResponseRegex, []string) {
	return compilePresetTextRegex(config, model, false)
}

func compilePresetTextRegex(config *dto.SillyTavernPresetConfig, model string, send bool) ([]presetResponseRegex, []string) {
	if config == nil || (send && !config.EnableSendRegex) || (!send && !config.EnableEmbeddedRegex) || (len(config.Models) > 0 && !slices.Contains(config.Models, model)) {
		return nil, nil
	}
	preset, err := config.ParseAndValidate()
	if err != nil {
		return nil, []string{err.Error()}
	}
	var compiled []presetResponseRegex
	var warnings []string
	for _, script := range preset.Extensions.RegexScripts {
		enabled := !script.Disabled
		if override, ok := config.RegexOverrides[script.ID]; ok {
			enabled = override
		}
		if !enabled || script.FindRegex == "" {
			continue
		}
		roles := []string{"assistant"}
		if send {
			if script.MarkdownOnly && !script.PromptOnly {
				continue
			}
			roles = nil
			if slices.Contains(script.Placement, 1) {
				roles = append(roles, "user")
			}
			if slices.Contains(script.Placement, 2) {
				roles = append(roles, "assistant")
			}
			if len(roles) == 0 {
				continue
			}
		} else if !slices.Contains(script.Placement, 2) || script.PromptOnly && !script.MarkdownOnly {
			continue
		}
		if !send && script.MinDepth != nil && *script.MinDepth > 0 {
			continue
		}
		if script.SubstituteRegex != 0 {
			warnings = append(warnings, fmt.Sprintf("embedded regex %q requires unsupported find-pattern macro substitution; skipped", script.ID))
			continue
		}
		replacement := presetRegexMatchMacro.ReplaceAllLiteralString(script.ReplaceString, "$0")
		unsupportedMacro := strings.Contains(replacement, "{{")
		for _, trim := range script.TrimStrings {
			unsupportedMacro = unsupportedMacro || strings.Contains(trim, "{{")
		}
		if unsupportedMacro {
			warnings = append(warnings, fmt.Sprintf("embedded regex %q requires unsupported replacement/trim macros; skipped", script.ID))
			continue
		}
		re, global, err := dto.CompileTextRegex(script.FindRegex)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("embedded regex %q cannot be compiled; skipped", script.ID))
			continue
		}
		compiled = append(compiled, presetResponseRegex{id: script.ID, pattern: re, global: global, replaceBy: replacement, trimStrings: script.TrimStrings, roles: roles, minDepth: script.MinDepth, maxDepth: script.MaxDepth})
	}
	return compiled, warnings
}

func compileChannelTextRegex(config *dto.ResponseTextFilter, model, stage string) ([]presetResponseRegex, error) {
	if config == nil || config.Mode != "rules" || stage == "send" && !config.EnableSend || len(config.Models) > 0 && !slices.Contains(config.Models, model) {
		return nil, nil
	}
	if err := config.Validate(); err != nil {
		return nil, err
	}
	var rules []presetResponseRegex
	for _, rule := range config.Rules {
		if rule.Disabled || rule.Stage != stage || stage == "receive" && rule.MinDepth != nil && *rule.MinDepth > 0 {
			continue
		}
		re, global, err := dto.CompileTextRegex(rule.Pattern)
		if err != nil {
			return nil, err
		}
		roles := rule.Roles
		if len(roles) == 0 {
			roles = []string{"user", "assistant"}
		}
		rules = append(rules, presetResponseRegex{id: rule.ID, pattern: re, global: global, replaceBy: presetRegexMatchMacro.ReplaceAllLiteralString(rule.Replacement, "$0"), extract: rule.Action == "extract", missingEmpty: rule.MissingMatch == "empty", trimCapture: rule.TrimCapture, trimStrings: rule.TrimStrings, roles: roles, minDepth: rule.MinDepth, maxDepth: rule.MaxDepth})
	}
	return rules, nil
}

func (r presetResponseRegex) replace(value string) (string, error) {
	// Check every append, including individual captures: checking the final
	// result after ReplaceFunc allows a small response to allocate gigabytes.
	if len(value) > maxFilteredResponseBytes {
		return "", errors.New("embedded regex input exceeds size limit")
	}
	deadline := time.Now().Add(200 * time.Millisecond)
	workRemaining := 64 << 20
	var result strings.Builder
	previous := 0
	match, err := r.pattern.FindStringMatch(value)
	if err == nil && match == nil && r.extract {
		if r.missingEmpty {
			return "", nil
		}
		return value, nil
	}
	for count := 0; match != nil && err == nil; count++ {
		if count >= 100000 || time.Now().After(deadline) {
			return "", errors.New("embedded regex execution limit exceeded")
		}
		start, length := match.ByteRange()
		if !r.extract {
			if err := appendPresetRegexText(&result, value[previous:start]); err != nil {
				return "", err
			}
		}
		previous = start + length
		replacement := r.replaceBy
		for replacement != "" {
			workRemaining -= len(replacement)
			if workRemaining < 0 || time.Now().After(deadline) {
				return "", errors.New("embedded regex execution limit exceeded")
			}
			location := presetRegexCapture.FindStringIndex(replacement)
			if location == nil {
				if err := appendPresetRegexText(&result, replacement); err != nil {
					return "", err
				}
				break
			}
			if err := appendPresetRegexText(&result, replacement[:location[0]]); err != nil {
				return "", err
			}
			token := replacement[location[0]:location[1]]
			replacement = replacement[location[1]:]
			capture := ""
			var group *regexp2.Group
			if strings.HasPrefix(token, "$<") {
				group = match.GroupByName(token[2 : len(token)-1])
			} else if index, err := strconv.Atoi(token[1:]); err == nil {
				group = match.GroupByNumber(index)
			}
			if group != nil {
				captureStart, captureLength := group.ByteRange()
				capture = value[captureStart : captureStart+captureLength]
			}
			if r.trimCapture {
				capture = strings.TrimSpace(capture)
			}
			for _, trim := range r.trimStrings {
				if trim != "" {
					workRemaining -= len(capture) + len(trim)
					if workRemaining < 0 || time.Now().After(deadline) {
						return "", errors.New("embedded regex execution limit exceeded")
					}
					capture = strings.ReplaceAll(capture, trim, "")
				}
			}
			if err := appendPresetRegexText(&result, capture); err != nil {
				return "", err
			}
		}
		if !r.global {
			break
		}
		match, err = r.pattern.FindNextMatch(match)
	}
	if err != nil {
		// regexp2 errors include input text and patterns; never expose those in logs.
		return "", errors.New("embedded regex matching failed or timed out")
	}
	if !r.extract {
		if err := appendPresetRegexText(&result, value[previous:]); err != nil {
			return "", err
		}
	}
	return result.String(), nil
}

func appendPresetRegexText(output *strings.Builder, value string) error {
	if len(value) > maxFilteredResponseBytes-output.Len() {
		return errors.New("embedded regex output exceeds size limit")
	}
	output.WriteString(value)
	return nil
}
