package service

import (
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestPresetScriptFailureDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		name, cause, want string
	}{
		{"memory", "preset script execution exceeded its memory limit", "memory limit"},
		{"deadline", "preset script execution exceeded its deadline", "deadline"},
		{"conflict", "ToolBindings has a duplicate or conflicting tool name", "conflicting tool name"},
		{"nested", `FinalRequestProcessing contains unsupported nested data in "credential"`, "field details withheld"},
		{"untrusted", "private prompt credential https://secret.invalid", "script execution or result validation failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := newResponseStageError(FailureStageScript, errors.New(tc.cause))
			require.ErrorContains(t, err, "preset script processing failed")
			require.ErrorContains(t, err, tc.want)
			require.NotContains(t, err.Error(), "credential")
			require.NotContains(t, err.Error(), "private prompt")
			require.NotContains(t, err.Error(), "secret.invalid")
			require.Equal(t, FailureStageScript, AdaptationErrorStage(err))
			require.EqualError(t, errors.Unwrap(err), tc.cause)
		})
	}
	t.Run("JS exception remains private", func(t *testing.T) {
		var output any
		err := runPresetScript(`() => { throw new Error("credential private prompt"); }`, nil, &output, time.Time{})
		require.Error(t, err)
		staged := newResponseStageError(FailureStageScript, err)
		require.NotContains(t, staged.Error(), "credential")
		require.NotContains(t, staged.Error(), "private prompt")
	})
	t.Run("execution timeout is distinguished", func(t *testing.T) {
		var output any
		err := runPresetScript(`() => { while (true) {} }`, nil, &output, time.Time{})
		require.ErrorContains(t, err, "execution exceeded its deadline")
	})
}

func TestSillyTavernNativeToolTextJSONAndStream(t *testing.T) {
	var preset dto.SillyTavernPresetConfig
	require.NoError(t, common.Unmarshal([]byte(`{
		"tool_text":{"enabled":true,"name":"newapi_text","argument":"display_stream"},
		"preset":{"prompts":[{"identifier":"main","content":"hi"}],
		"prompt_order":[{"order":[{"identifier":"main","enabled":true}]}]}
	}`), &preset))
	cases := []struct {
		name, body string
		stream     bool
	}{
		{"json", `{"choices":[{"index":0,"message":{"role":"assistant","reasoning_content":"keep","tool_calls":[{"id":"transport","type":"function","function":{"name":"newapi_text","arguments":"{\"display_stream\":\"正文\\n😀\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"total_tokens":42}}`, false},
		{"stream", "data: {\"choices\":[{\"index\":0,\"delta\":{\"reasoning_content\":\"keep\",\"tool_calls\":[{\"index\":0,\"id\":\"transport\",\"function\":{\"name\":\"newapi_text\",\"arguments\":\"{\\\"display_stream\\\":\\\"正文\\\\n\"}}]}}]}\n\n" +
			"data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"😀\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}],\"usage\":{\"total_tokens\":42}}\n\n" +
			"data: [DONE]\n\n", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			context, _ := gin.CreateTestContext(recorder)
			writer := BeginResponseTextFilterWithPreset(context, nil, &preset, "model")
			require.NotNil(t, writer, "native transport must work without regex rules")
			if tc.stream {
				context.Writer.Header().Set("Content-Type", "text/event-stream")
			}
			_, err := context.Writer.WriteString(tc.body)
			require.NoError(t, err)
			require.NoError(t, writer.Finish(context, true))
			output := recorder.Body.String()
			assert.Contains(t, output, `"content":"正文\n😀"`)
			assert.Contains(t, output, `"reasoning_content":"keep"`)
			assert.Contains(t, output, `"total_tokens":42`)
			assert.NotContains(t, output, `"tool_calls"`)
			assert.Contains(t, output, `"finish_reason":"stop"`)
			assert.NotContains(t, output, `"name":"newapi_text"`)
			if tc.stream {
				assert.True(t, strings.HasSuffix(output, "data: [DONE]\n\n"))
			}
		})
	}
}

