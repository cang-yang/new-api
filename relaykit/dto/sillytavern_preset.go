package dto

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
	_ "time/tzdata"

	kitutil "github.com/QuantumNous/new-api/relaykit/relayconvert/kitutil"
)

// SillyTavernPresetConfig is deliberately opt-in and channel scoped. The
// original preset is kept intact so it can be edited or exported losslessly.
type SillyTavernPresetConfig struct {
	RegexFailurePolicy  string                   `json:"regex_failure_policy,omitempty"`
	Preset              json.RawMessage          `json:"preset"`
	Models              []string                 `json:"models,omitempty"`
	User                string                   `json:"user,omitempty"`
	Char                string                   `json:"char,omitempty"`
	ParameterPolicy     string                   `json:"parameter_policy,omitempty"` // preset (default) or client
	PostProcessing      string                   `json:"post_processing,omitempty"`  // none (default) or strict
	ReferenceSource     string                   `json:"reference_source,omitempty"` // custom, openai, or newapi (default)
	ContextMode         string                   `json:"context_mode,omitempty"`     // compatibility (default) or exact
	Patches             []SillyTavernPresetPatch `json:"patches,omitempty"`
	EntryOverrides      map[string]bool          `json:"entry_overrides,omitempty"`
	EnableEmbeddedRegex bool                     `json:"enable_embedded_regex,omitempty"`
	EnableSendRegex     bool                     `json:"enable_send_regex,omitempty"`
	RegexOverrides      map[string]bool          `json:"regex_overrides,omitempty"`
	MacroValues         map[string]string        `json:"macro_values,omitempty"`
	TimeZone            string                   `json:"time_zone,omitempty"`
}

// SillyTavernPresetPatch is an explicit literal edit to a preset prompt,
// useful for adapting an imported preset without hardcoding preset-specific rules.
type SillyTavernPresetPatch struct {
	Identifier string `json:"identifier,omitempty"`
	Find       string `json:"find"`
	Replace    string `json:"replace"`
}

type SillyTavernPrompt struct {
	Identifier        string   `json:"identifier"`
	Name              string   `json:"name,omitempty"`
	Role              string   `json:"role,omitempty"`
	Content           string   `json:"content,omitempty"`
	Marker            bool     `json:"marker,omitempty"`
	SystemPrompt      bool     `json:"system_prompt,omitempty"`
	InjectionPosition int      `json:"injection_position,omitempty"`
	InjectionDepth    int      `json:"injection_depth,omitempty"`
	InjectionOrder    int      `json:"injection_order,omitempty"`
	InjectionTrigger  []string `json:"injection_trigger,omitempty"`
}

type SillyTavernPromptOrder struct {
	CharacterID int                           `json:"character_id"`
	Order       []SillyTavernPromptOrderEntry `json:"order"`
}

type SillyTavernPromptOrderEntry struct {
	Identifier string `json:"identifier"`
	Enabled    bool   `json:"enabled"`
}

type SillyTavernPreset struct {
	Name                 string                   `json:"name,omitempty"`
	Prompts              []SillyTavernPrompt      `json:"prompts"`
	PromptOrder          []SillyTavernPromptOrder `json:"prompt_order"`
	Temperature          *float64                 `json:"temperature,omitempty"`
	TopP                 *float64                 `json:"top_p,omitempty"`
	TopK                 *int                     `json:"top_k,omitempty"`
	TopA                 json.RawMessage          `json:"top_a,omitempty"`
	MinP                 json.RawMessage          `json:"min_p,omitempty"`
	RepetitionPenalty    json.RawMessage          `json:"repetition_penalty,omitempty"`
	FrequencyPenalty     *float64                 `json:"frequency_penalty,omitempty"`
	PresencePenalty      *float64                 `json:"presence_penalty,omitempty"`
	Seed                 *float64                 `json:"seed,omitempty"`
	MaxTokens            *uint                    `json:"openai_max_tokens,omitempty"`
	ReasoningEffort      string                   `json:"reasoning_effort,omitempty"`
	Verbosity            json.RawMessage          `json:"verbosity,omitempty"`
	N                    *int                     `json:"n,omitempty"`
	SquashSystemMessages bool                     `json:"squash_system_messages,omitempty"`
	NewChatPrompt        string                   `json:"new_chat_prompt,omitempty"`
	NewExampleChatPrompt string                   `json:"new_example_chat_prompt,omitempty"`
	ScenarioFormat       string                   `json:"scenario_format,omitempty"`
	PersonalityFormat    string                   `json:"personality_format,omitempty"`
	WorldInfoFormat      string                   `json:"wi_format,omitempty"`
	Extensions           struct {
		RegexScripts []SillyTavernRegexScript `json:"regex_scripts,omitempty"`
	} `json:"extensions,omitempty"`
}

// SillyTavernRegexScript preserves the preset's script settings. Display-only
// scripts are retained for export, but are never executed by the API gateway.
type SillyTavernRegexScript struct {
	ID              string   `json:"id,omitempty"`
	ScriptName      string   `json:"scriptName,omitempty"`
	FindRegex       string   `json:"findRegex,omitempty"`
	ReplaceString   string   `json:"replaceString,omitempty"`
	Placement       []int    `json:"placement,omitempty"`
	TrimStrings     []string `json:"trimStrings,omitempty"`
	Disabled        bool     `json:"disabled,omitempty"`
	PromptOnly      bool     `json:"promptOnly,omitempty"`
	MarkdownOnly    bool     `json:"markdownOnly,omitempty"`
	SubstituteRegex int      `json:"substituteRegex,omitempty"`
	MinDepth        *int     `json:"minDepth,omitempty"`
	MaxDepth        *int     `json:"maxDepth,omitempty"`
}

