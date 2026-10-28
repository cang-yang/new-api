package controller

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type bodyAuditDetail struct {
	RequestId                   string               `json:"request_id"`
	CreatedAt                   int64                `json:"created_at"`
	UpdatedAt                   int64                `json:"updated_at"`
	ModelName                   string               `json:"model_name"`
	ChannelId                   int                  `json:"channel_id"`
	RequestBody                 string               `json:"request_body"`
	RequestBodyEncoding         string               `json:"request_body_encoding"`
	RequestBodySize             int64                `json:"request_body_size"`
	RequestBodyTruncated        bool                 `json:"request_body_truncated"`
	ResponseBody                string               `json:"response_body"`
	ResponseBodyEncoding        string               `json:"response_body_encoding"`
	ResponseBodySize            int64                `json:"response_body_size"`
	ResponseBodyTruncated       bool                 `json:"response_body_truncated"`
	ResponseStatus              int                  `json:"response_status"`
	ResponseContentType         string               `json:"response_content_type"`
	ResponseComplete            bool                 `json:"response_complete"`
	ClientResponseBody          string               `json:"client_response_body"`
	ClientResponseBodyEncoding  string               `json:"client_response_body_encoding"`
	ClientResponseBodySize      int64                `json:"client_response_body_size"`
	ClientResponseBodyTruncated bool                 `json:"client_response_body_truncated"`
	ClientResponseStatus        int                  `json:"client_response_status"`
	ClientResponseContentType   string               `json:"client_response_content_type"`
	ClientResponseComplete      bool                 `json:"client_response_complete"`
	Trace                       *auditTraceDetail    `json:"trace,omitempty"`
	Attempts                    []auditAttemptDetail `json:"attempts,omitempty"`
}

type auditTraceDetail struct {
	Id                 int64  `json:"id"`
	Status             string `json:"status"`
	Outcome            string `json:"outcome"`
	Source             string `json:"source"`
	FinalAttemptId     int64  `json:"final_attempt_id"`
	ClientTerminalKind string `json:"client_terminal_kind"`
	CompletedAt        int64  `json:"completed_at"`
}

type auditAttemptDetail struct {
	Id                    int64                      `json:"id"`
	AttemptNo             int                        `json:"attempt_no"`
	RoutingRetryIndex     int                        `json:"routing_retry_index"`
	ChannelId             int                        `json:"channel_id"`
	ChannelType           int                        `json:"channel_type"`
	ChannelName           string                     `json:"channel_name"`
	RequestModel          string                     `json:"request_model"`
	UpstreamModel         string                     `json:"upstream_model"`
	RequestFormat         string                     `json:"request_format"`
	UpstreamFormat        string                     `json:"upstream_format"`
	Target                string                     `json:"target"`
	State                 string                     `json:"state"`
	Outcome               string                     `json:"outcome"`
	HTTPStatus            int                        `json:"http_status"`
	ErrorCode             string                     `json:"error_code"`
	ErrorMessage          string                     `json:"error_message"`
	RetryAction           string                     `json:"retry_action"`
	TerminalKind          string                     `json:"terminal_kind"`
	Complete              bool                       `json:"complete"`
	DurationMs            int64                      `json:"duration_ms"`
	RequestBody           string                     `json:"request_body"`
	RequestBodyEncoding   string                     `json:"request_body_encoding"`
	RequestBodySize       int64                      `json:"request_body_size"`
	RequestBodyTruncated  bool                       `json:"request_body_truncated"`
	ResponseBody          string                     `json:"response_body"`
	ResponseBodyEncoding  string                     `json:"response_body_encoding"`
	ResponseBodySize      int64                      `json:"response_body_size"`
	ResponseBodyTruncated bool                       `json:"response_body_truncated"`
	ConfigSnapshot        *auditConfigSnapshotDetail `json:"config_snapshot,omitempty"`
}

type auditConfigSnapshotDetail struct {
	Id            int64           `json:"id"`
	Digest        string          `json:"digest"`
	SchemaVersion int             `json:"schema_version"`
	CanonicalJson json.RawMessage `json:"canonical_json"`
}

func bodyAuditPayload(data []byte) (string, string) {
	if utf8.Valid(data) {
		return string(data), "utf-8"
	}
	return base64.StdEncoding.EncodeToString(data), "base64"
}

