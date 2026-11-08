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
