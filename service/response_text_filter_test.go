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