func TestSillyTavernNativeToolTextIsolationAndValidation(t *testing.T) {
	tool := &dto.SillyTavernToolText{Enabled: true, Name: "newapi_text", Argument: "display_stream"}
	body := []byte(`{"choices":[
		{"index":0,"message":{"content":"prefix","tool_calls":[{"function":{"name":"newapi_text","arguments":"{\"display_stream\":\"first\"}"}},{"function":{"name":"newapi_text","arguments":"{\"display_stream\":\"second\"}"}}]},"finish_reason":"tool_calls"},
		{"index":1,"message":{"tool_calls":[{"function":{"name":"lookup","arguments":"{}"}}]},"finish_reason":"tool_calls"}
	],"usage":{"total_tokens":9007199254740993}}`)
	restored, err := restorePresetToolText(body, false, tool, time.Time{})
	require.Error(t, err)
	assert.Empty(t, restored, "ambiguous transport conversion must be atomic")
	for _, arguments := range []string{`{"display_stream":null}`, `{}`, `{"display_stream":42}`, `{"display_stream":"unfinished`} {
		encoded, err := common.Marshal(arguments)
		require.NoError(t, err)
		invalid := []byte(`{"choices":[{"message":{"tool_calls":[{"function":{"name":"newapi_text","arguments":` + string(encoded) + `}}]}}]}`)
		_, err = restorePresetToolText(invalid, false, tool, time.Time{})
		require.Error(t, err)
	}
	unchanged := []byte(`{"choices":[{"message":{"content":"ordinary","tool_calls":[{"function":{"name":"newapi_text_other","arguments":"{}"}}]}}]}`)
	result, err := restorePresetToolText(unchanged, false, tool, time.Time{})
	require.NoError(t, err)
	assert.Equal(t, unchanged, result)
}

func TestSillyTavernNativeToolTextRunsBeforeRegexAndHonorsModels(t *testing.T) {
	preset := &dto.SillyTavernPresetConfig{
		Models:   []string{"chosen"},
		Preset:   []byte(`{"prompts":[{"identifier":"main","content":"hi"}],"prompt_order":[{"order":[{"identifier":"main","enabled":true}]}]}`),
		ToolText: &dto.SillyTavernToolText{Enabled: true, Name: "text", Argument: "display_stream"},
	}
	require.Nil(t, BeginResponseTextFilterWithPreset(newFilterTestContext(), nil, preset, "other"))
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	filter := &dto.ResponseTextFilter{Mode: "tag_extract", StartTag: "<正文>", EndTag: "</正文>"}
	writer := BeginResponseTextFilterWithPreset(context, filter, preset, "chosen")
	require.NotNil(t, writer)
	_, err := writer.WriteString(`{"choices":[{"message":{"tool_calls":[{"function":{"name":"text","arguments":"{\"display_stream\":\"hidden<正文>hello</正文>\"}"}}]},"finish_reason":"tool_calls"}]}`)
	require.NoError(t, err)
	require.NoError(t, writer.Finish(context, true))
	assert.Equal(t, "hello", gjson.Get(recorder.Body.String(), "choices.0.message.content").String())
	assert.NotContains(t, recorder.Body.String(), "hidden")
}

func newFilterTestContext() *gin.Context {
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	return context
}

func TestSillyTavernNativeToolTextFailureDoesNotLeakArguments(t *testing.T) {
	preset := &dto.SillyTavernPresetConfig{
		Preset:   []byte(`{"prompts":[{"identifier":"main","content":"hi"}],"prompt_order":[{"order":[{"identifier":"main","enabled":true}]}]}`),
		ToolText: &dto.SillyTavernToolText{Enabled: true, Name: "text", Argument: "display_stream"},
	}
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	writer := BeginResponseTextFilterWithPreset(context, nil, preset, "model")
	require.NotNil(t, writer)
	_, err := writer.WriteString(`{"choices":[{"message":{"tool_calls":[{"function":{"name":"text","arguments":"{\"display_stream\":\"unfinished"}}]}}]}`)
	require.NoError(t, err)
	require.Error(t, writer.Finish(context, true))
	assert.Empty(t, recorder.Body.String())
}

