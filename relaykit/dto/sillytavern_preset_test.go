package dto

import (
	"encoding/json"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/relayconvert/kitutil"
	"github.com/stretchr/testify/require"
)

func TestPresetRegexSourcesMergeByIdentity(t *testing.T) {
	first := json.RawMessage(`{"id":"history","scriptName":"Hide earlier analysis","findRegex":"/secret/g","replaceString":"","placement":[2],"promptOnly":true}`)
	copyWithDifferentKeyOrder := json.RawMessage(`{"placement":[2],"replaceString":"","findRegex":"/secret/g","scriptName":"Hide earlier analysis","promptOnly":true,"id":"history"}`)
	second := json.RawMessage(`{"id":"reply","scriptName":"Clean reply","findRegex":"/tag/g","replaceString":"","placement":[2]}`)
	for _, tc := range []struct {
		name       string
		top, bound []json.RawMessage
		want       int
		fail       bool
	}{
		{"same rule is not executed twice", []json.RawMessage{first}, []json.RawMessage{copyWithDifferentKeyOrder, second}, 2, false},
		{"binding-only rules remain available", nil, []json.RawMessage{first}, 1, false},
		{"conflicting copies require review", []json.RawMessage{first}, []json.RawMessage{json.RawMessage(`{"id":"history","findRegex":"different"}`)}, 0, true},
		{"missing identities cannot be linked safely", []json.RawMessage{json.RawMessage(`{"findRegex":"x"}`)}, nil, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data, err := kitutil.Marshal(map[string]any{"extensions": map[string]any{"regex_scripts": tc.top, "SPreset": map[string]any{"RegexBinding": map[string]any{"regexes": tc.bound}}}})
			require.NoError(t, err)
			scripts, err := MergeSillyTavernRegexScripts(data)
			if tc.fail {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Len(t, scripts, tc.want)
			require.Equal(t, "history", scripts[0].ID)
		})
	}
}

func TestSillyTavernToolProtocolValidation(t *testing.T) {
	valid := &SillyTavernPresetConfig{
		Preset: []byte(`{"prompts":[{"identifier":"main","content":"x"}],"prompt_order":[{"order":[{"identifier":"main","enabled":true}]}]}`),
		ToolProtocol: &SillyTavernToolProtocol{
			ClientFinal: "client_reply",
			Auxiliary:   []string{"context"},
			Action:      []string{"lookup"},
			Content:     []string{"reply_payload"},
			PhasePolicy: "always_final",
		},
	}
	_, err := valid.ParseAndValidate()
	require.NoError(t, err)

	invalid := *valid
	invalid.ToolProtocol = &SillyTavernToolProtocol{ResponseTarget: "xml"}
	_, err = invalid.ParseAndValidate()
	require.ErrorContains(t, err, "tool_protocol response_target")

	withArgument := &SillyTavernPresetConfig{
		Preset: valid.Preset,
		ToolProtocol: &SillyTavernToolProtocol{
			PresetFinal:      "answer",
			ResponseTarget:   "json",
			ResponseArgument: "extra.payload",
		},
	}
	_, err = withArgument.ParseAndValidate()
	require.NoError(t, err)

	invalidArgument := *withArgument
	invalidArgument.ToolProtocol = &SillyTavernToolProtocol{
		PresetFinal:      "answer",
		ResponseTarget:   "auto",
		ResponseArgument: "extra",
	}
	_, err = invalidArgument.ParseAndValidate()
	require.ErrorContains(t, err, "response_argument requires response_target")

	invalidClientArgument := *withArgument
	invalidClientArgument.ToolProtocol = &SillyTavernToolProtocol{
		ClientFinal:      "reply",
		ResponseTarget:   "json",
		ResponseArgument: "extra",
	}
	_, err = invalidClientArgument.ParseAndValidate()
	require.ErrorContains(t, err, "response_argument requires a preset_final")

	invalidPhasePolicy := *valid
	invalidPhasePolicy.ToolProtocol = &SillyTavernToolProtocol{PhasePolicy: "sometimes"}
	_, err = invalidPhasePolicy.ParseAndValidate()
	require.ErrorContains(t, err, "tool_protocol phase_policy")

	invalidContent := *valid
	invalidContent.ToolProtocol = &SillyTavernToolProtocol{Content: []string{"reply_payload", "reply_payload"}}
	_, err = invalidContent.ParseAndValidate()
	require.ErrorContains(t, err, "tool_protocol content names")

	invalidVisibility := *valid
	invalidVisibility.ToolProtocol = &SillyTavernToolProtocol{
		Action: []string{"lookup"},
		VisibilityPolicy: &SillyTavernToolVisibility{
			Action: "gateway_internal",
		},
	}
	_, err = invalidVisibility.ParseAndValidate()
	require.ErrorContains(t, err, "tool_protocol visibility")

	conflictingRoles := *valid
	conflictingRoles.ToolProtocol = &SillyTavernToolProtocol{
		Auxiliary: []string{"context"},
		Action:    []string{"context"},
	}
	_, err = conflictingRoles.ParseAndValidate()
	require.ErrorContains(t, err, "tool_protocol tool roles")

	invalidFailurePolicy := *valid
	invalidFailurePolicy.FailurePolicy = &SillyTavernFailurePolicy{Worldbook: "explode"}
	_, err = invalidFailurePolicy.ParseAndValidate()
	require.ErrorContains(t, err, "failure_policy.worldbook")
}

func TestReplyAdapterGenerationCapabilities(t *testing.T) {
	valid := ReplyAdapter{Version: 2, StructuredOutput: true, RequireContainer: true, FormattedOutput: "client_format",
		Containers: []ReplyContainer{{Name: "reply", Formatter: true, OutputKind: "text"}}}
	require.NoError(t, valid.Validate())
	encoded, err := kitutil.Marshal(valid)
	require.NoError(t, err)
	var decoded ReplyAdapter
	require.NoError(t, kitutil.Unmarshal(encoded, &decoded))
	require.Equal(t, valid, decoded)
	for _, tc := range []struct {
		name string
		edit func(*ReplyAdapter)
		want string
	}{
		{"missing required source", func(a *ReplyAdapter) { a.StructuredOutput = false; a.FormattedOutput = ""; a.Containers = nil }, "requires a declared reply source"},
		{"unknown boundary", func(a *ReplyAdapter) { a.FormattedOutput = "guess" }, "unsupported formatted reply output"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate := valid
			tc.edit(&candidate)
			require.ErrorContains(t, candidate.Validate(), tc.want)
		})
	}
	legacy := valid
	legacy.RequireContainer, legacy.StructuredOutput, legacy.FormattedOutput = false, false, ""
	require.NoError(t, legacy.Validate(), "existing optional text adapters remain valid")
	valid.StructuredOutput = false
	require.NoError(t, valid.Validate(), "disabling conversion keeps the formatter boundary for later re-enablement")
}
