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
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSillyTavernUnknownEnabledOrderEntryIsDiagnosed(t *testing.T) {
	config := &dto.SillyTavernPresetConfig{Preset: []byte(`{"prompts":[{"identifier":"chatHistory","marker":true}],"prompt_order":[{"order":[{"identifier":"missing","enabled":true},{"identifier":"chatHistory","enabled":true}]}]}`)}
	request := &dto.GeneralOpenAIRequest{Model: "m", Messages: []dto.Message{{Role: "user", Content: "hello"}}}
	compiled, trace, err := CompileSillyTavernPreset(config, request, SillyTavernContext{})
	require.NoError(t, err)
	assert.Equal(t, request.Messages, compiled.Messages)
	assert.Contains(t, trace.Warnings, `enabled prompt_order identifier "missing" has no matching prompt`)
	config.ContextMode = "exact"
	config.ReferenceSource = "custom"
	_, _, err = CompileSillyTavernPreset(config, request, SillyTavernContext{User: "u", Char: "c", Markers: map[string]string{}, ChatHistory: request.Messages, DialogueExamples: [][]dto.Message{}})
	require.ErrorContains(t, err, "missing")
}

func TestSillyTavernInactiveGenerationPromptIsDiagnosed(t *testing.T) {
	config := &dto.SillyTavernPresetConfig{Preset: []byte(`{"prompts":[{"identifier":"chatHistory","marker":true},{"identifier":"continue","content":"continue only","injection_trigger":["continue"]}],"prompt_order":[{"order":[{"identifier":"chatHistory","enabled":true},{"identifier":"continue","enabled":true}]}]}`)}
	request := &dto.GeneralOpenAIRequest{Model: "m", Messages: []dto.Message{{Role: "user", Content: "hello"}}}
	compiled, trace, err := CompileSillyTavernPreset(config, request, SillyTavernContext{})
	require.NoError(t, err)
	assert.Equal(t, request.Messages, compiled.Messages)
	assert.Contains(t, trace.Warnings, `preset prompt "continue" is not applicable to normal generation`)
}

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

func TestSillyTavernSavedEntryContentAndOrderAreApplied(t *testing.T) {
	// This is the channel settings payload after editing and saving a preset.
	settingsJSON := []byte(`{"sillytavern_preset":{"post_processing":"none","preset":{"prompts":[{"identifier":"first","name":"Renamed","role":"assistant","content":"edited {{user}}"},{"identifier":"second","role":"system","content":"second"},{"identifier":"off","role":"system","content":"disabled"},{"identifier":"chatHistory","marker":true}],"prompt_order":[{"character_id":100000,"order":[{"identifier":"first","enabled":true}]},{"character_id":100001,"order":[{"identifier":"second","enabled":true},{"identifier":"first","enabled":true},{"identifier":"off","enabled":false},{"identifier":"chatHistory","enabled":true}]}]},"user":"Alice"}}`)
	var settings dto.ChannelOtherSettings
	require.NoError(t, common.Unmarshal(settingsJSON, &settings))
	// Simulate serialization and reopening the saved channel.
	saved, err := common.Marshal(settings)
	require.NoError(t, err)
	require.NoError(t, common.Unmarshal(saved, &settings))
	request := &dto.GeneralOpenAIRequest{Model: "m", Messages: []dto.Message{{Role: "user", Content: "client"}}}
	got, _, err := CompileSillyTavernPreset(settings.SillyTavernPreset, request, SillyTavernContext{})
	require.NoError(t, err)
	require.Len(t, got.Messages, 3)
	assert.Equal(t, []dto.Message{{Role: "system", Content: "second"}, {Role: "assistant", Content: "edited Alice"}, {Role: "user", Content: "client"}}, got.Messages)
	assert.Equal(t, []dto.Message{{Role: "user", Content: "client"}}, request.Messages)
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

	merged := mergeAdjacentSillyTavernRoles(input)
	require.Len(t, merged, 5)
	assert.Equal(t, "setup\n\nrules", merged[0].message.Content)
	assert.Equal(t, "first\n\nsecond", merged[1].message.Content)
	assert.Equal(t, "assistant", merged[2].message.Role)
	assert.Equal(t, "system", merged[3].message.Role)

	semiStrict, err := semiStrictSillyTavernPostProcess(input)
	require.NoError(t, err)
	require.Len(t, semiStrict, 4)
	assert.Equal(t, "system", semiStrict[0].message.Role)
	assert.Equal(t, "setup\n\nrules", semiStrict[0].message.Content)
	assert.Equal(t, "user", semiStrict[3].message.Role)
	assert.Equal(t, "late rules\n\nlast", semiStrict[3].message.Content)

	singleUser, err := singleUserSillyTavernPostProcess(input, "", "")
	require.NoError(t, err)
	require.Len(t, singleUser, 1)
	assert.Equal(t, "user", singleUser[0].message.Role)
	assert.Equal(t, "setup\n\nrules\n\nfirst\n\nsecond\n\nreply\n\nlate rules\n\nlast", singleUser[0].message.Content)
}

