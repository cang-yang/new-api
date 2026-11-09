package service

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
)

type PresetCapabilityEntry struct {
	Identifier string `json:"identifier"`
	Name       string `json:"name,omitempty"`
	Role       string `json:"role,omitempty"`
	Kind       string `json:"kind"`
	Status     string `json:"status"`
	Detail     string `json:"detail,omitempty"`
}

type PresetCapabilityNotice struct {
	Code     string `json:"code"`
	Severity string `json:"severity"`
	Detail   string `json:"detail,omitempty"`
}

type PresetCapabilityReport struct {
	Name             string                   `json:"name"`
	Mode             string                   `json:"mode"`
	Model            string                   `json:"model"`
	HasScriptRuntime bool                     `json:"has_script_runtime"`
	ScriptRuntime    string                   `json:"script_runtime,omitempty"`
	ToolNames        []string                 `json:"tool_names"`
	Capabilities     []string                 `json:"capabilities"`
	Warnings         []string                 `json:"warnings"`
	Notices          []PresetCapabilityNotice `json:"notices"`
	Entries          []PresetCapabilityEntry  `json:"entries"`
	ProbeStatus      string                   `json:"probe_status"`
	ReplySources     []dto.ReplyContainer     `json:"reply_sources,omitempty"`
	HistoryTools     []string                 `json:"history_tools,omitempty"`
}

type AdaptationScenario struct {
	Kind   string `json:"kind"`
	Status string `json:"status"`
	Reason string `json:"reason"`
}

type AdaptationScopeReport struct {
	Fingerprint string               `json:"fingerprint"`
	Model       string               `json:"model"`
	Scenarios   []AdaptationScenario `json:"scenarios"`
	Buffered    bool                 `json:"buffered"`
}

// InspectAdaptationScope uses the relay's protocol classifier, not a model or
// script probe. "adapted_unverified" deliberately does not certify model output.
func InspectAdaptationScope(scope, model string, preset *dto.SillyTavernPresetConfig, worldbook *dto.WorldbookConfig, filter *dto.ResponseTextFilter) (*AdaptationScopeReport, error) {
	if scope != "" && scope != "plain_chat_only" && scope != "all_supported" {
		return nil, fmt.Errorf("invalid adaptation scope")
	}
	if preset != nil {
		if _, err := preset.ParseAndValidate(); err != nil {
			return nil, err
		}
	}
	if err := worldbook.Validate(); err != nil {
		return nil, err
	}
	if err := filter.Validate(); err != nil {
		return nil, err
	}
	profile := NewAdaptationProfile(preset, worldbook, filter, filter)
	fingerprint, err := AdaptationProfileFingerprint(profile)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256([]byte(scope + "\000" + model + "\000" + fingerprint))
	report := &AdaptationScopeReport{Model: model, Fingerprint: hex.EncodeToString(sum[:])}
	activePreset := preset
	if preset != nil && len(preset.Models) > 0 && !slices.Contains(preset.Models, model) {
		activePreset = nil
	}
	internal := activePreset != nil && (activePreset.ToolText != nil && activePreset.ToolText.Enabled ||
		activePreset.ReplyAdapter != nil && len(activePreset.ReplyAdapter.Containers) > 0)
	if activePreset != nil && activePreset.ScriptRuntime == "spreset-v1" {
		settings, err := parseSPresetSettings(activePreset)
		if err != nil {
			return nil, err
		}
		internal = internal || settings != nil && settings.OutputPreprocessing.ConsumeToolCalls && settings.OutputPreprocessing.ToolCallFormatter.Enabled && len(settings.ToolBindings) > 0
	}
	receiveRules, err := compileChannelTextRegex(filter, model, "receive")
	if err != nil {
		return nil, err
	}
	embedded, warnings := compileEffectivePresetTextRegex(activePreset, filter, model, false)
	tool := dto.ToolCallRequest{Type: "function", Function: dto.FunctionRequest{Name: "example", Parameters: []byte(`{"type":"object"}`)}}
	for _, kind := range []string{"text", "tools", "tool_history", "json", "schema", "stream", "other_protocol"} {
		request := &dto.GeneralOpenAIRequest{Model: model, Messages: []dto.Message{{Role: "user", Content: "Example"}}}
		switch kind {
		case "tools":
			request.Tools = []dto.ToolCallRequest{tool}
			request.ToolChoice = "auto"
		case "tool_history":
			request.Messages = append(request.Messages,
				dto.Message{Role: "assistant", ToolCalls: []byte(`[{"id":"example_call","type":"function","function":{"name":"example","arguments":"{}"}}]`)},
				dto.Message{Role: "tool", ToolCallId: "example_call", Content: "Example result"})
		case "json":
			request.ResponseFormat = &dto.ResponseFormat{Type: "json_object"}
		case "schema":
			request.ResponseFormat = &dto.ResponseFormat{Type: "json_schema"}
		case "stream":
			request.Stream = common.GetPointer(true)
		}
		var input dto.Request = request
		if kind == "other_protocol" {
			input = nil
		}
		applies, reason := AdaptationApplies(scope, input)
		scenario := AdaptationScenario{Kind: kind, Status: "native", Reason: reason}
		active := activePreset != nil || worldbook != nil && worldbook.Enabled || filter != nil && (len(filter.Models) == 0 || slices.Contains(filter.Models, model))
		if applies && active {
			scenario.Status, scenario.Reason = "adapted_unverified", "model_output_not_checked"
			switch {
			case kind == "other_protocol":
				scenario.Status, scenario.Reason = "conflict", "chat_completions_only"
			case internal && (kind == "tools" || kind == "tool_history"):
				scenario.Status, scenario.Reason = "conflict", "internal_transport_with_client_tools"
			case kind == "schema" && (internal || filter != nil && len(filter.JSONTextPointers) > 0):
				scenario.Status, scenario.Reason = "conflict", "schema_transformation"
			case kind == "json" && internal:
				scenario.Status, scenario.Reason = "conflict", "text_transport_with_json"
				if activePreset.ReplyAdapter != nil {
					scenario.Status, scenario.Reason = "adapted_unverified", "model_output_not_checked"
					for _, container := range activePreset.ReplyAdapter.Containers {
						if container.OutputKind != "json_object" {
							scenario.Status, scenario.Reason = "conflict", "text_transport_with_json"
						}
					}
				}
			}
			if kind == "stream" {
				report.Buffered = activePreset != nil && activePreset.ScriptRuntime != "" ||
					internal || len(receiveRules) > 0 || len(embedded) > 0 || len(warnings) > 0 ||
					filter != nil && filter.Mode != "rules" && (len(filter.Models) == 0 || slices.Contains(filter.Models, model))
			}
		}
		report.Scenarios = append(report.Scenarios, scenario)
	}
	return report, nil
}

