package service

import (
	"encoding/json"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPresetAuditDoesNotSerializePreviewOrRequestBodies(t *testing.T) {
	trace := PresetCompileTrace{
		Worldbook: &WorldbookPreview{
			Text:         "SECRET_WORLDBOOK_BODY",
			FinalRequest: &dto.GeneralOpenAIRequest{Messages: []dto.Message{{Role: "user", Content: "SECRET_FINAL_REQUEST"}}},
			Entries:      []WorldbookDecision{{ID: "entry", Inserted: true}},
		},
		Plan: &AdaptationResolvedPlan{Base: &AdaptationBasePlan{Original: &dto.GeneralOpenAIRequest{Messages: []dto.Message{{Role: "user", Content: "SECRET_ORIGINAL"}}}}},
	}
	encoded, err := common.Marshal(trace)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "SECRET_")
	assert.NotContains(t, string(encoded), "final_request")
	assert.Contains(t, string(encoded), `"inserted":true`)
	assert.Equal(t, "SECRET_WORLDBOOK_BODY", trace.Worldbook.Text, "audit serialization must not mutate a live preview")
}

func TestAdaptationDiagnosticDistinguishesWorldbookFallback(t *testing.T) {
	for _, test := range []struct{ phase, reason, status string }{
		{"skipped", "failure_policy", "fallback"},
		{"skipped", "body_passthrough", "skipped"},
		{"final_request", "", "completed"},
	} {
		t.Run(test.reason+test.status, func(t *testing.T) {
			diagnostic := BuildAdaptationDiagnostic(&PresetCompileTrace{Worldbook: &WorldbookPreview{
				Phase: test.phase, BypassReason: test.reason, PlacementPhase: "before_scripts",
			}})
			require.NotNil(t, diagnostic)
			stage := diagnostic.Stages[0]
			assert.Equal(t, "worldbook", stage.Stage)
			assert.Equal(t, test.status, stage.Status)
			assert.Equal(t, "before_scripts", stage.Details["placement_phase"])
			if test.reason != "" {
				assert.NotContains(t, stage.Summary, "completed")
				assert.Equal(t, test.reason, stage.Details["bypass_reason"])
			}
		})
	}
}

func TestBuildAdaptationDiagnosticRecordsDecisionStagesWithoutBodies(t *testing.T) {
	trace := &PresetCompileTrace{
		Name: "fixture preset",
		Mode: "compatibility",
		Worldbook: &WorldbookPreview{
			Entries: []WorldbookDecision{
				{
					BookID:    "book",
					ID:        "entry",
					Name:      "City",
					Status:    "matched",
					Reason:    "keyword",
					Placement: &dto.WorldbookPlacement{Mode: "before_last_user", Index: 3, Role: "system"},
				},
			},
			Placement: &dto.WorldbookPlacement{Mode: "before_last_user", Index: 3, Role: "system"},
		},
		Protocol: &PresetProtocolAudit{
			Enabled:     true,
			Eligible:    true,
			Activated:   true,
			Reason:      "projection_planned",
			Contract:    presetProtocolContractJSON,
			FinalTool:   "preset_reply",
			Contents:    []string{"preset_reply"},
			Visibility:  "content=gateway_internal",
			Fingerprint: "protocol-fingerprint",
		},
		Plan: &AdaptationResolvedPlan{
			Base: &AdaptationBasePlan{
				Contract:           ContractJSON,
				ProfileFingerprint: "profile-fingerprint",
				Fingerprint:        "base-fingerprint",
			},
			ProtocolFingerprint: "protocol-fingerprint",
			Phase:               presetProtocolPhaseFinal,
			FinalTool:           "preset_reply",
			Fingerprint:         "resolved-fingerprint",
		},
	}

	diagnostic := BuildAdaptationDiagnostic(trace)
	require.NotNil(t, diagnostic)
	require.GreaterOrEqual(t, len(diagnostic.Stages), 5)

	encoded, err := json.Marshal(diagnostic)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "SECRET_BODY")
	assert.Contains(t, string(encoded), "before_last_user")
	assert.Contains(t, string(encoded), "preset_reply")

	stages := map[string]AdaptationDiagnosticStage{}
	for _, stage := range diagnostic.Stages {
		stages[stage.Stage] = stage
	}
	assert.Equal(t, "completed", stages["preflight"].Status)
	entries, ok := stages["worldbook"].Details["entries"].([]map[string]any)
	require.True(t, ok)
	require.Len(t, entries, 1)
	assert.Equal(t, map[string]any{
		"book_id":   "book",
		"id":        "entry",
		"name":      "City",
		"status":    "matched",
		"reason":    "keyword",
		"order":     0,
		"eligible":  false,
		"triggered": false,
		"selected":  false,
		"inserted":  false,
		"placement": map[string]any{
			"mode": "before_last_user", "index": 3, "role": "system", "fallback": false,
		},
	}, entries[0])
	assert.Equal(t, "frozen", stages["plan"].Status)
	assert.Equal(t, "resolved-fingerprint", stages["plan"].Details["fingerprint"])
}