func TestSillyTavernPostProcessingAliases(t *testing.T) {
	preset := []byte(`{"prompts":[{"identifier":"main","content":"main"}],"prompt_order":[{"order":[{"identifier":"main","enabled":true}]}]}`)
	request := &dto.GeneralOpenAIRequest{Model: "test", Messages: []dto.Message{{Role: "user", Content: "hello"}}}
	for _, mode := range []string{"claude", "semi", "single"} {
		t.Run(mode, func(t *testing.T) {
			config := &dto.SillyTavernPresetConfig{Preset: preset, PostProcessing: mode}
			compiled, _, err := CompileSillyTavernPreset(config, request, SillyTavernContext{})
			require.NoError(t, err)
			require.NotEmpty(t, compiled.Messages)
		})
	}
}

func TestSillyTavernSingleUserPrefixesSpeakersOnlyOnce(t *testing.T) {
	config := &dto.SillyTavernPresetConfig{
		Preset: []byte(`{"prompts":[{"identifier":"main","content":"policy"}],"prompt_order":[{"order":[{"identifier":"main","enabled":true}]}]}`),
		User:   "Alice", Char: "Bob", PostProcessing: "single_user",
	}
	request := &dto.GeneralOpenAIRequest{
		Model: "test",
		Messages: []dto.Message{
			{Role: "user", Content: "hello"},
			{Role: "assistant", Content: "Bob: welcome"},
		},
	}
	compiled, _, err := CompileSillyTavernPreset(config, request, SillyTavernContext{})
	require.NoError(t, err)
	require.Len(t, compiled.Messages, 1)
	assert.Equal(t, "user", compiled.Messages[0].Role)
	assert.Equal(t, "policy\n\nAlice: hello\n\nBob: welcome", compiled.Messages[0].Content)
	assert.Equal(t, "hello", request.Messages[0].Content)
}

func TestSillyTavernToolTextAddsOnlyDeclarativeTransportTool(t *testing.T) {
	request := &dto.GeneralOpenAIRequest{
		Model:    "test",
		Messages: []dto.Message{{Role: "user", Content: "hello"}},
		Tools: []dto.ToolCallRequest{{
			Type:     "function",
			Function: dto.FunctionRequest{Name: "lookup"},
		}},
	}
	config := &dto.SillyTavernPresetConfig{
		ToolText: &dto.SillyTavernToolText{Enabled: true, Name: "newapi_text", Argument: "display_stream"},
		Preset:   []byte(`{"prompts":[{"identifier":"main","content":"policy"}],"prompt_order":[{"order":[{"identifier":"main","enabled":true}]}]}`),
	}
	compiled, _, err := CompileSillyTavernPreset(config, request, SillyTavernContext{})
	require.NoError(t, err)
	require.Len(t, compiled.Tools, 2)
	assert.Equal(t, "lookup", compiled.Tools[0].Function.Name)
	assert.Equal(t, "newapi_text", compiled.Tools[1].Function.Name)
	assert.NotNil(t, compiled.Tools[1].Function.Parameters)
	assert.Nil(t, compiled.ToolChoice)
	assert.Len(t, request.Tools, 1, "compilation must not mutate client tools")
}

