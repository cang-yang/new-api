package service

import (
	"crypto/sha256"
	"encoding/hex"
	"slices"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
)

type ContractClass string

const (
	ContractTool    ContractClass = "tool"
	ContractJSON    ContractClass = "json"
	ContractText    ContractClass = "text"
	ContractLegacy  ContractClass = "legacy"
	ContractUnknown ContractClass = "unknown"
)

type FailureStage string

const (
	FailureStagePreflight          FailureStage = "preflight"
	FailureStageWorldbook          FailureStage = "worldbook"
	FailureStagePrecompile         FailureStage = "precompile"
	FailureStagePreset             FailureStage = "preset"
	FailureStageScript             FailureStage = "script"
	FailureStageToolResolution     FailureStage = "tool_resolution"
	FailureStageResponseValidation FailureStage = "response_validation"
	FailureStageResponseRestore    FailureStage = "response_restore"
	FailureStageOutputProcessing   FailureStage = "output_processing"
	FailureStageRegex              FailureStage = "regex"
	FailureStageCrossChannelRetry  FailureStage = "cross_channel_retry"
	FailureStageResponseStarted    FailureStage = "response_started"
)

type FailureMode string

const (
	FailureRequestPassthrough      FailureMode = "request_passthrough"
	FailureSkip                    FailureMode = "skip"
	FailureFail                    FailureMode = "fail"
	FailureToolProtocolPassthrough FailureMode = "tool_protocol_passthrough"
	FailureSafeRaw                 FailureMode = "safe_raw"
	FailurePreserve                FailureMode = "preserve"
	FailureEmpty                   FailureMode = "empty"
	FailureReject                  FailureMode = "reject"
	FailureStop                    FailureMode = "stop"
)

// AdaptationProfile is the channel-scoped policy compiled from the existing
// channel settings. It is request-local and never persisted as hidden state.
type AdaptationProfile struct {
	Enabled      bool
	Preset       *dto.SillyTavernPresetConfig
	Worldbook    *dto.WorldbookConfig
	SendRules    *dto.ResponseTextFilter
	ReceiveRules *dto.ResponseTextFilter
}

type AdaptationBasePlan struct {
	Profile            *AdaptationProfile        `json:"-"`
	Original           *dto.GeneralOpenAIRequest `json:"-"`
	Matched            *dto.GeneralOpenAIRequest `json:"-"`
	Contract           ContractClass
	ClientFinal        string
	ProfileFingerprint string
	RequestFingerprint string
	Fingerprint        string
}

type AdaptationResolvedPlan struct {
	Base                *AdaptationBasePlan `json:"-"`
	ProtocolFingerprint string
	Phase               string
	FinalTool           string
	Fingerprint         string
}

type AdaptationRequestPlan struct {
	Base     *AdaptationBasePlan
	Resolved *AdaptationResolvedPlan
}

func NewAdaptationProfile(preset *dto.SillyTavernPresetConfig, worldbook *dto.WorldbookConfig, sendRules, receiveRules *dto.ResponseTextFilter) *AdaptationProfile {
	enabled := preset != nil || worldbook != nil && worldbook.Enabled || sendRules != nil || receiveRules != nil
	if !enabled {
		return nil
	}
	return &AdaptationProfile{
		Enabled:      true,
		Preset:       preset,
		Worldbook:    worldbook,
		SendRules:    sendRules,
		ReceiveRules: receiveRules,
	}
}

type AdaptationFailurePolicy struct {
	preflight          FailureMode
	worldbook          FailureMode
	precompile         FailureMode
	preset             FailureMode
	script             FailureMode
	toolResolution     FailureMode
	responseValidation FailureMode
	responseRestore    FailureMode
	outputProcessing   FailureMode
	regex              FailureMode
	crossChannelRetry  FailureMode
	responseStarted    FailureMode
}

func (p AdaptationFailurePolicy) Mode(stage FailureStage) FailureMode {
	switch stage {
	case FailureStagePreflight:
		return p.preflight
	case FailureStageWorldbook:
		return p.worldbook
	case FailureStagePrecompile:
		return p.precompile
	case FailureStagePreset:
		return p.preset
	case FailureStageScript:
		return p.script
	case FailureStageToolResolution:
		return p.toolResolution
	case FailureStageResponseValidation:
		return p.responseValidation
	case FailureStageResponseRestore:
		return p.responseRestore
	case FailureStageOutputProcessing:
		return p.outputProcessing
	case FailureStageRegex:
		return p.regex
	case FailureStageCrossChannelRetry:
		return p.crossChannelRetry
	case FailureStageResponseStarted:
		return p.responseStarted
	default:
		return FailureFail
	}
}

