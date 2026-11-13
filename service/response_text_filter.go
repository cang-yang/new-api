package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/gin-gonic/gin"
)

const maxFilteredResponseBytes = 8 << 20

// Acquire capacity before calling a provider, not after buffering its reply.
var responseProcessingSlots = make(chan struct{}, 16)
var ErrReplyProcessingBusy = errors.New("reply processing is busy; retry later")

// Per-instance fairness budgets can only reduce, not silently raise, the
// established memory bound. Defaults preserve existing capacity until E3.
var responseAdmission = struct {
	sync.Mutex
	users, channels         map[int]int
	userLimit, channelLimit int
}{
	users: make(map[int]int), channels: make(map[int]int),
	userLimit:    max(1, min(16, common.GetEnvOrDefault("REPLY_PROCESSING_PER_USER", 16))),
	channelLimit: max(1, min(16, common.GetEnvOrDefault("REPLY_PROCESSING_PER_CHANNEL", 16))),
}

// ResponseTextFilterWriter filters the client-facing JSON/SSE after the adaptor
// has accounted for the original upstream response. A bounded buffer lets a
// regex match span arbitrary SSE chunks and keeps protocol metadata intact.
type ResponseTextFilterWriter struct {
	gin.ResponseWriter
	admitted         bool
	admissionUser    int
	admissionChannel int
	providerCancel   context.CancelFunc
	parentContext    context.Context
	requirements     *ClientRequirements
	structured       bool
	checkpoint       []byte
	onlyOutput       bool
	outputProcessed  bool
	skipOutput       bool
	skipChoices      map[string]bool
	config           dto.ResponseTextFilter
	pattern          *regexp.Regexp
	regexes          []presetResponseRegex
	hasFilter        bool
	toolText         *dto.SillyTavernToolText
	scriptResponse   *PresetScriptResponse
	diagnostic       *AdaptationDiagnostic
	policy           *AdaptationFailurePolicy
	forceEmpty       bool
	status           int
	buffer           bytes.Buffer
	blocked          bool
	warned           bool
	transformErr     error
	deadline         time.Time
}

// responseStageError tags a processing failure with the pipeline stage that
// produced it so the writer can apply the channel's stage-scoped failure policy.
type responseStageError struct {
	stage FailureStage
	err   error
}

func (e *responseStageError) Error() string {
	if e.stage == FailureStageScript || e.stage == FailureStageOutputProcessing {
		return "preset script processing failed: " + presetScriptFailureSummary(e.err)
	}
	return e.err.Error()
}

// Only host-defined diagnostics may reach the client or ordinary server logs.
// Never stringify an arbitrary JS exception, schema, script name, or input.
func presetScriptFailureSummary(err error) string {
	if err == nil {
		return "unknown processing failure"
	}
	for errors.Unwrap(err) != nil {
		err = errors.Unwrap(err)
	}
	message := err.Error()
	switch message {
	case "preset script execution deadline exceeded",
		"preset script runtime is busy",
		"preset script input is invalid or exceeds 8 MiB",
		"preset script could not compile as an isolated function expression",
		"preset script preparation exceeded its deadline",
		"preset script initialization failed",
		"preset script entry point is unavailable",
		"preset script input conversion failed",
		"preset script execution exceeded its deadline",
		"preset script execution exceeded its memory limit",
		"preset script failed, exceeded its limits, or requires an unavailable API",
		"preset script output conversion failed",
		"preset script output is invalid or exceeds 8 MiB",
		"preset script returned an incompatible result",
		"preset scripts allow at most 128 tools",
		"preset scripts cannot modify client tools",
		"preset scripts cannot remove client tools",
		"preset scripts returned duplicate tools",
		"preset scripts must declare named function tools with object parameters",
		"ToolBindings has a duplicate or conflicting tool name",
		"FinalRequestProcessing returned invalid request fields",
		"FinalRequestProcessing must return a request object",
		"FinalRequestProcessing requires 1 to 4096 messages",
		"FinalRequestProcessing returned an unsupported message role",
		"FinalRequestProcessing toolConfig cannot be represented by tool_choice",
		"FinalRequestProcessing toolConfig contains unsupported options",
		"FinalRequestProcessing toolConfig conflicts with tool_choice",
		"FinalRequestProcessing toolConfig references an unknown or duplicate tool",
		"FinalRequestProcessing restricted toolConfig cannot be represented without changing tool selection",
		"FinalRequestProcessing custom_include_body only supports a matching reasoning_effort",
		"preset generated an incomplete tool history block",
		"preset generated invalid tool history",
		"preset generated an undeclared or invalid history tool call",
		"preset generated an unmatched tool result",
		"preset generated incomplete tool history",
		"preset generated duplicate tool history IDs":
		return message
	}
	for _, prefix := range []string{
		"FinalRequestProcessing cannot change request field ",
		"FinalRequestProcessing returned unsupported request field ",
		"FinalRequestProcessing contains unsupported nested data in ",
	} {
		if strings.HasPrefix(message, prefix) {
			// A field name can itself contain arbitrary script-provided text.
			return strings.TrimSpace(prefix) + " (field details withheld)"
		}
	}
	return "script execution or result validation failed"
}
func (e *responseStageError) Unwrap() error { return e.err }

