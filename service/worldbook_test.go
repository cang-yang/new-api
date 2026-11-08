package service

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorldbookMatchingAndIsolation(t *testing.T) {
	entry := dto.WorldbookEntry{ID: "one", Enabled: true, Content: "Moon lore", Keys: []string{"moon"}, SecondaryKeys: []string{"silver"}, SecondaryLogic: "all", WholeWords: true}
	config := &dto.WorldbookConfig{Enabled: true, Books: []dto.Worldbook{{ID: "book", Enabled: true, Entries: []dto.WorldbookEntry{entry}}}}
	request := &dto.GeneralOpenAIRequest{Messages: []dto.Message{{Role: "system", Content: "hidden silver moon"}, {Role: "user", Content: "moonlight silver"}}}
	preview, err := MatchWorldbook(config, request)
	require.NoError(t, err)
	assert.Empty(t, preview.Text, "system and partial words must not match")
	request.Messages[1].Content = "SILVER moon"
	preview, err = MatchWorldbook(config, request)
	require.NoError(t, err)
	assert.Equal(t, "Moon lore", preview.Text)
	compiled, _, err := CompileWorldbook(config, nil, request, SillyTavernContext{})
	require.NoError(t, err)
	require.Len(t, compiled.Messages, 3)
	assert.Equal(t, "Moon lore", compiled.Messages[1].Content)
	compiled.Messages[0].Content = "changed"
	assert.Equal(t, "hidden silver moon", request.Messages[0].Content, "input request mutated")
	second, _, _ := CompileWorldbook(config, nil, request, SillyTavernContext{})
	assert.Len(t, second.Messages, 3, "retry accumulated injection")
}

func TestWorldbookBudgetRecursionAndImport(t *testing.T) {
	config := &dto.WorldbookConfig{Enabled: true, MaxChars: 256, Recursive: true, Books: []dto.Worldbook{{ID: "b", Enabled: true, Entries: []dto.WorldbookEntry{
		{ID: "a", Enabled: true, Constant: true, Content: "silver", Priority: 9},
		{ID: "b", Enabled: true, Keys: []string{"silver"}, Content: "moon"},
		{ID: "c", Enabled: true, Keys: []string{"moon"}, Content: "stars"},
		{ID: "d", Enabled: true, Keys: []string{"stars"}, Content: "too far"},
		{ID: "e", Enabled: true, Constant: true, Content: strings.Repeat("界", 251)},
		{ID: "f", Enabled: true, Constant: true, Content: "silver"},
	}}}}
	result, err := MatchWorldbook(config, &dto.GeneralOpenAIRequest{})
	require.NoError(t, err)
	assert.Equal(t, "moon\n\nstars\n\nsilver", result.Text)
	assert.Equal(t, "no_match", result.Entries[3].Reason)
	assert.Equal(t, "budget", result.Entries[4].Reason)
	assert.Equal(t, "duplicate", result.Entries[5].Reason)
	book, err := ImportWorldbook("Lore", []byte(`{"entries":{"0":{"uid":0,"key":["moon"],"keysecondary":["night"],"selective":true,"selectiveLogic":1,"content":"lore","probability":50,"position":4}}}`))
	require.NoError(t, err)
	require.Len(t, book.Entries, 1)
	assert.Empty(t, book.Entries[0].Issues)
	assertWorldbookNormalizedField(t, book.Entries[0], "probability")
	assertWorldbookNormalizedField(t, book.Entries[0], "position")
	assert.Equal(t, "not_all", book.Entries[0].SecondaryLogic)
	assert.NotEmpty(t, book.Entries[0].Source)
	config.Books[0].Entries = []dto.WorldbookEntry{{ID: "unicode", Enabled: true, Constant: true, Content: strings.Repeat("界", 256)}}
	result, err = MatchWorldbook(config, &dto.GeneralOpenAIRequest{})
	require.NoError(t, err)
	assert.Equal(t, strings.Repeat("界", 256), result.Text, "budget counts Unicode characters")
}

func TestWorldbookRegexAndValidation(t *testing.T) {
	config := &dto.WorldbookConfig{Enabled: true, Books: []dto.Worldbook{{ID: "b", Enabled: true, Entries: []dto.WorldbookEntry{{ID: "r", Enabled: true, Keys: []string{"/^MOON$/im"}, Content: "lore"}}}}}
	result, err := MatchWorldbook(config, &dto.GeneralOpenAIRequest{Messages: []dto.Message{{Role: "user", Content: "night\nmoon"}}})
	require.NoError(t, err)
	assert.Equal(t, "lore", result.Text)
	config.Books[0].Entries[0].Keys = []string{"/moon/u"}
	assert.Error(t, config.Validate(), "unsupported regex flag accepted")
	book, err := ImportWorldbook("invalid regex", []byte(`{"entries":[{"key":["/(?=moon)/"],"content":"lore"}]}`))
	require.NoError(t, err)
	assert.Contains(t, book.Entries[0].Issues, "regex", "invalid imported regex must be blocked")
}

