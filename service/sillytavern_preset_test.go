package service

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The checked-in hashes were computed from real provider-bound SillyTavern
// captures. The preset itself remains in the user's archive rather than being
// copied into source control. Set ST_PRESET_ZIP to run this offline regression.
func TestSillyTavernARGOGolden(t *testing.T) {
	archivePath := os.Getenv("ST_PRESET_ZIP")
	if archivePath == "" {
		t.Skip("ST_PRESET_ZIP is required")
	}
	fixtureBytes, err := os.ReadFile("testdata/sillytavern-golden/ARGO-1.6/reference.json")
	require.NoError(t, err)
	var fixture struct {
		Context struct {
			User               string `json:"user"`
			Char               string `json:"char"`
			CurrentUserMessage string `json:"current_user_message"`
		} `json:"context"`
		ProviderFields map[string]any        `json:"provider_fields"`
		Modes          map[string][][]string `json:"modes"`
	}
	require.NoError(t, json.Unmarshal(fixtureBytes, &fixture))
	archive, err := zip.OpenReader(archivePath)
	require.NoError(t, err)
	defer archive.Close()
	var preset []byte
	for _, file := range archive.File {
		if file.Name == "ARGO-1.6.json" {
			reader, openErr := file.Open()
			require.NoError(t, openErr)
			preset, err = io.ReadAll(reader)
			require.NoError(t, reader.Close())
			require.NoError(t, err)
			break
		}
	}
	require.NotEmpty(t, preset)
	randomID := regexp.MustCompile(`[0-9A-F]{64}`)
	for _, mode := range []string{"none", "strict"} {
		t.Run(mode, func(t *testing.T) {
			stream := true
			request := &dto.GeneralOpenAIRequest{Model: "oracle-test", Stream: &stream, Messages: []dto.Message{{Role: "user", Content: fixture.Context.CurrentUserMessage}}}
			compiled, _, compileErr := CompileSillyTavernPreset(&dto.SillyTavernPresetConfig{
				Preset: preset, User: fixture.Context.User, Char: fixture.Context.Char,
				PostProcessing: mode, ReferenceSource: "custom",
			}, request, SillyTavernContext{})
			require.NoError(t, compileErr)
			golden := fixture.Modes[mode]
			require.Len(t, compiled.Messages, len(golden), "message boundaries/count differ from real SillyTavern capture")
			for index, message := range compiled.Messages {
				require.Len(t, golden[index], 2)
				assert.Equalf(t, golden[index][0], message.Role, "message[%d].role", index)
				content, ok := message.Content.(string)
				require.Truef(t, ok, "message[%d].content must be text", index)
				contentHash := sha256.Sum256([]byte(randomID.ReplaceAllString(content, "<RANDOM>")))
				assert.Equalf(t, golden[index][1], fmt.Sprintf("%x", contentHash), "message[%d].content SHA-256", index)
			}
			compiledJSON, marshalErr := common.Marshal(compiled)
			require.NoError(t, marshalErr)
			var fields map[string]any
			require.NoError(t, common.Unmarshal(compiledJSON, &fields))
			delete(fields, "messages")
			assert.Equal(t, fixture.ProviderFields, fields, "provider request fields")
		})
	}
}