func newResponseStageError(stage FailureStage, err error) error {
	if err == nil {
		return nil
	}
	return &responseStageError{stage: stage, err: err}
}

func responseErrorStage(err error) FailureStage {
	var staged *responseStageError
	if errors.As(err, &staged) {
		return staged.stage
	}
	return FailureStageResponseValidation
}

func AdaptationErrorStage(err error) FailureStage {
	var staged *responseStageError
	if errors.As(err, &staged) {
		return staged.stage
	}
	return FailureStagePreset
}

func BeginResponseTextFilter(c *gin.Context, config *dto.ResponseTextFilter, model string) *ResponseTextFilterWriter {
	return BeginResponseTextFilterWithPreset(c, config, nil, model)
}

// BeginResponseTextFilterWithPreset applies an optional explicit extractor and
// the enabled preset's receive-side regex scripts to assistant text only.
func BeginResponseTextFilterWithPreset(c *gin.Context, config *dto.ResponseTextFilter, preset *dto.SillyTavernPresetConfig, model string) *ResponseTextFilterWriter {
	if c == nil || c.Writer == nil {
		return nil
	}
	if config != nil && len(config.Models) > 0 {
		matched := false
		for _, name := range config.Models {
			if name == model {
				matched = true
				break
			}
		}
		if !matched {
			config = nil
		}
	}
	normalized, setupErr := config.Normalized()
	if setupErr == nil {
		config = normalized
	}
	regexes, warnings := compileEffectivePresetTextRegex(preset, config, model, false)
	channelRules, compileErr := compileChannelTextRegex(config, model, "receive")
	if compileErr != nil {
		setupErr = compileErr
	}
	regexes = append(channelRules, regexes...)
	if len(warnings) > 0 {
		logger.LogWarn(c, "Preset response regex compatibility validation failed; the entire rule chain follows the failure policy")
	}
	hasFilter := config != nil && config.Mode != "rules"
	var toolText *dto.SillyTavernToolText
	var scriptResponse *PresetScriptResponse
	scriptEnabled := preset != nil && preset.ScriptRuntime != "" && (len(preset.Models) == 0 || slices.Contains(preset.Models, model))
	if scriptEnabled {
		value, _ := c.Get("preset_script_response")
		scriptResponse, _ = value.(*PresetScriptResponse)
		if scriptResponse == nil || scriptResponse.fingerprint != presetScriptFingerprint(preset) {
			setupErr = fmt.Errorf("preset script response context is missing or belongs to another configuration")
		}
	}
	if preset != nil && preset.ToolText != nil && preset.ToolText.Enabled && (len(preset.Models) == 0 || slices.Contains(preset.Models, model)) {
		toolText = preset.ToolText
		if _, err := preset.ParseAndValidate(); err != nil {
			setupErr = err
		}
	}
	requirements, _ := c.Get(ClientRequirementsKey)
	client, _ := requirements.(*ClientRequirements)
	protectClient := client != nil && client.request != nil && (clientDeclaresToolContract(client.request) || client.request.ResponseFormat != nil && client.request.ResponseFormat.Type != "" && client.request.ResponseFormat.Type != "text")
	protectClient = protectClient || client != nil && client.defaultOutput != nil && client.defaultOutput.Format != "text"
	if !protectClient && !hasFilter && toolText == nil && scriptResponse == nil && len(regexes) == 0 && len(warnings) == 0 && setupErr == nil {
		return nil
	}
	w := &ResponseTextFilterWriter{ResponseWriter: c.Writer, regexes: regexes, hasFilter: hasFilter, toolText: toolText, scriptResponse: scriptResponse, status: c.Writer.Status()}
	if value, exists := c.Get(ClientRequirementsKey); exists {
		w.requirements, _ = value.(*ClientRequirements)
	}
	w.diagnostic = adaptationDiagnosticFromContext(c)
	w.policy = responseFailurePolicy(preset, config, model, false)
	if len(warnings) > 0 {
		w.transformErr = newResponseStageError(FailureStageRegex, fmt.Errorf("preset regex compatibility failure"))
	}
	if setupErr != nil {
		w.transformErr = newResponseStageError(FailureStageRegex, fmt.Errorf("response regex configuration failure"))
	}
	if config != nil {
		w.config = *config
	}
	if hasFilter && setupErr == nil {
		w.pattern = regexp.MustCompile(config.Pattern)
	}
	c.Writer = w
	return w
}

