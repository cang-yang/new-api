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
	config.ToolProtocol = &dto.SillyTavernToolProtocol{Enabled: true, Auxiliary: []string{"context"}}
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
