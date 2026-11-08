package service

import (
	"fmt"
	"slices"
	"sort"

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

type PresetCapabilityReport struct {
	Name             string                  `json:"name"`
	Mode             string                  `json:"mode"`
	Model            string                  `json:"model"`
	HasScriptRuntime bool                    `json:"has_script_runtime"`
	ScriptRuntime    string                  `json:"script_runtime,omitempty"`
	ToolNames        []string                `json:"tool_names"`
	Capabilities     []string                `json:"capabilities"`
	Warnings         []string                `json:"warnings"`
	Entries          []PresetCapabilityEntry `json:"entries"`
}

// BuildPresetCapabilityReport parses the preset and runs one bounded probe so
// the UI can show static prompts plus tools that the server-side script
// actually observed for the supplied request shape.
func BuildPresetCapabilityReport(config *dto.SillyTavernPresetConfig, request *dto.GeneralOpenAIRequest) (*PresetCapabilityReport, error) {
	if config == nil {
		return &PresetCapabilityReport{
			Name:         "No preset",
			Capabilities: []string{"passthrough"},
			ToolNames:    []string{},
			Warnings:     []string{},
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
		request = &dto.GeneralOpenAIRequest{
			Model:    "capability-probe",
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
		Entries:          []PresetCapabilityEntry{},
	}
	if report.Name == "" {
		report.Name = "SillyTavern preset"
	}
	report.Capabilities = append(report.Capabilities, "prompt assembly")
	if report.HasScriptRuntime {
		report.Capabilities = append(report.Capabilities, "script runtime")
	}
	if config.ToolText != nil && config.ToolText.Enabled {
		report.Capabilities = append(report.Capabilities, "tool text transport")
	}
	if config.ToolProtocol != nil && config.ToolProtocol.Enabled {
		report.Capabilities = append(report.Capabilities, "tool protocol coordination")
	}
	if trace.ScriptResponse != nil {
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