func TestSillyTavernExternalPresetCompatibility(t *testing.T) {
	path := os.Getenv("ST_COMPAT_PRESET_PATH")
	if path == "" {
		t.Skip("ST_COMPAT_PRESET_PATH is required")
	}
	preset, err := os.ReadFile(path)
	require.NoError(t, err)
	config := &dto.SillyTavernPresetConfig{Preset: preset, User: "User", Char: "Assistant"}
	request := &dto.GeneralOpenAIRequest{Model: "test", Messages: []dto.Message{{Role: "user", Content: "hello"}}}
	before := string(config.Preset)
	for _, mode := range []string{"none", "merge", "semi_strict", "strict", "single_user"} {
		t.Run(mode, func(t *testing.T) {
			config.PostProcessing = mode
			compiled, trace, err := CompileSillyTavernPreset(config, request, SillyTavernContext{})
			require.NoError(t, err)
			require.NotEmpty(t, compiled.Messages)
			assert.Contains(t, trace.Warnings, "Browser scripts are preserved but not executed; use native declarative features for server-side compatibility")
			for _, message := range compiled.Messages {
				if content, ok := message.Content.(string); ok {
					assert.NotRegexp(t, `(?i)\{\{roll[ :]+`, content, "numeric macros must expand automatically in the imported preset")
				}
			}
			assert.NotContains(t, strings.Join(trace.Warnings, ","), "unsupported dice macro")
			assert.Equal(t, "hello", request.Messages[0].Content)
			assert.Equal(t, before, string(config.Preset))
		})
	}
}

func TestSillyTavernToolTextDoesNotOverrideExplicitToolChoice(t *testing.T) {
	choice := "required"
	request := &dto.GeneralOpenAIRequest{Model: "test", ToolChoice: &choice, Messages: []dto.Message{{Role: "user", Content: "hello"}}}
	config := &dto.SillyTavernPresetConfig{
		ToolText: &dto.SillyTavernToolText{Enabled: true, Name: "newapi_text", Argument: "display_stream"},
		Preset:   []byte(`{"prompts":[{"identifier":"main","content":"policy"}],"prompt_order":[{"order":[{"identifier":"main","enabled":true}]}]}`),
	}
	_, _, err := CompileSillyTavernPreset(config, request, SillyTavernContext{})
	require.ErrorContains(t, err, "explicit client tool_choice")
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
	compiled, _, err = CompileSillyTavernPreset(config, request, SillyTavernContext{})
	require.NoError(t, err)
	assert.Same(t, request, compiled)
	// An unrelated model bypasses this preset; validate malformed data only
	// after selecting a model to which the preset actually applies.
	request.Model = "selected"
	_, _, err = CompileSillyTavernPreset(config, request, SillyTavernContext{})
	require.Error(t, err)
}

func TestSillyTavernPresetRejectsDuplicateOrderEntries(t *testing.T) {
	for _, identifier := range []string{"chatHistory", "main"} {
		t.Run(identifier, func(t *testing.T) {
			config := &dto.SillyTavernPresetConfig{Preset: []byte(fmt.Sprintf(`{"prompts":[{"identifier":%q,"content":"POLICY"}],"prompt_order":[{"order":[{"identifier":%q,"enabled":true},{"identifier":%q,"enabled":true}]}]}`, identifier, identifier, identifier))}
			_, err := config.ParseAndValidate()
			require.ErrorContains(t, err, "duplicate prompt_order identifier")
		})
	}
}

func TestSillyTavernPresetRejectsHistoryTriggerThatDropsClientMessages(t *testing.T) {
	config := &dto.SillyTavernPresetConfig{Preset: []byte(`{"prompts":[{"identifier":"main","content":"POLICY"},{"identifier":"chatHistory","marker":true,"injection_trigger":["continue"]}],"prompt_order":[{"order":[{"identifier":"main","enabled":true},{"identifier":"chatHistory","enabled":true}]}]}`)}
	request := &dto.GeneralOpenAIRequest{Model: "test", Messages: []dto.Message{{Role: "user", Content: "CURRENT REQUEST"}}}
	_, _, err := CompileSillyTavernPreset(config, request, SillyTavernContext{})
	require.ErrorContains(t, err, "chatHistory")
	assert.Equal(t, "CURRENT REQUEST", request.Messages[0].Content)
}

