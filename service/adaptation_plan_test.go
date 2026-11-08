package service

import (
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAdaptationBasePlanKeepsOriginalContractAndMatchedScanSource(t *testing.T) {
	original := &dto.GeneralOpenAIRequest{
		Model: "m",
		Messages: []dto.Message{
			{Role: "user", Content: "before"},
		},
		Tools: []dto.ToolCallRequest{{
			Type: "function",
			Function: dto.FunctionRequest{
				Name:       "reply",
				Parameters: []byte(`{"type":"object"}`),
			},
		}},
		ToolChoice: "required",
	}
	matched := &dto.GeneralOpenAIRequest{
		Model: "m",
		Messages: []dto.Message{
			{Role: "user", Content: "after"},
		},
		Tools:      original.Tools,
		ToolChoice: original.ToolChoice,
	}
	profile := &AdaptationProfile{
		Enabled: true,
		Preset: &dto.SillyTavernPresetConfig{
			Preset: []byte(`{"prompts":[],"prompt_order":[]}`),
		},
	}

	plan, err := BuildBasePlan(profile, original, matched)
	require.NoError(t, err)
	require.NotNil(t, plan)
	assert.Equal(t, ContractTool, plan.Contract)
	assert.Equal(t, "reply", plan.ClientFinal)
	assert.NotSame(t, original, plan.Original)
	assert.NotSame(t, matched, plan.Matched)
	assert.NotEmpty(t, plan.Fingerprint)

	original.Messages[0].Content = "mutated"
	assert.Equal(t, "before", plan.Original.Messages[0].Content, "the plan must freeze its original snapshot")
}

func TestAdaptationFailurePolicyUsesStageDefaultsAndExplicitOverrides(t *testing.T) {
	profile := &AdaptationProfile{
		Enabled: true,
		Preset: &dto.SillyTavernPresetConfig{
			Preset: []byte(`{"prompts":[],"prompt_order":[]}`),
		},
	}
	policy := profile.FailurePolicy()
	assert.Equal(t, FailureSkip, policy.Mode(FailureStageWorldbook))
	assert.Equal(t, FailureRequestPassthrough, policy.Mode(FailureStagePreflight))
	assert.Equal(t, FailureToolProtocolPassthrough, policy.Mode(FailureStageToolResolution))
	assert.Equal(t, FailureSafeRaw, policy.Mode(FailureStageResponseValidation))

	profile = &AdaptationProfile{
		Enabled: true,
		Preset: &dto.SillyTavernPresetConfig{
			Preset: []byte(`{"prompts":[],"prompt_order":[]}`),
			FailurePolicy: &dto.SillyTavernFailurePolicy{
				Worldbook:          "fail",
				ToolResolution:     "fail",
				ResponseValidation: "fail",
			},
		},
	}
	policy = profile.FailurePolicy()
	assert.Equal(t, FailureFail, policy.Mode(FailureStageWorldbook))
	assert.Equal(t, FailureFail, policy.Mode(FailureStageToolResolution))
	assert.Equal(t, FailureFail, policy.Mode(FailureStageResponseValidation))
}

func TestAdaptationPlanFingerprintChangesWithPolicy(t *testing.T) {
	original := &dto.GeneralOpenAIRequest{Model: "m", Messages: []dto.Message{{Role: "user", Content: "hello"}}}
	baseProfile := &AdaptationProfile{Enabled: true, Preset: &dto.SillyTavernPresetConfig{
		Preset: []byte(`{"prompts":[],"prompt_order":[]}`),
	}}
	first, err := BuildBasePlan(baseProfile, original, original)
	require.NoError(t, err)

	changedProfile := &AdaptationProfile{Enabled: true, Preset: &dto.SillyTavernPresetConfig{
		Preset: []byte(`{"prompts":[],"prompt_order":[]}`),
		ToolProtocol: &dto.SillyTavernToolProtocol{
			Enabled:     true,
			ClientFinal: "reply",
		},
	}}
	second, err := BuildBasePlan(changedProfile, original, original)
	require.NoError(t, err)
	assert.NotEqual(t, first.ProfileFingerprint, second.ProfileFingerprint)
	assert.NotEqual(t, first.Fingerprint, second.Fingerprint)
	assert.False(t, SameAdaptationPlan(first, second))
	assert.True(t, SameAdaptationPlan(first, first))
	assert.False(t, SameAdaptationPlan(first, nil))
	assert.True(t, SameAdaptationPlan(nil, nil))
}

func TestAdaptationProfileFingerprintIsDeterministic(t *testing.T) {
	preset := &dto.SillyTavernPresetConfig{Preset: []byte(`{"prompts":[],"prompt_order":[]}`)}
	first, err := AdaptationProfileFingerprint(&AdaptationProfile{Enabled: true, Preset: preset})
	require.NoError(t, err)
	second, err := AdaptationProfileFingerprint(&AdaptationProfile{Enabled: true, Preset: preset})
	require.NoError(t, err)
	assert.Equal(t, first, second)

	changed, err := AdaptationProfileFingerprint(&AdaptationProfile{Enabled: true, Preset: &dto.SillyTavernPresetConfig{
		Preset:       []byte(`{"prompts":[],"prompt_order":[]}`),
		ToolProtocol: &dto.SillyTavernToolProtocol{Enabled: true, ClientFinal: "reply"},
	}})
	require.NoError(t, err)
	assert.NotEqual(t, first, changed)
}

func TestAdaptationPlanMetadataCacheIsBoundedAndRequestScoped(t *testing.T) {
	clearAdaptationPlanMetadataCache()
	profile := &AdaptationProfile{Enabled: true, Preset: &dto.SillyTavernPresetConfig{
		Preset: []byte(`{"prompts":[],"prompt_order":[]}`),
	}}
	firstRequest := &dto.GeneralOpenAIRequest{Model: "m", Messages: []dto.Message{{Role: "user", Content: "one"}}}
	secondRequest := &dto.GeneralOpenAIRequest{Model: "m", Messages: []dto.Message{{Role: "user", Content: "two"}}}

	first, err := BuildBasePlan(profile, firstRequest, firstRequest)
	require.NoError(t, err)
	require.NotNil(t, first)
	second, err := BuildBasePlan(profile, firstRequest, firstRequest)
	require.NoError(t, err)
	require.NotNil(t, second)
	third, err := BuildBasePlan(profile, secondRequest, secondRequest)
	require.NoError(t, err)
	require.NotNil(t, third)

	adaptationPlanMetadataCache.Lock()
	entries := len(adaptationPlanMetadataCache.entries)
	adaptationPlanMetadataCache.Unlock()
	assert.Equal(t, 2, entries)
	assert.NotSame(t, first.Original, second.Original)
	assert.Equal(t, "one", first.Original.Messages[0].Content)
	assert.Equal(t, "two", third.Original.Messages[0].Content)
}

func TestResolveAdaptationPlanFreezesPhaseAndRoleIdentity(t *testing.T) {
	original := &dto.GeneralOpenAIRequest{Model: "m", Messages: []dto.Message{{Role: "user", Content: "hello"}}}
	profile := &AdaptationProfile{Enabled: true, Preset: &dto.SillyTavernPresetConfig{Preset: []byte(`{"prompts":[],"prompt_order":[]}`)}}
	base, err := BuildBasePlan(profile, original, original)
	require.NoError(t, err)
	require.NotNil(t, base)

	first := ResolveAdaptationPlan(base, &presetProtocolPlan{
		audit:       PresetProtocolAudit{Fingerprint: "protocol-a", Activated: true},
		phase:       presetProtocolPhaseAuxiliary,
		finalTool:   "reply",
		clientFinal: "reply",
	})
	require.NotNil(t, first)
	assert.Equal(t, presetProtocolPhaseAuxiliary, first.Phase)
	assert.Equal(t, "reply", first.FinalTool)
	assert.NotEmpty(t, first.Fingerprint)

	second := ResolveAdaptationPlan(base, &presetProtocolPlan{
		audit:     PresetProtocolAudit{Fingerprint: "protocol-b", Activated: true},
		phase:     presetProtocolPhaseFinal,
		finalTool: "reply",
	})
	require.NotNil(t, second)
	assert.NotEqual(t, first.Fingerprint, second.Fingerprint, "a changed protocol resolution must change the resolved fingerprint")
}
