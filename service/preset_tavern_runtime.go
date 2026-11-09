package service

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/Calcium-Ion/moejs"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/gin-gonic/gin"
)

//go:embed preset_tavern_host.js
var presetTavernHost string

// Idle realms retain only this request's closure state until the response.
// Admission is bounded independently of the short execution-time semaphore.
var presetTavernSessions = make(chan struct{}, 16)

type presetTavernScript struct {
	Type    string                     `json:"type"`
	ID      string                     `json:"id"`
	Name    string                     `json:"name"`
	Enabled bool                       `json:"enabled"`
	Content string                     `json:"content"`
	Data    map[string]json.RawMessage `json:"data"`
	Scripts []presetTavernScript       `json:"scripts"`
}

type presetTavernSession struct {
	mu          sync.Mutex
	runtime     *moejs.Runtime
	module      *moejs.Module
	closed      bool
	failed      bool
	finished    bool
	diagnostic  presetTavernDiagnostic
	diagnostics []presetTavernDiagnostic
	scriptNames map[string]string
}

// Diagnostics contain only host-selected classifications and configuration
// identities. Never inspect an exception's message, stack or object fields.
type presetTavernDiagnostic struct {
	Phase      string `json:"phase"`
	ScriptID   string `json:"script_id,omitempty"`
	ScriptName string `json:"script_name,omitempty"`
	Capability string `json:"capability"`
	Module     string `json:"module,omitempty"`
}

func tavernDiagnosticIdentity(value string) string {
	value, _, _ = strings.Cut(value, "?")
	value, _, _ = strings.Cut(value, "#")
	parsed, err := url.Parse(value)
	if err != nil && (strings.Contains(value, "://") || strings.HasPrefix(value, "//")) {
		return "invalid-module-identifier"
	}
	if err == nil && parsed.Host != "" {
		parsed.User = nil
		value = parsed.String()
	}
	value = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, value)
	runes := []rune(value)
	return string(runes[:min(len(runes), 160)])
}

func (d presetTavernDiagnostic) Error() string {
	encoded, _ := common.Marshal(d)
	return "Tavern runtime: " + string(encoded)
}

func ClosePresetScriptResponse(c *gin.Context) {
	if c == nil {
		return
	}
	value, _ := c.Get("preset_script_response")
	if response, ok := value.(*PresetScriptResponse); ok && response != nil && response.tavern != nil {
		response.tavern.Close()
	}
}

func (s *presetTavernSession) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed {
		s.closed = true
		s.runtime, s.module = nil, nil
		<-presetTavernSessions
	}
}

// call serializes access and covers module loading, promise jobs and conversion
// with one time budget. A failed realm is never reused or returned to a pool.
func (s *presetTavernSession) call(name string, input any, output any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.failed {
		return fmt.Errorf("Tavern runtime is no longer available")
	}
	if name == "finish" && s.finished {
		return fmt.Errorf("Tavern runtime supports one final response choice")
	}
	encoded, err := common.Marshal(input)
	if err != nil || len(encoded) > maxFilteredResponseBytes {
		return fmt.Errorf("Tavern runtime input exceeds 8 MiB or is invalid")
	}
	timer := time.NewTimer(300 * time.Millisecond)
	defer timer.Stop()
	select {
	case presetScriptSlots <- struct{}{}:
		defer func() { <-presetScriptSlots }()
	case <-timer.C:
		return fmt.Errorf("Tavern runtime is busy")
	}
	done := make(chan struct{})
	interrupt := time.AfterFunc(300*time.Millisecond, func() {
		s.runtime.Interrupt(errors.New("Tavern execution timeout"))
		close(done)
	})
	defer func() {
		if !interrupt.Stop() {
			<-done
		}
	}()
	s.failed = true
	// Track promise identity only, never rejection values. The engine drains
	// jobs before Call returns, so handlers added later in that turn remove
	// their rejection. Bound retained identities and fail closed on overflow.
	unhandled := make(map[*moejs.Object]presetTavernDiagnostic)
	var rejectionLimit *presetTavernDiagnostic
	s.runtime.SetPromiseRejectionTracker(func(p moejs.Value, operation moejs.PromiseRejectionOperation) {
		if operation == moejs.PromiseRejectionHandle {
			delete(unhandled, p.AsObject())
			return
		}
		diagnostic := s.diagnostic
		diagnostic.Capability = "unhandled-promise-rejection"
		if len(unhandled) >= 256 {
			if rejectionLimit == nil {
				diagnostic.Capability = "promise-rejection-limit"
				rejectionLimit = &diagnostic
			}
			return
		}
		unhandled[p.AsObject()] = diagnostic
	})
	defer s.runtime.SetPromiseRejectionTracker(nil)
	if name == "start" {
		if err := s.runtime.Load(s.module); err != nil {
			return fmt.Errorf("Tavern host initialization failed")
		}
	}
	hook, err := s.module.Hook(name)
	if err != nil {
		return fmt.Errorf("Tavern host entry point is unavailable")
	}
	arg, err := s.runtime.FromGo(string(encoded))
	if err != nil {
		return fmt.Errorf("Tavern input conversion failed")
	}
	result, err := s.runtime.Call(hook, arg)
	if err != nil {
		return s.diagnostic
	}
	if state, value, promise := moejs.PromiseResult(result); promise {
		if state != moejs.PromiseFulfilled {
			if state == moejs.PromisePending && s.diagnostic.Capability == "script-failed" {
				s.diagnostic.Capability = "unresolved-promise"
			}
			return s.diagnostic
		}
		result = value
	}
	if rejectionLimit != nil {
		return *rejectionLimit
	}
	for _, diagnostic := range unhandled {
		return diagnostic
	}
	value, err := s.runtime.ToGo(result)
	if err != nil {
		return fmt.Errorf("Tavern output conversion failed")
	}
	text, ok := value.(string)
	if !ok || len(text) > maxFilteredResponseBytes || common.Unmarshal([]byte(text), output) != nil {
		return fmt.Errorf("Tavern script returned invalid or oversized data")
	}
	s.failed = false
	if name == "finish" {
		s.finished = true
	}
	return nil
}

