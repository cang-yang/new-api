package common

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/tidwall/gjson"
)

const redactedOverrideValue = "[REDACTED]"

var supportedParamOperationModes = map[string]struct{}{
	"delete": {}, "set": {}, "move": {}, "copy": {}, "prepend": {}, "append": {},
	"trim_prefix": {}, "trim_suffix": {}, "ensure_prefix": {}, "ensure_suffix": {},
	"trim_space": {}, "to_lower": {}, "to_upper": {}, "replace": {}, "regex_replace": {},
	"return_error": {}, "prune_objects": {}, "set_header": {}, "delete_header": {},
	"copy_header": {}, "move_header": {}, "pass_headers": {}, "sync_fields": {},
}

var supportedConditionModes = map[string]struct{}{
	"full": {}, "prefix": {}, "suffix": {}, "contains": {},
	"gt": {}, "gte": {}, "lt": {}, "lte": {},
}

type ParamOverrideDiagnostic struct {
	Severity       string `json:"severity"`
	Code           string `json:"code"`
	OperationIndex *int   `json:"operation_index,omitempty"`
	Field          string `json:"field,omitempty"`
	Message        string `json:"message"`
}

type ParamOverrideOperationTrace struct {
	Index   int    `json:"index"`
	Mode    string `json:"mode"`
	Path    string `json:"path,omitempty"`
	From    string `json:"from,omitempty"`
	To      string `json:"to,omitempty"`
	Status  string `json:"status"`
	Reason  string `json:"reason,omitempty"`
	Changed bool   `json:"changed"`
}

type ParamOverrideSimulation struct {
	Before      json.RawMessage               `json:"before"`
	After       json.RawMessage               `json:"after"`
	Operations  []ParamOverrideOperationTrace `json:"operations"`
	Headers     map[string]interface{}        `json:"headers"`
	Diagnostics []ParamOverrideDiagnostic     `json:"diagnostics"`
}

type ParamOverrideCompileError struct {
	Diagnostics []ParamOverrideDiagnostic
}

func (e *ParamOverrideCompileError) Error() string {
	if e == nil || len(e.Diagnostics) == 0 {
		return "parameter override compilation failed"
	}
	return e.Diagnostics[0].Message
}

