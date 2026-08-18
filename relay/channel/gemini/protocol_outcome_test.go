package gemini

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func geminiProtocolTestContext() (*gin.Context, *httptest.ResponseRecorder, *relaycommon.RelayInfo) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	info := &relaycommon.RelayInfo{
		RelayFormat: types.RelayFormatOpenAI,
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gemini-test"},
	}
	return c, recorder, info
}

func geminiSSE(lines ...string) *http.Response {
	return &http.Response{Body: io.NopCloser(strings.NewReader("data: " + strings.Join(lines, "\ndata: ") + "\n"))}
}

func TestGeminiStreamProtocolOutcomes(t *testing.T) {
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
			name:        "text plus finish reason is complete without done sentinel",
			lines:       []string{`{"candidates":[{"content":{"role":"model","parts":[{"text":"hello"}]},"finishReason":"STOP"}]}`},
			wantOutcome: relaycommon.ResponseOutcomeComplete,
		},
		{
			name:        "tool call is meaningful",
			lines:       []string{`{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"name":"lookup","args":{}}}]},"finishReason":"STOP"}]}`},
			wantOutcome: relaycommon.ResponseOutcomeComplete,
		},
		{
			name:        "usage chunk after terminal remains complete",
			lines:       []string{`{"candidates":[{"content":{"role":"model","parts":[{"text":"hello"}]},"finishReason":"STOP"}]}`, `{"usageMetadata":{"promptTokenCount":2,"candidatesTokenCount":1,"totalTokenCount":3}}`},
			wantOutcome: relaycommon.ResponseOutcomeComplete,
		},
		{
			name:        "transport eof without finish reason is incomplete",
			lines:       []string{`{"candidates":[{"content":{"role":"model","parts":[{"text":"partial"}]}}]}`},
			wantOutcome: relaycommon.ResponseOutcomeIncomplete,
			wantError:   true,
		},
		{
			name:        "terminal stream without output is empty",
			lines:       []string{`{"candidates":[{"content":{"role":"model","parts":[]},"finishReason":"STOP"}]}`},
			wantOutcome: relaycommon.ResponseOutcomeEmpty,
			wantError:   true,
		},
		{
			name:        "blocked prompt is failed",
			lines:       []string{`{"promptFeedback":{"blockReason":"SAFETY"}}`},
			wantOutcome: relaycommon.ResponseOutcomeUpstreamFailed,
			wantError:   true,
		},
		{
			name:        "failed finish reason is failed",
			lines:       []string{`{"candidates":[{"content":{"role":"model","parts":[]},"finishReason":"SAFETY"}]}`},
			wantOutcome: relaycommon.ResponseOutcomeUpstreamFailed,
			wantError:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _, info := geminiProtocolTestContext()
			_, apiErr := geminiStreamHandler(c, info, geminiSSE(tt.lines...), func(_ string, _ *dto.GeminiChatResponse) bool { return true })
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

func TestGeminiNonStreamHandlersRejectEmptyBeforeWriting(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handlers := []struct {
		name string
		fn   func(*gin.Context, *relaycommon.RelayInfo, *http.Response) (*dto.Usage, *types.NewAPIError)
	}{
		{name: "chat", fn: GeminiChatHandler},
		{name: "native", fn: GeminiTextGenerationHandler},
		{name: "responses", fn: GeminiResponsesHandler},
	}

	for _, handler := range handlers {
		t.Run(handler.name, func(t *testing.T) {
			c, recorder, info := geminiProtocolTestContext()
			resp := &http.Response{Body: io.NopCloser(strings.NewReader(`{"candidates":[{"content":{"role":"model","parts":[]},"finishReason":"STOP"}]}`))}

			_, apiErr := handler.fn(c, info, resp)

			require.NotNil(t, apiErr)
			require.Equal(t, types.ErrorCodeEmptyResponse, apiErr.GetErrorCode())
			require.Equal(t, 0, recorder.Body.Len())
		})
	}
}
