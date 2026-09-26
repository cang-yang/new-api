package dto

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/dlclark/regexp2/v2"
)

// TextRegexRule operates on text fields, never on an entire protocol document.
type TextRegexRule struct {
	ID           string   `json:"id"`
	Name         string   `json:"name,omitempty"`
	Disabled     bool     `json:"disabled,omitempty"`
	Stage        string   `json:"stage"`
	Action       string   `json:"action"`
	Pattern      string   `json:"pattern"`
	Replacement  string   `json:"replacement"`
	Roles        []string `json:"roles,omitempty"`
	MinDepth     *int     `json:"min_depth,omitempty"`
	MaxDepth     *int     `json:"max_depth,omitempty"`
	MissingMatch string   `json:"missing_match,omitempty"`
	TrimCapture  bool     `json:"trim_capture,omitempty"`
	TrimStrings  []string `json:"trim_strings,omitempty"`
}

func (r TextRegexRule) Validate() error {
	if len(r.TrimStrings) > 100 {
		return fmt.Errorf("text regex has too many trim strings")
	}
	for _, trim := range r.TrimStrings {
		if len(trim) > 16<<10 {
			return fmt.Errorf("text regex trim string exceeds 16 KiB")
		}
	}
	if r.ID == "" || len(r.ID) > 128 || len(r.Name) > 256 {
		return fmt.Errorf("text regex requires a bounded rule ID/name")
	}
	if r.Stage != "send" && r.Stage != "receive" {
		return fmt.Errorf("text regex stage must be send or receive")
	}
	if r.Action != "replace" && r.Action != "extract" {
		return fmt.Errorf("text regex action must be replace or extract")
	}
	if len(r.Replacement) > 512<<10 {
		return fmt.Errorf("text regex replacement exceeds 512 KiB")
	}
	if len(r.Pattern) > 16<<10 {
		return fmt.Errorf("text regex pattern exceeds 16 KiB")
	}
	if r.MissingMatch != "" && r.MissingMatch != "passthrough" && r.MissingMatch != "empty" {
		return fmt.Errorf("text regex missing_match must be passthrough or empty")
	}
	for _, role := range r.Roles {
		if !slices.Contains([]string{"user", "assistant", "system", "developer"}, role) {
			return fmt.Errorf("text regex has unsupported message role")
		}
	}
	if r.MinDepth != nil && *r.MinDepth < -1 || r.MaxDepth != nil && *r.MaxDepth < -1 {
		return fmt.Errorf("text regex depth must be -1 or nonnegative")
	}
	if r.MinDepth != nil && r.MaxDepth != nil && *r.MaxDepth >= 0 && *r.MinDepth > *r.MaxDepth {
		return fmt.Errorf("text regex min_depth exceeds max_depth")
	}
	if r.Disabled {
		return nil
	}
	_, _, err := CompileTextRegex(r.Pattern)
	return err
}

// CompileTextRegex shares the bounded ECMAScript-compatible engine between
// channel rules and imported presets. Errors intentionally omit private patterns.
func CompileTextRegex(pattern string) (*regexp2.Regexp, bool, error) {
	if pattern == "" || len(pattern) > 16<<10 {
		return nil, false, fmt.Errorf("text regex pattern must be 1-16384 bytes")
	}
	flags := ""
	if strings.HasPrefix(pattern, "/") {
		end := strings.LastIndex(pattern, "/")
		backslashes := 0
		for i := end - 1; i > 0 && pattern[i] == '\\'; i-- {
			backslashes++
		}
		if end <= 0 || backslashes%2 != 0 {
			return nil, false, fmt.Errorf("invalid /pattern/flags syntax")
		}
		flags = pattern[end+1:]
		pattern = pattern[1:end]
	}
	options := regexp2.ECMAScript
	seen := map[rune]bool{}
	for _, flag := range flags {
		if seen[flag] {
			return nil, false, fmt.Errorf("duplicate regex flag")
		}
		seen[flag] = true
		switch flag {
		case 'g':
		case 'i':
			options |= regexp2.IgnoreCase
		case 'm':
			options |= regexp2.Multiline
		case 's':
			options |= regexp2.Singleline
		case 'u':
			options |= regexp2.Unicode
		default:
			return nil, false, fmt.Errorf("supported regex flags are g i m s u")
		}
	}
	re, err := regexp2.Compile(pattern, options)
	if err != nil {
		return nil, false, fmt.Errorf("invalid text regex pattern")
	}
	re.MatchTimeout = 200 * time.Millisecond
	return re, strings.Contains(flags, "g"), nil
}