func CompileParamOverride(paramOverride map[string]interface{}) []ParamOverrideDiagnostic {
	_, exists := paramOverride["operations"]
	if !exists {
		return nil
	}

	opMaps, err := parseOperationMapsForCompile(paramOverride["operations"])
	if err != nil {
		return []ParamOverrideDiagnostic{{
			Severity: "error",
			Code:     "operations_type",
			Field:    "operations",
			Message:  err.Error(),
		}}
	}

	diagnostics := make([]ParamOverrideDiagnostic, 0)
	for index, opMap := range opMaps {
		operationIndex := index
		add := func(code, field, message string) {
			diagnostics = append(diagnostics, ParamOverrideDiagnostic{
				Severity:       "error",
				Code:           code,
				OperationIndex: &operationIndex,
				Field:          field,
				Message:        message,
			})
		}

		mode, modeOK := opMap["mode"].(string)
		if !modeOK || strings.TrimSpace(mode) == "" {
			add("mode_required", "mode", "operation mode must be a non-empty string")
			continue
		}
		if _, ok := supportedParamOperationModes[mode]; !ok {
			add("unknown_operation_mode", "mode", "operation mode is not supported")
			continue
		}
		allowedFields := map[string]struct{}{
			"mode": {}, "path": {}, "value": {}, "keep_origin": {}, "from": {}, "to": {},
			"conditions": {}, "logic": {}, "description": {},
		}
		for field := range opMap {
			if _, allowed := allowedFields[field]; !allowed {
				add("unknown_operation_field", field, "operation contains an unsupported field")
			}
		}
		for _, field := range []string{"path", "from", "to", "description"} {
			if value, exists := opMap[field]; exists {
				if _, ok := value.(string); !ok {
					add("invalid_field_type", field, fmt.Sprintf("%s must be a string", field))
				}
			}
		}
		if value, exists := opMap["keep_origin"]; exists {
			if _, ok := value.(bool); !ok {
				add("invalid_field_type", "keep_origin", "keep_origin must be a boolean")
			}
		}

		path, _ := opMap["path"].(string)
		from, _ := opMap["from"].(string)
		to, _ := opMap["to"].(string)
		_, valueExists := opMap["value"]
		if operationRequiresPath(mode) && strings.TrimSpace(path) == "" {
			add("path_required", "path", fmt.Sprintf("%s operation requires path", mode))
		}
		if operationRequiresValue(mode) && !valueExists {
			add("value_required", "value", fmt.Sprintf("%s operation requires value", mode))
		}
		switch mode {
		case "move", "copy", "sync_fields":
			if strings.TrimSpace(from) == "" {
				add("from_required", "from", fmt.Sprintf("%s operation requires from", mode))
			}
			if strings.TrimSpace(to) == "" {
				add("to_required", "to", fmt.Sprintf("%s operation requires to", mode))
			}
		case "replace", "regex_replace":
			if strings.TrimSpace(from) == "" {
				add("from_required", "from", fmt.Sprintf("%s operation requires from", mode))
			}
		case "copy_header", "move_header":
			if strings.TrimSpace(from) == "" && strings.TrimSpace(path) == "" {
				add("from_required", "from", fmt.Sprintf("%s operation requires from or path", mode))
			}
			if strings.TrimSpace(to) == "" && strings.TrimSpace(path) == "" {
				add("to_required", "to", fmt.Sprintf("%s operation requires to or path", mode))
			}
		}

		logic, logicExists := opMap["logic"]
		if logicExists {
			logicString, ok := logic.(string)
			if !ok || (strings.ToUpper(strings.TrimSpace(logicString)) != "AND" && strings.ToUpper(strings.TrimSpace(logicString)) != "OR") {
				add("invalid_condition_logic", "logic", "condition logic must be AND or OR")
			}
		}
		if rawConditions, ok := opMap["conditions"]; ok {
			conditions, parseErr := parseConditionMapsForCompile(rawConditions)
			if parseErr != nil {
				add("invalid_conditions", "conditions", parseErr.Error())
			} else {
				for conditionIndex, condition := range conditions {
					conditionPath, _ := condition["path"].(string)
					conditionMode, _ := condition["mode"].(string)
					if strings.TrimSpace(conditionPath) == "" {
						add("condition_path_required", fmt.Sprintf("conditions.%d.path", conditionIndex), "condition path is required")
					}
					if strings.TrimSpace(conditionMode) == "" {
						add("condition_mode_required", fmt.Sprintf("conditions.%d.mode", conditionIndex), "condition mode is required")
					} else if _, supported := supportedConditionModes[strings.ToLower(strings.TrimSpace(conditionMode))]; !supported {
						add("unknown_condition_mode", fmt.Sprintf("conditions.%d.mode", conditionIndex), "condition mode is not supported")
					}
					if _, valueExists := condition["value"]; !valueExists {
						add("condition_value_required", fmt.Sprintf("conditions.%d.value", conditionIndex), "condition value is required")
					}
					for _, booleanField := range []string{"invert", "pass_missing_key"} {
						if value, exists := condition[booleanField]; exists {
							if _, ok := value.(bool); !ok {
								add("invalid_condition_field_type", fmt.Sprintf("conditions.%d.%s", conditionIndex, booleanField), fmt.Sprintf("condition %s must be a boolean", booleanField))
							}
						}
					}
				}
			}
		}
		if mode == "return_error" && valueExists {
			if _, parseErr := parseParamOverrideReturnError(opMap["value"]); parseErr != nil {
				add("invalid_return_error", "value", parseErr.Error())
			}
		}
	}
	return diagnostics
}