func TestSillyTavernNativeToolTextRepeatedStreamNameAndSSEMetadata(t *testing.T) {
	config := &dto.SillyTavernToolText{Enabled: true, Name: "text", Argument: "body"}
	body := []byte("event: chunk\nid: 7\ndata: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"name\":\"text\",\"arguments\":\"{\\\"body\\\":\\\"hi\"}}]}}]}\n\n" +
		"data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"name\":\"text\",\"arguments\":\"\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}]}\nretry: 100\n\n" +
		"data: [DONE]\n\n")
	output, err := restorePresetToolText(body, true, config, time.Time{})
	require.NoError(t, err)
	assert.Contains(t, string(output), "event: chunk\n")
	assert.Contains(t, string(output), "id: 7\n")
	assert.Contains(t, string(output), "retry: 100\n")
	assert.Contains(t, string(output), `"content":"hi"`)
	assert.Contains(t, string(output), `"finish_reason":"stop"`)
	assert.NotContains(t, string(output), "tool_calls")
	assert.True(t, strings.HasSuffix(string(output), "data: [DONE]\n\n"))
	changedName := strings.Replace(string(body), `"name":"text","arguments":"\"}"`, `"name":"other","arguments":"\"}"`, 1)
	require.NotEqual(t, string(body), changedName)
	output, err = restorePresetToolText([]byte(changedName), true, config, time.Time{})
	require.Error(t, err, "legacy owned tool name changes must fail closed")
	assert.Empty(t, output)
}

func TestResponseTextFilterPreservesProtocolMetadata(t *testing.T) {
	config := &dto.ResponseTextFilter{Mode: "tag_extract", StartTag: "<主体>", EndTag: "</主体>"}
	cases := []struct {
		name string
		body string
		path string
	}{
		{"chat completion", `{"choices":[{"index":0,"message":{"content":"<思考>hidden</思考><主体>hello</主体>","reasoning":"keep"},"finish_reason":"stop"}],"usage":{"total_tokens":42}}`, "choices.0.message.content"},
		{"responses", `{"output":[{"type":"message","content":[{"type":"output_text","text":"<主体>hello</主体>"}]}],"usage":{"total_tokens":42}}`, "output.0.content.0.text"},
		{"claude", `{"content":[{"type":"thinking","thinking":"keep"},{"type":"text","text":"<主体>hello</主体>"}],"usage":{"output_tokens":42}}`, "content.1.text"},
		{"gemini", `{"candidates":[{"content":{"parts":[{"thought":true,"text":"keep"},{"text":"<主体>hello</主体>"}]}}],"usageMetadata":{"totalTokenCount":42}}`, "candidates.0.content.parts.1.text"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			context, _ := gin.CreateTestContext(recorder)
			writer := BeginResponseTextFilter(context, config, "model")
			require.NotNil(t, writer)
			_, err := context.Writer.WriteString(tc.body)
			require.NoError(t, err)
			require.NoError(t, writer.Finish(context, true))
			assert.Equal(t, "hello", gjson.Get(recorder.Body.String(), tc.path).String())
			assert.True(t, gjson.Get(recorder.Body.String(), "usage.total_tokens").Int() == 42 || gjson.Get(recorder.Body.String(), "usage.output_tokens").Int() == 42 || gjson.Get(recorder.Body.String(), "usageMetadata.totalTokenCount").Int() == 42)
			assert.NotContains(t, recorder.Body.String(), "hidden")
		})
	}
}

func TestResponseTextFilterSSEAcrossChunks(t *testing.T) {
	config := &dto.ResponseTextFilter{Mode: "regex_extract", Pattern: `(?s)<主体>\s*(.*?)\s*</主体>`}
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	writer := BeginResponseTextFilter(context, config, "model")
	require.NotNil(t, writer)
	context.Writer.Header().Set("Content-Type", "text/event-stream")
	context.Writer.WriteHeader(-1) // Gin uses -1 for stream events.
	_, err := context.Writer.WriteString(": PING\n\n")
	require.NoError(t, err)
	frames := []string{
		`data: {"choices":[{"index":0,"delta":{"content":"<思考>skip</思考><主","reasoning":"keep"}}]}` + "\n\n",
		`data: {"choices":[{"index":0,"delta":{"content":"体>hello</主体>"}}]}` + "\n\n",
		"data: [DONE]\n\n",
	}
	for _, frame := range frames {
		_, err = context.Writer.WriteString(frame)
		require.NoError(t, err)
	}
	require.NoError(t, writer.Finish(context, true))
	assert.NotContains(t, recorder.Body.String(), ": PING", "buffered replies must not commit headers before validation")
	assert.Contains(t, recorder.Body.String(), `"content":"hello"`)
	assert.Contains(t, recorder.Body.String(), `"reasoning":"keep"`)
	assert.NotContains(t, recorder.Body.String(), "skip")
	assert.True(t, strings.HasSuffix(recorder.Body.String(), "data: [DONE]\n\n"))
}

