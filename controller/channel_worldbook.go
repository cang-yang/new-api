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
		SendRules         *dto.ResponseTextFilter      `json:"send_rules,omitempty"`
		Context           service.SillyTavernContext   `json:"context,omitempty"`
		DraftRevision     string                       `json:"draft_revision,omitempty"`
		FinalRequest      bool                         `json:"final_request,omitempty"`
		AdaptationScope   string                       `json:"adaptation_scope,omitempty"`
		Config            *dto.WorldbookConfig         `json:"config"`
		Request           *dto.GeneralOpenAIRequest    `json:"request"`
		SillyTavernPreset *dto.SillyTavernPresetConfig `json:"sillytavern_preset,omitempty"`
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, dto.MaxWorldbookBytes+(2<<20))
	if err := common.DecodeJson(c.Request.Body, &request); err != nil || request.Config == nil || request.Request == nil || len(request.DraftRevision) > 128 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Worldbook preview request is invalid or too large"})
		return
	}
	preview, err := service.PreviewWorldbookDraft(request.Config, request.SillyTavernPreset, request.SendRules, request.Request, request.Context, request.DraftRevision, request.FinalRequest, request.AdaptationScope)
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
		CheckScope        bool                         `json:"check_scope,omitempty"`
		AdaptationScope   string                       `json:"adaptation_scope,omitempty"`
		Model             string                       `json:"model,omitempty"`
		Worldbook         *dto.WorldbookConfig         `json:"worldbook,omitempty"`
		Filter            *dto.ResponseTextFilter      `json:"response_text_filter,omitempty"`
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, dto.MaxSillyTavernPresetBytes+dto.MaxWorldbookBytes+(2<<20))
	if err := common.DecodeJson(c.Request.Body, &request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Preset capability request is invalid or too large"})
		return
	}
	if request.CheckScope {
		report, err := service.InspectAdaptationScope(request.AdaptationScope, request.Model, request.SillyTavernPreset, request.Worldbook, request.Filter)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": err.Error()})
			return
		}
		common.ApiSuccess(c, report)
		return
	}
	report, err := service.BuildPresetCapabilityReport(request.SillyTavernPreset, request.Request)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": err.Error()})
		return
	}
	common.ApiSuccess(c, report)
}