func GetBodyAudit(c *gin.Context) {
	audit, err := model.GetBodyAuditByRequestId(c.Param("request_id"))
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{
				"success": false,
				"message": "body audit not found",
			})
			return
		}
		common.ApiError(c, err)
		return
	}
	requestBody, requestEncoding := bodyAuditPayload(audit.RequestBody)
	responseBody, responseEncoding := bodyAuditPayload(audit.ResponseBody)
	clientResponseBody, clientResponseEncoding := bodyAuditPayload(audit.ClientResponseBody)
	detail := bodyAuditDetail{
		RequestId:                   audit.RequestId,
		CreatedAt:                   audit.CreatedAt,
		UpdatedAt:                   audit.UpdatedAt,
		ModelName:                   audit.ModelName,
		ChannelId:                   audit.ChannelId,
		RequestBody:                 requestBody,
		RequestBodyEncoding:         requestEncoding,
		RequestBodySize:             audit.RequestBodySize,
		RequestBodyTruncated:        audit.RequestBodyTruncated,
		ResponseBody:                responseBody,
		ResponseBodyEncoding:        responseEncoding,
		ResponseBodySize:            audit.ResponseBodySize,
		ResponseBodyTruncated:       audit.ResponseBodyTruncated,
		ResponseStatus:              audit.ResponseStatus,
		ResponseContentType:         audit.ResponseContentType,
		ResponseComplete:            audit.ResponseComplete,
		ClientResponseBody:          clientResponseBody,
		ClientResponseBodyEncoding:  clientResponseEncoding,
		ClientResponseBodySize:      audit.ClientResponseBodySize,
		ClientResponseBodyTruncated: audit.ClientResponseBodyTruncated,
		ClientResponseStatus:        audit.ClientResponseStatus,
		ClientResponseContentType:   audit.ClientResponseContentType,
		ClientResponseComplete:      audit.ClientResponseComplete,
	}
	// Preserve the historical full response for callers that do not send the
	// query parameter. The log UI opts out explicitly and fetches attempt
	// payloads only when a multi-attempt trace is opened.
	includeAttemptBodies := c.Query("include_attempt_bodies") != "false"
	var trace *model.AuditTrace
	var attempts []model.AuditAttempt
	blobs := map[int64]model.AuditBlob{}
	var traceErr error
	if includeAttemptBodies {
		trace, attempts, blobs, traceErr = model.GetAuditTraceBundleByRequestId(audit.RequestId)
	} else {
		trace, traceErr = model.GetAuditTraceByRequestId(audit.RequestId)
		if traceErr == nil {
			attempts, traceErr = model.ListAuditAttempts(trace.Id)
		}
	}
	if traceErr == nil {
		detail.Trace = &auditTraceDetail{
			Id: trace.Id, Status: trace.Status, Outcome: trace.Outcome, Source: trace.Source,
			FinalAttemptId: trace.FinalAttemptId, ClientTerminalKind: trace.ClientTerminalKind,
			CompletedAt: trace.CompletedAt,
		}
		detail.Attempts = make([]auditAttemptDetail, 0, len(attempts))
		snapshotIds := make([]int64, 0, len(attempts))
		for _, attempt := range attempts {
			if attempt.ConfigSnapshotId != 0 {
				snapshotIds = append(snapshotIds, attempt.ConfigSnapshotId)
			}
		}
		snapshots, snapshotErr := model.GetAuditConfigSnapshots(snapshotIds)
		if snapshotErr != nil {
			snapshots = map[int64]model.AuditConfigSnapshot{}
		}
		for _, attempt := range attempts {
			attemptDetail := auditAttemptDetail{
				Id: attempt.Id, AttemptNo: attempt.AttemptNo, RoutingRetryIndex: attempt.RoutingRetryIndex,
				ChannelId: attempt.ChannelId, ChannelType: attempt.ChannelType, ChannelName: attempt.ChannelName,
				RequestModel: attempt.RequestModel, UpstreamModel: attempt.UpstreamModel,
				RequestFormat: attempt.RequestFormat, UpstreamFormat: attempt.UpstreamFormat,
				Target: attempt.Target, State: attempt.State, Outcome: attempt.Outcome,
				HTTPStatus: attempt.HTTPStatus, ErrorCode: attempt.ErrorCode, ErrorMessage: attempt.ErrorMessage,
				RetryAction: attempt.RetryAction, TerminalKind: attempt.TerminalKind,
				Complete: attempt.Complete, DurationMs: attempt.DurationMs,
			}
			if includeAttemptBodies {
				requestBlob := blobs[attempt.RequestBlobId]
				responseBlob := blobs[attempt.ResponseBlobId]
				attemptDetail.RequestBody, attemptDetail.RequestBodyEncoding = bodyAuditPayload(requestBlob.Body)
				attemptDetail.RequestBodySize, attemptDetail.RequestBodyTruncated = requestBlob.OriginalSize, requestBlob.Truncated
				attemptDetail.ResponseBody, attemptDetail.ResponseBodyEncoding = bodyAuditPayload(responseBlob.Body)
				attemptDetail.ResponseBodySize, attemptDetail.ResponseBodyTruncated = responseBlob.OriginalSize, responseBlob.Truncated
			}
			if snapshot, exists := snapshots[attempt.ConfigSnapshotId]; exists && json.Valid(snapshot.CanonicalJson) {
				attemptDetail.ConfigSnapshot = &auditConfigSnapshotDetail{
					Id: snapshot.Id, Digest: snapshot.Digest, SchemaVersion: snapshot.SchemaVersion,
					CanonicalJson: json.RawMessage(append([]byte(nil), snapshot.CanonicalJson...)),
				}
			}
			detail.Attempts = append(detail.Attempts, attemptDetail)
		}
	}
	common.ApiSuccess(c, detail)
}