// responseFailurePolicy resolves the authoritative stage policy for the
// response pipeline. A preset's explicit failure policy wins; otherwise the
// documented defaults apply. Legacy regex strictness maps onto the regex and
// response stages so older channel rules keep failing safe.
func responseFailurePolicy(preset *dto.SillyTavernPresetConfig, config *dto.ResponseTextFilter, model string, send bool) *AdaptationFailurePolicy {
	policy := (&AdaptationProfile{Preset: preset}).FailurePolicy()
	if config != nil && config.FailurePolicy == "passthrough" || preset != nil && preset.RegexFailurePolicy == "passthrough" {
		policy.regex = FailurePreserve
	}
	if preset != nil && preset.FailurePolicy != nil && preset.FailurePolicy.Regex != "" {
		policy.regex = FailureMode(preset.FailurePolicy.Regex)
	}
	// A preset that owns a response script or a declarative tool transport has
	// a contracted response pipeline: the run must complete or the gateway
	// cannot guarantee what the client receives. Unless the operator set an
	// explicit stage value, those stages fail closed instead of leaking.
	strictPipeline := preset != nil && (preset.ScriptRuntime != "" || preset.ToolText != nil && preset.ToolText.Enabled)
	if strictPipeline {
		configured := preset.FailurePolicy
		if configured == nil || configured.ResponseValidation == "" {
			policy.responseValidation = FailureFail
		}
		if configured == nil || configured.ResponseRestore == "" {
			policy.responseRestore = FailureFail
		}
		if configured == nil || configured.OutputProcessing == "" {
			policy.outputProcessing = FailureFail
		}
	}
	if preset != nil && preset.RegexFailurePolicy == "error" {
		policy.regex = FailureFail
	}
	if textRegexRequiresStrictFailure(config, preset, model, send) {
		policy.responseValidation = FailureFail
		policy.responseRestore = FailureFail
		if preset == nil || preset.RegexFailurePolicy == "" {
			policy.regex = FailureFail
		}
	}
	return &policy
}

// stageMode returns the failure behavior for a stage. When no policy is
// attached the documented safe defaults apply so callers never silently leak.
func (w *ResponseTextFilterWriter) stageMode(stage FailureStage) FailureMode {
	if w.policy != nil {
		return w.policy.Mode(stage)
	}
	if stage == FailureStageResponseStarted {
		return FailureStop
	}
	return FailureFail
}

// internalToolNames reports the gateway-internal tools whose presence in a raw
// upstream body would leak preset internals to the client.
func (w *ResponseTextFilterWriter) internalToolNames() map[string]bool {
	internal := make(map[string]bool)
	if w.toolText != nil && w.toolText.Enabled && w.toolText.Name != "" {
		internal[w.toolText.Name] = true
	}
	if w.scriptResponse != nil {
		if w.scriptResponse.protocol != nil && w.scriptResponse.protocol.audit.Activated {
			for name := range w.scriptResponse.protocol.InternalToolNames() {
				internal[name] = true
			}
		} else {
			for name := range w.scriptResponse.ownedTools {
				internal[name] = true
			}
		}
	}
	if len(internal) == 0 {
		return nil
	}
	return internal
}

// rawResponseSafeForFallback proves that bypassing projection cannot leak a
// gateway-internal tool to the client.
func (w *ResponseTextFilterWriter) rawResponseSafeForFallback() bool {
	return len(w.internalToolNames()) == 0
}

func (w *ResponseTextFilterWriter) WriteHeader(code int) {
	if code > 0 {
		w.status = code
	}
}
func (w *ResponseTextFilterWriter) WriteHeaderNow() {}
func (w *ResponseTextFilterWriter) Status() int     { return w.status }
func (w *ResponseTextFilterWriter) Written() bool {
	return w.buffer.Len() > 0 || w.ResponseWriter.Written()
}

func (w *ResponseTextFilterWriter) Write(data []byte) (int, error) {
	if w.blocked {
		return 0, fmt.Errorf("response text filter exceeded %d bytes", maxFilteredResponseBytes)
	}
	if bytes.Equal(data, []byte(": PING\n\n")) {
		return len(data), nil
	}
	if w.buffer.Len()+len(data) > maxFilteredResponseBytes {
		w.blocked = true
		w.buffer.Reset()
		if w.providerCancel != nil {
			w.providerCancel()
		}
		return 0, fmt.Errorf("response text filter exceeded %d bytes", maxFilteredResponseBytes)
	}

	return w.buffer.Write(data)
}

func (w *ResponseTextFilterWriter) WriteString(data string) (int, error) {
	return w.Write([]byte(data))
}