func TestCompileSillyTavernPresetPreservesStructureAndContext(t *testing.T) {
	config := &dto.SillyTavernPresetConfig{Preset: []byte(`{
		"name":"ARGO-like","temperature":0,"openai_max_tokens":120,
		"prompts":[
			{"identifier":"setup","role":"system","content":"{{setvar::id::{{random::A}}}}SETTINGS {{getvar::id}}"},
			{"identifier":"charDescription","role":"system","marker":true},
			{"identifier":"main","role":"system","content":"FILE {{user}} {{char}} {{getvar::id}}"},
			{"identifier":"before","role":"user","content":"<主体>","injection_position":1,"injection_depth":1,"injection_order":100},
			{"identifier":"chatHistory","marker":true},
			{"identifier":"after","role":"user","content":"</主体>","injection_position":1,"injection_depth":0,"injection_order":100},
			{"identifier":"config","role":"system","content":"CONFIG"}
		],
		"prompt_order":[{"character_id":100001,"order":[
			{"identifier":"setup","enabled":true},{"identifier":"charDescription","enabled":true},
			{"identifier":"main","enabled":true},{"identifier":"before","enabled":true},
			{"identifier":"chatHistory","enabled":true},{"identifier":"after","enabled":true},
			{"identifier":"config","enabled":true}
		]}]
	}`), User: "苍阳", Char: "乔治"}
	media := []any{map[string]any{"type": "text", "text": "你好"}, map[string]any{"type": "image_url", "image_url": map[string]any{"url": "https://example.test/img.png"}}}
	request := &dto.GeneralOpenAIRequest{Model: "test-model", Messages: []dto.Message{
		{Role: "system", Content: "角色与世界状态"},
		{Role: "assistant", Content: "早上好"},
		{Role: "user", Content: media},
	}}
	compiled, trace, err := CompileSillyTavernPreset(config, request, SillyTavernContext{})
	require.NoError(t, err)
	require.Len(t, compiled.Messages, 8)
	assert.Equal(t, "SETTINGS A", compiled.Messages[0].Content)
	assert.Equal(t, "FILE 苍阳 乔治 A", compiled.Messages[1].Content)
	assert.Equal(t, "角色与世界状态", compiled.Messages[2].Content)
	assert.Equal(t, "<主体>", compiled.Messages[4].Content)
	assert.Equal(t, "早上好", compiled.Messages[3].Content)
	assert.Equal(t, media, compiled.Messages[5].Content)
	assert.Equal(t, "</主体>", compiled.Messages[6].Content)
	assert.Equal(t, "CONFIG", compiled.Messages[7].Content)
	assert.Equal(t, "in_chat", trace.Messages[4].Source)
	assert.Equal(t, "chat_history", trace.Messages[5].Source)
	assert.Contains(t, strings.Join(trace.Warnings, ","), "not inferred as SillyTavern markers")
	require.NotNil(t, compiled.Temperature)
	assert.Zero(t, *compiled.Temperature)
	require.NotNil(t, compiled.MaxTokens)
	assert.Equal(t, uint(120), *compiled.MaxTokens)
	assert.Len(t, request.Messages, 3)
	config.Patches = []dto.SillyTavernPresetPatch{{Identifier: "config", Find: "CONFIG", Replace: "CUSTOM"}}
	patched, _, err := CompileSillyTavernPreset(config, request, SillyTavernContext{})
	require.NoError(t, err)
	assert.Equal(t, "CUSTOM", patched.Messages[7].Content)
}

func TestSillyTavernPostProcessingModes(t *testing.T) {
	input := []presetMessage{
		{message: dto.Message{Role: "system", Content: "setup"}, id: "setup"},
		{message: dto.Message{Role: "system", Content: "rules"}, id: "rules"},
		{message: dto.Message{Role: "user", Content: "first"}, id: "first"},
		{message: dto.Message{Role: "user", Content: "second"}, id: "second"},
		{message: dto.Message{Role: "assistant", Content: "reply"}, id: "reply"},
		{message: dto.Message{Role: "system", Content: "late rules"}, id: "late"},
		{message: dto.Message{Role: "user", Content: "last"}, id: "last"},
	}
	// The "none" option must not invoke post-processing: boundaries and roles
	// are retained, even when adjacent messages have identical roles.
	assert.Equal(t, []string{"system", "system", "user", "user", "assistant", "system", "user"}, []string{
		input[0].message.Role, input[1].message.Role, input[2].message.Role, input[3].message.Role,
		input[4].message.Role, input[5].message.Role, input[6].message.Role,
	})
	strict, err := strictSillyTavernPostProcess(input)
	require.NoError(t, err)
	require.Len(t, strict, 4)
	assert.Equal(t, "system", strict[0].message.Role)
	assert.Equal(t, "setup\n\nrules", strict[0].message.Content)
	assert.Equal(t, "user", strict[1].message.Role)
	assert.Equal(t, "first\n\nsecond", strict[1].message.Content)
	assert.Equal(t, "assistant", strict[2].message.Role)
	assert.Equal(t, "user", strict[3].message.Role)
	assert.Equal(t, "late rules\n\nlast", strict[3].message.Content)
	// The transformation must not mutate the source messages, so retries and
	// channel comparisons can still inspect their original boundaries.
	assert.Equal(t, "system", input[5].message.Role)
	assert.Equal(t, "late rules", input[5].message.Content)

	placeholder, err := strictSillyTavernPostProcess([]presetMessage{{message: dto.Message{Role: "system", Content: "only system"}}})
	require.NoError(t, err)
	require.Len(t, placeholder, 2)
	assert.Equal(t, "[Start a new chat]", placeholder[1].message.Content)
}

