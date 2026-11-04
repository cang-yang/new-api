package controller

import (
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

// PrepareChannelPresetScripts returns a draft only. Saving the channel remains
// the user's explicit action, so failed preparation cannot replace live scripts.
func PrepareChannelPresetScripts(c *gin.Context) {
	var request struct {
		Config *dto.SillyTavernPresetConfig `json:"config"`
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, dto.MaxSillyTavernPresetBytes+(2<<20))
	if err := common.DecodeJson(c.Request.Body, &request); err != nil || request.Config == nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Invalid script preparation request"})
		return
	}
	result, err := service.PreparePresetScriptDependencies(c.Request.Context(), request.Config)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Script preparation failed"})
		return
	}
	common.ApiSuccess(c, result)
}
