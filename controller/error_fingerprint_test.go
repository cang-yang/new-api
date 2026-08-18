package controller

import (
	"errors"
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/stretchr/testify/require"
)

func TestFingerprintChannelErrorIsStableAndStructureOnly(t *testing.T) {
	base := channelErrorSignature{
		RequestPath: "/v1/chat/completions",
		ErrorType:   "upstream_error",
		ErrorCode:   "bad_response_status_code",
		StatusCode:  502,
		ChannelType: 1,
		Model:       "deepseek-v4",
		IsStream:    true,
	}

	first := fingerprintChannelError(base)
	second := fingerprintChannelError(base)
	require.Equal(t, first, second)
	require.Regexp(t, `^cef1_[0-9a-f]{24}$`, first)

	changed := base
	changed.StatusCode = 429
	require.NotEqual(t, first, fingerprintChannelError(changed))
}

func TestShouldAggregateChannelErrorOnlyTracksChannelHealthFailures(t *testing.T) {
	tests := []struct {
		name string
		err  *types.NewAPIError
		want bool
	}{
		{"rate limit", types.NewOpenAIError(errors.New("limited"), types.ErrorCodeBadResponseStatusCode, http.StatusTooManyRequests), true},
		{"upstream server error", types.NewOpenAIError(errors.New("bad gateway"), types.ErrorCodeBadResponseStatusCode, http.StatusBadGateway), true},
		{"empty protocol response", types.NewOpenAIError(errors.New("empty"), types.ErrorCodeEmptyResponse, http.StatusOK), true},
		{"client request error", types.NewOpenAIError(errors.New("bad request"), types.ErrorCodeBadRequestBody, http.StatusBadRequest), false},
		{"channel-marked credential error", types.NewError(errors.New("invalid channel key"), types.ErrorCodeChannelInvalidKey, types.ErrOptionWithStatusCode(http.StatusUnauthorized)), true},
		{"client disconnected", types.NewOpenAIError(errors.New("gone"), types.ErrorCodeBadResponse, 499), false},
		{"stream client gone", types.NewOpenAIError(errors.New("upstream stream ended with outcome client_gone"), types.ErrorCodeBadResponseBody, http.StatusBadGateway), false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, shouldAggregateChannelError(test.err))
		})
	}
}

func TestClientCancellationNeverAutoDisablesChannel(t *testing.T) {
	previous := common.AutomaticDisableChannelEnabled
	common.AutomaticDisableChannelEnabled = true
	t.Cleanup(func() { common.AutomaticDisableChannelEnabled = previous })
	err := types.NewOpenAIError(
		errors.New("upstream stream ended with outcome client_gone"),
		types.ErrorCodeBadResponseBody,
		http.StatusBadGateway,
	)
	require.False(t, shouldAutoDisableChannelForRequest(nil, err))
}

func TestFingerprintChannelErrorDoesNotDependOnChannelIdentity(t *testing.T) {
	signature := channelErrorSignature{
		RequestPath: "/v1/responses",
		ErrorCode:   "upstream_error",
		StatusCode:  502,
		ChannelType: 40,
		Model:       "gpt-5",
	}

	// Channel id/name and raw error text are intentionally not fields on the
	// signature, so backup channels producing the same structural failure group
	// together without hashing prompt or provider request-id text.
	require.NotEmpty(t, fingerprintChannelError(signature))
}