func TestWorldbookSecondaryConditionsAndTextScope(t *testing.T) {
	for _, test := range []struct {
		logic, text string
		want        bool
	}{
		{"none", "moon", true}, {"any", "moon silver", true}, {"any", "moon", false},
		{"all", "moon silver night", true}, {"all", "moon silver", false},
		{"not_any", "moon", true}, {"not_any", "moon night", false},
		{"not_all", "moon silver", true}, {"not_all", "moon silver night", false},
	} {
		t.Run(test.logic+test.text, func(t *testing.T) {
			entry := dto.WorldbookEntry{Enabled: true, Keys: []string{"moon"}, SecondaryKeys: []string{"silver", "night"}, SecondaryLogic: test.logic}
			assert.Equal(t, test.want, worldbookEntryMatches(entry, test.text, nil))
		})
	}
	assert.False(t, worldbookEntryMatches(dto.WorldbookEntry{Keys: []string{"cafe"}, WholeWords: true}, "cafe\u0301", nil), "combining marks belong to the word")
	config := &dto.WorldbookConfig{Enabled: true, ScanDepth: 1, Books: []dto.Worldbook{{ID: "b", Enabled: true, Entries: []dto.WorldbookEntry{{ID: "e", Enabled: true, Keys: []string{"moon"}, Content: "lore"}}}}}
	request := &dto.GeneralOpenAIRequest{Messages: []dto.Message{{Role: "user", Content: "moon"}, {Role: "assistant", Content: []any{map[string]any{"type": "image_url", "image_url": "moon"}, map[string]any{"type": "text", "text": "night"}}}, {Role: "tool", Content: "moon"}}}
	result, err := MatchWorldbook(config, request)
	require.NoError(t, err)
	assert.Equal(t, "lore", result.Text, "the latest tool result must be scanned")
	request.Messages = []dto.Message{{Role: "tool", Content: "nothing"}, {Role: "assistant", Content: []any{map[string]any{"type": "image_url", "image_url": "moon"}, map[string]any{"type": "text", "text": "night"}}}}
	result, err = MatchWorldbook(config, request)
	require.NoError(t, err)
	assert.Empty(t, result.Text, "media metadata and older turns must not match")
	request.Messages = []dto.Message{{Role: "user", Content: "moon" + strings.Repeat("x", 256<<10)}}
	result, err = MatchWorldbook(config, request)
	require.NoError(t, err)
	assert.Empty(t, result.Text, "scan uses bounded recent text")
	assert.NotEmpty(t, result.Warnings)
}

func TestWorldbookEmptySecondaryKeysDoNotBlockPrimaryMatch(t *testing.T) {
	for logic, name := range []string{"any", "not_all", "not_any", "all"} {
		t.Run(name, func(t *testing.T) {
			book, err := ImportWorldbook("background", []byte(fmt.Sprintf(`{"entries":{"0":{"key":["moon"],"keysecondary":[],"selective":true,"selectiveLogic":%d,"content":"Moon background"}}}`, logic)))
			require.NoError(t, err)
			require.Len(t, book.Entries, 1)
			assert.Equal(t, name, book.Entries[0].SecondaryLogic)
			config := &dto.WorldbookConfig{Enabled: true, Books: []dto.Worldbook{book}}
			request := &dto.GeneralOpenAIRequest{Messages: []dto.Message{{Role: "user", Content: "moon"}}}

			compiled, trace, err := CompileWorldbook(config, nil, request, SillyTavernContext{})
			require.NoError(t, err)
			require.NotNil(t, trace.Worldbook)
			assert.Equal(t, "included", trace.Worldbook.Entries[0].Reason)
			require.Len(t, compiled.Messages, 2)
			assert.Equal(t, "Moon background", compiled.Messages[0].Content)
			assert.Len(t, request.Messages, 1, "the client request must not be mutated")

			request.Messages[0].Content = "unrelated"
			preview, err := MatchWorldbook(config, request)
			require.NoError(t, err)
			assert.Empty(t, preview.Text, "an empty secondary list must not bypass the primary trigger")
			assert.Equal(t, "no_match", preview.Entries[0].Reason)
		})
	}
}