func TestResponseTextFilterProtocolStreamFrames(t *testing.T) {
	config := &dto.ResponseTextFilter{Mode: "tag_extract", StartTag: "<主体>", EndTag: "</主体>"}
	cases := []struct {
		name   string
		frames string
		want   string
		keep   string
	}{
		{
			"responses",
			"event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"output_index\":0,\"content_index\":0,\"delta\":\"<主体>hel\"}\n\n" +
				"event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"output_index\":0,\"content_index\":0,\"delta\":\"lo</主体>\"}\n\n" +
				"event: response.output_text.done\ndata: {\"type\":\"response.output_text.done\",\"output_index\":0,\"content_index\":0,\"text\":\"<主体>hello</主体>\"}\n\n",
			`"delta":"hello"`, `"text":"hello"`,
		},
		{
			"claude",
			"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"thinking_delta\",\"thinking\":\"keep\"}}\n\n" +
				"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"text_delta\",\"text\":\"<主体>hel\"}}\n\n" +
				"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"text_delta\",\"text\":\"lo</主体>\"}}\n\n",
			`"text":"hello"`, `"thinking":"keep"`,
		},
		{
			"gemini",
			"data: {\"candidates\":[{\"index\":0,\"content\":{\"parts\":[{\"thought\":true,\"text\":\"keep\"},{\"text\":\"<主体>hel\"}]}}]}\n\n" +
				"data: {\"candidates\":[{\"index\":0,\"content\":{\"parts\":[{\"text\":\"lo</主体>\"}]}}]}\n\n",
			`"text":"hello"`, `"text":"keep"`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			context, _ := gin.CreateTestContext(recorder)
			writer := BeginResponseTextFilter(context, config, "model")
			require.NotNil(t, writer)
			context.Writer.Header().Set("Content-Type", "text/event-stream")
			_, err := context.Writer.WriteString(tc.frames)
			require.NoError(t, err)
			require.NoError(t, writer.Finish(context, true))
			assert.Contains(t, recorder.Body.String(), tc.want)
			assert.Contains(t, recorder.Body.String(), tc.keep)
			assert.NotContains(t, recorder.Body.String(), "</主体>")
		})
	}
}

func TestResponseTextFilterMissingMatchPassesThrough(t *testing.T) {
	config := &dto.ResponseTextFilter{Mode: "tag_extract", StartTag: "<主体>", EndTag: "</主体>", Models: []string{"model-a"}}
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	assert.Nil(t, BeginResponseTextFilter(context, config, "model-b"))
	writer := BeginResponseTextFilter(context, config, "model-a")
	require.NotNil(t, writer)
	original := `{"choices":[{"message":{"content":"plain text","reasoning":"keep"}}]}`
	_, err := context.Writer.WriteString(original)
	require.NoError(t, err)
	require.NoError(t, writer.Finish(context, true))
	assert.JSONEq(t, original, recorder.Body.String())
}

func TestResponseTextFilterMissingMatchCanSuppressContent(t *testing.T) {
	config := &dto.ResponseTextFilter{Mode: "tag_extract", StartTag: "<主体>", EndTag: "</主体>", MissingMatch: "empty"}
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	writer := BeginResponseTextFilter(context, config, "model")
	require.NotNil(t, writer)
	_, err := context.Writer.WriteString(`{"choices":[{"message":{"content":"unmatched","reasoning":"keep"}}]}`)
	require.NoError(t, err)
	require.NoError(t, writer.Finish(context, true))
	assert.Equal(t, "", gjson.Get(recorder.Body.String(), "choices.0.message.content").String())
	assert.Equal(t, "keep", gjson.Get(recorder.Body.String(), "choices.0.message.reasoning").String())
}

