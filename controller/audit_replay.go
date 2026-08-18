package controller

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay"
	relaychannel "github.com/QuantumNous/new-api/relay/channel"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

const (
	auditReplayModeClientLevel   = "client_level"
	auditReplayModeExactUpstream = "exact_upstream"
	auditReplayConfirmationTTL   = 5 * time.Minute
	auditReplayResponseLimit     = int64(32 * 1024 * 1024)
)

type auditReplayPreviewRequest struct {
	Mode      string `json:"mode"`
	AttemptId int64  `json:"attempt_id"`
}

type auditReplayExecuteRequest struct {
	ConfirmationToken string `json:"confirmation_token"`
}

type auditReplayResources struct {
	attempt *model.AuditAttempt
	body    *model.AuditBlob
	channel *model.Channel
	target  *url.URL
}

type clientReplayResources struct {
	body                   *model.AuditBlob
	token                  *model.Token
	route                  *url.URL
	credentialQueryRemoved bool
}

var auditReplayLoopbackBaseURL = func() (string, error) {
	port := strings.TrimSpace(os.Getenv("PORT"))
	if port == "" {
		port = strconv.Itoa(*common.Port)
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return "", errors.New("server listening port is invalid")
	}
	return "http://" + net.JoinHostPort("127.0.0.1", port), nil
}

func confirmationDigest(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func newAuditReplayConfirmation() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func isCredentialQueryKey(key string) bool {
	normalized := strings.NewReplacer("_", "", "-", "", " ", "").Replace(strings.ToLower(strings.TrimSpace(key)))
	switch normalized {
	case "authorization", "auth", "apikey", "xapikey", "xgoogapikey", "key", "accesskey",
		"accesstoken", "token", "signature", "sig", "password", "secret", "secretkey",
		"clientsecret", "credential":
		return true
	default:
		return false
	}
}

func isCredentialBodyField(key string) bool {
	normalized := strings.NewReplacer("_", "", "-", "", " ", "").Replace(strings.ToLower(strings.TrimSpace(key)))
	switch normalized {
	case "authorization", "apikey", "accesstoken", "token", "bearertoken",
		"idtoken", "refreshtoken", "password", "secret", "clientsecret", "signingsecret":
		return true
	default:
		return false
	}
}

func scrubCredentialQuery(query url.Values) url.Values {
	cleaned := make(url.Values, len(query))
	for key, values := range query {
		if isCredentialQueryKey(key) {
			continue
		}
		cleaned[key] = append([]string(nil), values...)
	}
	return cleaned
}

func sanitizedReplayTarget(rawTarget string) string {
	parsed, err := url.Parse(rawTarget)
	if err != nil {
		return "[invalid target]"
	}
	parsed.User = nil
	parsed.RawQuery = scrubCredentialQuery(parsed.Query()).Encode()
	parsed.Fragment = ""
	return parsed.String()
}

func buildExactReplayTarget(channelBaseURL, historicalTarget string) (*url.URL, error) {
	base, err := url.Parse(channelBaseURL)
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" || base.User != nil {
		return nil, errors.New("current channel base URL is not a safe HTTP target")
	}
	historical, err := url.Parse(historicalTarget)
	if err != nil || historical.Path == "" {
		return nil, errors.New("historical upstream target is invalid")
	}

	target := *base
	basePath := strings.TrimSuffix(base.EscapedPath(), "/")
	historicalPath := historical.EscapedPath()
	if basePath == "" || basePath == "/" || strings.HasPrefix(historicalPath, basePath+"/") || historicalPath == basePath {
		target.RawPath = historical.RawPath
		target.Path = historical.Path
	} else {
		joined := path.Join(base.Path, historical.Path)
		if !strings.HasPrefix(joined, "/") {
			joined = "/" + joined
		}
		target.Path = joined
		target.RawPath = ""
	}
	target.RawQuery = scrubCredentialQuery(historical.Query()).Encode()
	target.Fragment = ""
	target.User = nil
	return &target, nil
}