func TestWorldbookScansToolCallAndToolResultText(t *testing.T) {
	config := &dto.WorldbookConfig{Enabled: true, ScanDepth: 3, Books: []dto.Worldbook{{
		ID:      "b",
		Enabled: true,
		Entries: []dto.WorldbookEntry{
			{ID: "call", Enabled: true, Keys: []string{"moon"}, Content: "call lore"},
			{ID: "result", Enabled: true, Keys: []string{"night"}, Content: "result lore"},
		},
	}}}
	request := &dto.GeneralOpenAIRequest{Messages: []dto.Message{
		{
			Role:      "assistant",
			Content:   nil,
			ToolCalls: json.RawMessage(`[{"id":"call_1","type":"function","function":{"name":"context","arguments":"{\"query\":\"moon\"}"}}]`),
		},
		{Role: "tool", Content: "night result"},
		{Role: "user", Content: "continue"},
	}}

	result, err := MatchWorldbook(config, request)
	require.NoError(t, err)
	assert.Equal(t, "call lore\n\nresult lore", result.Text)
	assert.Equal(t, "included", result.Entries[0].Reason)
	assert.Equal(t, "included", result.Entries[1].Reason)
}

func TestWorldbookFinalPriorityAndImportedRestrictions(t *testing.T) {
	config := &dto.WorldbookConfig{Enabled: true, Recursive: true, Books: []dto.Worldbook{{ID: "b", Enabled: true, Entries: []dto.WorldbookEntry{
		{ID: "a", Enabled: true, Constant: true, Content: "silver"},
		{ID: "b", Enabled: true, Keys: []string{"silver"}, Content: "priority", Priority: 100},
	}}}}
	result, err := MatchWorldbook(config, &dto.GeneralOpenAIRequest{})
	require.NoError(t, err)
	assert.Equal(t, "silver\n\npriority", result.Text)
	assert.Equal(t, 1, result.Entries[0].Order)
	assert.Equal(t, 2, result.Entries[1].Order)
	book, err := ImportWorldbook("malformed", []byte(`{"entries":[{"key":["moon"],"content":"lore","selective":"true"}]}`))
	require.NoError(t, err)
	assert.NotEmpty(t, book.Entries[0].Issues, "malformed restriction must block")
	book, err = ImportWorldbook("override", []byte(`{"scan_depth":2,"entries":[{"constant":true,"content":"lore"}]}`))
	require.NoError(t, err)
	assertWorldbookNormalizedField(t, book.Entries[0], "book.scan_depth")
	assert.NotNil(t, book.Entries[0].Keys)
	assert.NotNil(t, book.Entries[0].SecondaryKeys)
	book, err = ImportWorldbook("empty", []byte(`{"id":"exported","enabled":true,"entries":[]}`))
	require.NoError(t, err)
	assert.NotNil(t, book.Entries)
}

func TestWorldbookPreviewRunsBeforePresetCompilation(t *testing.T) {
	config := &dto.WorldbookConfig{
		Enabled:       true,
		InjectionMode: dto.WorldbookInjectionInherit,
		Books: []dto.Worldbook{{
			ID:      "b",
			Enabled: true,
			Entries: []dto.WorldbookEntry{{
				ID:       "e",
				Enabled:  true,
				Constant: true,
				Content:  "lore",
			}},
		}},
	}
	preset := &dto.SillyTavernPresetConfig{Preset: json.RawMessage(`{"prompts":[{"identifier":"chatHistory","marker":true}],"prompt_order":[{"order":[{"identifier":"chatHistory","enabled":true}]}]}`)}
	request := &dto.GeneralOpenAIRequest{Messages: []dto.Message{{Role: "user", Content: "hello"}}}

	preview, err := PreviewWorldbook(config, preset, request)
	require.NoError(t, err)
	require.NotNil(t, preview.Placement)
	assert.Equal(t, dto.WorldbookInjectionSystemPrelude, preview.Placement.Mode)
	assert.Equal(t, 0, preview.Placement.Index)
	assert.Equal(t, "system", preview.Placement.Role)
	assert.True(t, preview.Placement.Fallback, "a preset without worldbook markers uses the background fallback")
}

func TestWorldbookPresetProcessesInsertedLore(t *testing.T) {
	lore := "lore {{user}}"
	config := &dto.WorldbookConfig{Enabled: true, Books: []dto.Worldbook{{ID: "b", Enabled: true, Entries: []dto.WorldbookEntry{{ID: "e", Enabled: true, Constant: true, Content: lore}}}}}
	for _, enabled := range []bool{true, false} {
		preset := &dto.SillyTavernPresetConfig{User: "Alice", Preset: json.RawMessage(fmt.Sprintf(`{"prompts":[{"identifier":"optional","content":"optional"},{"identifier":"chatHistory","marker":true}],"prompt_order":[{"order":[{"identifier":"optional","enabled":%v},{"identifier":"chatHistory","enabled":true}]}]}`, enabled))}
		context := SillyTavernContext{Markers: map[string]string{"worldInfoBefore": "explicit lore"}}
		request := &dto.GeneralOpenAIRequest{Messages: []dto.Message{{Role: "user", Content: "hello"}}}
		compiled, _, err := CompileWorldbook(config, preset, request, context)
		require.NoError(t, err)
		joined := ""
		for _, message := range compiled.Messages {
			if text, ok := message.Content.(string); ok {
				joined += text
			}
		}
		assert.Equal(t, 1, strings.Count(joined, "lore Alice"))
		assert.NotContains(t, joined, lore)
		assert.Equal(t, "explicit lore", context.Markers["worldInfoBefore"])
		assert.Len(t, request.Messages, 1)
	}
}

