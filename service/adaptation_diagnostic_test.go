package service

import (
	"encoding/json"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
		"book_id": "book",
		"id":      "entry",
		"name":    "City",
		"status":  "matched",
		"reason":  "keyword",
		"order":   0,
		"placement": map[string]any{
			"mode": "before_last_user", "index": 3, "role": "system", "fallback": false,
		},
	}, entries[0])
	assert.Equal(t, "frozen", stages["plan"].Status)
	assert.Equal(t, "resolved-fingerprint", stages["plan"].Details["fingerprint"])
}