func isReplayableAIPath(method, requestPath string) bool {
	if method != http.MethodPost {
		return false
	}
	cleaned := strings.TrimSuffix(requestPath, "/")
	if cleaned == "" || !strings.HasPrefix(cleaned, "/") || path.Clean(cleaned) != cleaned {
		return false
	}
	standardEndpointSuffixes := []string{
		"/chat/completions", "/v1/completions", "/v1/responses", "/v1/responses/compact",
		"/v1/messages", "/v1/embeddings", "/v1/rerank", "/rerank", "/v1/images/generations",
	}
	for _, suffix := range standardEndpointSuffixes {
		if strings.HasSuffix(cleaned, suffix) && isSafeReplayPathPrefix(strings.TrimSuffix(cleaned, suffix)) {
			return true
		}
	}
	if strings.HasPrefix(cleaned, "/v1beta/models/") || strings.HasPrefix(cleaned, "/v1/models/") {
		separator := strings.LastIndex(cleaned, ":")
		if separator < 0 {
			return false
		}
		switch cleaned[separator+1:] {
		case "generateContent", "streamGenerateContent", "embedContent", "batchEmbedContents", "predict":
			return true
		}
	}
	return false
}

func isSafeReplayPathPrefix(prefix string) bool {
	for _, segment := range strings.Split(strings.Trim(prefix, "/"), "/") {
		normalized := strings.NewReplacer("_", "", "-", "").Replace(strings.ToLower(segment))
		switch normalized {
		case "admin", "admins", "administration", "manage", "management", "console",
			"control", "controlplane", "billing", "delete", "deletion", "destroy", "revoke":
			return false
		}
	}
	return true
}

func redactReplayPreviewValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		copyValue := make(map[string]any, len(typed))
		for key, item := range typed {
			if isCredentialBodyField(key) {
				copyValue[key] = "[REDACTED]"
				continue
			}
			copyValue[key] = redactReplayPreviewValue(item)
		}
		return copyValue
	case []any:
		copyValue := make([]any, len(typed))
		for index, item := range typed {
			copyValue[index] = redactReplayPreviewValue(item)
		}
		return copyValue
	default:
		return value
	}
}

func containsHistoricalCredential(value any) bool {
	switch typed := value.(type) {
	case map[string]any:
		for key, item := range typed {
			if isCredentialBodyField(key) {
				return true
			}
			if containsHistoricalCredential(item) {
				return true
			}
		}
	case []any:
		for _, item := range typed {
			if containsHistoricalCredential(item) {
				return true
			}
		}
	}
	return false
}

func replayBodyIsCredentialFreeJSON(body []byte) (bool, bool) {
	var value any
	if err := common.Unmarshal(body, &value); err != nil {
		return false, false
	}
	return !containsHistoricalCredential(value), true
}

func auditReplayBodyPreview(blob *model.AuditBlob) any {
	var value any
	if common.Unmarshal(blob.Body, &value) == nil {
		return redactReplayPreviewValue(value)
	}
	body, encoding := bodyAuditPayload(blob.Body)
	return gin.H{"value": body, "encoding": encoding}
}

func readAuditReplayResponse(reader io.Reader, limit int64) ([]byte, int64, bool, error) {
	observed, err := io.ReadAll(io.LimitReader(reader, limit+1))
	observedSize := int64(len(observed))
	truncated := observedSize > limit
	if truncated {
		observed = observed[:limit]
	}
	return observed, observedSize, truncated, err
}

func loadExactReplayResources(trace *model.AuditTrace, attemptId int64) (*auditReplayResources, string, error) {
	if attemptId == 0 {
		attemptId = trace.FinalAttemptId
	}
	if attemptId == 0 {
		return nil, "attempt_not_selected", nil
	}
	attempt, err := model.GetAuditAttemptById(attemptId)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, "attempt_not_found", nil
		}
		return nil, "", err
	}
	if attempt.TraceId != trace.Id {
		return nil, "attempt_not_in_trace", nil
	}
	if attempt.RequestBlobId == 0 {
		return nil, "upstream_request_not_captured", nil
	}
	body, err := model.GetAuditBlob(attempt.RequestBlobId)
	if err != nil {
		return nil, "", err
	}
	if body.Truncated || !body.Complete {
		return nil, "upstream_request_capture_incomplete", nil
	}
	credentialFree, validJSON := replayBodyIsCredentialFreeJSON(body.Body)
	if !validJSON {
		return nil, "upstream_request_body_is_not_json", nil
	}
	if !credentialFree {
		return nil, "historical_credentials_in_request_body", nil
	}
	if !isReplayableAIPath(attempt.Method, func() string {
		parsed, parseErr := url.Parse(attempt.Target)
		if parseErr != nil {
			return ""
		}
		return parsed.Path
	}()) {
		return nil, "non_ai_or_side_effecting_path", nil
	}
	channel, err := model.GetChannelById(attempt.ChannelId, true)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, "current_channel_not_found", nil
		}
		return nil, "", err
	}
	target, err := buildExactReplayTarget(channel.GetBaseURL(), attempt.Target)
	if err != nil {
		return nil, "unsafe_or_invalid_target", nil
	}
	return &auditReplayResources{attempt: attempt, body: body, channel: channel, target: target}, "", nil
}