func TestWorldbookPresetHistoryProcessesInsertedLore(t *testing.T) {
	config := &dto.WorldbookConfig{Enabled: true, Books: []dto.Worldbook{{ID: "b", Enabled: true, Entries: []dto.WorldbookEntry{{ID: "e", Enabled: true, Keys: []string{"moon"}, Content: "lore"}}}}}
	preset := &dto.SillyTavernPresetConfig{User: "Alice", Preset: json.RawMessage(`{"prompts":[{"identifier":"chatHistory","marker":true}],"prompt_order":[{"order":[{"identifier":"chatHistory","enabled":true}]}]}`)}
	source := &dto.GeneralOpenAIRequest{Messages: []dto.Message{{Role: "user", Content: "ignored"}}}
	compiled, _, err := CompileWorldbook(config, preset, source, SillyTavernContext{ChatHistory: []dto.Message{{Role: "user", Content: "moon"}}})
	require.NoError(t, err)
	require.Len(t, compiled.Messages, 2)
	assert.Equal(t, "lore", compiled.Messages[0].Content)
	assert.Equal(t, "moon", compiled.Messages[1].Content)
	assert.Len(t, source.Messages, 1)
	assert.Equal(t, "ignored", source.Messages[0].Content)
}

func TestWorldbookRunsBeforePresetScripts(t *testing.T) {
	config := &dto.WorldbookConfig{Enabled: true, Books: []dto.Worldbook{{ID: "b", Enabled: true, Entries: []dto.WorldbookEntry{{ID: "e", Enabled: true, Keys: []string{"moon"}, SecondaryLogic: "any", Content: "lore"}}}}}
	preset := scriptPresetConfig(t, map[string]any{"FinalRequestProcessing": map[string]any{
		"enabled": true,
		"script":  `({request}) => { for (const message of request.messages) { if (typeof message.content === "string") message.content = message.content.toUpperCase(); } return request; }`,
	}})
	request := &dto.GeneralOpenAIRequest{Model: "m", Messages: []dto.Message{{Role: "user", Content: "moon"}}}

	compiled, trace, err := CompileWorldbook(config, preset, request, SillyTavernContext{})
	require.NoError(t, err)
	require.Len(t, compiled.Messages, 3)
	assert.True(t, slices.ContainsFunc(compiled.Messages, func(message dto.Message) bool {
		return message.Content == "LORE"
	}), "the preset script must see worldbook content before it runs")
	require.NotNil(t, trace.Worldbook)
	assert.Equal(t, "included", trace.Worldbook.Entries[0].Reason)
	assert.Equal(t, "moon", request.Messages[0].Content)
}

func TestWorldbookSurvivesDisabledChatMarker(t *testing.T) {
	config := &dto.WorldbookConfig{Enabled: true, Books: []dto.Worldbook{{ID: "b", Enabled: true, Entries: []dto.WorldbookEntry{{ID: "e", Enabled: true, Constant: true, Content: "lore"}}}}}
	preset := &dto.SillyTavernPresetConfig{Preset: json.RawMessage(`{"prompts":[{"identifier":"main","content":"instructions"},{"identifier":"chatHistory","marker":true}],"prompt_order":[{"order":[{"identifier":"main","enabled":true},{"identifier":"chatHistory","enabled":false}]}]}`)}
	compiled, trace, err := CompileWorldbook(config, preset, &dto.GeneralOpenAIRequest{Messages: []dto.Message{{Role: "user", Content: "hello"}}}, SillyTavernContext{})
	require.NoError(t, err)
	assert.True(t, slices.ContainsFunc(compiled.Messages, func(m dto.Message) bool { return m.Content == "lore" }))
	require.NotNil(t, trace.Worldbook.Placement)
}

func TestWorldbookInheritsPresetMarkerBeforeScripts(t *testing.T) {
	config := &dto.WorldbookConfig{Enabled: true, InjectionMode: "inherit", Books: []dto.Worldbook{{ID: "b", Enabled: true, Entries: []dto.WorldbookEntry{{ID: "e", Enabled: true, Constant: true, Content: "lore"}}}}}
	preset := &dto.SillyTavernPresetConfig{Preset: json.RawMessage(`{"prompts":[{"identifier":"worldInfoBefore","marker":true},{"identifier":"main","content":"instructions"},{"identifier":"chatHistory","marker":true}],"prompt_order":[{"order":[{"identifier":"worldInfoBefore","enabled":true},{"identifier":"main","enabled":true},{"identifier":"chatHistory","enabled":true}]}]}`)}
	compiled, _, err := CompileWorldbook(config, preset, &dto.GeneralOpenAIRequest{Messages: []dto.Message{{Role: "user", Content: "hello"}}}, SillyTavernContext{})
	require.NoError(t, err)
	assert.Equal(t, "lore", compiled.Messages[0].Content)
	assert.Equal(t, "instructions", compiled.Messages[1].Content)
}

