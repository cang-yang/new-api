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
}
