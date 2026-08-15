package controller

import (
	"encoding/base64"
	"errors"
	"net/http"
	"strconv"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type bodyAuditDetail struct {
	RequestId                   string `json:"request_id"`
	CreatedAt                   int64  `json:"created_at"`
	UpdatedAt                   int64  `json:"updated_at"`
	ModelName                   string `json:"model_name"`
	ChannelId                   int    `json:"channel_id"`
	RequestBody                 string `json:"request_body"`
	RequestBodyEncoding         string `json:"request_body_encoding"`
	RequestBodySize             int64  `json:"request_body_size"`
	RequestBodyTruncated        bool   `json:"request_body_truncated"`
	ResponseBody                string `json:"response_body"`
	ResponseBodyEncoding        string `json:"response_body_encoding"`
	ResponseBodySize            int64  `json:"response_body_size"`
	ResponseBodyTruncated       bool   `json:"response_body_truncated"`
	ResponseStatus              int    `json:"response_status"`
	ResponseContentType         string `json:"response_content_type"`
	ResponseComplete            bool   `json:"response_complete"`
	ClientResponseBody          string `json:"client_response_body"`
	ClientResponseBodyEncoding  string `json:"client_response_body_encoding"`
	ClientResponseBodySize      int64  `json:"client_response_body_size"`
	ClientResponseBodyTruncated bool   `json:"client_response_body_truncated"`
	ClientResponseStatus        int    `json:"client_response_status"`
	ClientResponseContentType   string `json:"client_response_content_type"`
	ClientResponseComplete      bool   `json:"client_response_complete"`
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
	common.ApiSuccess(c, bodyAuditDetail{
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
	})
}

func GetAllLogs(c *gin.Context) {
	pageInfo := common.GetPageQuery(c)
	logType, _ := strconv.Atoi(c.Query("type"))
	startTimestamp, _ := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	endTimestamp, _ := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	username := c.Query("username")
	tokenName := c.Query("token_name")
	modelName := c.Query("model_name")
	channel, _ := strconv.Atoi(c.Query("channel"))
	group := c.Query("group")
	requestId := c.Query("request_id")
	upstreamRequestId := c.Query("upstream_request_id")
	logs, total, err := model.GetAllLogs(logType, startTimestamp, endTimestamp, modelName, username, tokenName, pageInfo.GetStartIdx(), pageInfo.GetPageSize(), channel, group, requestId, upstreamRequestId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(logs)
	common.ApiSuccess(c, pageInfo)
	return
}

func GetUserLogs(c *gin.Context) {
	pageInfo := common.GetPageQuery(c)
	userId := c.GetInt("id")
	logType, _ := strconv.Atoi(c.Query("type"))
	startTimestamp, _ := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	endTimestamp, _ := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	tokenName := c.Query("token_name")
	modelName := c.Query("model_name")
	group := c.Query("group")
	requestId := c.Query("request_id")
	upstreamRequestId := c.Query("upstream_request_id")
	logs, total, err := model.GetUserLogs(userId, logType, startTimestamp, endTimestamp, modelName, tokenName, pageInfo.GetStartIdx(), pageInfo.GetPageSize(), group, requestId, upstreamRequestId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(logs)
	common.ApiSuccess(c, pageInfo)
	return
}

// Deprecated: SearchAllLogs 已废弃，前端未使用该接口。
func SearchAllLogs(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"success": false,
		"message": "该接口已废弃",
	})
}

// Deprecated: SearchUserLogs 已废弃，前端未使用该接口。
func SearchUserLogs(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"success": false,
		"message": "该接口已废弃",
	})
}

func GetLogByKey(c *gin.Context) {
	tokenId := c.GetInt("token_id")
	if tokenId == 0 {
		c.JSON(200, gin.H{
			"success": false,
			"message": "无效的令牌",
		})
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