const MaxSillyTavernPresetBytes = 6 << 20

func (c *SillyTavernPresetConfig) ParseAndValidate() (*SillyTavernPreset, error) {
	if c == nil {
		return nil, nil
	}
	if c.RegexFailurePolicy != "" && c.RegexFailurePolicy != "passthrough" && c.RegexFailurePolicy != "error" {
		return nil, fmt.Errorf("preset regex_failure_policy must be passthrough or error")
	}
	if len(c.Preset) == 0 || len(c.Preset) > MaxSillyTavernPresetBytes {
		return nil, fmt.Errorf("sillytavern_preset must contain a preset JSON of at most %d bytes", MaxSillyTavernPresetBytes)
	}
	if c.ParameterPolicy != "" && c.ParameterPolicy != "preset" && c.ParameterPolicy != "client" {
		return nil, fmt.Errorf("sillytavern_preset parameter_policy must be preset or client")
	}
	if c.PostProcessing != "" && c.PostProcessing != "none" && c.PostProcessing != "strict" {
		return nil, fmt.Errorf("sillytavern_preset post_processing must be none or strict")
	}
	if c.ReferenceSource != "" && c.ReferenceSource != "newapi" && c.ReferenceSource != "custom" && c.ReferenceSource != "openai" {
		return nil, fmt.Errorf("sillytavern_preset reference_source must be newapi, custom, or openai")
	}
	if c.ContextMode != "" && c.ContextMode != "compatibility" && c.ContextMode != "exact" {
		return nil, fmt.Errorf("sillytavern_preset context_mode must be compatibility or exact")
	}
	for _, model := range c.Models {
		if strings.TrimSpace(model) == "" {
			return nil, fmt.Errorf("sillytavern_preset models must not contain empty names")
		}
	}
	if len(c.Patches) > 32 {
		return nil, fmt.Errorf("sillytavern_preset allows at most 32 patches")
	}
	for _, patch := range c.Patches {
		if patch.Find == "" || len(patch.Find) > 4096 || len(patch.Replace) > 4096 {
			return nil, fmt.Errorf("sillytavern_preset patches require a nonempty find value and at most 4096 bytes per value")
		}
	}
	if len(c.MacroValues) > 64 {
		return nil, fmt.Errorf("sillytavern_preset allows at most 64 manual macro values")
	}
	for name, value := range c.MacroValues {
		if len(name) == 0 || len(name) > 64 || len(value) > 16384 || strings.ContainsAny(name, "{}:\\ \t\n\r") {
			return nil, fmt.Errorf("sillytavern_preset has an invalid manual macro name or value")
		}
	}
	if c.TimeZone != "" {
		if len(c.TimeZone) > 100 {
			return nil, fmt.Errorf("sillytavern_preset time_zone is too long")
		}
		if _, err := time.LoadLocation(c.TimeZone); err != nil {
			return nil, fmt.Errorf("sillytavern_preset time_zone must be a valid IANA time zone: %w", err)
		}
	}
	var preset SillyTavernPreset
	if err := kitutil.Unmarshal(c.Preset, &preset); err != nil {
		return nil, fmt.Errorf("invalid sillytavern_preset: %w", err)
	}
	if len(preset.Prompts) == 0 || len(preset.Prompts) > 500 || len(preset.PromptOrder) == 0 {
		return nil, fmt.Errorf("sillytavern_preset requires prompts and prompt_order (at most 500 prompts)")
	}
	seen := make(map[string]bool, len(preset.Prompts))
	for _, prompt := range preset.Prompts {
		if prompt.Identifier == "" || seen[prompt.Identifier] || prompt.InjectionPosition < 0 || prompt.InjectionPosition > 1 || prompt.InjectionDepth < 0 || prompt.InjectionDepth > 1000 {
			return nil, fmt.Errorf("sillytavern_preset has invalid prompt identifier or injection position/depth")
		}
		seen[prompt.Identifier] = true
		if prompt.Role != "" && prompt.Role != "system" && prompt.Role != "user" && prompt.Role != "assistant" {
			return nil, fmt.Errorf("sillytavern_preset has unsupported prompt role %q", prompt.Role)
		}
	}
	for identifier := range c.EntryOverrides {
		if !seen[identifier] {
			return nil, fmt.Errorf("sillytavern_preset entry override references unknown prompt %q", identifier)
		}
	}
	if len(preset.Extensions.RegexScripts) > 100 {
		return nil, fmt.Errorf("sillytavern_preset allows at most 100 embedded regex scripts")
	}
	regexIDs := make(map[string]bool, len(preset.Extensions.RegexScripts))
	for _, script := range preset.Extensions.RegexScripts {
		if script.ID == "" || regexIDs[script.ID] {
			return nil, fmt.Errorf("sillytavern_preset embedded regex scripts require unique nonempty IDs")
		}
		if len(script.FindRegex) > 16384 || len(script.ReplaceString) > 512<<10 || len(script.TrimStrings) > 100 {
			return nil, fmt.Errorf("sillytavern_preset embedded regex script %q exceeds size limits", script.ID)
		}
		regexIDs[script.ID] = true
	}
	for identifier := range c.RegexOverrides {
		if !regexIDs[identifier] {
			return nil, fmt.Errorf("sillytavern_preset regex override references unknown script %q", identifier)
		}
	}
	if preset.MaxTokens != nil && *preset.MaxTokens > 1073741823 {
		return nil, fmt.Errorf("sillytavern_preset openai_max_tokens exceeds request limit")
	}
	if preset.N != nil && (*preset.N < 1 || *preset.N > 16) {
		return nil, fmt.Errorf("sillytavern_preset n must be between 1 and 16")
	}
	return &preset, nil
}
