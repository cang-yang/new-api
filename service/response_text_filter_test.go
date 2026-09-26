package service

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

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
	assert.True(t, strings.HasPrefix(recorder.Body.String(), ": PING\n\n"))
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
			{"id":"receiver","findRegex":"/foo(?=bar)/g","replaceString":"X","placement":[2],"markdownOnly":true},
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
	preset := &dto.SillyTavernPresetConfig{EnableEmbeddedRegex: true, Preset: []byte(`{"prompts":[{"identifier":"main","content":"hi"}],"prompt_order":[{"character_id":100001,"order":[{"identifier":"main","enabled":true}]}],"extensions":{"regex_scripts":[{"id":"trim","findRegex":"/(?<body><body>[^<]+<\\/body>)/","replaceString":"prefix:$<body>:suffix","trimStrings":["<body>","</body>"],"placement":[2],"markdownOnly":true}]}}`)}
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