func (p *AdaptationProfile) FailurePolicy() AdaptationFailurePolicy {
	policy := AdaptationFailurePolicy{
		preflight:          FailureFail,
		worldbook:          FailureFail,
		precompile:         FailureFail,
		preset:             FailureFail,
		script:             FailureFail,
		toolResolution:     FailureFail,
		responseValidation: FailureFail,
		responseRestore:    FailureFail,
		outputProcessing:   FailureFail,
		regex:              FailureFail,
		crossChannelRetry:  FailureReject,
		responseStarted:    FailureStop,
	}
	if p == nil || p.Preset == nil {
		return policy
	}
	configured := p.Preset.FailurePolicy
	if configured == nil {
		return policy
	}
	set := func(target *FailureMode, value string) {
		if value != "" {
			*target = FailureMode(value)
		}
	}
	set(&policy.preflight, configured.Preflight)
	set(&policy.worldbook, configured.Worldbook)
	set(&policy.precompile, configured.Precompile)
	set(&policy.preset, configured.Preset)
	set(&policy.script, configured.Script)
	set(&policy.toolResolution, configured.ToolResolution)
	set(&policy.responseValidation, configured.ResponseValidation)
	set(&policy.responseRestore, configured.ResponseRestore)
	set(&policy.outputProcessing, configured.OutputProcessing)
	set(&policy.regex, configured.Regex)
	set(&policy.crossChannelRetry, configured.CrossChannelRetry)
	set(&policy.responseStarted, configured.ResponseStarted)
	return policy
}

func AdaptationFailureMode(config *dto.SillyTavernPresetConfig, stage FailureStage) FailureMode {
	return (&AdaptationProfile{Preset: config}).FailurePolicy().Mode(stage)
}

// Request fallback is an explicit all-or-nothing cancellation. Required reply
// cleanup cannot be bypassed just because request preparation failed first.
func (p *AdaptationProfile) CanCancelBeforeProvider(model string) bool {
	if p == nil {
		return true
	}
	if p.Preset != nil && (p.Preset.ReplyAdapter != nil && len(p.Preset.ReplyAdapter.Containers) > 0 ||
		p.Preset.ToolText != nil && p.Preset.ToolText.Enabled ||
		p.Preset.ScriptRuntime != "" && p.FailurePolicy().Mode(FailureStageOutputProcessing) == FailureFail) {
		return false
	}
	if textRegexRequiresStrictFailure(p.ReceiveRules, p.Preset, model, false) {
		return false
	}
	if p.ReceiveRules != nil {
		if p.ReceiveRules.MissingMatch == "error" {
			return false
		}
		for _, rule := range p.ReceiveRules.Rules {
			if !rule.Disabled && rule.Stage == "receive" && rule.MissingMatch == "error" {
				return false
			}
		}
	}
	return true
}

// AdaptationProfileFingerprint exposes the deterministic channel-policy
// fingerprint. The relay uses it to reject cross-channel retries that would
// silently change the frozen request plan.
func AdaptationProfileFingerprint(profile *AdaptationProfile) (string, error) {
	return adaptationProfileFingerprint(profile)
}

// SameAdaptationPlan reports whether two frozen base plans describe the same
// adaptation. A nil plan on either side only matches a nil plan.
func SameAdaptationPlan(a, b *AdaptationBasePlan) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Fingerprint == b.Fingerprint
}

func BuildBasePlan(profile *AdaptationProfile, original, matched *dto.GeneralOpenAIRequest) (*AdaptationBasePlan, error) {
	if profile == nil {
		return nil, nil
	}
	originalCopy, err := cloneGeneralRequest(original)
	if err != nil {
		return nil, err
	}
	matchedCopy, err := cloneGeneralRequest(matched)
	if err != nil {
		return nil, err
	}
	frozenProfile, err := common.DeepCopy(profile)
	if err != nil {
		return nil, err
	}
	profile = frozenProfile
	contract := ClassifyContract(originalCopy)
	clientFinal := ""
	profileFingerprint, err := adaptationProfileFingerprint(profile)
	if err != nil {
		return nil, err
	}
	requestProfile := adaptationRequestProfile(profile, originalCopy.Model)
	requestFingerprint, err := adaptationProfileFingerprint(requestProfile)
	if err != nil {
		return nil, err
	}

	plan := &AdaptationBasePlan{
		Profile:            profile,
		Original:           originalCopy,
		Matched:            matchedCopy,
		Contract:           contract,
		ClientFinal:        clientFinal,
		ProfileFingerprint: profileFingerprint,
		RequestFingerprint: requestFingerprint,
	}
	plan.Fingerprint = adaptationPlanFingerprint(plan)
	return plan, nil
}