func SimulateParamOverride(jsonData []byte, paramOverride map[string]interface{}, conditionContext map[string]interface{}) (*ParamOverrideSimulation, error) {
	if !gjson.ValidBytes(jsonData) {
		return nil, errors.New("upstream request must be valid JSON")
	}
	diagnostics := CompileParamOverride(paramOverride)
	if len(diagnostics) > 0 {
		return nil, &ParamOverrideCompileError{Diagnostics: diagnostics}
	}

	context := cloneOverrideMap(conditionContext)
	secretValues := collectSensitiveContextValues(context)
	working := append([]byte(nil), jsonData...)
	legacy := buildLegacyParamOverride(paramOverride)
	if len(legacy) > 0 {
		var err error
		working, err = ApplyParamOverride(working, legacy, context)
		if err != nil {
			return nil, err
		}
	}

	operations, present, err := tryParseOperations(paramOverride)
	if err != nil {
		return nil, err
	}
	traces := make([]ParamOverrideOperationTrace, 0, len(operations))
	if present {
		terminated := false
		for index, operation := range operations {
			trace := ParamOverrideOperationTrace{
				Index: index, Mode: operation.Mode, Path: operation.Path, From: operation.From, To: operation.To,
			}
			if terminated {
				trace.Status = "skipped"
				trace.Reason = "not_reached"
				traces = append(traces, trace)
				continue
			}
			contextJSON, marshalErr := marshalContextJSON(context)
			if marshalErr != nil {
				return nil, marshalErr
			}
			matches, conditionErr := checkConditions(working, contextJSON, operation.Conditions, operation.Logic)
			if conditionErr != nil {
				return nil, conditionErr
			}
			if !matches {
				trace.Status = "skipped"
				trace.Reason = "condition_false"
				traces = append(traces, trace)
				continue
			}
			if isPathBasedOperation(operation.Mode) && strings.Contains(operation.Path, "*") {
				paths, resolveErr := resolveOperationPaths(working, processNegativeIndex(working, operation.Path))
				if resolveErr != nil {
					return nil, resolveErr
				}
				if len(paths) == 0 {
					trace.Status = "skipped"
					trace.Reason = "path_not_found"
					traces = append(traces, trace)
					continue
				}
			}

			beforeBody := string(working)
			beforeHeaders := common.GetJsonString(context[paramOverrideContextHeaderOverride])
			singleOverride := map[string]interface{}{"operations": []interface{}{paramOperationToMap(operation)}}
			working, err = ApplyParamOverride(working, singleOverride, context)
			if err != nil {
				if _, isReturnError := AsParamOverrideReturnError(err); isReturnError {
					trace.Status = "applied"
					trace.Reason = "would_return_error"
					trace.Changed = false
					traces = append(traces, trace)
					terminated = true
					continue
				}
				return nil, fmt.Errorf("operation %d simulation failed: %w", index, err)
			}
			trace.Status = "applied"
			trace.Changed = beforeBody != string(working) || beforeHeaders != common.GetJsonString(context[paramOverrideContextHeaderOverride])
			traces = append(traces, trace)
		}
	}

	secretValues = append(secretValues, collectSensitiveContextValues(context)...)
	before, err := redactOverrideJSON(jsonData, secretValues)
	if err != nil {
		return nil, err
	}
	after, err := redactOverrideJSON(working, secretValues)
	if err != nil {
		return nil, err
	}
	headers := redactOverrideHeaders(sanitizeHeaderOverrideMap(mapFromContext(context, paramOverrideContextHeaderOverride)), secretValues)
	return &ParamOverrideSimulation{
		Before: json.RawMessage(before), After: json.RawMessage(after), Operations: traces, Headers: headers, Diagnostics: []ParamOverrideDiagnostic{},
	}, nil
}

func paramOperationToMap(operation ParamOperation) map[string]interface{} {
	result := map[string]interface{}{
		"mode":        operation.Mode,
		"path":        operation.Path,
		"value":       operation.Value,
		"keep_origin": operation.KeepOrigin,
		"from":        operation.From,
		"to":          operation.To,
		"logic":       operation.Logic,
	}
	if len(operation.Conditions) > 0 {
		conditions := make([]interface{}, 0, len(operation.Conditions))
		for _, condition := range operation.Conditions {
			conditions = append(conditions, map[string]interface{}{
				"path": condition.Path, "mode": condition.Mode, "value": condition.Value,
				"invert": condition.Invert, "pass_missing_key": condition.PassMissingKey,
			})
		}
		result["conditions"] = conditions
	}
	return result
}

func operationRequiresPath(mode string) bool {
	switch mode {
	case "delete", "set", "prepend", "append", "trim_prefix", "trim_suffix", "ensure_prefix", "ensure_suffix", "trim_space", "to_lower", "to_upper", "replace", "regex_replace", "prune_objects", "set_header", "delete_header":
		return true
	default:
		return false
	}
}

func operationRequiresValue(mode string) bool {
	switch mode {
	case "set", "prepend", "append", "trim_prefix", "trim_suffix", "ensure_prefix", "ensure_suffix", "set_header", "pass_headers", "prune_objects", "return_error":
		return true
	default:
		return false
	}
}

func parseOperationMapsForCompile(raw interface{}) ([]map[string]interface{}, error) {
	switch operations := raw.(type) {
	case []interface{}:
		result := make([]map[string]interface{}, 0, len(operations))
		for index, operation := range operations {
			operationMap, ok := operation.(map[string]interface{})
			if !ok {
				return nil, fmt.Errorf("operation %d must be an object", index)
			}
			result = append(result, operationMap)
		}
		return result, nil
	case []map[string]interface{}:
		return operations, nil
	default:
		return nil, errors.New("parameter override operations must be an array")
	}
}

