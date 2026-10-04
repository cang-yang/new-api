package service

import (
	"os/exec"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/stretchr/testify/require"
)

// Public synthetic fixtures run without private presets. Node is an independent
// JavaScript oracle, not a mock of the gateway's regexp2 executor.
func TestSillyTavernRegexJavaScriptContract(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node is required for the JavaScript differential oracle")
	}
	for _, tc := range []struct{ Name, Pattern, Flags, Input, Replacement string }{
		{"multiline thinking", `<thinking>[\s\S]*?</thinking>`, "gi", "<thinking>line1\nline2</thinking>body", ""},
		{"global captures", `<body>(.*?)</body>`, "gs", "before<body>one</body>between<body>two</body>after", "$1"},
		{"named and numbered captures", `(?<first>a)(b)`, "g", "ab ab", "$1:$2:$<first>"},
		{"lookbehind", `(?<=<body>)(.*?)(?=</body>)`, "gs", "<body>你好</body>", "$1!"},
		{"zero width and unicode suffix", `^|$`, "g", "你好", "!"},
		{"unicode scalar dot", `.`, "gu", "😀", "X"},
	} {
		t.Run(tc.Name, func(t *testing.T) {
			raw, err := common.Marshal(tc)
			require.NoError(t, err)
			output, err := exec.Command(node, "-e", `const x=JSON.parse(process.argv[1]);process.stdout.write(JSON.stringify(x.Input.replace(new RegExp(x.Pattern,x.Flags),x.Replacement)))`, string(raw)).Output()
			require.NoError(t, err)
			var want string
			require.NoError(t, common.Unmarshal(output, &want))
			pattern, global, err := dto.CompileTextRegex("/" + tc.Pattern + "/" + tc.Flags)
			require.NoError(t, err)
			got, err := (presetResponseRegex{pattern: pattern, global: global, replaceBy: tc.Replacement}).replace(tc.Input)
			require.NoError(t, err)
			require.Equal(t, want, got)
		})
	}
}

func TestSillyTavernRegexDocumentsUTF16Boundary(t *testing.T) {
	// Without /u JavaScript treats this emoji as two UTF-16 code units, while
	// regexp2 uses one Unicode scalar. Do not claim full JS equivalence or
	// silently rewrite the input to surrogate pairs (which breaks captures).
	pattern, global, err := dto.CompileTextRegex("/./g")
	require.NoError(t, err)
	got, err := (presetResponseRegex{pattern: pattern, global: global, replaceBy: "X"}).replace("😀")
	require.NoError(t, err)
	require.Equal(t, "X", got)
}