// Flush must not release an incomplete SSE frame before extraction is known.
func (w *ResponseTextFilterWriter) Flush() {}

func (w *ResponseTextFilterWriter) Admit(c *gin.Context) error {
	if w.admitted {
		return nil
	}
	if w.transformErr != nil {
		return w.transformErr
	}
	if c.Request != nil && c.Request.Context().Err() != nil {
		return c.Request.Context().Err()
	}
	responseAdmission.Lock()
	defer responseAdmission.Unlock()
	user := common.GetContextKeyInt(c, constant.ContextKeyUserId)
	channel := common.GetContextKeyInt(c, constant.ContextKeyChannelId)
	if responseAdmission.users[user] >= responseAdmission.userLimit ||
		responseAdmission.channels[channel] >= responseAdmission.channelLimit {
		RecordAdaptationStage(c, "admission", "failed", "reply processing capacity exhausted", nil)
		return ErrReplyProcessingBusy
	}
	select {
	case responseProcessingSlots <- struct{}{}:
		w.admitted = true
		w.admissionUser, w.admissionChannel = user, channel
		responseAdmission.users[user]++
		responseAdmission.channels[channel]++
		if c.Request != nil {
			w.parentContext = c.Request.Context()
			var providerContext context.Context
			providerContext, w.providerCancel = context.WithCancel(w.parentContext)
			c.Request = c.Request.WithContext(providerContext)
		}
		return nil
	default:
		RecordAdaptationStage(c, "admission", "failed", "reply processing capacity exhausted", nil)
		return ErrReplyProcessingBusy
	}
}

func (w *ResponseTextFilterWriter) releaseAdmission() {
	responseAdmission.Lock()
	defer responseAdmission.Unlock()
	if !w.admitted {
		return
	}
	<-responseProcessingSlots
	w.admitted = false
	responseAdmission.users[w.admissionUser]--
	responseAdmission.channels[w.admissionChannel]--
	if responseAdmission.users[w.admissionUser] == 0 {
		delete(responseAdmission.users, w.admissionUser)
	}
	if responseAdmission.channels[w.admissionChannel] == 0 {
		delete(responseAdmission.channels, w.admissionChannel)
	}
}

func (w *ResponseTextFilterWriter) Finish(c *gin.Context, success bool) error {
	if w == nil {
		return nil
	}
	if w.admitted {
		defer w.releaseAdmission()
	}
	c.Writer = w.ResponseWriter
	if w.providerCancel != nil {
		defer w.providerCancel()
		defer func() {
			if c.Request != nil {
				c.Request = c.Request.WithContext(w.parentContext)
			}
		}()
	}
	if w.blocked {
		w.Header().Del("Content-Length")
		return fmt.Errorf("response text filter exceeded %d bytes", maxFilteredResponseBytes)
	}
	if c.Request != nil && c.Request.Context().Err() != nil {
		w.buffer.Reset()
		return c.Request.Context().Err()
	}
	if !success {
		// The controller owns errors/retries. Never concatenate a buffered failed
		// attempt with a later successful response or the controller's error JSON.
		w.buffer.Reset()
		return nil
	}
	body := w.buffer.Bytes()
	if len(body) == 0 {
		if w.requirements != nil {
			return newResponseStageError(FailureStageResponseValidation, fmt.Errorf("provider returned an empty response"))
		}
		return nil
	}
	if success && w.status < 400 {
		w.deadline = time.Now().Add(2 * time.Second)
		stream := strings.Contains(w.Header().Get("Content-Type"), "text/event-stream")
		filtered, err := w.processResponse(body, stream)
		if err != nil {
			stage := responseErrorStage(err)
			w.recordStage(stage, "failed", err)
			w.Header().Del("Content-Length")
			return fmt.Errorf("reply processing failed at %s: %w", stage, err)
		} else {
			body = filtered
			w.recordStage(FailureStageResponseValidation, "completed", nil)
		}
	}
	w.Header().Del("Content-Length")
	w.ResponseWriter.WriteHeader(w.status)
	written, err := w.ResponseWriter.Write(body)
	if err == nil && written != len(body) {
		err = io.ErrShortWrite
	}
	if err != nil {
		w.recordStage(FailureStageResponseStarted, "failed", err, map[string]any{"body_bytes": written, "delivery_incomplete": true})
	}
	if err == nil {
		w.recordStage(FailureStageResponseStarted, "completed", nil, map[string]any{
			"body_bytes": len(body),
			"stream":     strings.Contains(w.Header().Get("Content-Type"), "text/event-stream"),
		})
	}
	return err
}