func TestWorldbookDepthZeroAndToolExchangePlacement(t *testing.T) {
	messages := []dto.Message{{Role: "user", Content: "hello"}, {Role: "assistant", ToolCalls: json.RawMessage(`[{"id":"call1","type":"function","function":{"name":"lookup","arguments":"{}"}}]`)}, {Role: "tool", ToolCallId: "call1", Content: "result"}}
	assert.Equal(t, len(messages), worldbookDepthIndex(messages, 0), "depth zero follows the latest message")
	config := &dto.WorldbookConfig{InjectionMode: "at_depth", InjectionDepth: 1}
	placement := resolveWorldbookPlacement(config, messages)
	assert.Equal(t, 1, placement.Index, "insertion must precede the complete tool exchange")
}

func TestWorldbookPlacementModes(t *testing.T) {
	config := &dto.WorldbookConfig{Enabled: true, Books: []dto.Worldbook{{ID: "b", Enabled: true, Entries: []dto.WorldbookEntry{{ID: "e", Enabled: true, Constant: true, Content: "lore"}}}}}
	base := &dto.GeneralOpenAIRequest{Messages: []dto.Message{
		{Role: "system", Content: "system"},
		{Role: "user", Content: "first"},
		{Role: "assistant", Content: "reply"},
		{Role: "user", Content: "second"},
	}}
	tests := []struct {
		name  string
		mode  string
		depth int
		index int
	}{
		{name: "system prelude", mode: dto.WorldbookInjectionSystemPrelude, index: 1},
		{name: "before last user", mode: dto.WorldbookInjectionBeforeLastUser, index: 3},
		{name: "at depth", mode: dto.WorldbookInjectionAtDepth, depth: 1, index: 3},
		{name: "unavailable depth", mode: dto.WorldbookInjectionAtDepth, depth: 20, index: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config.InjectionMode = test.mode
			config.InjectionDepth = test.depth
			compiled, _, err := ApplyWorldbook(config, base, base, PresetCompileTrace{})
			require.NoError(t, err)
			require.Greater(t, len(compiled.Messages), test.index)
			assert.Equal(t, "system", compiled.Messages[test.index].Role)
			assert.Equal(t, "lore", compiled.Messages[test.index].Content)
		})
	}
}

func TestWorldbookPerEntryPlacement(t *testing.T) {
	var config dto.WorldbookConfig
	require.NoError(t, common.Unmarshal([]byte(`{"enabled":true,"injection_mode":"before_last_user","books":[{"id":"b","enabled":true,"entries":[{"id":"old","enabled":true,"constant":true,"content":"old"},{"id":"follow","enabled":true,"constant":true,"content":"follow","injection_mode":"follow_global"},{"id":"prelude","enabled":true,"constant":true,"content":"prelude","injection_mode":"system_prelude"},{"id":"marker","enabled":true,"constant":true,"content":"marker","injection_mode":"inherit"},{"id":"depth","enabled":true,"constant":true,"content":"depth","injection_mode":"at_depth","injection_depth":1}]}]}`), &config))
	request := &dto.GeneralOpenAIRequest{Messages: []dto.Message{{Role: "user", Content: "first"}, {Role: "assistant", ToolCalls: json.RawMessage(`[{"id":"call","type":"function","function":{"name":"lookup","arguments":"{}"}}]`)}, {Role: "tool", ToolCallId: "call", Content: "result"}}}
	for _, withMarker := range []bool{false, true} {
		t.Run(fmt.Sprint(withMarker), func(t *testing.T) {
			var preset *dto.SillyTavernPresetConfig
			if withMarker {
				preset = &dto.SillyTavernPresetConfig{Preset: json.RawMessage(`{"prompts":[{"identifier":"worldInfoBefore","marker":true},{"identifier":"main","content":"instructions"},{"identifier":"chatHistory","marker":true}],"prompt_order":[{"order":[{"identifier":"worldInfoBefore","enabled":true},{"identifier":"main","enabled":true},{"identifier":"chatHistory","enabled":true}]}]}`)}
			}
			compiled, trace, err := CompileWorldbook(&config, preset, request, SillyTavernContext{})
			require.NoError(t, err)
			joined := ""
			for _, message := range compiled.Messages {
				joined += fmt.Sprint(message.Content) + "|"
			}
			assert.Contains(t, joined, "depth|<nil>|result|", "depth must precede the intact tool exchange")
			for _, lore := range []string{"old", "follow", "prelude", "marker", "depth"} {
				assert.Equal(t, 1, strings.Count(joined, lore))
			}
			assert.NotContains(t, joined, "prelude\n\nmarker\n\ndepth", "different placements must not collapse into one global block")
			data, err := common.Marshal(trace.Worldbook)
			require.NoError(t, err)
			var preview struct {
				Entries []struct {
					Placement *dto.WorldbookPlacement `json:"placement"`
				} `json:"entries"`
			}
			require.NoError(t, common.Unmarshal(data, &preview))
			for _, decision := range preview.Entries {
				require.NotNil(t, decision.Placement)
			}
			assert.Equal(t, withMarker, preview.Entries[3].Placement.Mode == "inherit")
			assert.Equal(t, !withMarker, preview.Entries[3].Placement.Fallback)
			assert.Equal(t, "before_last_user", preview.Entries[0].Placement.Mode)
			assert.Equal(t, preview.Entries[0].Placement, preview.Entries[1].Placement)
			assert.Len(t, request.Messages, 3)
		})
	}
}