func loadClientReplayResources(trace *model.AuditTrace) (*clientReplayResources, string, error) {
	if trace.OriginalRequestBlobId == 0 {
		return nil, "original_client_request_not_captured", nil
	}
	body, err := model.GetAuditBlob(trace.OriginalRequestBlobId)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, "original_client_request_not_captured", nil
		}
		return nil, "", err
	}
	if body.Truncated || !body.Complete {
		return nil, "original_client_request_capture_incomplete", nil
	}
	route, err := url.ParseRequestURI(trace.Route)
	if err != nil || route.IsAbs() || route.Host != "" || !strings.HasPrefix(route.Path, "/") {
		return nil, "invalid_original_client_route", nil
	}
	if !isReplayableAIPath(trace.Method, route.Path) {
		return nil, "non_ai_or_side_effecting_path", nil
	}
	originalQueryCount := len(route.Query())
	cleanedQuery := scrubCredentialQuery(route.Query())
	route.RawQuery = cleanedQuery.Encode()
	credentialQueryRemoved := len(cleanedQuery) != originalQueryCount
	credentialFree, validJSON := replayBodyIsCredentialFreeJSON(body.Body)
	if !validJSON {
		return nil, "original_client_request_body_is_not_json", nil
	}
	if !credentialFree {
		return nil, "historical_credentials_in_request_body", nil
	}
	token, err := model.GetTokenById(trace.TokenId)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) || trace.TokenId == 0 {
			return nil, "current_token_not_found", nil
		}
		return nil, "", err
	}
	if strings.TrimSpace(token.Key) == "" {
		return nil, "current_token_not_found", nil
	}
	if trace.UserId != 0 && token.UserId != trace.UserId {
		return nil, "current_token_owner_changed", nil
	}
	return &clientReplayResources{
		body: body, token: token, route: route, credentialQueryRemoved: credentialQueryRemoved,
	}, "", nil
}

