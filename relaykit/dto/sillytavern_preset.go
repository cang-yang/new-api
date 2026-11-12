package dto

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"
	_ "time/tzdata"

	kitutil "github.com/QuantumNous/new-api/relaykit/relayconvert/kitutil"
)

// SillyTavernPresetConfig is deliberately opt-in and channel scoped. The
// original preset is kept intact so it can be edited or exported losslessly.
type SillyTavernPresetConfig struct {
	SourcePresetID     string          `json:"source_preset_id,omitempty"`
	ReplyAdapter       *ReplyAdapter   `json:"reply_adapter,omitempty"`
	RegexFailurePolicy string          `json:"regex_failure_policy,omitempty"`
	Preset             json.RawMessage `json:"preset"`
	Models             []string        `json:"models,omitempty"`
	User               string          `json:"user,omitempty"`
	Char               string          `json:"char,omitempty"`
	ParameterPolicy    string          `json:"parameter_policy,omitempty"` // preset (default) or client
	// Deprecated display hint. All modes retain the declared reply transport;
	// this field never bypasses the preset formatter or other required hooks.
	DeliveryMode string `json:"delivery_mode,omitempty"` // empty/preset or client
	// PostProcessing controls the optional final message normalization:
	// none, merge, semi_strict, strict, or single_user.
	PostProcessing      string                    `json:"post_processing,omitempty"`
	ReferenceSource     string                    `json:"reference_source,omitempty"` // custom, openai, or newapi (default)
	ContextMode         string                    `json:"context_mode,omitempty"`     // compatibility (default) or exact
	Patches             []SillyTavernPresetPatch  `json:"patches,omitempty"`
	EntryOverrides      map[string]bool           `json:"entry_overrides,omitempty"`
	EnableEmbeddedRegex bool                      `json:"enable_embedded_regex,omitempty"`
	EnableSendRegex     bool                      `json:"enable_send_regex,omitempty"`
	RegexOverrides      map[string]bool           `json:"regex_overrides,omitempty"`
	MacroValues         map[string]string         `json:"macro_values,omitempty"`
	TimeZone            string                    `json:"time_zone,omitempty"`
	ToolText            *SillyTavernToolText      `json:"tool_text,omitempty"`
	ScriptRuntime       string                    `json:"script_runtime,omitempty"` // empty (off), spreset-v1, tavern-v1
	ScriptModules       map[string]string         `json:"script_modules,omitempty"` // administrator-pinned JS modules; never fetched
	ToolProtocol        *SillyTavernToolProtocol  `json:"tool_protocol,omitempty"`
	FailurePolicy       *SillyTavernFailurePolicy `json:"failure_policy,omitempty"`
}

// ReplyAdapter declares internal response transports; it never owns client tools.
type ReplyAdapter struct {
	Version    int              `json:"version"`
	Containers []ReplyContainer `json:"containers"`
	// StructuredOutput approves text-source conversion, not native provider
	// schema enforcement. The complete processing chain runs before validation.
	StructuredOutput bool `json:"structured_output,omitempty"`
	// RequireContainer constrains internal generation, not client business calls.
	// Auto clients may still receive a business call or a restored final answer.
	RequireContainer bool `json:"require_container,omitempty"`
	// Client-format output is already formatted text/JSON, not a legacy envelope
	// that requires whole-text regex extraction before JSON validation.
	FormattedOutput string `json:"formatted_output,omitempty"` // empty/text or client_format
}

type ReplyContainer struct {
	Name              string  `json:"name"`
	ArgumentPointer   *string `json:"argument_pointer,omitempty"`
	Formatter         bool    `json:"formatter,omitempty"`
	OutputKind        string  `json:"output_kind"` // text or json_object
	SourcePromptID    string  `json:"source_prompt_id,omitempty"`
	SourceFingerprint string  `json:"source_fingerprint,omitempty"`
}

// SillyTavernToolProtocol optionally resolves ambiguous tool roles without
// tying the gateway to a specific preset, script, or tool name.
type SillyTavernToolProtocol struct {
	Enabled          bool                       `json:"enabled"`
	ClientFinal      string                     `json:"client_final,omitempty"`
	PresetFinal      string                     `json:"preset_final,omitempty"`
	Auxiliary        []string                   `json:"auxiliary,omitempty"`
	Action           []string                   `json:"action,omitempty"`
	Content          []string                   `json:"content,omitempty"`
	ResponseTarget   string                     `json:"response_target,omitempty"`   // auto, text, json, tool
	ResponseArgument string                     `json:"response_argument,omitempty"` // optional tool argument path
	PhasePolicy      string                     `json:"phase_policy,omitempty"`      // request (default), always_auxiliary, always_final, disabled
	VisibilityPolicy *SillyTavernToolVisibility `json:"visibility_policy,omitempty"`
}

