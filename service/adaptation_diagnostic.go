package service

import (
	"context"
	"encoding/hex"
	"slices"
	"strings"
	"sync"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

const adaptationDiagnosticContextKey = "adaptation_diagnostic"

const adaptationDiagnosticSchemaVersion = 1

type AdaptationHealthGroup struct {
	Profile string         `json:"profile"`
	Counts  map[string]int `json:"counts"`
}

type AdaptationHealth struct {
	AuditEnabled bool                    `json:"audit_enabled"`
	SampleSize   int                     `json:"sample_size"`
	Counts       map[string]int          `json:"counts"`
	Versions     []AdaptationHealthGroup `json:"versions"`
}

func RecentAdaptationHealth(ctx context.Context, channelID int) (*AdaptationHealth, error) {
	attempts, snapshots, err := model.RecentChannelAdaptationAudits(ctx, channelID)
	if err != nil {
		return nil, err
	}
	result := &AdaptationHealth{AuditEnabled: constant.BodyAuditEnabled, SampleSize: len(attempts), Counts: make(map[string]int), Versions: []AdaptationHealthGroup{}}
	groups := make(map[string]int)
	for _, attempt := range attempts {
		var data struct {
			Preset struct {
				Diagnostics *AdaptationDiagnostic `json:"diagnostics"`
				Plan        struct {
					Profile string `json:"profile_fingerprint"`
				} `json:"plan"`
			} `json:"sillytavern_preset"`
		}
		snapshot := snapshots[attempt.ConfigSnapshotId]
		// Missing, oversized or old snapshots stay unknown, never "successful".
		_ = common.Unmarshal(snapshot.CanonicalJson, &data)
		outcome := adaptationAttemptOutcome(attempt, data.Preset.Diagnostics)
		result.Counts[outcome]++
		profile := data.Preset.Plan.Profile
		if _, err := hex.DecodeString(profile); len(profile) != 64 || err != nil {
			profile = "unknown"
		}
		index, exists := groups[profile]
		if !exists {
			index = len(result.Versions)
			groups[profile] = index
			result.Versions = append(result.Versions, AdaptationHealthGroup{Profile: profile, Counts: make(map[string]int)})
		}
		result.Versions[index].Counts[outcome]++
	}
	return result, nil
}

func adaptationAttemptOutcome(attempt model.AuditAttempt, diagnostic *AdaptationDiagnostic) string {
	if attempt.ErrorCode == "reply_processing_busy" {
		return "overloaded"
	}
	written, degraded, failed := false, false, false
	if diagnostic != nil {
		for _, stage := range diagnostic.Stages {
			if stage.Stage == "response_started" && stage.Status == "failed" {
				return "interrupted"
			}
			if stage.Stage == "admission" && stage.Status == "failed" {
				return "overloaded"
			}
			degraded = degraded || stage.Status == "fallback"
			failed = failed || stage.Status == "failed"
			if stage.Stage == "writeback" {
				written = stage.Status == "completed"
			}
		}
	}
	if attempt.HTTPStatus >= 400 {
		return "upstream_failed"
	}
	if written {
		if degraded {
			return "degraded"
		}
		return "completed"
	}
	if failed {
		return "failed_before_delivery"
	}
	return "unknown"
}

// AdaptationDiagnosticStage is a bounded, secret-free explanation of one
// request-adaptation phase. Details must contain metadata only; response and
// prompt bodies belong in the existing body audit, not in this structure.
type AdaptationDiagnosticStage struct {
	Stage   string         `json:"stage"`
	Status  string         `json:"status"`
	Summary string         `json:"summary,omitempty"`
	Details map[string]any `json:"details,omitempty"`
}

// AdaptationDiagnostic is request-local. It is serialized into the immutable
// attempt config snapshot after the response pipeline has completed.
type AdaptationDiagnostic struct {
	SchemaVersion int                         `json:"schema_version"`
	Stages        []AdaptationDiagnosticStage `json:"stages"`

	mu sync.Mutex
}

func newAdaptationDiagnostic() *AdaptationDiagnostic {
	return &AdaptationDiagnostic{
		SchemaVersion: adaptationDiagnosticSchemaVersion,
		Stages:        []AdaptationDiagnosticStage{},
	}
}

func (d *AdaptationDiagnostic) Record(stage, status, summary string, details map[string]any) {
	if d == nil || stage == "" {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.SchemaVersion == 0 {
		d.SchemaVersion = adaptationDiagnosticSchemaVersion
	}
	if len(d.Stages) >= 64 {
		return
	}
	summary = strings.ToValidUTF8(summary, "")
	if len([]rune(summary)) > 512 {
		summary = string([]rune(summary)[:512])
	}
	d.Stages = append(d.Stages, AdaptationDiagnosticStage{
		Stage:   stage,
		Status:  status,
		Summary: summary,
		Details: cloneDiagnosticDetails(details),
	})
}

func (d *AdaptationDiagnostic) snapshotDraft() *AdaptationDiagnostic {
	if d == nil {
		return nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return &AdaptationDiagnostic{
		SchemaVersion: d.SchemaVersion,
		Stages:        append([]AdaptationDiagnosticStage(nil), d.Stages...),
	}
}

func cloneDiagnosticDetails(details map[string]any) map[string]any {
	if len(details) == 0 {
		return nil
	}
	result := make(map[string]any, len(details))
	for key, value := range details {
		switch item := value.(type) {
		case map[string]any:
			result[key] = cloneDiagnosticDetails(item)
		case []map[string]any:
			bounded := make([]map[string]any, 0, min(len(item), 64))
			for _, entry := range item[:min(len(item), 64)] {
				bounded = append(bounded, cloneDiagnosticDetails(entry))
			}
			result[key] = bounded
		case bool, int, int64, float64:
			result[key] = item
		case string:
			if len(item) > 256 {
				item = item[:256]
			}
			result[key] = item
		case []string:
			if len(item) <= 32 {
				bounded := make([]string, len(item))
				for i, text := range item {
					if len(text) > 256 {
						text = text[:256]
					}
					bounded[i] = text
				}
				result[key] = bounded
			}
		}
	}
	return result
}

// AttachAdaptationDiagnostic makes the request-local trace visible to the
// response filter and the audit finalizer.
func AttachAdaptationDiagnostic(c *gin.Context, diagnostic *AdaptationDiagnostic) {
	if c == nil || diagnostic == nil {
		return
	}
	c.Set(adaptationDiagnosticContextKey, diagnostic)
}

// RecordAdaptationStage appends one request-local decision to the diagnostic
// timeline. It is safe to call when adaptation was never initialized.
func RecordAdaptationStage(c *gin.Context, stage, status, summary string, details map[string]any) {
	adaptationDiagnosticFromContext(c).Record(stage, status, summary, details)
}

func adaptationDiagnosticFromContext(c *gin.Context) *AdaptationDiagnostic {
	if c == nil {
		return nil
	}
	value, exists := c.Get(adaptationDiagnosticContextKey)
	if !exists {
		return nil
	}
	diagnostic, _ := value.(*AdaptationDiagnostic)
	return diagnostic
}

// BuildAdaptationDiagnostic converts the existing structured audit fields into
// the timeline consumed by the audit workspace. It intentionally copies only
// decision metadata, never message content.
func BuildAdaptationDiagnostic(trace *PresetCompileTrace) *AdaptationDiagnostic {
	if trace == nil {
		return nil
	}
	diagnostic := newAdaptationDiagnostic()
	if trace.Plan != nil && trace.Plan.Base != nil {
		base := trace.Plan.Base
		diagnostic.Record("preflight", "completed", "request contract classified", map[string]any{
			"contract":            string(base.Contract),
			"client_final":        base.ClientFinal,
			"profile_fingerprint": base.ProfileFingerprint,
			"plan_fingerprint":    base.Fingerprint,
		})
	}
	if trace.Worldbook != nil {
		summary := "worldbook matching completed"
		if trace.Worldbook.BypassReason == "failure_policy" {
			summary = "worldbook failed and was skipped by the configured policy"
		} else if trace.Worldbook.Phase == "skipped" {
			summary = "worldbook was not applied to this request"
		}
		diagnostic.Record("worldbook", worldbookDiagnosticStatus(trace.Worldbook), summary, worldbookDiagnosticDetails(trace.Worldbook))
	} else {
		diagnostic.Record("worldbook", "skipped", "worldbook is not configured for this request", nil)
	}
	if trace.Name != "" || trace.Mode != "" || len(trace.Messages) > 0 {
		details := map[string]any{
			"name":          trace.Name,
			"mode":          trace.Mode,
			"message_count": len(trace.Messages),
			"warning_count": len(trace.Warnings),
			"has_script":    trace.ScriptResponse != nil,
		}
		if response := trace.ScriptResponse; response != nil && response.settings != nil {
			details["delivery"] = "preset"
			details["client_format_conversion"] = response.protocol != nil && slices.Contains(response.protocol.actions, "client_format_conversion")
			details["request_script_executed"] = response.requestHookExecuted
			details["request_messages_changed"] = response.requestMessagesChanged
			details["request_input_messages"] = response.requestInputMessages
			details["request_output_messages"] = response.requestOutputMessages
		}
		diagnostic.Record("preset", "completed", "preset compiled before upstream conversion", details)
	} else {
		diagnostic.Record("preset", "skipped", "no preset was selected for this request", nil)
	}
	if trace.Protocol != nil {
		diagnostic.Record("tool_resolution", protocolDiagnosticStatus(trace.Protocol), "tool visibility and response target resolved", protocolDiagnosticDetails(trace.Protocol))
	} else {
		diagnostic.Record("tool_resolution", "skipped", "tool protocol coordination was not active", nil)
	}
	if trace.Plan != nil {
		diagnostic.Record("plan", "frozen", "request plan frozen after tool resolution", map[string]any{
			"fingerprint":          trace.Plan.Fingerprint,
			"protocol_fingerprint": trace.Plan.ProtocolFingerprint,
			"phase":                trace.Plan.Phase,
			"final_tool":           trace.Plan.FinalTool,
		})
	}
	return diagnostic.snapshotDraft()
}

func worldbookDiagnosticStatus(preview *WorldbookPreview) string {
	if preview == nil {
		return "skipped"
	}
	if preview.BypassReason == "failure_policy" {
		return "fallback"
	}
	if preview.Phase == "skipped" {
		return "skipped"
	}
	return "completed"
}

func worldbookDiagnosticDetails(preview *WorldbookPreview) map[string]any {
	if preview == nil {
		return nil
	}
	matched, skipped, disabled, invalid := 0, 0, 0, 0
	entries := make([]map[string]any, 0, len(preview.Entries))
	for _, entry := range preview.Entries {
		switch entry.Reason {
		case "matched", "included":
			matched++
		case "disabled":
			disabled++
		case "invalid", "needs_review":
			invalid++
		default:
			skipped++
		}
		item := map[string]any{
			"book_id":  entry.BookID,
			"id":       entry.ID,
			"name":     entry.Name,
			"status":   entry.Status,
			"reason":   entry.Reason,
			"order":    entry.Order,
			"eligible": entry.Eligible, "triggered": entry.Triggered, "selected": entry.Selected, "inserted": entry.Inserted,
		}
		if entry.Placement != nil {
			item["placement"] = map[string]any{
				"mode":     entry.Placement.Mode,
				"index":    entry.Placement.Index,
				"role":     entry.Placement.Role,
				"fallback": entry.Placement.Fallback,
			}
		}
		entries = append(entries, item)
	}
	details := map[string]any{
		"matched":  matched,
		"skipped":  skipped,
		"disabled": disabled,
		"invalid":  invalid,
		"entries":  entries,
	}
	if preview.PlacementPhase != "" {
		details["placement_phase"] = preview.PlacementPhase
	}
	if preview.BypassReason != "" {
		details["bypass_reason"] = preview.BypassReason
	}
	if preview.Placement != nil {
		details["placement"] = map[string]any{
			"mode":     preview.Placement.Mode,
			"index":    preview.Placement.Index,
			"role":     preview.Placement.Role,
			"fallback": preview.Placement.Fallback,
		}
	}
	if len(preview.Warnings) > 0 {
		details["warnings"] = append([]string(nil), preview.Warnings...)
	}
	return details
}

func protocolDiagnosticStatus(protocol *PresetProtocolAudit) string {
	if protocol == nil {
		return "skipped"
	}
	if protocol.Activated {
		return "completed"
	}
	if protocol.Enabled || protocol.Eligible {
		return "fallback"
	}
	return "skipped"
}

func protocolDiagnosticDetails(protocol *PresetProtocolAudit) map[string]any {
	if protocol == nil {
		return nil
	}
	details := map[string]any{
		"enabled":      protocol.Enabled,
		"eligible":     protocol.Eligible,
		"activated":    protocol.Activated,
		"reason":       protocol.Reason,
		"phase":        protocol.Phase,
		"contract":     protocol.Contract,
		"client_final": protocol.ClientFinal,
		"preset_final": protocol.PresetFinal,
		"final_tool":   protocol.FinalTool,
		"visibility":   protocol.Visibility,
		"fingerprint":  protocol.Fingerprint,
	}
	if len(protocol.Auxiliary) > 0 {
		details["auxiliary"] = append([]string(nil), protocol.Auxiliary...)
	}
	if len(protocol.Actions) > 0 {
		details["actions"] = append([]string(nil), protocol.Actions...)
	}
	if len(protocol.Contents) > 0 {
		details["contents"] = append([]string(nil), protocol.Contents...)
	}
	if len(protocol.Diagnostics) > 0 {
		details["diagnostics"] = append([]string(nil), protocol.Diagnostics...)
	}
	return details
}

// RecordAdaptationWriteback marks the final client-facing write decision.
// The body audit already contains the bytes; this stage records only status
// and whether the request completed without a relay error.
func RecordAdaptationWriteback(c *gin.Context, success bool) {
	diagnostic := adaptationDiagnosticFromContext(c)
	if diagnostic == nil {
		return
	}
	status := "completed"
	summary := "client response written"
	if !success {
		status = "failed"
		summary = "request ended before a successful client response"
	}
	httpStatus := 0
	if c != nil && c.Writer != nil {
		httpStatus = c.Writer.Status()
	}
	diagnostic.Record("writeback", status, summary, map[string]any{
		"success":     success,
		"http_status": httpStatus,
	})
}