// processResponse runs restore, validation and transformation in the fixed
// order and tags any failure with its pipeline stage.
func (w *ResponseTextFilterWriter) processResponse(body []byte, stream bool) ([]byte, error) {
	w.outputProcessed = false
	if w.transformErr != nil {
		w.recordStage(FailureStageResponseValidation, "failed", w.transformErr)
		return nil, w.transformErr
	}
	if _, err := responseChoices(body, stream); err != nil {
		return nil, newResponseStageError(FailureStageResponseValidation, err)
	}
	prepared, err := restorePresetToolText(body, stream, w.toolText, w.deadline)
	if err != nil {
		w.recordStage(FailureStageResponseRestore, "failed", err)
		return nil, newResponseStageError(FailureStageResponseRestore, err)
	}
	if w.scriptResponse != nil {
		prepared, err = w.scriptResponse.restoreTools(prepared, stream, w.deadline)
		if err != nil {
			w.recordStage(FailureStageResponseRestore, "failed", err)
			if w.stageMode(FailureStageResponseRestore) == FailureSafeRaw && w.rawResponseSafeForFallback() && !w.mandatoryRegex() && w.validateDelivery(body, stream) == nil {
				w.recordStage(FailureStageResponseRestore, "fallback", nil)
				return body, nil
			}
			return nil, newResponseStageError(FailureStageResponseRestore, err)
		}
	}
	if err := w.validateResponseContract(prepared, stream, false); err != nil {
		return nil, newResponseStageError(FailureStageResponseRestore, err)
	}
	w.recordStage(FailureStageResponseRestore, "completed", nil, map[string]any{
		"changed":    !bytes.Equal(body, prepared),
		"body_bytes": len(prepared),
	})
	w.checkpoint = prepared
	states, err := responseChoices(prepared, stream)
	if err != nil {
		return nil, newResponseStageError(FailureStageResponseValidation, err)
	}
	w.skipChoices = map[string]bool{}
	for key, state := range states {
		if len(state.tools) > 0 && state.text.Len() == 0 || state.refused || state.finish == "length" || state.finish == "content_filter" {
			w.skipChoices["choice:"+key] = true
		}
	}
	run := w.filterJSON
	if stream {
		run = w.filterSSE
	}
	structured := w.requirements != nil && w.requirements.request.ResponseFormat != nil && w.requirements.request.ResponseFormat.Type != "" && w.requirements.request.ResponseFormat.Type != "text"
	if w.requirements != nil && w.requirements.defaultOutput != nil && w.requirements.defaultOutput.Format == "json_object" {
		structured = true
	}
	if w.scriptResponse != nil && w.scriptResponse.protocol != nil && w.scriptResponse.protocol.contract == presetProtocolContractJSON {
		structured = true
	}
	if w.scriptResponse != nil && w.scriptResponse.textToClientFormat {
		// The confirmed reply source produces text. Its formatter, output hook
		// and text rules must finish before validating the final JSON contract.
		structured = false
	}
	w.structured = structured
	// An output hook's buffer is the complete choice text, even if that text
	// contains JSON. Never skip a required hook or invoke it once per JSON field.
	processed := prepared
	if w.scriptResponse != nil {
		w.onlyOutput = true
		w.structured = false
		processed, err = run(prepared)
		w.structured = structured
		w.onlyOutput = false
		if err != nil {
			if w.stageMode(FailureStageOutputProcessing) != FailurePreserve {
				return nil, err
			}
			w.transformErr = nil
			w.recordStage(FailureStageOutputProcessing, "fallback", nil)
			// Roll back the entire failed output stage, not the remaining
			// pipeline. Independent host cleanup may still be mandatory.
			processed = prepared
		} else {
			if w.stageMode(FailureStageOutputProcessing) == FailurePreserve &&
				!bytes.Equal(processed, prepared) && w.validateDelivery(prepared, stream) == nil &&
				w.validateDelivery(processed, stream) != nil {
				processed = prepared
				w.recordStage(FailureStageOutputProcessing, "fallback", nil, map[string]any{"reason": "result_contract_invalid"})
			} else {
				status := "skipped"
				if w.outputProcessed {
					status = "completed"
				}
				w.recordStage(FailureStageOutputProcessing, status, nil)
			}
		}
	} else {
		w.recordStage(FailureStageOutputProcessing, "skipped", nil)
	}
	w.checkpoint = processed
	if structured && len(w.config.JSONTextPointers) == 0 {
		w.recordStage(FailureStageRegex, "skipped", nil)
		if err := w.validateDelivery(processed, stream); err != nil {
			return nil, newResponseStageError(FailureStageResponseValidation, err)
		}
		return processed, nil
	}
	w.skipOutput = true
	filtered, err := run(processed)
	w.skipOutput = false
	if err != nil {
		mode := w.stageMode(FailureStageRegex)
		if w.mandatoryRegex() || mode != FailurePreserve && mode != FailureEmpty {
			return nil, err
		}
		w.transformErr = nil
		filtered = processed
		if mode == FailureEmpty {
			if structured {
				return nil, newResponseStageError(FailureStageRegex, fmt.Errorf("empty text fallback cannot satisfy JSON output"))
			}
			filtered, err = w.emptyResponse(processed, stream)
			if err != nil {
				return nil, err
			}
		}
		w.recordStage(FailureStageRegex, "fallback", nil)
	} else {
		w.recordStage(FailureStageRegex, regexDiagnosticStatus(w), nil)
	}
	if err := w.validateDelivery(filtered, stream); err != nil {
		if w.stageMode(FailureStageRegex) == FailurePreserve && !w.mandatoryRegex() &&
			!bytes.Equal(filtered, processed) && w.validateDelivery(processed, stream) == nil {
			// A successful transform can still break the client's contract.
			// Only a validated checkpoint may be delivered as fallback.
			w.recordStage(FailureStageRegex, "fallback", nil, map[string]any{"reason": "result_contract_invalid"})
			return processed, nil
		}
		return nil, newResponseStageError(FailureStageResponseValidation, err)
	}
	return filtered, nil
}