// ST_ORACLE_CAPTURE_URL points at a local mock provider that has captured a
// real SillyTavern /v1/chat/completions request. This intentionally compares
// against the provider-bound payload, not another implementation of ST logic.
func TestSillyTavernARGOOracle(t *testing.T) {
	archivePath, captureURL := os.Getenv("ST_PRESET_ZIP"), os.Getenv("ST_ORACLE_CAPTURE_URL")
	if archivePath == "" || captureURL == "" {
		t.Skip("ST_PRESET_ZIP and ST_ORACLE_CAPTURE_URL are required")
	}
	archive, err := zip.OpenReader(archivePath)
	require.NoError(t, err)
	defer archive.Close()
	var preset []byte
	for _, file := range archive.File {
		if file.Name != "ARGO-1.6.json" {
			continue
		}
		reader, openErr := file.Open()
		require.NoError(t, openErr)
		preset, err = io.ReadAll(reader)
		require.NoError(t, reader.Close())
		require.NoError(t, err)
	}
	require.NotEmpty(t, preset)
	response, err := http.Get(captureURL)
	require.NoError(t, err)
	defer response.Body.Close()
	require.Equal(t, http.StatusOK, response.StatusCode)
	var captured []struct {
		Body json.RawMessage `json:"body"`
	}
	require.NoError(t, common.DecodeJson(response.Body, &captured))
	require.Len(t, captured, 2, "capture once with None, then once with Strict using the same temporary chat input")
	for captureIndex, mode := range []string{"none", "strict"} {
		t.Run(mode, func(t *testing.T) {
			var oracleRequest struct {
				Messages []struct {
					Role    string `json:"role"`
					Content string `json:"content"`
				} `json:"messages"`
			}
			require.NoError(t, common.Unmarshal(captured[captureIndex].Body, &oracleRequest))
			oracle := oracleRequest.Messages
			stream := true
			request := &dto.GeneralOpenAIRequest{Model: "oracle-test", Stream: &stream, Messages: []dto.Message{{Role: "user", Content: "<reader_input>\n你好，这是一次预设差分测试。\n</reader_input>"}}}
			compiled, _, compileErr := CompileSillyTavernPreset(&dto.SillyTavernPresetConfig{Preset: preset, User: "阳", Char: "Assistant", PostProcessing: mode, ReferenceSource: "custom"}, request, SillyTavernContext{})
			require.NoError(t, compileErr)
			assert.Equal(t, len(oracle), len(compiled.Messages), "message boundary/count")
			randomID := regexp.MustCompile(`[0-9A-F]{64}`)
			for index := range min(len(oracle), len(compiled.Messages)) {
				actual := compiled.Messages[index]
				assert.Equalf(t, oracle[index].Role, actual.Role, "message[%d].role", index)
				content, ok := actual.Content.(string)
				require.Truef(t, ok, "message[%d].content must be string", index)
				assert.Equalf(t, randomID.ReplaceAllString(oracle[index].Content, "<RANDOM>"), randomID.ReplaceAllString(content, "<RANDOM>"), "message[%d].content", index)
			}
			var oracleFields, compiledFields map[string]any
			require.NoError(t, common.Unmarshal(captured[captureIndex].Body, &oracleFields))
			compiledJSON, marshalErr := common.Marshal(compiled)
			require.NoError(t, marshalErr)
			require.NoError(t, common.Unmarshal(compiledJSON, &compiledFields))
			delete(oracleFields, "messages")
			delete(compiledFields, "messages")
			assert.Equal(t, oracleFields, compiledFields, "provider-bound request fields")
		})
	}
}

