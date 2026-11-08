package dto

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSillyTavernToolProtocolValidation(t *testing.T) {
	valid := &SillyTavernPresetConfig{
		Preset: []byte(`{"prompts":[{"identifier":"main","content":"x"}],"prompt_order":[{"order":[{"identifier":"main","enabled":true}]}]}`),
		ToolProtocol: &SillyTavernToolProtocol{
			ClientFinal: "client_reply",
			Auxiliary:   []string{"context"},
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
}