func (w *ResponseTextFilterWriter) mandatoryRegex() bool {
	if w.hasFilter && w.config.MissingMatch == "error" {
		return true
	}
	for _, rule := range w.config.Rules {
		if !rule.Disabled && rule.Stage == "receive" && rule.MissingMatch == "error" {
			return true
		}
	}
	return (w.hasFilter || len(w.regexes) > 0) && w.stageMode(FailureStageRegex) == FailureFail
}

func (w *ResponseTextFilterWriter) recordStage(stage FailureStage, status string, err error, details ...map[string]any) {
	if w == nil || w.diagnostic == nil {
		return
	}
	if len(details) > 0 && len(details[0]) > 0 {
		w.diagnostic.Record(string(stage), status, "", details[0])
		return
	}
	summary := ""
	if err != nil {
		summary = err.Error()
	}
	w.diagnostic.Record(string(stage), status, summary, nil)
}

func regexDiagnosticStatus(w *ResponseTextFilterWriter) string {
	if w == nil || !w.hasFilter && len(w.regexes) == 0 {
		return "skipped"
	}
	return "completed"
}

// emptyResponse emits the client-visible text slots as empty strings. It only
// runs for the explicit regex "empty" failure policy.
func (w *ResponseTextFilterWriter) emptyResponse(body []byte, stream bool) ([]byte, error) {
	w.forceEmpty = true
	w.transformErr = nil
	defer func() { w.forceEmpty = false }()
	if stream {
		return w.filterSSE(body)
	}
	return w.filterJSON(body)
}

type responseTextSlot struct {
	parent map[string]any
	field  string
	group  string
}

func (w *ResponseTextFilterWriter) transform(text string) (string, bool) {
	original := text
	if w.forceEmpty {
		return "", text != ""
	}
	if !w.skipOutput && w.scriptResponse != nil {
		var err error
		text, err = w.scriptResponse.processText(text, w.deadline)
		if err != nil {
			w.transformErr = newResponseStageError(FailureStageOutputProcessing, err)
			return original, false
		}
		w.outputProcessed = w.outputProcessed || w.scriptResponse.tavern != nil ||
			w.scriptResponse.settings != nil && w.scriptResponse.settings.OutputPreprocessing.Enabled
	}
	if w.onlyOutput {
		return text, text != original
	}
	matched := false
	if w.hasFilter {
		match := w.pattern.FindStringSubmatch(text)
		if len(match) >= 2 {
			matched = true
			text = match[1]
			if w.config.TrimCapture {
				text = strings.TrimSpace(text)
			}
		}
	}
	if !matched && w.hasFilter && w.config.MissingMatch == "error" {
		w.transformErr = newResponseStageError(FailureStageRegex, fmt.Errorf("required reply extraction did not match"))
		return original, false
	}
	if !matched && w.hasFilter && w.config.MissingMatch == "empty" {
		text = ""
	}
	for _, script := range w.regexes {
		if !w.deadline.IsZero() && time.Now().After(w.deadline) {
			w.transformErr = newResponseStageError(FailureStageRegex, fmt.Errorf("response regex execution budget exceeded"))
			return original, false
		}
		replaced, err := script.replace(text)
		if err != nil || len(replaced) > maxFilteredResponseBytes {
			if !w.warned {
				common.SysError("Response regex execution failed; the entire rule chain follows the failure policy; response text and patterns omitted")
				w.warned = true
			}
			w.transformErr = newResponseStageError(FailureStageRegex, fmt.Errorf("response regex execution failed or exceeded output limit"))
			return original, false
		}
		text = replaced
	}
	return text, text != original
}