// Set ST_PRESET_ZIP to the user's preset archive to verify real-world import
// and the ARGO prompt structure without checking private preset text into git.
func TestSillyTavernPresetArchive(t *testing.T) {
	archivePath := os.Getenv("ST_PRESET_ZIP")
	if archivePath == "" {
		t.Skip("ST_PRESET_ZIP is not set")
	}
	archive, err := zip.OpenReader(archivePath)
	require.NoError(t, err)
	defer archive.Close()
	for _, file := range archive.File {
		reader, openErr := file.Open()
		require.NoError(t, openErr)
		body, readErr := io.ReadAll(io.LimitReader(reader, dto.MaxSillyTavernPresetBytes+1))
		require.NoError(t, reader.Close())
		require.NoError(t, readErr)
		config := &dto.SillyTavernPresetConfig{Preset: body}
		_, validateErr := config.ParseAndValidate()
		require.NoError(t, validateErr, file.Name)
		config.EnableEmbeddedRegex = true
		responseScripts, regexWarnings := compilePresetResponseRegex(config, "test-model")
		t.Logf("%s: %d receive-side scripts active, %d compatibility warnings", file.Name, len(responseScripts), len(regexWarnings))
		require.NotEmpty(t, responseScripts, file.Name)
		config.EnableEmbeddedRegex = false
		if file.Name != "ARGO-1.6.json" {
			continue
		}
		request := &dto.GeneralOpenAIRequest{Model: "test-model", Messages: []dto.Message{
			{Role: "system", Content: "角色背景"}, {Role: "user", Content: "你好"},
		}}
		compiled, trace, compileErr := CompileSillyTavernPreset(config, request, SillyTavernContext{User: "苍阳", Char: "乔治"})
		require.NoError(t, compileErr)
		require.NotEmpty(t, trace.Messages)
		var before, current, after int = -1, -1, -1
		for index, message := range compiled.Messages {
			if message.Content == "<主体>" {
				before = index
			}
			if message.Content == "你好" {
				current = index
			}
			if message.Content == "</主体>" {
				after = index
			}
			if content, ok := message.Content.(string); ok {
				assert.NotContains(t, content, "{{random::")
				assert.NotContains(t, content, "{{setvar::")
				assert.NotContains(t, content, "{{getvar::")
			}
		}
		assert.True(t, before >= 0 && before < current && current < after, "ARGO in-chat brackets should surround the current user message")
		assert.False(t, strings.Contains(strings.Join(trace.Warnings, ","), "unsupported macro"))
		config.Patches = []dto.SillyTavernPresetPatch{{Identifier: "jailbreak", Find: "⦿ 篇幅定额：\n思考1000字，页眉50字，主体1000字", Replace: ""}}
		patched, _, patchErr := CompileSillyTavernPreset(config, request, SillyTavernContext{User: "苍阳", Char: "乔治"})
		require.NoError(t, patchErr)
		for _, message := range patched.Messages {
			if content, ok := message.Content.(string); ok {
				assert.NotContains(t, content, "思考1000字，页眉50字，主体1000字")
			}
		}
	}
}

func TestCompileSillyTavernPresetModelFilterAndMalformedPreset(t *testing.T) {
	request := &dto.GeneralOpenAIRequest{Model: "other", Messages: []dto.Message{{Role: "user", Content: "Hello"}}}
	config := &dto.SillyTavernPresetConfig{Models: []string{"selected"}, Preset: []byte(`{"prompts":[{"identifier":"main","role":"system","content":"Hi"}],"prompt_order":[{"character_id":100001,"order":[{"identifier":"main","enabled":true}]}]}`)}
	compiled, _, err := CompileSillyTavernPreset(config, request, SillyTavernContext{})
	require.NoError(t, err)
	assert.Same(t, request, compiled)
	config.Preset = []byte(`{"prompts":[]}`)
	_, _, err = CompileSillyTavernPreset(config, request, SillyTavernContext{})
	require.Error(t, err)
}

func TestDecodeSillyTavernContextKeepsStructuredMarkers(t *testing.T) {
	context, err := DecodeSillyTavernContext(strings.NewReader(`{"model":"test-model","_sillytavern_context":{"user":"苍阳","markers":{"scenario":"春日"},"chat_history":[{"role":"user","content":"你好"}]}}`))
	require.NoError(t, err)
	assert.Equal(t, "苍阳", context.User)
	assert.Equal(t, "春日", context.Markers["scenario"])
	require.Len(t, context.ChatHistory, 1)
	assert.Equal(t, "你好", context.ChatHistory[0].Content)
}

