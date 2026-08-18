package controller

import (
	"errors"
	"net/http"
	"regexp"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

var errorIncidentFingerprintPattern = regexp.MustCompile(`^cef1_[0-9a-f]{24}$`)

func validErrorIncidentFingerprint(c *gin.Context) (string, bool) {
	fingerprint := c.Param("fingerprint")
	if !errorIncidentFingerprintPattern.MatchString(fingerprint) {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "invalid error fingerprint"})
		return "", false
	}
	return fingerprint, true
}

func ListErrorIncidents(c *gin.Context) {
	pageInfo := common.GetPageQuery(c)
	unresolvedOnly, _ := strconv.ParseBool(c.DefaultQuery("unresolved_only", "false"))
	incidents, total, err := model.ListErrorIncidents(pageInfo.GetStartIdx(), pageInfo.GetPageSize(), unresolvedOnly)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(incidents)
	common.ApiSuccess(c, pageInfo)
}

type errorIncidentSample struct {
	CreatedAt         int64  `json:"created_at"`
	RequestId         string `json:"request_id"`
	UpstreamRequestId string `json:"upstream_request_id"`
	ChannelId         int    `json:"channel_id"`
	Model             string `json:"model"`
	IsStream          bool   `json:"is_stream"`
	UseTime           int    `json:"use_time"`
}

func GetErrorIncident(c *gin.Context) {
	fingerprint, ok := validErrorIncidentFingerprint(c)
	if !ok {
		return
	}
	incident, err := model.GetErrorIncident(fingerprint)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "error incident not found"})
			return
		}
		common.ApiError(c, err)
		return
	}
	logs, err := model.ListErrorIncidentSamples(fingerprint, 10)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	samples := make([]errorIncidentSample, 0, len(logs))
	for _, log := range logs {
		samples = append(samples, errorIncidentSample{
			CreatedAt: log.CreatedAt, RequestId: log.RequestId,
			UpstreamRequestId: log.UpstreamRequestId, ChannelId: log.ChannelId,
			Model: log.ModelName, IsStream: log.IsStream, UseTime: log.UseTime,
		})
	}
	common.ApiSuccess(c, gin.H{"incident": incident, "samples": samples})
}

func SetErrorIncidentResolved(c *gin.Context) {
	fingerprint, ok := validErrorIncidentFingerprint(c)
	if !ok {
		return
	}
	var body struct {
		Resolved bool `json:"resolved"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "invalid request body"})
		return
	}
	if err := model.SetErrorIncidentResolved(fingerprint, body.Resolved); err != nil {
		if errors.Is(err, model.ErrErrorIncidentNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "error incident not found"})
			return
		}
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"fingerprint": fingerprint, "resolved": body.Resolved})
}