func parseConditionMapsForCompile(raw interface{}) ([]map[string]interface{}, error) {
	switch conditions := raw.(type) {
	case []interface{}:
		result := make([]map[string]interface{}, 0, len(conditions))
		for index, condition := range conditions {
			conditionMap, ok := condition.(map[string]interface{})
			if !ok {
				return nil, fmt.Errorf("condition %d must be an object", index)
			}
			result = append(result, conditionMap)
		}
		return result, nil
	case map[string]interface{}:
		result := make([]map[string]interface{}, 0, len(conditions))
		for path, value := range conditions {
			result = append(result, map[string]interface{}{"path": path, "mode": "full", "value": value})
		}
		if len(result) == 0 {
			return nil, errors.New("conditions object must contain at least one key")
		}
		return result, nil
	default:
		return nil, errors.New("conditions must be an array or object")
	}
}

func cloneOverrideMap(source map[string]interface{}) map[string]interface{} {
	if source == nil {
		return map[string]interface{}{}
	}
	encoded, err := common.Marshal(source)
	if err != nil {
		return map[string]interface{}{}
	}
	var target map[string]interface{}
	if err = common.Unmarshal(encoded, &target); err != nil || target == nil {
		return map[string]interface{}{}
	}
	return target
}

func mapFromContext(context map[string]interface{}, key string) map[string]interface{} {
	value, _ := context[key].(map[string]interface{})
	return value
}

func collectSensitiveContextValues(context map[string]interface{}) []string {
	values := make([]string, 0)
	for _, contextKey := range []string{paramOverrideContextRequestHeaders, paramOverrideContextHeaderOverride} {
		for key, value := range mapFromContext(context, contextKey) {
			if !isSensitiveOverrideKey(key) {
				continue
			}
			text := strings.TrimSpace(fmt.Sprintf("%v", value))
			if text != "" {
				values = append(values, text)
			}
		}
	}
	return values
}

func redactOverrideJSON(data []byte, secretValues []string) ([]byte, error) {
	var value interface{}
	if err := common.Unmarshal(data, &value); err != nil {
		return nil, err
	}
	value = redactOverrideValue(value, "", secretValues)
	return common.Marshal(value)
}

func redactOverrideValue(value interface{}, key string, secretValues []string) interface{} {
	if isSensitiveOverrideKey(key) {
		return redactedOverrideValue
	}
	switch typed := value.(type) {
	case map[string]interface{}:
		result := make(map[string]interface{}, len(typed))
		for childKey, childValue := range typed {
			result[childKey] = redactOverrideValue(childValue, childKey, secretValues)
		}
		return result
	case []interface{}:
		result := make([]interface{}, len(typed))
		for index, child := range typed {
			result[index] = redactOverrideValue(child, "", secretValues)
		}
		return result
	case string:
		for _, secret := range secretValues {
			if secret != "" && strings.Contains(typed, secret) {
				return redactedOverrideValue
			}
		}
		if looksLikeOverrideSecret(typed) {
			return redactedOverrideValue
		}
	}
	return value
}

func redactOverrideHeaders(headers map[string]interface{}, secretValues []string) map[string]interface{} {
	result := make(map[string]interface{}, len(headers))
	for key, value := range headers {
		if isSensitiveOverrideKey(key) {
			result[key] = redactedOverrideValue
			continue
		}
		result[key] = redactOverrideValue(value, "", secretValues)
	}
	return result
}

func isSensitiveOverrideKey(key string) bool {
	normalized := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(key), "-", "_"), ".", "_"))
	switch normalized {
	case "authorization", "proxy_authorization", "api_key", "apikey", "x_api_key", "key",
		"access_token", "refresh_token", "id_token", "token", "cookie", "set_cookie",
		"password", "passwd", "client_secret", "secret", "credential", "credentials":
		return true
	}
	return strings.HasSuffix(normalized, "_api_key") ||
		strings.HasSuffix(normalized, "_access_token") ||
		strings.HasSuffix(normalized, "_refresh_token") ||
		strings.HasSuffix(normalized, "_auth_token") ||
		strings.HasSuffix(normalized, "_secret") ||
		strings.HasSuffix(normalized, "_password") ||
		strings.HasSuffix(normalized, "_credential")
}

func looksLikeOverrideSecret(value string) bool {
	trimmed := strings.TrimSpace(value)
	lower := strings.ToLower(trimmed)
	return strings.HasPrefix(lower, "bearer ") || strings.HasPrefix(lower, "basic ") || strings.HasPrefix(lower, "sk-")
}