// SillyTavernToolVisibility controls which role groups may be returned to the
// client. Content containers are gateway-internal by default; action and
// auxiliary tools remain client-visible so the client can complete the turn.
type SillyTavernToolVisibility struct {
	Auxiliary string `json:"auxiliary,omitempty"` // client_visible (default)
	Action    string `json:"action,omitempty"`    // client_visible (default)
	Content   string `json:"content,omitempty"`   // gateway_internal (default)
}

// SillyTavernFailurePolicy is deliberately stage-scoped. The gateway does not
// use a hidden global strict flag: every fallback decision reads the stage
// value that applies to the operation it is about to perform.
type SillyTavernFailurePolicy struct {
	Preflight          string `json:"preflight,omitempty"`
	Worldbook          string `json:"worldbook,omitempty"`
	Precompile         string `json:"precompile,omitempty"`
	Preset             string `json:"preset,omitempty"`
	Script             string `json:"script,omitempty"`
	ToolResolution     string `json:"tool_resolution,omitempty"`
	ResponseValidation string `json:"response_validation,omitempty"`
	ResponseRestore    string `json:"response_restore,omitempty"`
	OutputProcessing   string `json:"output_processing,omitempty"`
	Regex              string `json:"regex,omitempty"`
	CrossChannelRetry  string `json:"cross_channel_retry,omitempty"`
	ResponseStarted    string `json:"response_started,omitempty"`
}