func TestWorldbookPerEntryPlacementRunsBeforePresetScripts(t *testing.T) {
	config := &dto.WorldbookConfig{Enabled: true, Books: []dto.Worldbook{{ID: "b", Enabled: true, Entries: []dto.WorldbookEntry{
		{ID: "prelude", Enabled: true, Constant: true, Content: "prelude", InjectionMode: dto.WorldbookInjectionSystemPrelude},
		{ID: "latest", Enabled: true, Constant: true, Content: "latest", InjectionMode: dto.WorldbookInjectionBeforeLastUser},
	}}}}
	preset := scriptPresetConfig(t, map[string]any{"FinalRequestProcessing": map[string]any{
		"enabled": true,
		"script":  `({request}) => { for (const message of request.messages) { if (typeof message.content === "string") message.content = message.content.toUpperCase(); } return request; }`,
	}})
	request := &dto.GeneralOpenAIRequest{Model: "m", Messages: []dto.Message{{Role: "system", Content: "system"}, {Role: "assistant", Content: "previous"}, {Role: "user", Content: "hello"}}}

	compiled, trace, err := CompileWorldbook(config, preset, request, SillyTavernContext{})
	require.NoError(t, err)
	assert.True(t, slices.ContainsFunc(compiled.Messages, func(message dto.Message) bool {
		return message.Role == "system" && message.Content == "PRELUDE"
	}), "the preset script must process a per-entry system-prelude block")
	assert.True(t, slices.ContainsFunc(compiled.Messages, func(message dto.Message) bool {
		return message.Role == "system" && message.Content == "LATEST"
	}), "the preset script must process a per-entry before-last-user block")
	require.NotNil(t, trace.Worldbook)
	for _, decision := range trace.Worldbook.Entries {
		require.NotNil(t, decision.Placement)
		assert.Contains(t, fmt.Sprint(compiled.Messages[decision.Placement.Index].Content), strings.ToUpper(decision.ID))
	}
	assert.Equal(t, "system", request.Messages[0].Content)
	assert.Equal(t, "previous", request.Messages[1].Content)
	assert.Equal(t, "hello", request.Messages[2].Content)
}

func TestWorldbookPlacementValidationAndCanonicalImport(t *testing.T) {
	for _, raw := range []string{`{"injection_depth":-1}`, `{"injection_depth":21}`, `{"books":[{"id":"b","entries":[{"id":"e","injection_mode":"unsupported"}]}]}`, `{"books":[{"id":"b","entries":[{"id":"e","injection_depth":21}]}]}`} {
		var config dto.WorldbookConfig
		require.NoError(t, common.Unmarshal([]byte(raw), &config))
		assert.Error(t, config.Validate(), raw)
	}
	for _, prefix := range []string{"", `"id":"b","enabled":true,`} {
		book, err := ImportWorldbook("canonical", []byte(`{`+prefix+`"entries":[{"id":"e","enabled":true,"constant":true,"content":"lore","injection_mode":"at_depth","injection_depth":2}]}`))
		require.NoError(t, err)
		assert.Empty(t, book.Entries[0].NormalizedRules)
		assert.Equal(t, "at_depth", book.Entries[0].InjectionMode)
		assert.Equal(t, 2, book.Entries[0].InjectionDepth)
	}
}