func TestCompileSillyTavernPresetStructuredMarkersExamplesAndSquash(t *testing.T) {
	config := &dto.SillyTavernPresetConfig{ParameterPolicy: "client", Preset: []byte(`{
		"squash_system_messages":true,"new_chat_prompt":"Begin","new_example_chat_prompt":"[Example Chat]",
		"scenario_format":"Scenario: {{scenario}}","temperature":1,
		"prompts":[
			{"identifier":"first","role":"system","content":"One"},
			{"identifier":"scenario","role":"system","marker":true},
			{"identifier":"dialogueExamples","marker":true},
			{"identifier":"chatHistory","marker":true}
		],
		"prompt_order":[{"character_id":100001,"order":[
			{"identifier":"first","enabled":true},{"identifier":"scenario","enabled":true},
			{"identifier":"dialogueExamples","enabled":true},{"identifier":"chatHistory","enabled":true}
		]}]
	}`)}
	clientTemperature := 0.25
	request := &dto.GeneralOpenAIRequest{Model: "test-model", Temperature: &clientTemperature, Messages: []dto.Message{{Role: "user", Content: "ignored when chat_history is explicit"}}}
	context := SillyTavernContext{
		Markers:          map[string]string{"scenario": "spring"},
		DialogueExamples: [][]dto.Message{{{Role: "user", Content: "sample"}}},
		ChatHistory:      []dto.Message{{Role: "user", Content: "current"}},
	}
	compiled, trace, err := CompileSillyTavernPreset(config, request, context)
	require.NoError(t, err)
	require.Len(t, compiled.Messages, 5)
	assert.Equal(t, "One\nScenario: spring", compiled.Messages[0].Content)
	assert.Equal(t, "[Example Chat]", compiled.Messages[1].Content)
	assert.Equal(t, "system", compiled.Messages[2].Role)
	assert.Equal(t, "sample", compiled.Messages[2].Content)
	assert.Equal(t, "Begin", compiled.Messages[3].Content)
	assert.Equal(t, "current", compiled.Messages[4].Content)
	assert.Equal(t, "dialogue_example", trace.Messages[2].Source)
	assert.Equal(t, clientTemperature, *compiled.Temperature)
	config.ContextMode = "exact"
	config.ReferenceSource = "custom"
	_, _, err = CompileSillyTavernPreset(config, request, context)
	require.ErrorContains(t, err, "requires user, char")
	context.User, context.Char = "阳", "Assistant"
	exact, exactTrace, err := CompileSillyTavernPreset(config, request, context)
	require.NoError(t, err)
	assert.Equal(t, "exact", exactTrace.Mode)
	assert.Equal(t, compiled.Messages, exact.Messages)
	delete(context.Markers, "scenario")
	_, _, err = CompileSillyTavernPreset(config, request, context)
	require.ErrorContains(t, err, `missing marker "scenario"`)
}

func TestSillyTavernEntryAndEmbeddedRegexOverrides(t *testing.T) {
	config := &dto.SillyTavernPresetConfig{Preset: []byte(`{
		"prompts":[{"identifier":"main","role":"system","content":"ENABLED"},{"identifier":"optional","role":"system","content":"OPTIONAL"},{"identifier":"chatHistory","marker":true}],
		"prompt_order":[{"character_id":100001,"order":[{"identifier":"main","enabled":true},{"identifier":"optional","enabled":false},{"identifier":"chatHistory","enabled":true}]}],
		"extensions":{"regex_scripts":[
			{"id":"send","scriptName":"send","findRegex":"/foo/gi","replaceString":"bar","placement":[1],"promptOnly":true},
			{"id":"display","scriptName":"display","findRegex":"/bar/g","replaceString":"<script>bad()</script>","placement":[2],"markdownOnly":true}
		]}
	}`), EntryOverrides: map[string]bool{"main": false, "optional": true}, EnableEmbeddedRegex: true}
	request := &dto.GeneralOpenAIRequest{Model: "test-model", Messages: []dto.Message{{Role: "user", Content: "FOO foo"}, {Role: "assistant", Content: "bar"}}}
	compiled, _, err := CompileSillyTavernPreset(config, request, SillyTavernContext{})
	require.NoError(t, err)
	require.Len(t, compiled.Messages, 3)
	assert.Equal(t, "OPTIONAL", compiled.Messages[0].Content)
	assert.Equal(t, "FOO foo", compiled.Messages[1].Content, "receive-side regex must not alter upstream prompts")
	assert.Equal(t, "bar", compiled.Messages[2].Content, "display-only scripts must not rewrite API content")
	assert.Equal(t, "FOO foo", request.Messages[0].Content, "the original request must remain unchanged")
	config.EnableEmbeddedRegex = false
	plain, _, err := CompileSillyTavernPreset(config, request, SillyTavernContext{})
	require.NoError(t, err)
	assert.Equal(t, "FOO foo", plain.Messages[1].Content)
	config.EntryOverrides["unknown"] = true
	_, err = config.ParseAndValidate()
	require.ErrorContains(t, err, "unknown prompt")
	delete(config.EntryOverrides, "unknown")
	config.EntryOverrides["chatHistory"] = false
	withoutHistory, _, err := CompileSillyTavernPreset(config, request, SillyTavernContext{})
	require.NoError(t, err)
	require.Len(t, withoutHistory.Messages, 1, "disabling the chatHistory entry must remove history, not silently append it")
}