// SillyTavernToolText is a declarative transport, not an executable preset
// script. Only the exact configured tool and JSON string argument are restored.
type SillyTavernToolText struct {
	Enabled  bool   `json:"enabled"`
	Name     string `json:"name"`
	Argument string `json:"argument"`
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
		TavernHelper struct {
			Scripts []struct {
				Type    string `json:"type"`
				Enabled bool   `json:"enabled"`
			} `json:"scripts"`
		} `json:"tavern_helper"`
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
	if len(c.SourcePresetID) > 128 {
		return nil, fmt.Errorf("preset source identity exceeds 128 bytes")
	}
	if c.ScriptRuntime != "" && c.ScriptRuntime != "spreset-v1" && c.ScriptRuntime != "tavern-v1" {
		return nil, fmt.Errorf("unsupported preset script_runtime version")
	}
	if c.ScriptRuntime != "" && c.ToolText != nil && c.ToolText.Enabled {
		return nil, fmt.Errorf("preset script runtime and native tool_text transport cannot both be enabled")
	}
	if c.DeliveryMode != "" && c.DeliveryMode != "preset" && c.DeliveryMode != "client" {
		return nil, fmt.Errorf("preset delivery_mode must be preset or client")
	}
	if c.DeliveryMode == "client" && (c.ScriptRuntime == "" || c.ReplyAdapter == nil || c.ReplyAdapter.Version != 2 || len(c.ReplyAdapter.Containers) == 0) {
		return nil, fmt.Errorf("client delivery requires a data script and a declared reply adapter")
	}
	if err := c.ReplyAdapter.Validate(); err != nil {
		return nil, err
	}
	if err := validateSillyTavernToolProtocol(c.ToolProtocol); err != nil {
		return nil, err
	}
	if err := validateSillyTavernFailurePolicy(c.FailurePolicy); err != nil {
		return nil, err
	}
	if c.ToolText != nil && c.ToolText.Enabled {
		for _, value := range []string{c.ToolText.Name, c.ToolText.Argument} {
			if len(value) == 0 || len(value) > 64 {
				return nil, fmt.Errorf("preset tool_text identifiers require 1 to 64 ASCII letters, digits, underscores, or hyphens")
			}
			for _, ch := range value {
				if !(ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '_' || ch == '-') {
					return nil, fmt.Errorf("preset tool_text identifiers require 1 to 64 ASCII letters, digits, underscores, or hyphens")
				}
			}
		}
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
	switch c.PostProcessing {
	case "", "none", "merge", "claude", "semi", "semi_strict", "strict", "single", "single_user":
	default:
		return nil, fmt.Errorf("sillytavern_preset post_processing must be none, merge/claude, semi/semi_strict, strict, or single/single_user")
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
	merged, err := MergeSillyTavernRegexScripts(c.Preset)
	if err != nil {
		return nil, err
	}
	preset.Extensions.RegexScripts = merged
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
	for _, order := range preset.PromptOrder {
		ordered := make(map[string]bool, len(order.Order))
		for _, entry := range order.Order {
			if ordered[entry.Identifier] {
				return nil, fmt.Errorf("sillytavern_preset has duplicate prompt_order identifier %q", entry.Identifier)
			}
			ordered[entry.Identifier] = true
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

// Both extension layouts are used by plugin exporters. Exact copies share one
// identity; conflicting copies must be resolved by the user, never by ordering.
func MergeSillyTavernRegexScripts(data json.RawMessage) ([]SillyTavernRegexScript, error) {
	var envelope struct {
		Extensions struct {
			Scripts []json.RawMessage `json:"regex_scripts"`
			SPreset struct {
				RegexBinding struct {
					Regexes []json.RawMessage `json:"regexes"`
				} `json:"RegexBinding"`
			} `json:"SPreset"`
		} `json:"extensions"`
	}
	if err := kitutil.Unmarshal(data, &envelope); err != nil {
		return nil, err
	}
	if len(envelope.Extensions.Scripts)+len(envelope.Extensions.SPreset.RegexBinding.Regexes) > 200 {
		return nil, fmt.Errorf("sillytavern_preset allows at most 100 distinct embedded regex scripts")
	}
	seen := map[string]any{}
	var merged []SillyTavernRegexScript
	for _, raw := range append(envelope.Extensions.Scripts, envelope.Extensions.SPreset.RegexBinding.Regexes...) {
		var script SillyTavernRegexScript
		var value any
		if kitutil.Unmarshal(raw, &script) != nil || kitutil.Unmarshal(raw, &value) != nil || script.ID == "" {
			return nil, fmt.Errorf("sillytavern_preset embedded regex scripts require nonempty IDs")
		}
		if previous, exists := seen[script.ID]; exists {
			if !reflect.DeepEqual(previous, value) {
				return nil, fmt.Errorf("embedded regex %q has conflicting copies", script.ID)
			}
			continue
		}
		seen[script.ID] = value
		merged = append(merged, script)
	}
	return merged, nil
}

func validateSillyTavernToolProtocol(protocol *SillyTavernToolProtocol) error {
	if protocol == nil {
		return nil
	}
	if protocol.ClientFinal != "" && protocol.PresetFinal != "" {
		return fmt.Errorf("sillytavern_preset tool_protocol cannot declare both client_final and preset_final")
	}
	for _, name := range []string{protocol.ClientFinal, protocol.PresetFinal} {
		if name != "" && !validSillyTavernToolName(name) {
			return fmt.Errorf("sillytavern_preset tool_protocol names require 1 to 64 ASCII letters, digits, underscores, or hyphens")
		}
	}
	if len(protocol.Auxiliary) > 64 {
		return fmt.Errorf("sillytavern_preset tool_protocol allows at most 64 auxiliary tools")
	}
	seen := make(map[string]bool, len(protocol.Auxiliary))
	for _, name := range protocol.Auxiliary {
		if !validSillyTavernToolName(name) || seen[name] {
			return fmt.Errorf("sillytavern_preset tool_protocol auxiliary names must be unique and use 1 to 64 ASCII letters, digits, underscores, or hyphens")
		}
		seen[name] = true
	}
	for field, names := range map[string][]string{
		"action":  protocol.Action,
		"content": protocol.Content,
	} {
		if len(names) > 64 {
			return fmt.Errorf("sillytavern_preset tool_protocol allows at most 64 %s tools", field)
		}
		seen := make(map[string]bool, len(names))
		for _, name := range names {
			if !validSillyTavernToolName(name) || seen[name] {
				return fmt.Errorf("sillytavern_preset tool_protocol %s names must be unique and use 1 to 64 ASCII letters, digits, underscores, or hyphens", field)
			}
			seen[name] = true
		}
	}
	roleOwners := make(map[string]string)
	for field, names := range map[string][]string{
		"client_final": []string{protocol.ClientFinal},
		"preset_final": []string{protocol.PresetFinal},
		"auxiliary":    protocol.Auxiliary,
		"action":       protocol.Action,
		"content":      protocol.Content,
	} {
		for _, name := range names {
			if name == "" {
				continue
			}
			if previous, exists := roleOwners[name]; exists {
				if previous == "preset_final" && field == "content" || previous == "content" && field == "preset_final" {
					continue
				}
				return fmt.Errorf("sillytavern_preset tool_protocol tool roles must be mutually exclusive; %q is declared as both %s and %s", name, previous, field)
			}
			roleOwners[name] = field
		}
	}
	if protocol.VisibilityPolicy != nil {
		values := map[string]string{
			"auxiliary": protocol.VisibilityPolicy.Auxiliary,
			"action":    protocol.VisibilityPolicy.Action,
			"content":   protocol.VisibilityPolicy.Content,
		}
		defaults := map[string]string{
			"auxiliary": "client_visible",
			"action":    "client_visible",
			"content":   "gateway_internal",
		}
		for field, value := range values {
			if value == "" {
				continue
			}
			if value != "client_visible" && value != "gateway_internal" {
				return fmt.Errorf("sillytavern_preset tool_protocol visibility.%s must be client_visible or gateway_internal", field)
			}
			if field == "auxiliary" && value != defaults[field] {
				return fmt.Errorf("sillytavern_preset tool_protocol visibility.auxiliary must be client_visible")
			}
			if field == "action" && value != defaults[field] {
				return fmt.Errorf("sillytavern_preset tool_protocol visibility.action must be client_visible")
			}
			if field == "content" && value != defaults[field] {
				return fmt.Errorf("sillytavern_preset tool_protocol visibility.content must be gateway_internal")
			}
		}
	}
	switch protocol.ResponseTarget {
	case "", "auto", "text", "json", "tool":
	default:
		return fmt.Errorf("sillytavern_preset tool_protocol response_target must be auto, text, json, or tool")
	}
	switch protocol.PhasePolicy {
	case "", "request", "always_auxiliary", "always_final", "disabled":
	default:
		return fmt.Errorf("sillytavern_preset tool_protocol phase_policy must be request, always_auxiliary, always_final, or disabled")
	}
	if protocol.ResponseArgument == "" {
		return nil
	}
	if protocol.ClientFinal != "" {
		return fmt.Errorf("sillytavern_preset tool_protocol response_argument requires a preset_final tool")
	}
	if protocol.ResponseTarget != "json" && protocol.ResponseTarget != "text" {
		return fmt.Errorf("sillytavern_preset tool_protocol response_argument requires response_target json or text")
	}
	if !validSillyTavernToolArgumentPath(protocol.ResponseArgument) {
		return fmt.Errorf("sillytavern_preset tool_protocol response_argument must be a dot-separated argument path")
	}
	return nil
}

func validateSillyTavernFailurePolicy(policy *SillyTavernFailurePolicy) error {
	if policy == nil {
		return nil
	}
	allowed := map[string]map[string]bool{
		"preflight":           {"": true, "request_passthrough": true, "fail": true},
		"worldbook":           {"": true, "skip": true, "fail": true},
		"precompile":          {"": true, "request_passthrough": true, "fail": true},
		"preset":              {"": true, "request_passthrough": true, "fail": true},
		"script":              {"": true, "request_passthrough": true, "fail": true},
		"tool_resolution":     {"": true, "tool_protocol_passthrough": true, "fail": true},
		"response_validation": {"": true, "safe_raw": true, "fail": true},
		"response_restore":    {"": true, "safe_raw": true, "fail": true},
		"output_processing":   {"": true, "preserve": true, "fail": true},
		"regex":               {"": true, "preserve": true, "empty": true, "fail": true},
		"cross_channel_retry": {"": true, "reject": true},
		"response_started":    {"": true, "stop": true},
	}
	values := map[string]string{
		"preflight":           policy.Preflight,
		"worldbook":           policy.Worldbook,
		"precompile":          policy.Precompile,
		"preset":              policy.Preset,
		"script":              policy.Script,
		"tool_resolution":     policy.ToolResolution,
		"response_validation": policy.ResponseValidation,
		"response_restore":    policy.ResponseRestore,
		"output_processing":   policy.OutputProcessing,
		"regex":               policy.Regex,
		"cross_channel_retry": policy.CrossChannelRetry,
		"response_started":    policy.ResponseStarted,
	}
	for field, value := range values {
		if !allowed[field][value] {
			return fmt.Errorf("sillytavern_preset failure_policy.%s has unsupported value %q", field, value)
		}
	}
	return nil
}

func validSillyTavernToolName(name string) bool {
	if len(name) == 0 || len(name) > 64 {
		return false
	}
	for _, ch := range name {
		if ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '_' || ch == '-' {
			continue
		}
		return false
	}
	return true
}

func validSillyTavernToolArgumentPath(path string) bool {
	if len(path) == 0 || len(path) > 256 {
		return false
	}
	for segment := range strings.SplitSeq(path, ".") {
		if !validSillyTavernToolName(segment) {
			return false
		}
	}
	return true
}