func TestWorldbookSharedBoundaryPriorityAndMarkerIndex(t *testing.T) {
	preset := &dto.SillyTavernPresetConfig{Preset: json.RawMessage(`{"prompts":[{"identifier":"worldInfoBefore","marker":true,"role":"user"},{"identifier":"chatHistory","marker":true}],"prompt_order":[{"order":[{"identifier":"worldInfoBefore","enabled":true},{"identifier":"chatHistory","enabled":true}]}]}`)}
	config := &dto.WorldbookConfig{Enabled: true, Books: []dto.Worldbook{{ID: "b", Enabled: true, Entries: []dto.WorldbookEntry{
		{ID: "marker", Constant: true, Enabled: true, Content: "marker", Priority: -1},
		{ID: "low", Constant: true, Enabled: true, Content: "low", InjectionMode: "system_prelude"},
		{ID: "middle", Constant: true, Enabled: true, Content: "middle", Priority: 1, InjectionMode: "at_depth", InjectionDepth: 2},
		{ID: "high", Constant: true, Enabled: true, Content: "high", Priority: 2, InjectionMode: "system_prelude"},
	}}}}
	compiled, trace, err := CompileWorldbook(config, preset, &dto.GeneralOpenAIRequest{Messages: []dto.Message{{Role: "user", Content: "hello"}}}, SillyTavernContext{})
	require.NoError(t, err)
	assert.Equal(t, "low\n\nmiddle\n\nhigh", compiled.Messages[0].Content, "entries sharing a boundary must preserve the final priority order")
	for _, decision := range trace.Worldbook.Entries {
		require.NotNil(t, decision.Placement)
		assert.Contains(t, fmt.Sprint(compiled.Messages[decision.Placement.Index].Content), decision.ID)
	}
	assert.Equal(t, trace.Worldbook.Entries[0].Placement, trace.Worldbook.Placement)
}

func TestWorldbookPlacementFallsBackWithoutUserMessage(t *testing.T) {
	config := &dto.WorldbookConfig{Enabled: true, InjectionMode: dto.WorldbookInjectionBeforeLastUser, Books: []dto.Worldbook{{ID: "b", Enabled: true, Entries: []dto.WorldbookEntry{{ID: "e", Enabled: true, Constant: true, Content: "lore"}}}}}
	base := &dto.GeneralOpenAIRequest{Messages: []dto.Message{{Role: "system", Content: "system"}, {Role: "assistant", Content: "reply"}}}
	compiled, trace, err := ApplyWorldbook(config, base, base, PresetCompileTrace{})
	require.NoError(t, err)
	require.Len(t, compiled.Messages, 3)
	assert.Equal(t, "lore", compiled.Messages[1].Content)
	require.NotNil(t, trace.Worldbook)
	require.NotNil(t, trace.Worldbook.Placement)
	assert.True(t, trace.Worldbook.Placement.Fallback)
}

func TestWorldbookMatchingWorkBoundPreservesConstants(t *testing.T) {
	entries := make([]dto.WorldbookEntry, 600)
	for i := range entries {
		entries[i] = dto.WorldbookEntry{ID: fmt.Sprint(i), Enabled: true, Keys: []string{"missing"}, Content: "conditional"}
	}
	entries = append(entries, dto.WorldbookEntry{ID: "constant", Enabled: true, Constant: true, Content: "always"})
	config := &dto.WorldbookConfig{Enabled: true, Books: []dto.Worldbook{{ID: "b", Enabled: true, Entries: entries}}}
	result, err := MatchWorldbook(config, &dto.GeneralOpenAIRequest{Messages: []dto.Message{{Role: "user", Content: strings.Repeat("x", 64<<10)}}})
	require.NoError(t, err)
	assert.Equal(t, "always", result.Text)
	assert.Contains(t, result.Warnings, "Worldbook matching stopped after its work limit")
}

func TestWorldbookImportNormalizesPersonalGatewaySemantics(t *testing.T) {
	for _, raw := range []string{
		`{"id":"book","enabled":true,"entries":[{"id":"entry","enabled":true,"constant":true,"content":"lore","sticky":3,"probability":50}]}`,
		`{"entries":[{"constant":true,"content":"lore","sticky":3,"probability":50}]}`,
	} {
		book, err := ImportWorldbook("restrictions", []byte(raw))
		require.NoError(t, err)
		assert.Empty(t, book.Entries[0].Issues)
		assertWorldbookNormalizedField(t, book.Entries[0], "sticky")
		assertWorldbookNormalizedField(t, book.Entries[0], "probability")
		assert.False(t, book.Entries[0].Enabled, "behavior that cannot be reproduced must not enable the entry silently")
		assertWorldbookRuleAction(t, book.Entries[0], "sticky", "risk")
		assertWorldbookRuleAction(t, book.Entries[0], "probability", "risk")
		assert.NotEmpty(t, book.Entries[0].Source, "the original entry must be kept for review")
	}
	book, err := ImportWorldbook("simplified", []byte(`{"id":"book","enabled":true,"entries":[{"id":"entry","enabled":true,"constant":true,"content":"lore","keys":[],"secondary_keys":[],"secondary_logic":"none","source":{"sticky":3,"probability":50}}]}`))
	require.NoError(t, err)
	assert.Empty(t, book.Entries[0].Issues, "inspection source must not reblock an explicitly simplified entry")
	assert.Empty(t, book.Entries[0].NormalizedRules)
	for _, field := range []string{"key", "keys", "keysecondary", "secondary_keys"} {
		for _, prefix := range []string{"", `"id":"book","enabled":true,`} {
			_, err = ImportWorldbook("malformed", []byte(fmt.Sprintf(`{%s"entries":[{"key":["moon"],"selective":true,"selectiveLogic":2,"content":"lore",%q:[7]}]}`, prefix, field)))
			assert.Error(t, err, "malformed alias %s must not weaken a trigger", field)
		}
	}
}