const (
	presetCapabilityCacheLimit = 64
	presetCapabilityCacheTTL   = 10 * time.Minute
)

type presetCapabilityCacheEntry struct {
	expiresAt time.Time
	report    *PresetCapabilityReport
}

type presetCapabilityCacheStore struct {
	sync.Mutex
	entries map[string]presetCapabilityCacheEntry
	order   []string
}

var presetCapabilityCache = presetCapabilityCacheStore{
	entries: make(map[string]presetCapabilityCacheEntry),
}

// BuildPresetCapabilityReport parses the preset and runs one bounded probe so
// the UI can show static prompts plus tools that the server-side script
// actually observed for the supplied request shape.
func BuildPresetCapabilityReport(config *dto.SillyTavernPresetConfig, request *dto.GeneralOpenAIRequest) (*PresetCapabilityReport, error) {
	if config == nil {
		return buildPresetCapabilityReport(config, request)
	}
	key, err := presetCapabilityCacheKey(config, request)
	if err != nil {
		return nil, err
	}
	if report, ok := presetCapabilityCache.get(key); ok {
		return clonePresetCapabilityReport(report)
	}
	report, err := buildPresetCapabilityReport(config, request)
	if err != nil {
		return nil, err
	}
	presetCapabilityCache.put(key, report)
	return clonePresetCapabilityReport(report)
}