func textSlots(payload map[string]any, stream bool, includeEmpty ...bool) []responseTextSlot {
	var slots []responseTextSlot
	add := func(parent map[string]any, field, group string) {
		if value, ok := parent[field].(string); ok && (value != "" || len(includeEmpty) > 0 && includeEmpty[0]) {
			slots = append(slots, responseTextSlot{parent, field, group})
		}
	}
	if event, _ := payload["type"].(string); stream && event != "" {
		switch event {
		case "response.output_text.delta":
			add(payload, "delta", "responses:"+fmt.Sprint(payload["output_index"])+":"+fmt.Sprint(payload["content_index"]))
		case "response.output_text.done":
			add(payload, "text", "responses_done:"+fmt.Sprint(payload["output_index"])+":"+fmt.Sprint(payload["content_index"]))
		case "response.content_part.added", "response.content_part.done":
			if part, ok := payload["part"].(map[string]any); ok && part["type"] == "output_text" {
				add(part, "text", event+":"+fmt.Sprint(payload["output_index"])+":"+fmt.Sprint(payload["content_index"]))
			}
		case "response.output_item.added", "response.output_item.done":
			if item, ok := payload["item"].(map[string]any); ok {
				snapshots := textSlots(map[string]any{"output": []any{item}}, false, includeEmpty...)
				for i := range snapshots {
					snapshots[i].group = event + ":" + fmt.Sprint(payload["output_index"]) + ":" + snapshots[i].group
				}
				slots = append(slots, snapshots...)
			}
		case "content_block_delta":
			if delta, ok := payload["delta"].(map[string]any); ok && delta["type"] == "text_delta" {
				add(delta, "text", "claude:"+fmt.Sprint(payload["index"]))
			}
		case "content_block_start":
			if block, ok := payload["content_block"].(map[string]any); ok && block["type"] == "text" {
				add(block, "text", "claude:"+fmt.Sprint(payload["index"]))
			}
		}
		if event == "response.completed" || event == "response.done" {
			if response, ok := payload["response"].(map[string]any); ok {
				snapshots := textSlots(response, false, includeEmpty...)
				for i := range snapshots {
					snapshots[i].group = event + ":" + snapshots[i].group
				}
				return append(slots, snapshots...)
			}
		}
		return slots
	}
	if choices, ok := payload["choices"].([]any); ok {
		for i, raw := range choices {
			choice, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			group := "choice:" + strconv.Itoa(i)
			if index, ok := choice["index"].(json.Number); ok {
				group = "choice:" + index.String()
			}
			add(choice, "text", group)
			for _, key := range []string{"message", "delta"} {
				message, ok := choice[key].(map[string]any)
				if !ok {
					continue
				}
				add(message, "content", group)
				if parts, ok := message["content"].([]any); ok {
					for _, rawPart := range parts {
						part, ok := rawPart.(map[string]any)
						if ok && (part["type"] == "text" || part["type"] == "output_text") {
							add(part, "text", group)
						}
					}
				}
			}
		}
	}
	if output, ok := payload["output"].([]any); ok {
		for i, raw := range output {
			item, ok := raw.(map[string]any)
			if !ok || item["type"] != "message" {
				continue
			}
			if parts, ok := item["content"].([]any); ok {
				for j, rawPart := range parts {
					part, ok := rawPart.(map[string]any)
					if ok && part["type"] == "output_text" {
						add(part, "text", "output:"+strconv.Itoa(i)+":"+strconv.Itoa(j))
					}
				}
			}
		}
	}
	if content, ok := payload["content"].([]any); ok {
		for i, raw := range content {
			part, ok := raw.(map[string]any)
			if ok && part["type"] == "text" {
				add(part, "text", "claude:"+strconv.Itoa(i))
			}
		}
	}
	if candidates, ok := payload["candidates"].([]any); ok {
		for i, raw := range candidates {
			candidate, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			group := "candidate:" + strconv.Itoa(i)
			if index, ok := candidate["index"].(json.Number); ok {
				group = "candidate:" + index.String()
			}
			content, ok := candidate["content"].(map[string]any)
			if !ok {
				continue
			}
			parts, ok := content["parts"].([]any)
			if !ok {
				continue
			}
			for _, rawPart := range parts {
				part, ok := rawPart.(map[string]any)
				if ok && part["thought"] != true {
					add(part, "text", group)
				}
			}
		}
	}
	return slots
}

