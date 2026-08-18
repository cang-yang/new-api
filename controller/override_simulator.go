package controller

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
)

type paramOverrideSimulationRequest struct {
	UpstreamRequest json.RawMessage        `json:"upstream_request"`
	ParamOverride   map[string]interface{} `json:"param_override"`
	Context         map[string]interface{} `json:"context"`
}

func SimulateParamOverride(c *gin.Context) {
	var request paramOverrideSimulationRequest
	if err := common.DecodeJson(c.Request.Body, &request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "invalid simulator request: " + err.Error(),
		})
		return
	}
	if len(request.UpstreamRequest) == 0 || common.GetJsonType(request.UpstreamRequest) != "object" {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "upstream_request must be a JSON object",
		})
		return
	}
	if request.ParamOverride == nil {
		request.ParamOverride = map[string]interface{}{}
	}

	result, err := relaycommon.SimulateParamOverride(request.UpstreamRequest, request.ParamOverride, request.Context)
	if err != nil {
		var compileErr *relaycommon.ParamOverrideCompileError
		if errors.As(err, &compileErr) {
			c.JSON(http.StatusBadRequest, gin.H{
				"success": false,
				"message": "parameter override compilation failed",
				"data":    gin.H{"diagnostics": compileErr.Diagnostics},
			})
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "parameter override simulation failed",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    result,
	})
}