func TestEmbeddedPresetRegexTransformsClientResponseOnly(t *testing.T) {
	preset := &dto.SillyTavernPresetConfig{EnableEmbeddedRegex: true, Preset: []byte(`{
		"prompts":[{"identifier":"main","content":"hi"}],
		"prompt_order":[{"character_id":100001,"order":[{"identifier":"main","enabled":true}]}],
		"extensions":{"regex_scripts":[
			{"id":"receiver","findRegex":"/foo(?=bar)/g","replaceString":"X","placement":[2]},
			{"id":"sender","findRegex":"/bar/g","replaceString":"WRONG","placement":[2],"promptOnly":true},
			{"id":"disabled","disabled":true,"findRegex":"/X/g","replaceString":"WRONG","placement":[2],"markdownOnly":true}
		]}
	}`)}
	for _, streamed := range []bool{false, true} {
		recorder := httptest.NewRecorder()
		context, _ := gin.CreateTestContext(recorder)
		writer := BeginResponseTextFilterWithPreset(context, nil, preset, "model")
		require.NotNil(t, writer)
		if streamed {
			context.Writer.Header().Set("Content-Type", "text/event-stream")
			_, err := context.Writer.WriteString("data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"foo\",\"reasoning\":\"foobar\"}}]}\n\n" +
				"data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"bar foobar\"}}]}\n\n" + "data: [DONE]\n\n")
			require.NoError(t, err)
		} else {
			_, err := context.Writer.WriteString(`{"choices":[{"message":{"content":"foobar foobar","reasoning":"foobar"}}],"usage":{"total_tokens":42}}`)
			require.NoError(t, err)
		}
		require.NoError(t, writer.Finish(context, true))
		assert.Contains(t, recorder.Body.String(), "Xbar Xbar")
		assert.Contains(t, recorder.Body.String(), "foobar", "reasoning must remain untouched")
		assert.NotContains(t, recorder.Body.String(), "WRONG")
		if !streamed {
			assert.Equal(t, int64(42), gjson.Get(recorder.Body.String(), "usage.total_tokens").Int())
		}
	}
	preset.RegexOverrides = map[string]bool{"receiver": false}
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	assert.Nil(t, BeginResponseTextFilterWithPreset(context, nil, preset, "model"), "disabling the only receive-side rule should avoid buffering")
	preset.RegexOverrides = nil
	preset.Models = []string{"other-model"}
	context, _ = gin.CreateTestContext(httptest.NewRecorder())
	assert.Nil(t, BeginResponseTextFilterWithPreset(context, nil, preset, "model"), "model scope must also apply to embedded regex")
}

func TestEmbeddedPresetRegexTrimsCapturedTextOnly(t *testing.T) {
	preset := &dto.SillyTavernPresetConfig{EnableEmbeddedRegex: true, Preset: []byte(`{"prompts":[{"identifier":"main","content":"hi"}],"prompt_order":[{"character_id":100001,"order":[{"identifier":"main","enabled":true}]}],"extensions":{"regex_scripts":[{"id":"trim","findRegex":"/(?<body><body>[^<]+<\\/body>)/","replaceString":"prefix:$<body>:suffix","trimStrings":["<body>","</body>"],"placement":[2]}]}}`)}
	writer := &ResponseTextFilterWriter{}
	writer.regexes, _ = compilePresetResponseRegex(preset, "model")
	require.Len(t, writer.regexes, 1)
	output, changed := writer.transform("<body>hello</body>")
	assert.True(t, changed)
	assert.Equal(t, "prefix:hello:suffix", output)
}

func TestResponseTextFilterSSEFramingAndSnapshots(t *testing.T) {
	config := &dto.ResponseTextFilter{Mode: "tag_extract", StartTag: "<主体>", EndTag: "</主体>"}
	for _, input := range []string{
		"event: chunk\r\ndata:{\"choices\":[{\"delta\":{\"content\":\"<主体>hello</主体>\"}}]}\r\nid: retained\r\nretry: 1000\r\n\r\ndata: [DONE]\r\n\r\n",
		"event: chunk\ndata: {\"choices\":\ndata: [{\"delta\":{\"content\":\"<主体>hello</主体>\"}}]}\nid: retained\nretry: 1000\n\ndata: [DONE]\n\n",
	} {
		recorder := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(recorder)
		writer := BeginResponseTextFilter(ctx, config, "model")
		ctx.Writer.Header().Set("Content-Type", "text/event-stream")
		_, err := ctx.Writer.WriteString(input)
		require.NoError(t, err)
		require.NoError(t, writer.Finish(ctx, true))
		assert.Contains(t, recorder.Body.String(), `"content":"hello"`)
		assert.NotContains(t, recorder.Body.String(), "主体")
		assert.Contains(t, recorder.Body.String(), "id: retained")
		assert.Contains(t, recorder.Body.String(), "retry: 1000")
		assert.Contains(t, recorder.Body.String(), "data: [DONE]")
	}
	for _, event := range []string{
		`{"type":"response.content_part.done","output_index":0,"content_index":0,"part":{"type":"output_text","text":"<主体>hello</主体>"}}`,
		`{"type":"response.output_item.done","output_index":0,"item":{"type":"message","content":[{"type":"output_text","text":"<主体>hello</主体>"}]}}`,
	} {
		ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
		writer := BeginResponseTextFilter(ctx, config, "model")
		body, err := writer.filterSSE([]byte("data: " + event + "\n\n"))
		require.NoError(t, err)
		assert.NotContains(t, string(body), "主体")
		assert.Contains(t, string(body), `"text":"hello"`)
	}
}