func (w *ResponseTextFilterWriter) filterJSON(body []byte) ([]byte, error) {
	var payload map[string]any
	if err := common.UnmarshalPreservingNumbers(body, &payload); err != nil {
		return nil, newResponseStageError(FailureStageResponseValidation, err)
	}
	slots := textSlots(payload, false, w.scriptResponse != nil)
	groups := map[string][]responseTextSlot{}
	for _, slot := range slots {
		groups[slot.group] = append(groups[slot.group], slot)
	}
	if w.scriptResponse != nil && w.scriptResponse.tavern != nil && len(groups) != 1 && len(w.skipChoices) == 0 {
		return nil, newResponseStageError(FailureStageResponseValidation, fmt.Errorf("Tavern runtime requires exactly one final text response choice"))
	}
	changed := false
	for key, group := range groups {
		if w.skipChoices[key] {
			continue
		}
		var original strings.Builder
		for _, slot := range group {
			original.WriteString(slot.parent[slot.field].(string))
		}
		if extracted, changedText := w.transformContent(original.String()); changedText {
			group[0].parent[group[0].field] = extracted
			for _, slot := range group[1:] {
				slot.parent[slot.field] = ""
			}
			changed = true
		}
	}
	if !changed {
		if w.transformErr != nil {
			return nil, w.transformErr
		}
		return body, nil
	}
	if w.transformErr != nil {
		return nil, w.transformErr
	}
	encoded, err := common.Marshal(payload)
	if len(encoded) > maxFilteredResponseBytes {
		return nil, newResponseStageError(FailureStageResponseValidation, fmt.Errorf("filtered JSON response exceeds size limit"))
	}
	if err != nil {
		return nil, newResponseStageError(FailureStageResponseValidation, err)
	}
	return encoded, nil
}

func (w *ResponseTextFilterWriter) filterSSE(body []byte) ([]byte, error) {
	type frame struct {
		lines   []string
		payload map[string]any
		raw     string
		slots   []responseTextSlot
	}
	var frames []frame
	groups := map[string][]responseTextSlot{}
	// SSE accepts CRLF, CR, LF, data without a space, and multiple data lines.
	// Preserve event/id/retry/comment fields even when they follow the data.
	normalized := strings.ReplaceAll(strings.ReplaceAll(string(body), "\r\n", "\n"), "\r", "\n")
	for raw := range strings.SplitSeq(normalized, "\n\n") {
		if len(raw) == 0 {
			continue
		}
		item := frame{raw: raw, lines: strings.Split(raw, "\n")}
		var dataLines []string
		for _, line := range item.lines {
			field, value, _ := strings.Cut(line, ":")
			if field == "data" {
				dataLines = append(dataLines, strings.TrimPrefix(value, " "))
			}
		}
		data := strings.Join(dataLines, "\n")
		if data != "" && strings.TrimSpace(data) != "[DONE]" {
			if err := common.UnmarshalPreservingNumbers([]byte(data), &item.payload); err != nil {
				return nil, newResponseStageError(FailureStageResponseValidation, fmt.Errorf("invalid JSON in response SSE event"))
			}
			item.slots = textSlots(item.payload, true, w.scriptResponse != nil)
			for _, slot := range item.slots {
				groups[slot.group] = append(groups[slot.group], slot)
			}
		}
		frames = append(frames, item)
	}
	if w.scriptResponse != nil && w.scriptResponse.tavern != nil && len(groups) != 1 && len(w.skipChoices) == 0 {
		return nil, newResponseStageError(FailureStageResponseValidation, fmt.Errorf("Tavern runtime requires exactly one final text response choice"))
	}
	changed := false
	for key, group := range groups {
		if w.skipChoices[key] {
			continue
		}
		var original strings.Builder
		for _, slot := range group {
			original.WriteString(slot.parent[slot.field].(string))
		}
		if extracted, changedText := w.transformContent(original.String()); changedText {
			group[0].parent[group[0].field] = extracted
			for _, slot := range group[1:] {
				slot.parent[slot.field] = ""
			}
			changed = true
		}
	}
	if !changed {
		if w.transformErr != nil {
			return nil, w.transformErr
		}
		return body, nil
	}
	if w.transformErr != nil {
		return nil, w.transformErr
	}
	var out bytes.Buffer
	for _, item := range frames {
		if item.payload == nil || len(item.slots) == 0 {
			out.WriteString(item.raw)
			out.WriteByte('\n')
		} else {
			data, err := common.Marshal(item.payload)
			if err != nil {
				return nil, newResponseStageError(FailureStageResponseValidation, err)
			}
			written := false
			for _, line := range item.lines {
				field, _, _ := strings.Cut(line, ":")
				if field == "data" {
					if written {
						continue
					}
					out.WriteString("data: ")
					out.Write(data)
					written = true
				} else {
					out.WriteString(line)
				}
				out.WriteByte('\n')
			}
		}
		out.WriteByte('\n')
		if out.Len() > maxFilteredResponseBytes {
			return nil, newResponseStageError(FailureStageResponseValidation, fmt.Errorf("filtered SSE response exceeds size limit"))
		}
	}
	return out.Bytes(), nil
}