// AdaptationRequestFingerprint exposes the deterministic fingerprint used to
// reject cross-channel retries. It intentionally ignores receive-only rules.
func AdaptationRequestFingerprint(profile *AdaptationProfile, model string) (string, error) {
	return adaptationProfileFingerprint(adaptationRequestProfile(profile, model))
}

func adaptationRequestProfile(profile *AdaptationProfile, model string) *AdaptationProfile {
	if profile == nil {
		return nil
	}
	normalized := *profile
	normalized.ReceiveRules = nil
	normalized.SendRules = adaptationRequestTextFilter(profile.SendRules, model)
	if normalized.Preset != nil {
		if len(normalized.Preset.Models) > 0 && !slices.Contains(normalized.Preset.Models, model) {
			normalized.Preset = nil
		} else {
			preset := *normalized.Preset
			preset.Models = nil
			// Embedded regex scripts are receive-only. Send-side embedded rules
			// are gated by EnableSendRegex and stay represented below.
			preset.EnableEmbeddedRegex = false
			if !preset.EnableSendRegex {
				preset.RegexOverrides = nil
				preset.RegexFailurePolicy = ""
			}
			normalized.Preset = &preset
		}
	}
	if normalized.Preset == nil && normalized.Worldbook == nil && normalized.SendRules == nil {
		return nil
	}
	return &normalized
}

func adaptationRequestTextFilter(config *dto.ResponseTextFilter, model string) *dto.ResponseTextFilter {
	if config == nil || config.Mode != "rules" || !config.EnableSend ||
		len(config.Models) > 0 && !slices.Contains(config.Models, model) {
		return nil
	}
	copy := *config
	copy.Rules = nil
	for _, rule := range config.Rules {
		if !rule.Disabled && rule.Stage == "send" {
			copy.Rules = append(copy.Rules, rule)
		}
	}
	if len(copy.Rules) == 0 {
		return nil
	}
	copy.Pattern, copy.StartTag, copy.EndTag, copy.MissingMatch = "", "", "", ""
	copy.TrimCapture = false
	return &copy
}

func ResolveAdaptationPlan(base *AdaptationBasePlan, protocol *presetProtocolPlan) *AdaptationResolvedPlan {
	if base == nil {
		return nil
	}
	resolved := &AdaptationResolvedPlan{Base: base}
	if protocol != nil {
		resolved.ProtocolFingerprint = protocol.audit.Fingerprint
		resolved.Phase = protocol.phase
		resolved.FinalTool = protocol.finalTool
	}
	resolved.Fingerprint = adaptationResolvedFingerprint(resolved)
	return resolved
}

// AttachAdaptationPlan freezes the request plan after tool resolution and
// attaches it to the existing secret-free preset trace. It is intentionally
// side-effect-free when no adaptation plan exists.
func AttachAdaptationPlan(trace *PresetCompileTrace, base *AdaptationBasePlan) *AdaptationResolvedPlan {
	if trace == nil {
		return nil
	}
	if base == nil {
		trace.Diagnostics = BuildAdaptationDiagnostic(trace)
		return nil
	}
	var protocol *presetProtocolPlan
	if trace.ScriptResponse != nil {
		protocol = trace.ScriptResponse.protocol
	}
	resolved := ResolveAdaptationPlan(base, protocol)
	trace.Plan = resolved
	trace.Summary = &AdaptationSummary{Version: 2, Contract: string(base.Contract), ProfileFingerprint: base.ProfileFingerprint, Fingerprint: resolved.Fingerprint}
	if trace.ScriptResponse != nil {
		trace.ScriptResponse.resolvedPlan = resolved
	}
	trace.Diagnostics = BuildAdaptationDiagnostic(trace)
	return resolved
}

