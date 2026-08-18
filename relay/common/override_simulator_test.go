package common

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCompileParamOverrideReportsAllStaticErrors(t *testing.T) {
	override := map[string]interface{}{
		"operations": []interface{}{
			map[string]interface{}{"mode": "unknown"},
			map[string]interface{}{"mode": "set"},
			map[string]interface{}{
				"mode":  "delete",
				"path":  "messages",
				"logic": "XOR",
				"conditions": []interface{}{
					map[string]interface{}{"path": "retry_index", "mode": "approximately", "value": 1},
				},
			},
		},
	}

	diagnostics := CompileParamOverride(override)
	require.Len(t, diagnostics, 5)
	assert.Equal(t, []string{
		"unknown_operation_mode",
		"path_required",
		"value_required",
		"invalid_condition_logic",
		"unknown_condition_mode",
	}, diagnosticCodes(diagnostics))
}

func TestCompileParamOverrideRejectsMalformedOperationsContainer(t *testing.T) {
	diagnostics := CompileParamOverride(map[string]interface{}{"operations": map[string]interface{}{}})
	require.Len(t, diagnostics, 1)
	assert.Equal(t, "operations_type", diagnostics[0].Code)
}

func TestSimulateParamOverrideUsesRealEngineAndTracesSkippedOperations(t *testing.T) {
	override := map[string]interface{}{
		"operations": []interface{}{
			map[string]interface{}{"mode": "set", "path": "temperature", "value": 0.7},
			map[string]interface{}{
				"mode": "set", "path": "reasoning_effort", "value": "high",
				"conditions": []interface{}{
					map[string]interface{}{"path": "retry_index", "mode": "gt", "value": 0},
				},
			},
			map[string]interface{}{"mode": "set_header", "path": "X-Debug", "value": "yes"},
		},
	}
	context := map[string]interface{}{
		"retry_index": 0,
		"request_headers": map[string]interface{}{
			"Authorization": "Bearer simulator-secret",
		},
		"header_override": map[string]interface{}{
			"X-Api-Key": "private-key",
		},
	}

	result, err := SimulateParamOverride([]byte(`{"model":"demo","api_key":"body-secret"}`), override, context)
	require.NoError(t, err)
	require.Len(t, result.Operations, 3)
	assert.Equal(t, "applied", result.Operations[0].Status)
	assert.Equal(t, "skipped", result.Operations[1].Status)
	assert.Equal(t, "condition_false", result.Operations[1].Reason)
	assert.Equal(t, "applied", result.Operations[2].Status)

	var after map[string]interface{}
	require.NoError(t, common.Unmarshal(result.After, &after))
	assert.Equal(t, 0.7, after["temperature"])
	assert.NotContains(t, after, "reasoning_effort")
	assert.Equal(t, redactedOverrideValue, after["api_key"])
	assert.Equal(t, "yes", result.Headers["x-debug"])
	assert.Equal(t, redactedOverrideValue, result.Headers["x-api-key"])

	encoded, err := common.Marshal(result)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "simulator-secret")
	assert.NotContains(t, string(encoded), "private-key")
	assert.NotContains(t, string(encoded), "body-secret")
}

func TestSimulateParamOverrideDoesNotRunWhenCompilationFails(t *testing.T) {
	result, err := SimulateParamOverride(
		[]byte(`{"model":"demo"}`),
		map[string]interface{}{"operations": []interface{}{map[string]interface{}{"mode": "set", "path": "x"}}},
		nil,
	)
	require.Error(t, err)
	assert.Nil(t, result)
	var compileErr *ParamOverrideCompileError
	require.ErrorAs(t, err, &compileErr)
	assert.Equal(t, "value_required", compileErr.Diagnostics[0].Code)
}

func diagnosticCodes(diagnostics []ParamOverrideDiagnostic) []string {
	codes := make([]string, 0, len(diagnostics))
	for _, diagnostic := range diagnostics {
		codes = append(codes, diagnostic.Code)
	}
	return codes
}