func buildPresetCapabilityReport(config *dto.SillyTavernPresetConfig, request *dto.GeneralOpenAIRequest) (*PresetCapabilityReport, error) {
	if config == nil {
		return &PresetCapabilityReport{
			Name:         "No preset",
			Capabilities: []string{"passthrough"},
			ToolNames:    []string{},
			Warnings:     []string{},
			Notices:      []PresetCapabilityNotice{},
			Entries:      []PresetCapabilityEntry{},
		}, nil
	}
	preset, err := config.ParseAndValidate()
	if err != nil {
		return nil, err
	}
	if preset == nil {
		return nil, fmt.Errorf("preset is empty")
	}
	if request == nil {
		model := "capability-probe"
		if len(config.Models) > 0 {
			model = config.Models[0]
		}
		request = &dto.GeneralOpenAIRequest{
			Model:    model,
			Messages: []dto.Message{{Role: "user", Content: "capability probe"}},
		}
	}
	probe, err := common.DeepCopy(request)
	if err != nil {
		return nil, err
	}
	_, trace, err := CompileSillyTavernPreset(config, probe, SillyTavernContext{})
	if err != nil {
		return nil, err
	}
	if trace.ScriptResponse != nil && trace.ScriptResponse.tavern != nil {
		trace.ScriptResponse.tavern.Close()
	}
	report := &PresetCapabilityReport{
		Name:             preset.Name,
		Mode:             trace.Mode,
		Model:            probe.Model,
		HasScriptRuntime: config.ScriptRuntime != "",
		ScriptRuntime:    config.ScriptRuntime,
		ToolNames:        []string{},
		Capabilities:     []string{},
		Warnings:         append([]string(nil), trace.Warnings...),
		Notices:          classifyPresetCapabilityNotices(trace.Warnings),
		Entries:          []PresetCapabilityEntry{},
		ProbeStatus:      "completed",
	}
	if report.Name == "" {
		report.Name = "SillyTavern preset"
	}
	applicable := len(config.Models) == 0 || slices.Contains(config.Models, probe.Model)
	if !applicable {
		report.ProbeStatus = "skipped_model"
		report.Warnings = append(report.Warnings, "Sample model is outside the preset model scope; scripts were not run")
		report.Notices = append(report.Notices, PresetCapabilityNotice{Code: "model_scope_skipped", Severity: "warning"})
	} else {
		report.Capabilities = append(report.Capabilities, "prompt assembly")
	}
	if applicable && report.HasScriptRuntime {
		report.Capabilities = append(report.Capabilities, "script runtime")
	}
	if applicable && config.ToolText != nil && config.ToolText.Enabled {
		report.Capabilities = append(report.Capabilities, "tool text transport")
	}
	if applicable && config.ReplyAdapter != nil && len(config.ReplyAdapter.Containers) > 0 {
		report.Capabilities = append(report.Capabilities, "reply transport")
	}
	if trace.ScriptResponse != nil {
		response := trace.ScriptResponse
		for name := range response.historyTools {
			report.HistoryTools = append(report.HistoryTools, name)
		}
		sort.Strings(report.HistoryTools)
		if response.protocol != nil && response.protocol.audit.Activated {
			for _, name := range response.protocol.contents {
				report.ReplySources = append(report.ReplySources, response.protocol.containers[name])
			}
		}
		for name := range trace.ScriptResponse.ownedTools {
			report.ToolNames = append(report.ToolNames, name)
		}
		if trace.ScriptResponse.settings != nil {
			if trace.ScriptResponse.settings.OutputPreprocessing.Enabled {
				report.Capabilities = append(report.Capabilities, "output preprocessing")
			}
			if trace.ScriptResponse.settings.OutputPreprocessing.ToolCallFormatter.Enabled {
				report.Capabilities = append(report.Capabilities, "tool call formatter")
			}
		}
	}
	sort.Strings(report.ToolNames)
	report.ToolNames = slices.Compact(report.ToolNames)
	report.Capabilities = slices.Compact(report.Capabilities)

	order := preset.PromptOrder[0].Order
	for _, candidate := range preset.PromptOrder {
		if candidate.CharacterID == 100001 {
			order = candidate.Order
			break
		}
	}
	enabled := make(map[string]bool, len(order))
	for _, entry := range order {
		enabled[entry.Identifier] = entry.Enabled
	}
	for identifier, value := range config.EntryOverrides {
		enabled[identifier] = value
	}
	byID := make(map[string]dto.SillyTavernPrompt, len(preset.Prompts))
	for _, prompt := range preset.Prompts {
		byID[prompt.Identifier] = prompt
	}
	for _, entry := range order {
		prompt, ok := byID[entry.Identifier]
		if !ok {
			report.Entries = append(report.Entries, PresetCapabilityEntry{
				Identifier: entry.Identifier,
				Status:     "missing",
				Kind:       "prompt",
				Detail:     "prompt_order references a missing prompt",
			})
			continue
		}
		status := "disabled"
		if enabled[prompt.Identifier] {
			status = "enabled"
			if !applicable {
				status = "not_probed"
			}
		}
		kind := "prompt"
		detail := ""
		if prompt.Marker {
			kind = "marker"
			detail = "preset slot marker"
		}
		report.Entries = append(report.Entries, PresetCapabilityEntry{
			Identifier: prompt.Identifier,
			Name:       prompt.Name,
			Role:       prompt.Role,
			Kind:       kind,
			Status:     status,
			Detail:     detail,
		})
	}
	return report, nil
}