func prepareTavernScripts(config *dto.SillyTavernPresetConfig, request *dto.GeneralOpenAIRequest, macro *presetMacroContext) (*PresetScriptResponse, []string, error) {
	if request.N != nil && *request.N != 1 {
		return nil, nil, fmt.Errorf("Tavern runtime requires a single response choice (n=1)")
	}
	var preset struct {
		Extensions struct {
			Helper struct {
				Scripts []presetTavernScript `json:"scripts"`
			} `json:"tavern_helper"`
		} `json:"extensions"`
	}
	if err := common.Unmarshal(config.Preset, &preset); err != nil {
		return nil, nil, fmt.Errorf("invalid Tavern script tree")
	}
	var active []presetTavernScript
	var visit func([]presetTavernScript, int) error
	visit = func(scripts []presetTavernScript, depth int) error {
		if depth > 8 {
			return fmt.Errorf("Tavern script folder nesting exceeds 8")
		}
		for _, script := range scripts {
			if !script.Enabled {
				continue
			}
			if script.Type == "folder" {
				if err := visit(script.Scripts, depth+1); err != nil {
					return err
				}
			} else if script.Type == "script" {
				active = append(active, script)
			} else {
				return fmt.Errorf("unsupported Tavern script tree entry")
			}
		}
		return nil
	}
	if err := visit(preset.Extensions.Helper.Scripts, 0); err != nil {
		return nil, nil, err
	}
	if len(active) == 0 || len(active) > 32 || len(config.ScriptModules) > 64 {
		return nil, nil, fmt.Errorf("Tavern runtime requires 1 to 32 enabled scripts and at most 64 bundled modules")
	}
	// All code comes from channel-owned configuration. Import resolution never
	// opens a socket, reads a file or falls back to server/module search paths.
	sources := map[string]string{}
	total := 0
	for name, source := range config.ScriptModules {
		if strings.HasPrefix(name, "preset:") || name == "" {
			return nil, nil, fmt.Errorf("invalid bundled module identifier")
		}
		total += len(source)
		if len(source) > maxPresetScriptSource || total > 1<<20 {
			return nil, nil, fmt.Errorf("Tavern bundled sources exceed size limits")
		}
		sources[name] = source
	}
	var entries []map[string]any
	for _, script := range active {
		key := "preset:" + script.ID
		_, duplicate := sources[key]
		if script.ID == "" || len(script.ID) > 128 || duplicate {
			return nil, nil, fmt.Errorf("Tavern script identifiers must be nonempty and unique")
		}
		source := tavernScriptSource(script.Content)
		total += len(source)
		if len(source) > maxPresetScriptSource || total > 1<<20 {
			return nil, nil, fmt.Errorf("Tavern script sources exceed size limits")
		}
		sources[key] = source
		entries = append(entries, map[string]any{"id": script.ID, "module": key, "data": script.Data})
	}
	// Admission covers compilation as well as execution and retained closures.
	select {
	case presetTavernSessions <- struct{}{}:
	default:
		return nil, nil, fmt.Errorf("Tavern runtime has reached its active request limit")
	}
	session := &presetTavernSession{scriptNames: map[string]string{}}
	for _, script := range active {
		session.scriptNames[script.ID] = tavernDiagnosticIdentity(script.Name)
	}
	accepted := false
	defer func() {
		if !accepted {
			session.Close()
		}
	}()
	modules := map[string]*moejs.Module{}
	for name, source := range sources {
		module, err := moejs.Compile(name, source)
		if err != nil {
			diagnostic := presetTavernDiagnostic{Phase: "compile", Capability: "invalid-javascript", Module: tavernDiagnosticIdentity(name)}
			if id, ok := strings.CutPrefix(name, "preset:"); ok {
				diagnostic.ScriptID, diagnostic.ScriptName = tavernDiagnosticIdentity(id), session.scriptNames[id]
			}
			return nil, nil, diagnostic
		}
		modules[name] = module
	}
	resolve := func(referrer moejs.Referrer, name string) (*moejs.Module, error) {
		if (strings.HasPrefix(name, ".") || strings.HasPrefix(name, "/")) && referrer != nil {
			base, baseErr := url.Parse(referrer.Name())
			relative, relativeErr := url.Parse(name)
			if baseErr == nil && relativeErr == nil {
				name = base.ResolveReference(relative).String()
			}
		}
		if module := modules[name]; module != nil {
			return module, nil
		}
		session.diagnostic.Phase = "module-resolution"
		session.diagnostic.Capability = "missing-module"
		session.diagnostic.Module = tavernDiagnosticIdentity(name)
		return nil, fmt.Errorf("module is not in the administrator-pinned bundle")
	}
	host, err := moejs.Compile("gateway-tavern-host", presetDataHostSource+presetTavernHost)
	if err != nil {
		return nil, nil, fmt.Errorf("Tavern host could not compile")
	}
	session.module = host
	session.runtime = moejs.NewRuntime(moejs.Options{
		DisableDynamicCode: true, MemoryLimit: 32 << 20,
		Importer: &moejs.Importer{Resolve: resolve},
	})
	// The trusted host captures and deletes this callback before any user code
	// loads. It cannot be invoked by script code to forge diagnostic fields.
	if err := session.runtime.SetGlobal("__tavernDiagnostic", moejs.NativeFunc(func(_ *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
		phase, id, capability := moejs.Arg(args, 0).String(), moejs.Arg(args, 1).String(), moejs.Arg(args, 2).String()
		allowed := []string{"script-failed", "jquery-dom", "concurrent-or-nested-events", "unawaited-event", "unawaited-async-work", "text-message-only", "message-swipes", "chat-reload", "hidden-messages", "browser-dom", "external-io"}
		if !slices.Contains(allowed, capability) {
			switch capability {
			case "event:CHARACTER_MESSAGE_RENDERED", "event:USER_MESSAGE_RENDERED", "event:APP_READY", "event:CHAT_ID_CHANGED", "event:MESSAGE_EDITED", "event:MESSAGE_SWIPED", "event:GENERATION_STOPPED":
			default:
				if kind, _, ok := strings.Cut(capability, ":"); ok && slices.Contains([]string{"event", "message-field", "variable-scope"}, kind) {
					capability = kind + ":unsupported"
				} else {
					capability = "unavailable-host-operation"
				}
			}
		}
		session.diagnostic = presetTavernDiagnostic{Phase: tavernDiagnosticIdentity(phase), ScriptID: tavernDiagnosticIdentity(id), ScriptName: session.scriptNames[id], Capability: capability}
		if capability != "script-failed" && len(session.diagnostics) < 16 && !slices.Contains(session.diagnostics, session.diagnostic) {
			session.diagnostics = append(session.diagnostics, session.diagnostic)
		}
		return moejs.Undefined(), nil
	})); err != nil {
		return nil, nil, fmt.Errorf("Tavern diagnostic initialization failed")
	}
	before, err := common.Marshal(request)
	if err != nil {
		return nil, nil, err
	}
	var result struct {
		Request     map[string]json.RawMessage `json:"request"`
		Diagnostics []bool                     `json:"diagnostics"`
	}
	dataHost, err := newPresetDataHost(config)
	if err != nil {
		return nil, nil, err
	}
	if err := session.call("start", map[string]any{
		"request": json.RawMessage(before), "scripts": entries, "user": macro.user, "char": macro.char,
		"chatID": macro.chatID, "variables": macro.vars, "globalVariables": macro.globalVars,
		"dataHost": dataHost,
	}, &result); err != nil {
		return nil, nil, err
	}
	if err := applyPresetRequestChanges(before, result.Request, request); err != nil {
		return nil, nil, err
	}
	warnings := []string{"Tavern data scripts use request-local messages and variables; browser, persistence, external IO and autonomous generation are unavailable"}
	if len(result.Diagnostics) > 0 {
		warnings = append(warnings, "Tavern scripts caught unavailable host operations; review the script compatibility requirements")
		for _, diagnostic := range session.diagnostics {
			warnings = append(warnings, diagnostic.Error())
		}
	}
	accepted = true
	return &PresetScriptResponse{tavern: session, fingerprint: presetScriptFingerprint(config)}, warnings, nil
}