func TestWorldbookImportKeepsSupportedEntriesEnabled(t *testing.T) {
	raw := `{"entries":[
		{"comment":"plain","key":["moon"],"content":"lore"},
		{"comment":"defaults","constant":true,"content":"lore","probability":100,"position":0},
		{"comment":"inert filter","constant":true,"content":"lore","characterFilter":{"names":[],"isExclude":false,"scenario":null}},
		{"comment":"filtered","constant":true,"content":"lore","characterFilter":{"names":["Alice"],"isExclude":false,"scenario":null}}
	]}`
	book, err := ImportWorldbook("mix", []byte(raw))
	require.NoError(t, err)
	require.Len(t, book.Entries, 4)
	assert.True(t, book.Entries[0].Enabled, "an entry without unsupported behavior stays enabled")
	assert.True(t, book.Entries[1].Enabled, "probability 100 and placement 0 are the Tavern defaults")
	assert.True(t, book.Entries[2].Enabled, "an empty character filter does not restrict anything")
	assert.False(t, book.Entries[3].Enabled, "an active character filter must be reviewed first")
	assertWorldbookRuleAction(t, book.Entries[3], "characterFilter", "risk")
}

func TestWorldbookPreventRecursionStopsAssociation(t *testing.T) {
	config := &dto.WorldbookConfig{Enabled: true, Recursive: true, Books: []dto.Worldbook{{ID: "b", Enabled: true, Entries: []dto.WorldbookEntry{
		{ID: "a", Enabled: true, Constant: true, Content: "silver", PreventRecursion: true},
		{ID: "b", Enabled: true, Keys: []string{"silver"}, Content: "moon"},
		{ID: "c", Enabled: true, Keys: []string{"moon"}, Content: "stars"},
	}}}}
	result, err := MatchWorldbook(config, &dto.GeneralOpenAIRequest{})
	require.NoError(t, err)
	assert.Equal(t, "silver", result.Text)
}

func TestWorldbookIssueOnlyBlocksMatching(t *testing.T) {
	config := &dto.WorldbookConfig{Enabled: true, Books: []dto.Worldbook{{ID: "b", Enabled: true, Entries: []dto.WorldbookEntry{
		{ID: "invalid", Enabled: true, Constant: true, Content: "bad", Issues: []string{"regex"}},
		{ID: "valid", Enabled: true, Constant: true, Content: "good"},
	}}}}
	result, err := MatchWorldbook(config, &dto.GeneralOpenAIRequest{})
	require.NoError(t, err)
	assert.Equal(t, "good", result.Text)
	assert.Equal(t, "invalid", result.Entries[0].Reason)
}

func assertWorldbookNormalizedField(t *testing.T, entry dto.WorldbookEntry, field string) {
	t.Helper()
	for _, rule := range entry.NormalizedRules {
		if rule.Field == field {
			return
		}
	}
	t.Fatalf("normalized field %q not found: %#v", field, entry.NormalizedRules)
}

func assertWorldbookRuleAction(t *testing.T, entry dto.WorldbookEntry, field, action string) {
	t.Helper()
	for _, rule := range entry.NormalizedRules {
		if rule.Field == field {
			assert.Equal(t, action, rule.Action, "unexpected action for field %q", field)
			return
		}
	}
	t.Fatalf("normalized field %q not found: %#v", field, entry.NormalizedRules)
}

func TestWorldbookRegexWholeWordAndBoundedMediaTail(t *testing.T) {
	entry := dto.WorldbookEntry{Keys: []string{"/moon/i"}, WholeWords: true}
	assert.False(t, worldbookEntryMatches(entry, "moonlight", nil))
	assert.False(t, worldbookEntryMatches(entry, "moon\u0301", nil))
	assert.True(t, worldbookEntryMatches(entry, "夜 moon!", nil))
	content := []any{map[string]any{"type": "text", "text": strings.Repeat("old", 1<<18)}, map[string]any{"type": "image_url", "image_url": "ignored"}, map[string]any{"type": "input_text", "text": "月亮 moon"}}
	text, truncated := worldbookMessageText(content, 7)
	assert.True(t, truncated)
	assert.Equal(t, " moon", text, "cutting a Unicode tail must not retain partial UTF-8")
}