func TestSillyTavernPresetSingleCandidateOverridesClient(t *testing.T) {
	for _, policy := range []string{"preset", "client"} {
		t.Run(policy, func(t *testing.T) {
			three := 3
			request := &dto.GeneralOpenAIRequest{Model: "test", N: &three, Messages: []dto.Message{{Role: "user", Content: "HELLO"}}}
			config := &dto.SillyTavernPresetConfig{ParameterPolicy: policy, Preset: []byte(`{"n":1,"prompts":[{"identifier":"chatHistory","marker":true}],"prompt_order":[{"order":[{"identifier":"chatHistory","enabled":true}]}]}`)}
			compiled, _, err := CompileSillyTavernPreset(config, request, SillyTavernContext{})
			require.NoError(t, err)
			expected := 1
			if policy == "client" {
				expected = 3
			}
			require.NotNil(t, compiled.N)
			assert.Equal(t, expected, *compiled.N)
			assert.Equal(t, 3, *request.N, "compilation must not mutate the client's request")
		})
	}
}

func TestSillyTavernPresetKeepsDefaultCandidateOmitted(t *testing.T) {
	request := &dto.GeneralOpenAIRequest{Model: "test", Messages: []dto.Message{{Role: "user", Content: "HELLO"}}}
	config := &dto.SillyTavernPresetConfig{Preset: []byte(`{"n":1,"prompts":[{"identifier":"chatHistory","marker":true}],"prompt_order":[{"order":[{"identifier":"chatHistory","enabled":true}]}]}`)}
	compiled, _, err := CompileSillyTavernPreset(config, request, SillyTavernContext{})
	require.NoError(t, err)
	assert.Nil(t, compiled.N)
}

