package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/bytedance/gopkg/util/gopool"
	"github.com/gin-gonic/gin"
)

// DecideRelayRetry is the single retry decision for relay attempts. The reason
// is recorded in the request policy decision events of the log details.
func DecideRelayRetry(c *gin.Context, err *types.NewAPIError, retryTimes int) PolicyDecision {
	if err == nil {
		return PolicyDecision{Action: "stop", Reason: "request_completed", Source: "system"}
	}
	// Retrying after bytes reached the client would concatenate responses from
	// different channels and corrupt the downstream protocol stream.
	if c != nil && c.Writer != nil && c.Writer.Written() {
		return PolicyDecision{Action: "stop", Reason: "downstream_started", Source: "system"}
	}
	if ShouldSkipRetryAfterChannelAffinityFailure(c) {
		source := RequestPolicy(c).SessionModeSource
		if source == "" {
			source = "session_rule"
		}
		return PolicyDecision{Action: "stop", Reason: "strict_session", Source: source}
	}
	if GetChannelConstraints(c).SuppressesRetry() {
		return PolicyDecision{Action: "stop", Reason: "pinned_channel", Source: "channel_constraint"}
	}
	if types.IsChannelError(err) {
		return PolicyDecision{Action: "retry", Reason: "channel_error", Source: "system"}
	}
	if types.IsSkipRetryError(err) {
		return PolicyDecision{Action: "stop", Reason: "non_retryable_error", Source: "system"}
	}
	if retryTimes <= 0 {
		return PolicyDecision{Action: "stop", Reason: "attempt_budget_exhausted", Source: "global"}
	}
	code := err.StatusCode
	if code >= 200 && code < 300 {
		return PolicyDecision{Action: "stop", Reason: "system_retry_exclusion", Source: "system"}
	}
	if code < 100 || code > 599 {
		return PolicyDecision{Action: "retry", Reason: "unrecognized_status", Source: "system"}
	}
	if operation_setting.IsAlwaysSkipRetryCode(err.GetErrorCode()) || operation_setting.IsAlwaysSkipRetryStatusCode(code) {
		return PolicyDecision{Action: "stop", Reason: "system_retry_exclusion", Source: "system"}
	}
	if operation_setting.ShouldRetryByStatusCode(code) {
		return PolicyDecision{Action: "retry", Reason: "retry_status_matched", Source: "global"}
	}
	return PolicyDecision{Action: "stop", Reason: "status_not_retryable", Source: "global"}
}

func ShouldRetryRelayError(c *gin.Context, openaiErr *types.NewAPIError, retryTimes int) bool {
	return DecideRelayRetry(c, openaiErr, retryTimes).Action == "retry"
}

