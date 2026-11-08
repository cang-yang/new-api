package controller

import (
	"encoding/json"
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

func ImportChannelWorldbook(c *gin.Context) {
	var request struct {
		Name string          `json:"name"`
		Book json.RawMessage `json:"book"`
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, dto.MaxWorldbookBytes+(64<<10))
	if err := common.DecodeJson(c.Request.Body, &request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Worldbook import request is invalid or too large"})
		return
	}
	book, err := service.ImportWorldbook(request.Name, request.Book)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": err.Error()})
		return
	}
	common.ApiSuccess(c, book)
}

func PreviewChannelWorldbook(c *gin.Context) {
	var request struct {
		Config            *dto.WorldbookConfig         `json:"config"`
		Request           *dto.GeneralOpenAIRequest    `json:"request"`
		SillyTavernPreset *dto.SillyTavernPresetConfig `json:"sillytavern_preset,omitempty"`
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, dto.MaxWorldbookBytes+(2<<20))
	if err := common.DecodeJson(c.Request.Body, &request); err != nil || request.Config == nil || request.Request == nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Worldbook preview request is invalid or too large"})
		return
	}
	preview, err := service.PreviewWorldbook(request.Config, request.SillyTavernPreset, request.Request)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": err.Error()})
		return
	}
	common.ApiSuccess(c, preview)
}

func InspectChannelSillyTavernPreset(c *gin.Context) {
	var request struct {
		SillyTavernPreset *dto.SillyTavernPresetConfig `json:"sillytavern_preset,omitempty"`
		Request           *dto.GeneralOpenAIRequest    `json:"request,omitempty"`
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, dto.MaxSillyTavernPresetBytes+(2<<20))
	if err := common.DecodeJson(c.Request.Body, &request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Preset capability request is invalid or too large"})
		return
	}
	report, err := service.BuildPresetCapabilityReport(request.SillyTavernPreset, request.Request)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": err.Error()})
		return
	}
	common.ApiSuccess(c, report)
}
