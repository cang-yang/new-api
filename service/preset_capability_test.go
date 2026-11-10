package service

import (
	"encoding/json"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildPresetCapabilityReportIncludesStaticAndObservedTools(t *testing.T) {
	config := protocolScriptPresetConfig(t, map[string]any{
		"ToolBindings": map[string]any{
			"context": map[string]any{
				"enabled": true,
				"code":    `return {name:"context", parameters:{type:"object",properties:{}}, action:async()=>({ok:true})};`,
			},
		},
	})
	config.ToolProtocol = nil
	config.ReplyAdapter = &dto.ReplyAdapter{Version: 2, Containers: []dto.ReplyContainer{{Name: "context", ArgumentPointer: new("/query"), OutputKind: "text"}}}
	request := &dto.GeneralOpenAIRequest{
		Model:    "m",
		Messages: []dto.Message{{Role: "user", Content: "probe"}},
	}

	report, err := BuildPresetCapabilityReport(config, request)
	require.NoError(t, err)
	require.NotNil(t, report)
	assert.Equal(t, "m", report.Model)
	assert.Contains(t, report.ToolNames, "context")
	assert.True(t, report.HasScriptRuntime)
	assert.NotEmpty(t, report.Entries)
	assert.Contains(t, report.Capabilities, "script runtime")
}

func TestBuildPresetCapabilityReportRejectsMalformedPreset(t *testing.T) {
	_, err := BuildPresetCapabilityReport(&dto.SillyTavernPresetConfig{
		Preset: json.RawMessage(`{"prompts":[}`),
	}, &dto.GeneralOpenAIRequest{Model: "m"})
	require.Error(t, err)
}

func TestBuildPresetCapabilityReportCacheReturnsIndependentCopies(t *testing.T) {
	clearPresetCapabilityCache()
	config := &dto.SillyTavernPresetConfig{
		Preset: json.RawMessage(`{"prompts":[{"identifier":"main","name":"Main","role":"system","content":"hello"}],"prompt_order":[{"character_id":100001,"order":[{"identifier":"main","enabled":true}]}]}`),
	}
	request := &dto.GeneralOpenAIRequest{Model: "m", Messages: []dto.Message{{Role: "user", Content: "probe"}}}

	first, err := BuildPresetCapabilityReport(config, request)
	require.NoError(t, err)
	require.NotNil(t, first)
	first.ToolNames = append(first.ToolNames, "mutated")

	second, err := BuildPresetCapabilityReport(config, request)
	require.NoError(t, err)
	require.NotNil(t, second)
	assert.NotContains(t, second.ToolNames, "mutated")
}

func TestBuildPresetCapabilityReportCacheInvalidatesOnConfigChange(t *testing.T) {
	clearPresetCapabilityCache()
	request := &dto.GeneralOpenAIRequest{Model: "m", Messages: []dto.Message{{Role: "user", Content: "probe"}}}
	enabled := true
	first, err := BuildPresetCapabilityReport(&dto.SillyTavernPresetConfig{
		Preset:         json.RawMessage(`{"prompts":[{"identifier":"main","name":"Main","role":"system","content":"hello"}],"prompt_order":[{"character_id":100001,"order":[{"identifier":"main","enabled":true}]}]}`),
		EntryOverrides: map[string]bool{"main": enabled},
	}, request)
	require.NoError(t, err)
	require.NotNil(t, first)

	second, err := BuildPresetCapabilityReport(&dto.SillyTavernPresetConfig{
		Preset:         json.RawMessage(`{"prompts":[{"identifier":"main","name":"Changed","role":"system","content":"hello"}],"prompt_order":[{"character_id":100001,"order":[{"identifier":"main","enabled":true}]}]}`),
		EntryOverrides: map[string]bool{"main": enabled},
	}, request)
	require.NoError(t, err)
	require.NotNil(t, second)
	assert.Equal(t, "Changed", second.Entries[0].Name)
}

func TestClassifyPresetCapabilityNoticesUsesStableCodes(t *testing.T) {
	notices := classifyPresetCapabilityNotices([]string{
		"Browser scripts are not executed",
		"worldbook marker is not available",
		"embedded regex cannot be compiled",
		"embedded regex cannot be compiled",
	})

	require.Len(t, notices, 3)
	assert.Equal(t, "browser_scripts_unavailable", notices[0].Code)
	assert.Equal(t, "worldbook_slot_unavailable", notices[1].Code)
	assert.Equal(t, "receive_regex_compatibility", notices[2].Code)
	assert.Equal(t, "embedded regex cannot be compiled", notices[2].Detail)
}

func TestCapabilityModelScopeDoesNotClaimUnexecutedScripts(t *testing.T) {
	config := &dto.SillyTavernPresetConfig{
		Models: []string{"allowed"},
		Preset: json.RawMessage(`{"prompts":[{"identifier":"main","content":"hello"}],"prompt_order":[{"order":[{"identifier":"main","enabled":true}]}]}`),
	}
	report, err := BuildPresetCapabilityReport(config, &dto.GeneralOpenAIRequest{Model: "other"})
	require.NoError(t, err)
	assert.Equal(t, "skipped_model", report.ProbeStatus)
	assert.Empty(t, report.Capabilities)
	assert.Equal(t, "not_probed", report.Entries[0].Status)
	report, err = BuildPresetCapabilityReport(config, nil)
	require.NoError(t, err)
	assert.Equal(t, "allowed", report.Model)
	assert.Equal(t, "completed", report.ProbeStatus)
}

func TestAdaptationScopeReportDoesNotRunScriptsOrClaimModelCompatibility(t *testing.T) {
	config := protocolScriptPresetConfig(t, map[string]any{
		"FinalRequest": map[string]any{"enabled": true, "code": `throw new Error("scope check must not execute scripts")`},
	})
	config.ToolProtocol = nil
	config.ReplyAdapter = &dto.ReplyAdapter{Version: 2, Containers: []dto.ReplyContainer{{Name: "reply", ArgumentPointer: new("/content"), OutputKind: "text"}}}
	report, err := InspectAdaptationScope("plain_chat_only", "m", config, nil, nil)
	require.NoError(t, err)
	require.Len(t, report.Scenarios, 7)
	for _, scenario := range report.Scenarios {
		if scenario.Kind == "text" || scenario.Kind == "stream" {
			assert.Equal(t, "adapted_unverified", scenario.Status)
		} else {
			assert.Equal(t, "native", scenario.Status)
		}
	}
	strict, err := InspectAdaptationScope("all_supported", "m", config, nil, nil)
	require.NoError(t, err)
	assert.NotEqual(t, report.Fingerprint, strict.Fingerprint)
	for _, scenario := range strict.Scenarios {
		if scenario.Kind == "other_protocol" {
			assert.Equal(t, "conflict", scenario.Status, scenario.Kind)
		} else {
			assert.Equal(t, "adapted_unverified", scenario.Status, scenario.Kind)
		}
	}
	assert.True(t, strict.Buffered)
}