func PreviewAuditReplay(c *gin.Context) {
	var request auditReplayPreviewRequest
	if err := common.DecodeJson(c.Request.Body, &request); err != nil {
		common.ApiErrorMsg(c, "invalid replay preview request")
		return
	}
	trace, err := model.GetAuditTraceByRequestId(c.Param("request_id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if request.Mode == auditReplayModeClientLevel {
		resources, unavailableReason, loadErr := loadClientReplayResources(trace)
		if loadErr != nil {
			common.ApiError(c, loadErr)
			return
		}
		if unavailableReason != "" {
			common.ApiSuccess(c, gin.H{
				"mode": request.Mode, "available": false, "unavailable_reason": unavailableReason,
				"risks": []string{"re_enters_current_routing_and_billing"},
			})
			return
		}
		confirmation, confirmationErr := newAuditReplayConfirmation()
		if confirmationErr != nil {
			common.ApiError(c, confirmationErr)
			return
		}
		expiresAt := time.Now().Add(auditReplayConfirmationTTL).UnixMilli()
		if err := model.CreateAuditReplayGrant(&model.AuditReplayGrant{
			TokenDigest: confirmationDigest(confirmation), TraceId: trace.Id,
			Mode: request.Mode, State: "prepared", ExpiresAt: expiresAt,
		}); err != nil {
			common.ApiError(c, err)
			return
		}
		common.ApiSuccess(c, gin.H{
			"mode": request.Mode, "available": true, "method": trace.Method,
			"target": resources.route.String(), "body": auditReplayBodyPreview(resources.body),
			"body_digest": resources.body.Digest,
			"differences": []gin.H{
				{"path": "routing", "change": "current_token_policy_and_current_channel_selection_are_reapplied"},
				{"path": "request.credentials", "change": "generated_from_current_token; historical_headers_are_never_reused"},
				{"path": "request.query_credentials", "change": func() string {
					if resources.credentialQueryRemoved {
						return "historical_credential_query_parameters_removed"
					}
					return "no_historical_credential_query_parameters_present"
				}()},
				{"path": "request.body", "change": "unchanged"},
			},
			"risks": []string{
				"re_enters_current_routing_and_billing", "may_select_a_different_channel",
				"may_incur_provider_cost", "current_token_limits_and_quota_are_enforced",
			},
			"confirmation_token": confirmation, "expires_at": expiresAt,
		})
		return
	}
	if request.Mode != auditReplayModeExactUpstream {
		common.ApiErrorMsg(c, "unsupported replay mode")
		return
	}

	resources, unavailableReason, err := loadExactReplayResources(trace, request.AttemptId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if unavailableReason != "" {
		common.ApiSuccess(c, gin.H{
			"mode": request.Mode, "available": false, "unavailable_reason": unavailableReason,
		})
		return
	}
	confirmation, err := newAuditReplayConfirmation()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	expiresAt := time.Now().Add(auditReplayConfirmationTTL).UnixMilli()
	if err := model.CreateAuditReplayGrant(&model.AuditReplayGrant{
		TokenDigest: confirmationDigest(confirmation), TraceId: trace.Id,
		AttemptId: resources.attempt.Id, Mode: request.Mode, State: "prepared", ExpiresAt: expiresAt,
	}); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{
		"mode": request.Mode, "available": true, "attempt_id": resources.attempt.Id,
		"method":            resources.attempt.Method,
		"historical_target": sanitizedReplayTarget(resources.attempt.Target),
		"target":            resources.target.String(),
		"body":              auditReplayBodyPreview(resources.body), "body_digest": resources.body.Digest,
		"differences": []gin.H{
			{"path": "target", "before": sanitizedReplayTarget(resources.attempt.Target), "after": resources.target.String(), "change": "rewritten_to_current_channel_base_url"},
			{"path": "request.credentials", "change": "generated_from_current_channel; historical_authorization_is_never_reused"},
			{"path": "request.body", "change": "unchanged"},
		},
		"risks":              []string{"sends_real_upstream_request", "may_incur_provider_cost", "bypasses_gateway_conversion_and_billing"},
		"confirmation_token": confirmation, "expires_at": expiresAt,
	})
}

func auditReplayResult(grant *model.AuditReplayGrant, idempotent bool) gin.H {
	result := gin.H{
		"state": grant.State, "replay_trace_id": grant.ReplayTraceId,
		"response_status": grant.ResponseStatus, "idempotent_replay": idempotent,
	}
	if grant.ResponseBlobId != 0 {
		if blob, err := model.GetAuditBlob(grant.ResponseBlobId); err == nil {
			body, encoding := bodyAuditPayload(blob.Body)
			result["response_body"] = body
			result["response_body_encoding"] = encoding
			result["response_truncated"] = blob.Truncated
		}
	}
	if grant.ReplayTraceId != 0 {
		if trace, err := model.GetAuditTraceById(grant.ReplayTraceId); err == nil {
			result["replay_request_id"] = trace.RequestId
		}
	}
	if grant.ErrorMessage != "" {
		result["error"] = grant.ErrorMessage
	}
	return result
}

func ExecuteAuditReplay(c *gin.Context) {
	var request auditReplayExecuteRequest
	if err := common.DecodeJson(c.Request.Body, &request); err != nil || strings.TrimSpace(request.ConfirmationToken) == "" {
		common.ApiErrorMsg(c, "confirmation_token is required")
		return
	}
	trace, err := model.GetAuditTraceByRequestId(c.Param("request_id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	grant, claimed, err := model.ClaimAuditReplayGrant(confirmationDigest(request.ConfirmationToken), trace.Id, time.Now().UnixMilli())
	if err != nil {
		if errors.Is(err, model.ErrAuditReplayGrantExpired) {
			common.ApiErrorMsg(c, "replay confirmation expired; request a new preview")
			return
		}
		if errors.Is(err, gorm.ErrRecordNotFound) {
			common.ApiErrorMsg(c, "confirmation does not match this trace")
			return
		}
		common.ApiError(c, err)
		return
	}
	if !claimed {
		common.ApiSuccess(c, auditReplayResult(grant, true))
		return
	}
	if grant.Mode == auditReplayModeClientLevel {
		executeClientLevelAuditReplay(c, grant, trace)
		return
	}
	if grant.Mode != auditReplayModeExactUpstream {
		_ = model.CompleteAuditReplayGrant(grant.Id, 0, 0, 0, "failed", "unsupported replay mode")
		common.ApiErrorMsg(c, "unsupported replay mode")
		return
	}
	resources, unavailableReason, err := loadExactReplayResources(trace, grant.AttemptId)
	if err != nil || unavailableReason != "" {
		message := unavailableReason
		if err != nil {
			message = err.Error()
		}
		_ = model.CompleteAuditReplayGrant(grant.Id, 0, 0, 0, "failed", message)
		common.ApiErrorMsg(c, message)
		return
	}

	replayTrace := &model.AuditTrace{
		RequestId: common.NewRequestId(), Source: "replay_exact_upstream", Status: "recording",
		UserId: c.GetInt("id"), Method: resources.attempt.Method, Route: resources.target.Path,
		RequestModel: resources.attempt.RequestModel, RelayFormat: resources.attempt.RequestFormat,
		ReplayOfTraceId: trace.Id,
	}
	headerOverrideKeys := make([]string, 0, len(resources.channel.GetHeaderOverride()))
	for key := range resources.channel.GetHeaderOverride() {
		headerOverrideKeys = append(headerOverrideKeys, key)
	}
	sort.Strings(headerOverrideKeys)
	targetOrigin := resources.target.Scheme + "://" + resources.target.Host
	snapshotPayload, _ := common.Marshal(map[string]any{
		"channel_id": resources.channel.Id, "channel_type": resources.channel.Type,
		"channel_target_origin": targetOrigin, "header_override_names": headerOverrideKeys,
		"credentials":       "current_at_execute_not_persisted",
		"source_attempt_id": resources.attempt.Id, "request_body_digest": resources.body.Digest,
	})
	snapshot, snapshotErr := model.GetOrCreateAuditConfigSnapshot("exact_upstream_replay", snapshotPayload)
	if snapshotErr == nil {
		replayTrace.PolicySnapshotId = snapshot.Id
	}
	if err := model.CreateAuditTrace(replayTrace); err != nil {
		_ = model.CompleteAuditReplayGrant(grant.Id, 0, 0, 0, "failed", err.Error())
		common.ApiError(c, err)
		return
	}
	replayAttempt := &model.AuditAttempt{
		TraceId: replayTrace.Id, AttemptNo: 0, RoutingRetryIndex: 0,
		ChannelId: resources.channel.Id, ChannelType: resources.channel.Type,
		RequestModel: resources.attempt.RequestModel, UpstreamModel: resources.attempt.UpstreamModel,
		RequestFormat: resources.attempt.RequestFormat, UpstreamFormat: resources.attempt.UpstreamFormat,
		RequestBlobId: resources.body.Id, Method: resources.attempt.Method,
		Target: resources.target.String(), State: "recording", StartedAt: time.Now().UnixMilli(),
	}
	if snapshot != nil {
		replayAttempt.ConfigSnapshotId = snapshot.Id
	}
	if err := model.CreateAuditAttempt(replayAttempt); err != nil {
		_ = model.FinalizeAuditTrace(replayTrace.Id, 0, 0, "failed", "persistence_error")
		_ = model.UpdateAuditTraceResult(replayTrace.Id, 0, "failed")
		_ = model.CompleteAuditReplayGrant(grant.Id, replayTrace.Id, 0, 0, "failed", err.Error())
		common.ApiError(c, err)
		return
	}
	failExecution := func(responseStatus int, terminalKind, message string) {
		_ = model.UpdateAuditAttemptCapture(replayAttempt.Id, resources.body.Id, 0, "failed", responseStatus, false, terminalKind)
		_ = model.UpdateAuditAttemptOutcome(replayAttempt.Id, 0, "failed")
		_ = model.FinalizeAuditTrace(replayTrace.Id, replayAttempt.Id, 0, "failed", terminalKind)
		_ = model.UpdateAuditTraceResult(replayTrace.Id, responseStatus, "failed")
		_ = model.CompleteAuditReplayGrant(grant.Id, replayTrace.Id, responseStatus, 0, "failed", message)
	}

	timeoutContext, cancel := context.WithTimeout(c.Request.Context(), 2*time.Minute)
	defer cancel()
	upstreamRequest, err := http.NewRequestWithContext(timeoutContext, resources.attempt.Method, resources.target.String(), bytes.NewReader(resources.body.Body))
	if err != nil {
		failExecution(0, "request_build_error", err.Error())
		common.ApiError(c, err)
		return
	}
	contentType := resources.body.MediaType
	if contentType == "" {
		contentType = "application/json"
	}
	upstreamRequest.Header.Set("Content-Type", contentType)
	upstreamRequest.Header.Set("Accept", "application/json, text/event-stream")

	replayContext := c.Copy()
	replayContext.Request = upstreamRequest
	if apiError := middleware.SetupContextForSelectedChannel(replayContext, resources.channel, resources.attempt.UpstreamModel); apiError != nil {
		message := "failed to load current channel credentials"
		failExecution(0, "authentication_error", message)
		common.ApiErrorMsg(c, message)
		return
	}
	apiType, supported := common.ChannelType2APIType(resources.channel.Type)
	if !supported {
		message := "current channel type does not support exact replay authentication"
		failExecution(0, "authentication_error", message)
		common.ApiErrorMsg(c, message)
		return
	}
	info := &relaycommon.RelayInfo{
		RequestId: replayTrace.RequestId, IsChannelTest: true,
		RelayMode:       relayconstant.Path2RelayMode(resources.target.Path),
		OriginModelName: resources.attempt.RequestModel,
		RequestURLPath:  resources.target.RequestURI(),
		RelayFormat:     types.RelayFormat(resources.attempt.UpstreamFormat),
	}
	info.InitChannelMeta(replayContext)
	info.ApiType = apiType
	adaptor := relay.GetAdaptor(apiType)
	if adaptor == nil {
		message := "current channel adaptor is unavailable"
		failExecution(0, "authentication_error", message)
		common.ApiErrorMsg(c, message)
		return
	}
	adaptor.Init(info)
	headers := upstreamRequest.Header
	if err := adaptor.SetupRequestHeader(replayContext, &headers, info); err != nil {
		message := "failed to prepare current channel authentication"
		failExecution(0, "authentication_error", message)
		common.ApiErrorMsg(c, message)
		return
	}
	overrides, err := relaychannel.ResolveHeaderOverride(info, replayContext)
	if err != nil {
		message := "failed to prepare current channel header overrides"
		failExecution(0, "authentication_error", message)
		common.ApiErrorMsg(c, message)
		return
	}
	for key, value := range overrides {
		upstreamRequest.Header.Set(key, value)
		if strings.EqualFold(key, "Host") {
			upstreamRequest.Host = value
		}
	}
	// Historical headers are never loaded. These are the only values present:
	// newly generated adaptor headers, current channel overrides, and safe media headers.
	upstreamRequest.Header.Del("Cookie")
	upstreamRequest.Header.Del("Proxy-Authorization")

	startedAt := time.Now()
	response, err := service.GetHttpClient().Do(upstreamRequest)
	if err != nil {
		failExecution(0, "transport_error", err.Error())
		common.ApiError(c, err)
		return
	}
	defer response.Body.Close()
	responseBytes, readSize, truncated, readErr := readAuditReplayResponse(response.Body, auditReplayResponseLimit)
	complete := readErr == nil && !truncated
	responseBlob, blobErr := model.CreateAuditBlob(&model.AuditBlob{
		CaptureStage: "upstream_response", MediaType: response.Header.Get("Content-Type"), Body: responseBytes,
		OriginalSize: readSize, Complete: complete, Truncated: truncated,
	})
	if blobErr != nil {
		failExecution(response.StatusCode, "persistence_error", blobErr.Error())
		common.ApiError(c, blobErr)
		return
	}
	state := "completed"
	traceStatus := "succeeded"
	terminalKind := "response_complete"
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusBadRequest || !complete {
		state = "failed"
		traceStatus = "failed"
		terminalKind = "incomplete_or_error"
	}
	_ = model.UpdateAuditAttemptCapture(replayAttempt.Id, resources.body.Id, responseBlob.Id, traceStatus, response.StatusCode, complete, terminalKind)
	_ = model.UpdateAuditAttemptOutcome(replayAttempt.Id, time.Since(startedAt).Milliseconds(), traceStatus)
	_ = model.FinalizeAuditTrace(replayTrace.Id, replayAttempt.Id, responseBlob.Id, traceStatus, terminalKind)
	_ = model.UpdateAuditTraceResult(replayTrace.Id, response.StatusCode, traceStatus)
	if err := model.CompleteAuditReplayGrant(grant.Id, replayTrace.Id, response.StatusCode, responseBlob.Id, state, func() string {
		if readErr != nil {
			return fmt.Sprintf("read upstream response: %v", readErr)
		}
		return ""
	}()); err != nil {
		common.ApiError(c, err)
		return
	}
	grant.State = state
	grant.ReplayTraceId = replayTrace.Id
	grant.ResponseStatus = response.StatusCode
	grant.ResponseBlobId = responseBlob.Id
	common.ApiSuccess(c, auditReplayResult(grant, false))
}

func executeClientLevelAuditReplay(c *gin.Context, grant *model.AuditReplayGrant, sourceTrace *model.AuditTrace) {
	resources, unavailableReason, err := loadClientReplayResources(sourceTrace)
	if err != nil || unavailableReason != "" {
		message := unavailableReason
		if err != nil {
			message = err.Error()
		}
		_ = model.CompleteAuditReplayGrant(grant.Id, 0, 0, 0, "failed", message)
		common.ApiErrorMsg(c, message)
		return
	}
	baseURL, err := auditReplayLoopbackBaseURL()
	if err != nil {
		_ = model.CompleteAuditReplayGrant(grant.Id, 0, 0, 0, "failed", err.Error())
		common.ApiError(c, err)
		return
	}
	base, err := url.Parse(baseURL)
	if err != nil || base.Scheme != "http" || base.User != nil || base.Host == "" {
		message := "loopback replay endpoint is invalid"
		_ = model.CompleteAuditReplayGrant(grant.Id, 0, 0, 0, "failed", message)
		common.ApiErrorMsg(c, message)
		return
	}
	parsedIP := net.ParseIP(base.Hostname())
	if parsedIP == nil || !parsedIP.IsLoopback() {
		message := "client replay endpoint must be loopback"
		_ = model.CompleteAuditReplayGrant(grant.Id, 0, 0, 0, "failed", message)
		common.ApiErrorMsg(c, message)
		return
	}
	target := base.ResolveReference(resources.route)
	if target.Host != base.Host || target.Scheme != base.Scheme {
		message := "client replay route escaped loopback"
		_ = model.CompleteAuditReplayGrant(grant.Id, 0, 0, 0, "failed", message)
		common.ApiErrorMsg(c, message)
		return
	}
	timeoutContext, cancel := context.WithTimeout(c.Request.Context(), 2*time.Minute)
	defer cancel()
	request, err := http.NewRequestWithContext(timeoutContext, sourceTrace.Method, target.String(), bytes.NewReader(resources.body.Body))
	if err != nil {
		_ = model.CompleteAuditReplayGrant(grant.Id, 0, 0, 0, "failed", err.Error())
		common.ApiError(c, err)
		return
	}
	contentType := resources.body.MediaType
	if contentType == "" {
		contentType = "application/json"
	}
	request.Header.Set("Content-Type", contentType)
	request.Header.Set("Accept", "application/json, text/event-stream")
	request.Header.Set("Authorization", "Bearer sk-"+strings.TrimPrefix(resources.token.Key, "sk-"))

	loopbackClient := newAuditReplayLoopbackHTTPClient()
	defer loopbackClient.CloseIdleConnections()
	loopbackStartedAt := time.Now().UnixMilli()
	response, err := loopbackClient.Do(request)
	if err != nil {
		_ = model.CompleteAuditReplayGrant(grant.Id, 0, 0, 0, "failed", err.Error())
		common.ApiError(c, err)
		return
	}
	defer response.Body.Close()
	responseBytes, responseSize, truncated, readErr := readAuditReplayResponse(response.Body, auditReplayResponseLimit)
	replayedRequestId := response.Header.Get(common.RequestIdKey)
	if replayedRequestId == "" {
		message := "loopback replay response did not include a request id"
		_ = model.CompleteAuditReplayGrant(grant.Id, 0, response.StatusCode, 0, "failed", message)
		common.ApiErrorMsg(c, message)
		return
	}
	replayedTrace, err := model.GetAuditTraceByRequestId(replayedRequestId)
	if err != nil || replayedTrace.TokenId != resources.token.Id || replayedTrace.Method != sourceTrace.Method ||
		replayedTrace.Route != resources.route.RequestURI() || replayedTrace.CreatedAt < loopbackStartedAt || replayedTrace.ReplayOfTraceId != 0 {
		message := "loopback replay did not produce an auditable trace"
		_ = model.CompleteAuditReplayGrant(grant.Id, 0, response.StatusCode, 0, "failed", message)
		common.ApiErrorMsg(c, message)
		return
	}
	if err := model.MarkAuditTraceReplayOf(replayedRequestId, sourceTrace.Id, "replay_client_level"); err != nil {
		message := "failed to link loopback replay trace"
		_ = model.CompleteAuditReplayGrant(grant.Id, 0, response.StatusCode, 0, "failed", message)
		common.ApiErrorMsg(c, message)
		return
	}
	responseBlobId := replayedTrace.ClientResponseBlobId
	if responseBlobId == 0 {
		fallbackBlob, blobErr := model.CreateAuditBlob(&model.AuditBlob{
			CaptureStage: "client_response", MediaType: response.Header.Get("Content-Type"), Body: responseBytes,
			OriginalSize: responseSize, Complete: readErr == nil && !truncated, Truncated: truncated,
		})
		if blobErr != nil {
			_ = model.CompleteAuditReplayGrant(grant.Id, replayedTrace.Id, response.StatusCode, 0, "failed", blobErr.Error())
			common.ApiError(c, blobErr)
			return
		}
		responseBlobId = fallbackBlob.Id
		_ = model.FinalizeAuditTrace(replayedTrace.Id, replayedTrace.FinalAttemptId, fallbackBlob.Id, replayedTrace.Status, replayedTrace.ClientTerminalKind)
	}
	state := "completed"
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusBadRequest || readErr != nil || truncated {
		state = "failed"
	}
	errorMessage := ""
	if readErr != nil {
		errorMessage = "failed to read loopback replay response"
	} else if truncated {
		errorMessage = "loopback replay response exceeded capture limit"
	}
	if err := model.CompleteAuditReplayGrant(grant.Id, replayedTrace.Id, response.StatusCode, responseBlobId, state, errorMessage); err != nil {
		common.ApiError(c, err)
		return
	}
	grant.State = state
	grant.ReplayTraceId = replayedTrace.Id
	grant.ResponseStatus = response.StatusCode
	grant.ResponseBlobId = responseBlobId
	grant.ErrorMessage = errorMessage
	common.ApiSuccess(c, auditReplayResult(grant, false))
}

func newAuditReplayLoopbackHTTPClient() *http.Client {
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: -1}
	transport := &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return nil, errors.New("invalid loopback dial address")
			}
			ip := net.ParseIP(host)
			if ip == nil || !ip.IsLoopback() {
				return nil, errors.New("audit replay dial blocked non-loopback destination")
			}
			return dialer.DialContext(ctx, network, address)
		},
		DisableKeepAlives: true,
	}
	return &http.Client{
		Transport: transport,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Timeout: 2 * time.Minute,
	}
}