func TestResponseTextFilterDoesNotPublishFailedAttempt(t *testing.T) {
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	writer := BeginResponseTextFilter(ctx, &dto.ResponseTextFilter{Mode: "tag_extract", StartTag: "<主体>", EndTag: "</主体>"}, "model")
	_, err := writer.WriteString(`{"error":"failed attempt"}`)
	require.NoError(t, err)
	require.NoError(t, writer.Finish(ctx, false))
	assert.Empty(t, recorder.Body.String(), "retry must not concatenate failed and successful response bodies")
	assert.False(t, ctx.Writer.Written())
}

func TestResponseTextFilterStrictParseFailureDoesNotLeakRawText(t *testing.T) {
	for _, contentType := range []string{"application/json", "text/event-stream"} {
		recorder := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(recorder)
		writer := BeginResponseTextFilter(ctx, &dto.ResponseTextFilter{Mode: "tag_extract", StartTag: "<主体>", EndTag: "</主体>", MissingMatch: "empty"}, "model")
		ctx.Writer.Header().Set("Content-Type", contentType)
		_, err := writer.WriteString("data: {malformed private text\n\n")
		require.NoError(t, err)
		require.Error(t, writer.Finish(ctx, true))
		assert.Empty(t, recorder.Body.String())
	}
}

func TestResponseTextFilterPreservesLargeNumericMetadata(t *testing.T) {
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	writer := BeginResponseTextFilter(ctx, &dto.ResponseTextFilter{Mode: "tag_extract", StartTag: "<主体>", EndTag: "</主体>"}, "model")
	output, err := writer.filterJSON([]byte(`{"id":9007199254740993,"choices":[{"index":1,"message":{"content":"<主体>hello</主体>"}}]}`))
	require.NoError(t, err)
	assert.Equal(t, "9007199254740993", gjson.GetBytes(output, "id").Raw)
	assert.Equal(t, "hello", gjson.GetBytes(output, "choices.0.message.content").String())
}

func TestResponseTextFilterLegacyTagsUseRegexWithoutChangingSavedConfig(t *testing.T) {
	config := &dto.ResponseTextFilter{Mode: "tag_extract", StartTag: "[body.*]", EndTag: "[/body?]", MissingMatch: "empty"}
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	writer := BeginResponseTextFilter(ctx, config, "model")
	require.NotNil(t, writer)
	require.Equal(t, "regex_extract", writer.config.Mode)
	require.NotNil(t, writer.pattern)
	assert.Equal(t, "tag_extract", config.Mode, "shared channel settings must not be mutated")
	for _, tc := range []struct{ input, want string }{
		{"[body.*]\u2003 hello\n[/body?] trailing [body.*]second[/body?]", "hello"},
		{"[body.*][/body?][body.*]second[/body?]", ""},
		{"[body.*]missing close", ""},
	} {
		got, _ := writer.transform(tc.input)
		assert.Equal(t, tc.want, got)
	}
}

func TestResponseTextFilterNoApplicableRulesPreservesImmediateStreaming(t *testing.T) {
	for _, config := range []*dto.ResponseTextFilter{nil, {Mode: "regex_extract", Pattern: "(body)", Models: []string{"other"}}} {
		recorder := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(recorder)
		original := ctx.Writer
		writer := BeginResponseTextFilterWithPreset(ctx, config, &dto.SillyTavernPresetConfig{}, "model")
		assert.Nil(t, writer)
		assert.Same(t, original, ctx.Writer)
		_, err := ctx.Writer.WriteString("data: first\n\n")
		require.NoError(t, err)
		ctx.Writer.Flush()
		assert.Equal(t, "data: first\n\n", recorder.Body.String())
		assert.True(t, recorder.Flushed)
	}
}