func classifyPresetCapabilityNotices(warnings []string) []PresetCapabilityNotice {
	notices := make([]PresetCapabilityNotice, 0, len(warnings))
	seen := map[string]bool{}
	for _, warning := range warnings {
		code := "preset_warning"
		severity := "warning"
		switch {
		case strings.Contains(warning, "SPreset data hooks run"), strings.Contains(warning, "Tavern data scripts use request-local"):
			code, severity = "request_local_scripts", "info"
		case strings.Contains(warning, "SPreset feature LogUpload"):
			code, severity = "log_upload_skipped", "info"
		case strings.Contains(warning, "Browser scripts"):
			code = "browser_scripts_unavailable"
		case strings.Contains(warning, "unsupported macro"), strings.Contains(warning, "not mapped"):
			code = "unsupported_macro_or_parameter"
		case strings.Contains(warning, "exact SillyTavern mode"):
			code = "exact_mode_unresolved"
		case strings.Contains(warning, "worldbook"), strings.Contains(warning, "worldInfo"):
			code = "worldbook_slot_unavailable"
		case strings.Contains(warning, "chatHistory"):
			code = "chat_history_slot_missing"
		case strings.Contains(warning, "incoming system"):
			code = "incoming_system_history"
		case strings.Contains(warning, "not applicable"):
			code = "prompt_not_applicable"
		case strings.Contains(warning, "regex"):
			code = "receive_regex_compatibility"
		}
		key := code + "\x00" + warning
		if seen[key] {
			continue
		}
		seen[key] = true
		notices = append(notices, PresetCapabilityNotice{
			Code:     code,
			Severity: severity,
			Detail:   warning,
		})
	}
	return notices
}

func presetCapabilityCacheKey(config *dto.SillyTavernPresetConfig, request *dto.GeneralOpenAIRequest) (string, error) {
	payload, err := common.Marshal(struct {
		Config  *dto.SillyTavernPresetConfig
		Request *dto.GeneralOpenAIRequest
	}{Config: config, Request: request})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}

func clonePresetCapabilityReport(report *PresetCapabilityReport) (*PresetCapabilityReport, error) {
	if report == nil {
		return nil, nil
	}
	copy, err := common.DeepCopy(report)
	if err != nil {
		return nil, err
	}
	return copy, nil
}

func (c *presetCapabilityCacheStore) get(key string) (*PresetCapabilityReport, bool) {
	c.Lock()
	defer c.Unlock()
	entry, ok := c.entries[key]
	if !ok {
		return nil, false
	}
	if time.Now().After(entry.expiresAt) {
		delete(c.entries, key)
		return nil, false
	}
	return entry.report, true
}

func (c *presetCapabilityCacheStore) put(key string, report *PresetCapabilityReport) {
	if report == nil {
		return
	}
	c.Lock()
	defer c.Unlock()
	if _, exists := c.entries[key]; !exists {
		c.order = append(c.order, key)
	}
	c.entries[key] = presetCapabilityCacheEntry{
		expiresAt: time.Now().Add(presetCapabilityCacheTTL),
		report:    report,
	}
	for len(c.order) > presetCapabilityCacheLimit {
		oldest := c.order[0]
		c.order = c.order[1:]
		delete(c.entries, oldest)
	}
}

func clearPresetCapabilityCache() {
	presetCapabilityCache.Lock()
	defer presetCapabilityCache.Unlock()
	presetCapabilityCache.entries = make(map[string]presetCapabilityCacheEntry)
	presetCapabilityCache.order = nil
}
