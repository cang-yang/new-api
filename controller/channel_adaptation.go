package controller

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

func GetChannelAdaptation(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	state, err := model.GetChannelAdaptationState(c.Request.Context(), id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, state)
}

func GetChannelAdaptationHealth(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	report, err := service.RecentAdaptationHealth(c.Request.Context(), id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, report)
}

func ChangeChannelAdaptation(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, dto.MaxSillyTavernPresetBytes+dto.MaxWorldbookBytes+(4<<20))
	var change model.ChannelAdaptationChange
	if err := common.DecodeJson(c.Request.Body, &change); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	state, err := model.ChangeChannelAdaptation(c.Request.Context(), id, change, validateAdaptationCandidate)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, model.ErrAdaptationConflict) || errors.Is(err, model.ErrAdaptationActivationRequired) {
			status = http.StatusConflict
		}
		common.ApiErrorStatus(c, status, err)
		return
	}
	// Requests read their adaptation snapshot from the database; updating this
	// cache is only an optimization for the management UI, not a correctness gate.
	if change.Action == "activate" {
		model.InitChannelCache()
	}
	recordManageAudit(c, "channel.adaptation", map[string]any{
		"id": id, "action": change.Action, "revision": state.Revision, "active_revision": state.ActiveRevision,
	})
	common.ApiSuccess(c, state)
}

func validateAdaptationCandidate(channel *model.Channel) error {
	settings := channel.GetOtherSettings()
	models := strings.Split(channel.Models, ",")
	// Static compilation only: never execute arbitrary scripts while holding a
	// channel write lock, and never fetch dependencies or invoke an upstream.
	for _, name := range models {
		report, err := service.InspectAdaptationScope(settings.AdaptationScope, strings.TrimSpace(name),
			settings.SillyTavernPreset, settings.Worldbook, settings.ResponseTextFilter)
		if err != nil {
			return err
		}
		if err := service.ValidateAdaptationFailureChoices(report, settings.SillyTavernPreset); err != nil {
			return err
		}
	}
	return nil
}
