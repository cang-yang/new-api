package controller

import (
	"encoding/json"
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/gin-gonic/gin"
)

func applySelectedWorldbook(c *gin.Context, source, base *dto.GeneralOpenAIRequest, trace service.PresetCompileTrace) (*dto.GeneralOpenAIRequest, service.PresetCompileTrace) {
	settings, _ := common.GetContextKeyType[dto.ChannelOtherSettings](c, constant.ContextKeyChannelOtherSetting)
	channelSettings, _ := common.GetContextKeyType[dto.ChannelSettings](c, constant.ContextKeyChannelSetting)
	compiled, err := common.DeepCopy(base)
	if err != nil {
		return base, trace
	}
	passthrough := channelSettings.PassThroughBodyEnabled || model_setting.GetGlobalSettings().PassThroughRequestEnabled
	if settings.AdvancedCustom != nil && c.Request != nil && c.Request.URL != nil {
		if route, matched := settings.AdvancedCustom.MatchPathForModel(c.Request.URL.Path, source.Model); matched && route.PassThroughBodyEnabled {
			passthrough = true
		}
	}
	if passthrough {
		if settings.Worldbook != nil && settings.Worldbook.Enabled {
			trace.Worldbook = &service.WorldbookPreview{Entries: []service.WorldbookDecision{}, Warnings: []string{"Worldbook was skipped because body passthrough is enabled"}}
		}
		return compiled, trace
	}
	compiled, trace, _ = service.ApplyWorldbook(settings.Worldbook, source, compiled, trace)
	return compiled, trace
}

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
		Config  *dto.WorldbookConfig      `json:"config"`
		Request *dto.GeneralOpenAIRequest `json:"request"`
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, dto.MaxWorldbookBytes+(2<<20))
	if err := common.DecodeJson(c.Request.Body, &request); err != nil || request.Config == nil || request.Request == nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Worldbook preview request is invalid or too large"})
		return
	}
	preview, err := service.MatchWorldbook(request.Config, request.Request)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": err.Error()})
		return
	}
	common.ApiSuccess(c, preview)
}
