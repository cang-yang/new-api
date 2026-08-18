package controller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"

	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
)

type channelErrorSignature struct {
	RequestPath string `json:"request_path,omitempty"`
	ErrorType   string `json:"error_type,omitempty"`
	ErrorCode   string `json:"error_code,omitempty"`
	StatusCode  int    `json:"status_code"`
	ChannelType int    `json:"channel_type"`
	Model       string `json:"model,omitempty"`
	IsStream    bool   `json:"is_stream"`
}

func shouldAggregateChannelError(err *types.NewAPIError) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || strings.Contains(strings.ToLower(err.Error()), "client_gone") {
		return false
	}
	if types.IsChannelError(err) {
		return true
	}
	// Ordinary client-side 4xx responses (including disconnect-style 499)
	// are not evidence of a broken upstream channel. Rate limits are the one
	// useful 4xx exception for channel health analysis.
	if err.StatusCode == 429 {
		return true
	}
	if err.StatusCode >= 400 && err.StatusCode < 500 {
		return false
	}
	if err.StatusCode >= 500 {
		return true
	}
	switch err.GetErrorCode() {
	case types.ErrorCodeEmptyResponse, types.ErrorCodeBadResponse,
		types.ErrorCodeBadResponseBody, types.ErrorCodeReadResponseBodyFailed:
		return true
	default:
		return false
	}
}

func isClientGoneChannelError(c *gin.Context, err *types.NewAPIError) bool {
	if err != nil && (errors.Is(err, context.Canceled) || strings.Contains(strings.ToLower(err.Error()), "client_gone")) {
		return true
	}
	return c != nil && c.Request != nil && errors.Is(c.Request.Context().Err(), context.Canceled)
}

func shouldAutoDisableChannelForRequest(c *gin.Context, err *types.NewAPIError) bool {
	return !isClientGoneChannelError(c, err) && service.ShouldDisableChannel(err)
}

func fingerprintChannelError(signature channelErrorSignature) string {
	payload, _ := json.Marshal(signature)
	sum := sha256.Sum256(payload)
	return "cef1_" + hex.EncodeToString(sum[:12])
}