func ClassifyContract(request *dto.GeneralOpenAIRequest) ContractClass {
	if request == nil {
		return ContractUnknown
	}
	hasModern := len(request.Tools) > 0
	hasLegacy := len(request.Functions) > 0 || len(request.FunctionCall) > 0
	if hasModern && hasLegacy {
		return ContractUnknown
	}
	if hasModern {
		return ContractTool
	}
	if request.ResponseFormat != nil {
		switch request.ResponseFormat.Type {
		case "json", "json_object":
			return ContractJSON
		case "json_schema":
			return ContractUnknown
		}
	}
	if hasLegacy {
		return ContractLegacy
	}
	return ContractText
}

func cloneGeneralRequest(request *dto.GeneralOpenAIRequest) (*dto.GeneralOpenAIRequest, error) {
	if request == nil {
		return nil, nil
	}
	return common.DeepCopy(request)
}

func adaptationProfileFingerprint(profile *AdaptationProfile) (string, error) {
	if profile == nil {
		return "", nil
	}
	payload := struct {
		Enabled      bool
		Preset       *dto.SillyTavernPresetConfig
		Worldbook    *dto.WorldbookConfig
		SendRules    *dto.ResponseTextFilter
		ReceiveRules *dto.ResponseTextFilter
	}{
		Enabled:      profile.Enabled,
		Preset:       profile.Preset,
		Worldbook:    profile.Worldbook,
		SendRules:    profile.SendRules,
		ReceiveRules: profile.ReceiveRules,
	}
	encoded, err := common.Marshal(payload)
	if err != nil {
		return "", err
	}
	// Hash parsed configuration, not JSON indentation or display-only labels.
	// Script-readable names remain significant while a runtime is enabled.
	var semantic map[string]any
	if err := common.UnmarshalPreservingNumbers(encoded, &semantic); err != nil {
		return "", err
	}
	if worldbook, ok := semantic["Worldbook"].(map[string]any); ok {
		books, _ := worldbook["books"].([]any)
		for _, value := range books {
			book, _ := value.(map[string]any)
			delete(book, "id")
			delete(book, "name")
			delete(book, "warnings")
			entries, _ := book["entries"].([]any)
			for _, value := range entries {
				entry, _ := value.(map[string]any)
				delete(entry, "id")
				delete(entry, "name")
			}
		}
	}
	preset, _ := semantic["Preset"].(map[string]any)
	if preset["script_runtime"] == nil || preset["script_runtime"] == "" {
		if document, ok := preset["preset"].(map[string]any); ok {
			delete(document, "name")
			prompts, _ := document["prompts"].([]any)
			for _, value := range prompts {
				prompt, _ := value.(map[string]any)
				delete(prompt, "name")
			}
		}
	}
	for _, field := range []string{"SendRules", "ReceiveRules"} {
		if filter, ok := semantic[field].(map[string]any); ok {
			rules, _ := filter["rules"].([]any)
			for _, value := range rules {
				rule, _ := value.(map[string]any)
				delete(rule, "id")
				delete(rule, "name")
			}
		}
	}
	encoded, err = common.Marshal(semantic)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

func adaptationPlanFingerprint(plan *AdaptationBasePlan) string {
	payload := struct {
		Profile      string
		Contract     ContractClass
		ClientFinal  string
		OriginalHash string
		MatchedHash  string
	}{
		Profile:      plan.ProfileFingerprint,
		Contract:     plan.Contract,
		ClientFinal:  plan.ClientFinal,
		OriginalHash: hashRequest(plan.Original),
		MatchedHash:  hashRequest(plan.Matched),
	}
	encoded, _ := common.Marshal(payload)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func adaptationResolvedFingerprint(plan *AdaptationResolvedPlan) string {
	if plan == nil || plan.Base == nil {
		return ""
	}
	payload := struct {
		Base                string
		ProtocolFingerprint string
		Phase               string
		FinalTool           string
	}{
		Base:                plan.Base.Fingerprint,
		ProtocolFingerprint: plan.ProtocolFingerprint,
		Phase:               plan.Phase,
		FinalTool:           plan.FinalTool,
	}
	encoded, _ := common.Marshal(payload)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func hashRequest(request *dto.GeneralOpenAIRequest) string {
	if request == nil {
		return ""
	}
	encoded, err := common.Marshal(request)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}