func TestSillyTavernPresetIndependentCharacterOrdersKeepHistoryOnce(t *testing.T) {
	request := &dto.GeneralOpenAIRequest{Model: "test", Messages: []dto.Message{{Role: "user", Content: "HELLO"}}}
	config := &dto.SillyTavernPresetConfig{Preset: []byte(`{"prompts":[{"identifier":"chatHistory","marker":true,"injection_trigger":["normal","continue"]}],"prompt_order":[{"character_id":100000,"order":[{"identifier":"chatHistory","enabled":true}]},{"character_id":100001,"order":[{"identifier":"chatHistory","enabled":true}]}]}`)}
	compiled, _, err := CompileSillyTavernPreset(config, request, SillyTavernContext{})
	require.NoError(t, err)
	require.Len(t, compiled.Messages, 1)
	assert.Equal(t, "HELLO", compiled.Messages[0].Content)
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

func TestSillyTavernRollMacroIsAutomaticAndSupportsSTSyntax(t *testing.T) {
	for _, test := range []struct {
		input    string
		min, max int64
	}{
		{"{{roll 1999999}}", 1, 1999999},
		{"{{roll 1d99999}}", 1, 99999},
		{"{{roll::1d20}}", 1, 20},
		{"{{roll:2d6+3}}", 5, 15},
		{"{{dice::d4}}", 1, 4},
		{"{{ROLL 2d1-3}}", -1, -1},
	} {
		t.Run(test.input, func(t *testing.T) {
			config := &dto.SillyTavernPresetConfig{Preset: []byte(`{"prompts":[{"identifier":"main","content":"` + test.input + `"}],"prompt_order":[{"order":[{"identifier":"main","enabled":true}]}]}`)}
			request := &dto.GeneralOpenAIRequest{Model: "test", Messages: []dto.Message{{Role: "user", Content: "hello"}}}
			compiled, trace, err := CompileSillyTavernPreset(config, request, SillyTavernContext{})
			require.NoError(t, err)
			require.NotEmpty(t, compiled.Messages)
			content, ok := compiled.Messages[0].Content.(string)
			require.True(t, ok)
			assert.NotContains(t, content, "{{roll")
			assert.NotContains(t, content, "{{dice")
			assert.NotContains(t, strings.Join(trace.Warnings, ","), "unsupported dice macro")
			value, err := strconv.ParseInt(content, 10, 64)
			require.NoError(t, err)
			assert.GreaterOrEqual(t, value, test.min)
			assert.LessOrEqual(t, value, test.max)
		})
	}
}

func TestSillyTavernRollMacroRejectsUnsafeFormula(t *testing.T) {
	for _, formula := range []string{"0", "0d20", "1001d20", "1d100000001", "1d20+nope", "1d", "1d1+9223372036854775807"} {
		_, ok := rollSillyTavernDice(formula, nil)
		assert.False(t, ok, formula)
	}
	budget := int64(2)
	value, ok := rollSillyTavernDice("2d1", &budget)
	require.True(t, ok)
	assert.Equal(t, int64(2), value)
	_, ok = rollSillyTavernDice("1", &budget)
	assert.False(t, ok, "execution must stop when the per-request budget is exhausted")
}

func TestSillyTavernMacroSyntaxAndRequestScopedState(t *testing.T) {
	now := time.Date(2026, 10, 10, 12, 34, 56, 123_000_000, time.UTC)
	for _, test := range []struct {
		input, expected string
	}{
		{"<user>|<bot>|<char>|<group>", "Alice|Bot|Bot|Bot"},
		{"{{reverse:你好abc}}|{{newline}}{{noop}}{{// comment}}", "cba好你|\n"},
		{"{{setvar:: n ::1}}{{incvar::n}}|{{decvar::n}}|{{addvar::n::2}}{{getvar::n}}", "2|1|3"},
		{`{{setvar::a::["x"]}}{{addvar::a::y}}{{getvar::a}}`, `["x","y"]`},
		{"{{setvar::n::{{roll 1}}}}{{getvar::n}}", "1"},
		{"{{setvar::n::1}}{{roll {{getvar::n}}}}|{{reverse:{{getvar::n}}abc}}", "1|cba1"},
		{"{{random:A}}|{{random::A}}|{{random A}}", "A|A|A"},
		{"{{random:: A :: A }}", " A "},
		{`{{random::a\,b}}`, "a,b"},
		{"{{datetimeformat YYYY-MM-DD [at] HH:mm:ss.SSS Z}}", "2026-10-10 at 12:34:56.123 +00:00"},
		{"{{datetimeformat [2006 YYYY] d dd ddd dddd}}", "2006 YYYY 6 Sa Sat Saturday"},
		{"{{time_UTC+8}}|{{time_UTC-4}}", "20:34|08:34"},
		{"{{timeDiff::2026-10-10 12:00::2026-10-10 10:00}}", "in 2 hours"},
		{"{{timeDiff::2026-10-09::2026-10-10}}", "a day ago"},
		{"{{description}}|{{persona}}|{{lastGenerationType}}|{{model}}", "Character|User persona|normal|test"},
	} {
		t.Run(test.input, func(t *testing.T) {
			preset, err := common.Marshal(map[string]any{
				"prompts":      []map[string]any{{"identifier": "main", "content": test.input}},
				"prompt_order": []map[string]any{{"order": []map[string]any{{"identifier": "main", "enabled": true}}}},
			})
			require.NoError(t, err)
			config := &dto.SillyTavernPresetConfig{User: "Alice", Char: "Bot", Preset: preset}
			request := &dto.GeneralOpenAIRequest{Model: "test", Messages: []dto.Message{{Role: "user", Content: "hello"}}}
			context := SillyTavernContext{Now: now, Markers: map[string]string{"charDescription": "Character", "personaDescription": "User persona"}}
			compiled, trace, err := CompileSillyTavernPreset(config, request, context)
			require.NoError(t, err)
			assert.Equal(t, test.expected, compiled.Messages[0].Content)
			assert.NotContains(t, strings.Join(trace.Warnings, ","), "unsupported macro")
			assert.Equal(t, "hello", request.Messages[0].Content)
			again, _, err := CompileSillyTavernPreset(config, request, context)
			require.NoError(t, err)
			assert.Equal(t, compiled.Messages, again.Messages, "state must not leak into a later request")
		})
	}
}

func TestSillyTavernMacroContextLimitsAndStablePick(t *testing.T) {
	config := &dto.SillyTavernPresetConfig{Preset: []byte(`{"openai_max_context":8192,"openai_max_tokens":512,"prompts":[{"identifier":"main","content":"{{maxPrompt}}|{{maxContextTokens}}|{{maxResponse}}|{{lastMessageId}}|{{input}}|{{getglobalvar::color}}|{{pick::A::B::C}}|{{lastMessage}}|{{lastCharMessage}}"}],"prompt_order":[{"order":[{"identifier":"main","enabled":true}]}]}`)}
	request := &dto.GeneralOpenAIRequest{Model: "test", Messages: []dto.Message{{Role: "user", Content: "old"}, {Role: "assistant", Content: "reply"}, {Role: "assistant", Content: ""}, {Role: "system", Content: "system"}}}
	input := ""
	context := SillyTavernContext{ChatID: "chat-1", Input: &input, GlobalVariables: map[string]string{"color": "blue"}}
	compiled, trace, err := CompileSillyTavernPreset(config, request, context)
	require.NoError(t, err)
	content := compiled.Messages[0].Content.(string)
	assert.Regexp(t, `^7680\|8192\|512\|3\|\|blue\|[ABC]\|system\|$`, content)
	assert.NotContains(t, strings.Join(trace.Warnings, ","), "pick uses content-only")
	again, _, err := CompileSillyTavernPreset(config, request, context)
	require.NoError(t, err)
	assert.Equal(t, compiled.Messages, again.Messages)
	context.MacroValues = map[string]string{"maxContextTokens": "16384", "idle_duration": "an hour"}
	override, _, err := CompileSillyTavernPreset(config, request, context)
	require.NoError(t, err)
	assert.Contains(t, override.Messages[0].Content.(string), "|16384|")
	assert.Equal(t, map[string]string{"color": "blue"}, context.GlobalVariables)
}

func TestSillyTavernUnavailableMacrosRemainVisibleAndWarn(t *testing.T) {
	config := &dto.SillyTavernPresetConfig{Preset: []byte(`{"prompts":[{"identifier":"main","content":"{{idle_duration}}|{{setglobalvar::x::1}}|{{lastSwipeId}}|{{roll bad}}"}],"prompt_order":[{"order":[{"identifier":"main","enabled":true}]}]}`)}
	request := &dto.GeneralOpenAIRequest{Model: "test", Messages: []dto.Message{{Role: "user", Content: "hi"}}}
	compiled, trace, err := CompileSillyTavernPreset(config, request, SillyTavernContext{})
	require.NoError(t, err)
	assert.Contains(t, compiled.Messages[0].Content.(string), "{{idle_duration}}")
	assert.Contains(t, strings.Join(trace.Warnings, ","), "unsupported macro: idle_duration")
	assert.Contains(t, strings.Join(trace.Warnings, ","), "unsupported dice macro: bad")
}

func TestSillyTavernCommonMacros(t *testing.T) {
	config := &dto.SillyTavernPresetConfig{User: "Alice", Char: "Bot", TimeZone: "UTC", Preset: []byte(`{"prompts":[{"identifier":"main","content":"<USER>|<CHAR>|{{newline}}|{{reverse::abc}}|{{random::A,B}}|{{lastMessage}}|{{lastUserMessage}}|{{input}}|{{allChatRange}}|{{datetimeformat YYYY-MM-DD}}"}],"prompt_order":[{"order":[{"identifier":"main","enabled":true}]}]}`)}
	request := &dto.GeneralOpenAIRequest{Model: "test", Messages: []dto.Message{{Role: "user", Content: "hello"}, {Role: "assistant", Content: "reply"}}}
	compiled, trace, err := CompileSillyTavernPreset(config, request, SillyTavernContext{})
	require.NoError(t, err)
	content := compiled.Messages[0].Content.(string)
	assert.Contains(t, content, "Alice|Bot|\n|cba|")
	assert.Regexp(t, `\|(A|B)\|`, content)
	assert.Contains(t, content, "|reply|hello|hello|0-1|")
	assert.NotContains(t, content, "{{")
	assert.NotContains(t, strings.Join(trace.Warnings, ","), "unsupported macro")
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