func ProcessChannelError(c *gin.Context, channelError types.ChannelError, err *types.NewAPIError, relayInfo *relaycommon.RelayInfo) {
	if err == nil {
		return
	}
	logger.LogError(c, fmt.Sprintf("channel error (channel #%d, status code: %d): %s", channelError.ChannelId, err.StatusCode, common.LocalLogPreview(err.MaskSensitiveErrorWithStatusCode())))
	clientGone := isClientGoneChannelError(c, err)
	if !clientGone && ShouldDisableChannel(err) && channelError.AutoBan {
		reason := err.MaskSensitiveErrorWithStatusCode()
		gopool.Go(func() {
			DisableChannel(channelError, reason)
		})
	}

	if types.IsRecordErrorLog(err) {
		userId := c.GetInt("id")
		tokenName := c.GetString("token_name")
		modelName := c.GetString("original_model")
		tokenId := c.GetInt("token_id")
		userGroup := c.GetString("group")
		other := model.NewLogOther()
		if c.Request != nil && c.Request.URL != nil {
			other.SetPublic("request_path", c.Request.URL.Path)
		}
		other.SetPublic("error_type", err.GetErrorType())
		other.SetPublic("error_code", err.GetErrorCode())
		other.SetPublic("status_code", err.StatusCode)
		other.SetPublic("channel_id", channelError.ChannelId)
		other.SetPublic("channel_name", c.GetString("channel_name"))
		other.SetPublic("channel_type", c.GetInt("channel_type"))

		requestPath := ""
		if c.Request != nil && c.Request.URL != nil {
			requestPath = c.Request.URL.Path
		}
		signature := channelErrorSignature{
			RequestPath: requestPath,
			ErrorType:   string(err.GetErrorType()),
			ErrorCode:   string(err.GetErrorCode()),
			StatusCode:  err.StatusCode,
			ChannelType: c.GetInt("channel_type"),
			Model:       modelName,
			IsStream:    common.GetContextKeyBool(c, constant.ContextKeyIsStream),
		}
		fingerprint := fingerprintChannelError(signature)
		other.SetPublic("error_fingerprint", fingerprint)
		other.SetPublic("error_signature", signature)
		if !clientGone && shouldAggregateChannelError(err) {
			incident, incidentErr := model.UpsertErrorIncident(model.ErrorIncidentOccurrence{
				Fingerprint: fingerprint,
				RequestId:   c.GetString(common.RequestIdKey),
				StatusCode:  err.StatusCode,
				ErrorType:   string(err.GetErrorType()),
				ErrorCode:   string(err.GetErrorCode()),
				ChannelType: c.GetInt("channel_type"),
				Model:       modelName,
				Path:        requestPath,
			})
			if incidentErr != nil {
				logger.LogError(c, "failed to aggregate channel error incident: "+incidentErr.Error())
			} else {
				other.SetPublic("error_incident_count", incident.Count)
			}
		}
		if !constant.ErrorLogEnabled {
			return
		}
		AppendRelayLogAdminInfo(c, relayInfo, other)
		AppendResponseModelLogInfo(relayInfo, other)
		AppendTaskPluginContextAuditInfo(c, other)
		startTime := common.GetContextKeyTime(c, constant.ContextKeyRequestStartTime)
		if startTime.IsZero() {
			startTime = time.Now()
		}
		useTimeSeconds := int(time.Since(startTime).Seconds())
		model.RecordErrorLog(c, userId, channelError.ChannelId, modelName, tokenName, err.MaskSensitiveErrorWithStatusCode(), tokenId, useTimeSeconds, common.GetContextKeyBool(c, constant.ContextKeyIsStream), userGroup, other)
	}
}

type channelErrorSignature struct {
	RequestPath string `json:"request_path,omitempty"`
	ErrorType   string `json:"error_type,omitempty"`
	ErrorCode   string `json:"error_code,omitempty"`
	StatusCode  int    `json:"status_code"`
	ChannelType int    `json:"channel_type"`
	Model       string `json:"model,omitempty"`
	IsStream    bool   `json:"is_stream"`
}

func fingerprintChannelError(signature channelErrorSignature) string {
	payload, _ := common.Marshal(signature)
	sum := sha256.Sum256(payload)
	return "cef1_" + hex.EncodeToString(sum[:12])
}

func isClientGoneChannelError(c *gin.Context, err *types.NewAPIError) bool {
	if err != nil && (errors.Is(err, context.Canceled) || strings.Contains(strings.ToLower(err.Error()), "client_gone")) {
		return true
	}
	return c != nil && c.Request != nil && errors.Is(c.Request.Context().Err(), context.Canceled)
}

func shouldAggregateChannelError(err *types.NewAPIError) bool {
	if err == nil || errors.Is(err, context.Canceled) || strings.Contains(strings.ToLower(err.Error()), "client_gone") {
		return false
	}
	if types.IsChannelError(err) || err.StatusCode == 429 || err.StatusCode >= 500 {
		return true
	}
	if err.StatusCode >= 400 && err.StatusCode < 500 {
		return false
	}
	switch err.GetErrorCode() {
	case types.ErrorCodeEmptyResponse, types.ErrorCodeBadResponse,
		types.ErrorCodeBadResponseBody, types.ErrorCodeReadResponseBodyFailed:
		return true
	default:
		return false
	}
}
