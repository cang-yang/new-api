package service

import (
	"encoding/json"
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAdaptationHealthRequiresDeliveryEvidence(t *testing.T) {
	for _, test := range []struct {
		name    string
		attempt model.AuditAttempt
		stages  []AdaptationDiagnosticStage
		want    string
	}{
		{"upstream success alone is not delivery", model.AuditAttempt{HTTPStatus: 200}, nil, "unknown"},
		{"missing upstream status is not proof of upstream failure", model.AuditAttempt{State: "failed"}, nil, "unknown"},
		{"explicit upstream failure", model.AuditAttempt{HTTPStatus: 502}, nil, "upstream_failed"},
		{"writer completed", model.AuditAttempt{HTTPStatus: 200}, []AdaptationDiagnosticStage{{Stage: "writeback", Status: "completed"}}, "completed"},
		{"optional skip is distinct", model.AuditAttempt{HTTPStatus: 200}, []AdaptationDiagnosticStage{{Stage: "worldbook", Status: "fallback"}, {Stage: "writeback", Status: "completed"}}, "degraded"},
		{"core processing failed after upstream success", model.AuditAttempt{HTTPStatus: 200}, []AdaptationDiagnosticStage{{Stage: "writeback", Status: "failed"}}, "failed_before_delivery"},
		{"interruption takes precedence", model.AuditAttempt{HTTPStatus: 200}, []AdaptationDiagnosticStage{{Stage: "response_started", Status: "failed"}, {Stage: "writeback", Status: "failed"}}, "interrupted"},
		{"capacity is not a provider failure", model.AuditAttempt{ErrorCode: "reply_processing_busy", HTTPStatus: 503}, nil, "overloaded"},
	} {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, adaptationAttemptOutcome(test.attempt, &AdaptationDiagnostic{Stages: test.stages}))
		})
	}
}

func TestEffectiveFailurePlanMatchesRequiredPipelineAndOptionalCleanup(t *testing.T) {
	preset := &dto.SillyTavernPresetConfig{
		ToolText:      &dto.SillyTavernToolText{Enabled: true, Name: "reply", Argument: "text"},
		FailurePolicy: &dto.SillyTavernFailurePolicy{Preset: "request_passthrough", Script: "request_passthrough", OutputProcessing: "preserve", Regex: "preserve"},
	}
	filter := &dto.ResponseTextFilter{Mode: "regex_extract", Pattern: "(.*)", MissingMatch: "error"}
	plan := effectiveAdaptationFailurePlan(preset, nil, filter, "m")
	for _, item := range plan {
		switch item.Stage {
		case FailureStagePreset, FailureStageScript:
			require.Equal(t, FailureRequestPassthrough, item.Configured)
			require.Equal(t, FailureFail, item.Effective)
			require.Equal(t, "required_reply_pipeline", item.Reason)
		case FailureStageRegex:
			require.Equal(t, FailureFail, item.Effective)
			require.Equal(t, "required_cleanup", item.Reason)
		case FailureStageOutputProcessing:
			require.Equal(t, FailurePreserve, item.Effective, "mandatory host cleanup must not disable optional output rollback")
		}
	}
	require.NotEmpty(t, effectiveAdaptationFailurePlan(nil, nil, nil, "m"), "empty profiles must remain inspectable")
}

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

func TestPresetCapabilityDiscoveryDoesNotApproveOrRejectStaleReplySource(t *testing.T) {
	config := protocolScriptPresetConfig(t, map[string]any{
		"ToolBindings": map[string]any{
			"context": map[string]any{
				"enabled": true,
				"code":    `return {name:"new_reply",parameters:{type:"object",properties:{text:{type:"string"}}}};`,
			},
		},
		"OutputPreprocessing": map[string]any{
			"consumeToolCalls":  true,
			"toolCallFormatter": map[string]any{"enabled": true, "script": `(name,args)=>args.text`},
		},
	})
	config.ToolProtocol = nil
	config.ReplyAdapter = &dto.ReplyAdapter{Version: 2, Containers: []dto.ReplyContainer{{
		Name: "old_reply", Formatter: true, OutputKind: "text", SourcePromptID: "context",
		SourceFingerprint: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}}}
	request := &dto.GeneralOpenAIRequest{Model: "m", Messages: []dto.Message{{Role: "user", Content: "probe"}}}
	report, err := BuildPresetCapabilityReport(config, request)
	require.NoError(t, err)
	require.Len(t, report.ReplySources, 1)
	assert.Equal(t, "new_reply", report.ReplySources[0].Name)
	assert.NotEmpty(t, report.ReplySources[0].SourceFingerprint)
	assert.Equal(t, "old_reply", config.ReplyAdapter.Containers[0].Name)
	_, _, err = CompileSillyTavernPreset(config, request, SillyTavernContext{})
	require.Error(t, err, "discovery must not authorize a changed source for live requests")
}