// logFilterFromQuery reads the log search shared by the log list and the log
// export. Username and channel are read only for administrators.
func logFilterFromQuery(c *gin.Context, admin bool) model.LogFilter {
	logType, _ := strconv.Atoi(c.Query("type"))
	startTimestamp, _ := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	endTimestamp, _ := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	filter := model.LogFilter{
		Type:              logType,
		StartTimestamp:    startTimestamp,
		EndTimestamp:      endTimestamp,
		ModelName:         c.Query("model_name"),
		TokenName:         c.Query("token_name"),
		Group:             c.Query("group"),
		RequestId:         c.Query("request_id"),
		UpstreamRequestId: c.Query("upstream_request_id"),
	}
	if admin {
		filter.Username = c.Query("username")
		filter.Channel, _ = strconv.Atoi(c.Query("channel"))
	}
	return filter
}

// limitLogTimeRange returns the start of a log or dashboard search kept within
// the longest log time range. A missing start reaches back the whole range, and
// time after now does not count toward it.
func limitLogTimeRange(startTimestamp int64, endTimestamp int64) (int64, error) {
	maxRange := operation_setting.GetLogSetting().MaxRangeSeconds()
	end := common.GetTimestamp()
	if endTimestamp != 0 {
		end = min(end, endTimestamp)
	}
	if startTimestamp == 0 {
		return end - maxRange, nil
	}
	if end-startTimestamp > maxRange {
		return 0, common.NewMessage("The time range cannot exceed {{days}} days", map[string]any{"days": maxRange / (24 * 60 * 60)})
	}
	return startTimestamp, nil
}

