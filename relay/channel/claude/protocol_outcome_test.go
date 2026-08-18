package claude

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func claudeProtocolTestContext() (*gin.Context, *httptest.ResponseRecorder, *relaycommon.RelayInfo) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	info := &relaycommon.RelayInfo{
		RelayFormat: types.RelayFormatClaude,
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "claude-test"},
	}
	return c, recorder, info
}

func claudeSSE(lines ...string) *http.Response {
	return &http.Response{Body: io.NopCloser(strings.NewReader("data: " + strings.Join(lines, "\ndata: ") + "\n"))}
}

func TestClaudeStreamProtocolOutcomes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	oldTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 300
	t.Cleanup(func() { constant.StreamingTimeout = oldTimeout })

	tests := []struct {
		name        string
		lines       []string
		wantOutcome relaycommon.ResponseOutcome
		wantError   bool
	}{
		{
			name: "text plus message stop is complete without done sentinel",
			lines: []string{
				`{"type":"message_start","message":{"id":"m1","model":"claude-test","usage":{"input_tokens":2}}}`,
				`{"type":"content_block_delta","delta":{"type":"text_delta","text":"hello"}}`,
				`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":1}}`,
				`{"type":"message_stop"}`,
			},
			wantOutcome: relaycommon.ResponseOutcomeComplete,
		},
		{
			name: "tool call is meaningful",
			lines: []string{
				`{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"t1","name":"lookup","input":{}}}`,
				`{"type":"message_stop"}`,
			},
			wantOutcome: relaycommon.ResponseOutcomeComplete,
		},
		{
			name:        "transport eof without message stop is incomplete",
			lines:       []string{`{"type":"content_block_delta","delta":{"type":"text_delta","text":"partial"}}`},
			wantOutcome: relaycommon.ResponseOutcomeIncomplete,
			wantError:   true,
		},
		{
			name:        "terminal stream without output is empty",
			lines:       []string{`{"type":"message_start","message":{"id":"m1","model":"claude-test"}}`, `{"type":"message_stop"}`},
			wantOutcome: relaycommon.ResponseOutcomeEmpty,
			wantError:   true,
		},
		{
			name:        "upstream error event is failed",
			lines:       []string{`{"type":"error","error":{"type":"overloaded_error","message":"busy"}}`},
			wantOutcome: relaycommon.ResponseOutcomeUpstreamFailed,
			wantError:   true,
		},
		{
			name: "refusal terminal is failed",
			lines: []string{
				`{"type":"content_block_delta","delta":{"type":"text_delta","text":"cannot comply"}}`,
				`{"type":"message_delta","delta":{"stop_reason":"refusal"}}`,
			},
			wantOutcome: relaycommon.ResponseOutcomeUpstreamFailed,
			wantError:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _, info := claudeProtocolTestContext()
			_, apiErr := ClaudeStreamHandler(c, claudeSSE(tt.lines...), info)
			if tt.wantError {
				require.NotNil(t, apiErr)
			} else {
				require.Nil(t, apiErr)
			}
			require.NotNil(t, info.StreamStatus)
			require.Equal(t, tt.wantOutcome, info.StreamStatus.Outcome(info.ReceivedResponseCount))
		})
	}
}

func TestClaudeNonStreamRejectsEmptyBeforeWriting(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, recorder, info := claudeProtocolTestContext()
	resp := &http.Response{Body: io.NopCloser(strings.NewReader(`{"type":"message","content":[],"stop_reason":"end_turn"}`))}

	_, apiErr := ClaudeHandler(c, resp, info)

	require.NotNil(t, apiErr)
	require.Equal(t, types.ErrorCodeEmptyResponse, apiErr.GetErrorCode())
	require.Equal(t, 0, recorder.Body.Len())
}