func TestResponseFailurePolicyVariesByStageAndContract(t *testing.T) {
	base := &dto.SillyTavernPresetConfig{Preset: []byte(`{"prompts":[]}`)}
	policy := responseFailurePolicy(base, nil, "m", false)
	assert.Equal(t, FailureFail, policy.Mode(FailureStageResponseValidation))
	assert.Equal(t, FailureFail, policy.Mode(FailureStageResponseRestore))
	assert.Equal(t, FailureFail, policy.Mode(FailureStageOutputProcessing))
	assert.Equal(t, FailureFail, policy.Mode(FailureStageRegex))
	assert.Equal(t, FailureReject, policy.Mode(FailureStageCrossChannelRetry))
	assert.Equal(t, FailureStop, policy.Mode(FailureStageResponseStarted))

	// A preset that owns a response script or transport must finish or fail.
	scripted := &dto.SillyTavernPresetConfig{Preset: []byte(`{"prompts":[]}`), ScriptRuntime: "spreset-v1"}
	policy = responseFailurePolicy(scripted, nil, "m", false)
	assert.Equal(t, FailureFail, policy.Mode(FailureStageResponseValidation))
	assert.Equal(t, FailureFail, policy.Mode(FailureStageResponseRestore))
	assert.Equal(t, FailureFail, policy.Mode(FailureStageOutputProcessing))

	// An explicit stage override wins over the script default.
	override := &dto.SillyTavernPresetConfig{
		Preset:        []byte(`{"prompts":[]}`),
		ScriptRuntime: "spreset-v1",
		FailurePolicy: &dto.SillyTavernFailurePolicy{ResponseRestore: "safe_raw"},
	}
	policy = responseFailurePolicy(override, nil, "m", false)
	assert.Equal(t, FailureSafeRaw, policy.Mode(FailureStageResponseRestore))
	assert.Equal(t, FailureFail, policy.Mode(FailureStageResponseValidation))

	// Embedded regex error policy and explicit regex stages map cleanly.
	policy = responseFailurePolicy(&dto.SillyTavernPresetConfig{Preset: []byte(`{"prompts":[]}`), RegexFailurePolicy: "error"}, nil, "m", false)
	assert.Equal(t, FailureFail, policy.Mode(FailureStageRegex))
	policy = responseFailurePolicy(&dto.SillyTavernPresetConfig{
		Preset:        []byte(`{"prompts":[]}`),
		FailurePolicy: &dto.SillyTavernFailurePolicy{Regex: "empty"},
	}, nil, "m", false)
	assert.Equal(t, FailureEmpty, policy.Mode(FailureStageRegex))
}

func TestResponseWriterStageModeFallsBackToDocumentedDefaults(t *testing.T) {
	empty := &ResponseTextFilterWriter{}
	assert.Equal(t, FailureFail, empty.stageMode(FailureStageResponseRestore))
	assert.Equal(t, FailureFail, empty.stageMode(FailureStageOutputProcessing))
	assert.Equal(t, FailureStop, empty.stageMode(FailureStageResponseStarted))

	policy := AdaptationFailurePolicy{responseRestore: FailureFail, regex: FailureEmpty}
	configured := &ResponseTextFilterWriter{policy: &policy}
	assert.Equal(t, FailureFail, configured.stageMode(FailureStageResponseRestore))
	assert.Equal(t, FailureEmpty, configured.stageMode(FailureStageRegex))
}

func TestResponseErrorStageTagging(t *testing.T) {
	tagged := newResponseStageError(FailureStageRegex, errors.New("boom"))
	assert.Equal(t, FailureStageRegex, responseErrorStage(tagged))
	assert.Equal(t, FailureStageResponseValidation, responseErrorStage(errors.New("plain")))
	assert.Nil(t, newResponseStageError(FailureStageRegex, nil))
}

func TestResponseInternalToolNamesIncludeTransportAndContent(t *testing.T) {
	writer := &ResponseTextFilterWriter{toolText: &dto.SillyTavernToolText{Enabled: true, Name: "transport"}}
	assert.True(t, writer.internalToolNames()["transport"])
	assert.False(t, writer.rawResponseSafeForFallback())

	plain := &ResponseTextFilterWriter{}
	assert.Nil(t, plain.internalToolNames())
	assert.True(t, plain.rawResponseSafeForFallback())

	content := &ResponseTextFilterWriter{scriptResponse: &PresetScriptResponse{
		protocol: &presetProtocolPlan{
			audit:    PresetProtocolAudit{Activated: true},
			internal: map[string]bool{"final_reply": true},
		},
	}}
	assert.True(t, content.internalToolNames()["final_reply"])
	assert.False(t, content.rawResponseSafeForFallback())
}