func GetAllLogs(c *gin.Context) {
	pageInfo := common.GetPageQuery(c)
	logs, total, err := model.GetAllLogs(logFilterFromQuery(c, true), pageInfo.GetStartIdx(), pageInfo.GetPageSize())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if c.GetInt("role") < common.RoleRootUser {
		model.FormatAdminLogs(logs)
	} else {
		model.FormatRootLogs(logs)
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(logs)
	common.ApiSuccess(c, pageInfo)
	return
}

func GetUserLogs(c *gin.Context) {
	pageInfo := common.GetPageQuery(c)
	filter := logFilterFromQuery(c, false)
	if c.GetInt("role") < common.RoleAdminUser {
		startTimestamp, err := limitLogTimeRange(filter.StartTimestamp, filter.EndTimestamp)
		if err != nil {
			common.ApiError(c, err)
			return
		}
		filter.StartTimestamp = startTimestamp
	}
	logs, total, err := model.GetUserLogs(c.GetInt("id"), filter, pageInfo.GetStartIdx(), pageInfo.GetPageSize())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(logs)
	common.ApiSuccess(c, pageInfo)
	return
}

// ExportAllLogs streams every user's logs matching the search.
func ExportAllLogs(c *gin.Context) {
	exportLogs(c, true)
}

// ExportUserLogs streams the caller's own logs matching the search.
func ExportUserLogs(c *gin.Context) {
	if c.GetInt("role") < common.RoleAdminUser && !operation_setting.GetLogSetting().UserExportEnabled {
		common.ApiErrorT(c, "Log export is disabled")
		return
	}
	exportLogs(c, false)
}

// exportLogs writes one JSON log per line, newest first, and ends with an API
// response object: success with the log count, or the failure. Each user runs
// one export at a time, and every export stays within the longest log time
// range.
func exportLogs(c *gin.Context, all bool) {
	filter := logFilterFromQuery(c, all)
	startTimestamp, err := limitLogTimeRange(filter.StartTimestamp, filter.EndTimestamp)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	filter.StartTimestamp = startTimestamp

	userId := c.GetInt("id")
	release, ok, err := service.AcquireLogExportLock(userId)
	if err != nil {
		common.SysError(common.LogText("failed to lock log export: %s", err.Error()))
		common.ApiErrorT(c, "Failed to export logs")
		return
	}
	if !ok {
		common.ApiErrorT(c, "Another log export is running. Try again after it finishes.")
		return
	}
	defer release()

	c.Header("Content-Type", "application/x-ndjson; charset=utf-8")
	c.Header("Cache-Control", "no-store")
	c.Header("X-Accel-Buffering", "no")
	c.Status(http.StatusOK)

	count := 0
	writeLog := func(log *model.Log) error {
		line, err := common.Marshal(log)
		if err != nil {
			return err
		}
		if _, err := c.Writer.Write(append(line, '\n')); err != nil {
			return err
		}
		count++
		return nil
	}
	ctx := c.Request.Context()
	if all {
		err = model.StreamAllLogs(ctx, filter, c.GetInt("role"), writeLog)
	} else {
		err = model.StreamUserLogs(ctx, userId, filter, writeLog)
	}
	if ctx.Err() != nil {
		return
	}
	// The status is already sent, so the result object becomes the last line.
	if err != nil {
		common.SysError(common.LogText("failed to export logs: %s", err.Error()))
		common.ApiErrorT(c, "Failed to export logs")
		return
	}
	common.ApiSuccess(c, gin.H{"count": count})
}

// Deprecated: SearchAllLogs 已废弃，前端未使用该接口。
func SearchAllLogs(c *gin.Context) {
	common.ApiErrorT(c, "This API is deprecated")
}

// Deprecated: SearchUserLogs 已废弃，前端未使用该接口。
func SearchUserLogs(c *gin.Context) {
	common.ApiErrorT(c, "This API is deprecated")
}

func GetLogByKey(c *gin.Context) {
	tokenId := c.GetInt("token_id")
	if tokenId == 0 {
		common.ApiErrorI18n(c, i18n.MsgTokenInvalid)
		return
	}
	logs, err := model.GetLogByTokenId(tokenId)
	if err != nil {
		c.JSON(200, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	c.JSON(200, gin.H{
		"success": true,
		"message": "",
		"data":    logs,
	})
}

func GetLogsStat(c *gin.Context) {
	logType, _ := strconv.Atoi(c.Query("type"))
	startTimestamp, _ := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	endTimestamp, _ := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	tokenName := c.Query("token_name")
	username := c.Query("username")
	modelName := c.Query("model_name")
	channel, _ := strconv.Atoi(c.Query("channel"))
	group := c.Query("group")
	stat, err := model.SumUsedQuota(logType, startTimestamp, endTimestamp, modelName, username, tokenName, channel, group)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	//tokenNum := model.SumUsedToken(logType, startTimestamp, endTimestamp, modelName, username, "")
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"quota": stat.Quota,
			"rpm":   stat.Rpm,
			"tpm":   stat.Tpm,
		},
	})
	return
}

func GetLogsSelfStat(c *gin.Context) {
	username := c.GetString("username")
	logType, _ := strconv.Atoi(c.Query("type"))
	startTimestamp, _ := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	endTimestamp, _ := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	if c.GetInt("role") < common.RoleAdminUser {
		var err error
		if startTimestamp, err = limitLogTimeRange(startTimestamp, endTimestamp); err != nil {
			common.ApiError(c, err)
			return
		}
	}
	tokenName := c.Query("token_name")
	modelName := c.Query("model_name")
	channel, _ := strconv.Atoi(c.Query("channel"))
	group := c.Query("group")
	quotaNum, err := model.SumUsedQuota(logType, startTimestamp, endTimestamp, modelName, username, tokenName, channel, group)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	//tokenNum := model.SumUsedToken(logType, startTimestamp, endTimestamp, modelName, username, tokenName)
	c.JSON(200, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"quota": quotaNum.Quota,
			"rpm":   quotaNum.Rpm,
			"tpm":   quotaNum.Tpm,
			//"token": tokenNum,
		},
	})
	return
}
