package controller

import (
	"io"
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

func PreviewChannelRegex(c *gin.Context) {
	var request struct {
		Config *dto.ResponseTextFilter      `json:"config"`
		Preset *dto.SillyTavernPresetConfig `json:"preset"`
		Model  string                       `json:"model"`
		Stage  string                       `json:"stage"`
		Role   string                       `json:"role"`
		Depth  int                          `json:"depth"`
		Text   string                       `json:"text"`
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)
	if err := common.DecodeJson(c.Request.Body, &request); err != nil {
		if err == io.EOF {
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "regex preview request is empty"})
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "regex preview request is invalid or too large"})
		return
	}
	if len(request.Model) > 256 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "regex preview model is too long"})
		return
	}
	preview, err := service.PreviewTextRegex(request.Config, request.Preset, request.Model, request.Stage, request.Role, request.Depth, request.Text)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": err.Error()})
		return
	}
	common.ApiSuccess(c, preview)
}
