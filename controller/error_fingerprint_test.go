package controller

import (
	"testing"

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