func TestSillyTavernTimeAndManualMacros(t *testing.T) {
	config := &dto.SillyTavernPresetConfig{Preset: []byte(`{"prompts":[{"identifier":"main","content":"{{isodate}} {{isotime}} {{weekday}} {{custom}} {{date}}"},{"identifier":"chatHistory","marker":true}],"prompt_order":[{"character_id":100001,"order":[{"identifier":"main","enabled":true},{"identifier":"chatHistory","enabled":true}]}]}`), TimeZone: "Asia/Shanghai", MacroValues: map[string]string{"custom": "ready", "date": "第一年春一日"}}
	request := &dto.GeneralOpenAIRequest{Model: "test-model", Messages: []dto.Message{{Role: "user", Content: "hi"}}}
	compiled, _, err := CompileSillyTavernPreset(config, request, SillyTavernContext{Now: time.Date(2026, 9, 26, 16, 30, 0, 0, time.UTC)})
	require.NoError(t, err)
	assert.Equal(t, "2026-09-27 00:30 Sunday ready 第一年春一日", compiled.Messages[0].Content)
	config.TimeZone = "not/a/timezone"
	_, err = config.ParseAndValidate()
	require.ErrorContains(t, err, "IANA time zone")
}

func TestSillyTavernUnorderedEntriesRequireExplicitEnable(t *testing.T) {
	config := &dto.SillyTavernPresetConfig{Preset: []byte(`{"prompts":[{"identifier":"later","content":"LATER"},{"identifier":"main","content":"MAIN"},{"identifier":"last","content":"LAST"}],"prompt_order":[{"order":[{"identifier":"main","enabled":true}]}]}`)}
	request := &dto.GeneralOpenAIRequest{Model: "test", Messages: []dto.Message{{Role: "user", Content: "hello"}}}
	compiled, _, err := CompileSillyTavernPreset(config, request, SillyTavernContext{})
	require.NoError(t, err)
	require.Len(t, compiled.Messages, 2)
	config.EntryOverrides = map[string]bool{"later": true, "last": true}
	compiled, _, err = CompileSillyTavernPreset(config, request, SillyTavernContext{})
	require.NoError(t, err)
	require.Len(t, compiled.Messages, 4)
	assert.Equal(t, "MAIN", compiled.Messages[0].Content)
	assert.Equal(t, "LATER", compiled.Messages[1].Content)
	assert.Equal(t, "LAST", compiled.Messages[2].Content)
}

func TestSillyTavernBoundsIntermediateExpansion(t *testing.T) {
	base := []byte(`{"prompts":[{"identifier":"main","content":"{{getvar::large}}"}],"prompt_order":[{"order":[{"identifier":"main","enabled":true}]}]}`)
	request := &dto.GeneralOpenAIRequest{Model: "test", Messages: []dto.Message{{Role: "user", Content: "hello"}}}
	t.Run("single macro value", func(t *testing.T) {
		_, _, err := CompileSillyTavernPreset(&dto.SillyTavernPresetConfig{Preset: base}, request, SillyTavernContext{Variables: map[string]string{"large": strings.Repeat("a", (8<<20)+1)}})
		require.Error(t, err)
	})
	t.Run("patch growth before later shrink", func(t *testing.T) {
		config := &dto.SillyTavernPresetConfig{Preset: []byte(`{"prompts":[{"identifier":"main","content":"` + strings.Repeat("a", 2304) + `"}],"prompt_order":[{"order":[{"identifier":"main","enabled":true}]}]}`), Patches: []dto.SillyTavernPresetPatch{{Find: "a", Replace: strings.Repeat("b", 4096)}, {Find: "bbbb", Replace: "c"}}}
		_, _, err := CompileSillyTavernPreset(config, request, SillyTavernContext{})
		require.Error(t, err)
	})
}