func TestPresetCapabilitySourceApprovalTracksCodeNotRequestGeneratedSchema(t *testing.T) {
	settings := map[string]any{
		"ToolBindings": map[string]any{"context": map[string]any{
			"enabled": true, "code": `return {name:"reply",parameters:{type:"object",properties:{text:{type:"string"}}}};`,
		}},
		"FinalRequestProcessing": map[string]any{
			"enabled": true, "script": `({request}) => { request.tools[0].function.description=request.model; return request; }`,
		},
		"OutputPreprocessing": map[string]any{
			"consumeToolCalls": true, "toolCallFormatter": map[string]any{"enabled": true, "script": `(name,args)=>args.text`},
		},
	}
	config := protocolScriptPresetConfig(t, settings)
	config.ToolProtocol = nil
	config.ReplyAdapter = nil
	first := &dto.GeneralOpenAIRequest{Model: "first", Messages: []dto.Message{{Role: "user", Content: "hello"}}}
	second := &dto.GeneralOpenAIRequest{Model: "second", Messages: []dto.Message{{Role: "user", Content: "hello"}}}
	report, err := BuildPresetCapabilityReport(config, first)
	require.NoError(t, err)
	require.Len(t, report.ReplySources, 1)
	source := report.ReplySources[0]
	require.Equal(t, 2, source.SourceFingerprintVersion)
	other, err := BuildPresetCapabilityReport(config, second)
	require.NoError(t, err)
	require.Len(t, other.ReplySources, 1)
	require.Equal(t, source.SourceFingerprint, other.ReplySources[0].SourceFingerprint)
	require.NotEqual(t, source.LegacySourceFingerprint, other.ReplySources[0].LegacySourceFingerprint)
	config.ReplyAdapter = &dto.ReplyAdapter{Version: 2, Containers: []dto.ReplyContainer{source}}
	_, _, err = CompileSillyTavernPreset(config, second, SillyTavernContext{})
	require.NoError(t, err, "changing dynamic descriptions must not revoke approval")

	legacy := source
	legacy.SourceFingerprintVersion = 0
	legacy.SourceFingerprint = source.LegacySourceFingerprint
	config.ReplyAdapter.Containers = []dto.ReplyContainer{legacy}
	_, _, err = CompileSillyTavernPreset(config, first, SillyTavernContext{})
	require.NoError(t, err, "existing approved fingerprints must retain their previous meaning")
	_, _, err = CompileSillyTavernPreset(config, second, SillyTavernContext{})
	require.ErrorContains(t, err, "reply source has changed", "legacy approval must not silently become broader")

	settings["OutputPreprocessing"] = map[string]any{
		"consumeToolCalls": true, "toolCallFormatter": map[string]any{"enabled": true, "script": `(name,args)=>args.text+"changed"`},
	}
	changed := protocolScriptPresetConfig(t, settings)
	changed.ToolProtocol = nil
	changed.ReplyAdapter = &dto.ReplyAdapter{Version: 2, Containers: []dto.ReplyContainer{source}}
	_, _, err = CompileSillyTavernPreset(changed, first, SillyTavernContext{})
	require.ErrorContains(t, err, "reply source has changed")
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
		if scenario.Kind == "other_protocol" || scenario.Kind == "json" || scenario.Kind == "schema" {
			assert.Equal(t, "conflict", scenario.Status, scenario.Kind)
		} else {
			assert.Equal(t, "adapted_unverified", scenario.Status, scenario.Kind)
		}
	}
	assert.True(t, strict.Buffered)
	config.ReplyAdapter.StructuredOutput = true
	converted, err := InspectAdaptationScope("all_supported", "m", config, nil, nil)
	require.NoError(t, err)
	for _, scenario := range converted.Scenarios {
		if scenario.Kind == "json" || scenario.Kind == "schema" {
			assert.Equal(t, "adapted_unverified", scenario.Status, scenario.Kind)
		}
	}
}

func TestCandidateFailureChoicesRejectImpossibleContinueWithoutChangingLiveConfiguration(t *testing.T) {
	config := protocolScriptPresetConfig(t, nil)
	config.ToolProtocol = nil
	config.ReplyAdapter = &dto.ReplyAdapter{Version: 2, Containers: []dto.ReplyContainer{{Name: "reply", Formatter: true, OutputKind: "text"}}}
	config.FailurePolicy = &dto.SillyTavernFailurePolicy{Preset: "request_passthrough", Script: "request_passthrough"}
	report := &AdaptationScopeReport{FailurePlan: effectiveAdaptationFailurePlan(config, nil, nil, "m")}
	require.ErrorContains(t, ValidateAdaptationFailureChoices(report, config), "selected fallback")
	require.Equal(t, "request_passthrough", config.FailurePolicy.Preset, "checking must not mutate the live config")
	config.FailurePolicy.Preset, config.FailurePolicy.Script = "fail", "fail"
	report.FailurePlan = effectiveAdaptationFailurePlan(config, nil, nil, "m")
	require.NoError(t, ValidateAdaptationFailureChoices(report, config))
}
