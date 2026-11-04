package service

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

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
	Enabled bool                       `json:"enabled"`
	Content string                     `json:"content"`
	Data    map[string]json.RawMessage `json:"data"`
	Scripts []presetTavernScript       `json:"scripts"`
}

type presetTavernSession struct {
	mu       sync.Mutex
	runtime  *moejs.Runtime
	module   *moejs.Module
	closed   bool
	failed   bool
	finished bool
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
		return fmt.Errorf("Tavern script failed or exceeded execution limits")
	}
	if state, value, promise := moejs.PromiseResult(result); promise {
		if state != moejs.PromiseFulfilled {
			return fmt.Errorf("Tavern script requires an unavailable capability or dependency, failed, or left an unresolved promise")
		}
		result = value
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
		total += len(script.Content)
		if len(script.Content) > maxPresetScriptSource || total > 1<<20 {
			return nil, nil, fmt.Errorf("Tavern script sources exceed size limits")
		}
		sources[key] = script.Content
		entries = append(entries, map[string]any{"id": script.ID, "module": key, "data": script.Data})
	}
	// Admission covers compilation as well as execution and retained closures.
	select {
	case presetTavernSessions <- struct{}{}:
	default:
		return nil, nil, fmt.Errorf("Tavern runtime has reached its active request limit")
	}
	session := &presetTavernSession{}
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
			return nil, nil, fmt.Errorf("Tavern module could not compile as JavaScript")
		}
		modules[name] = module
	}
	resolve := func(referrer moejs.Referrer, name string) (*moejs.Module, error) {
		if strings.HasPrefix(name, ".") && referrer != nil {
			base, baseErr := url.Parse(referrer.Name())
			relative, relativeErr := url.Parse(name)
			if baseErr == nil && relativeErr == nil {
				name = base.ResolveReference(relative).String()
			}
		}
		if module := modules[name]; module != nil {
			return module, nil
		}
		return nil, fmt.Errorf("module is not in the administrator-pinned bundle")
	}
	host, err := moejs.Compile("gateway-tavern-host", presetTavernHost)
	if err != nil {
		return nil, nil, fmt.Errorf("Tavern host could not compile")
	}
	session.module = host
	session.runtime = moejs.NewRuntime(moejs.Options{
		DisableDynamicCode: true, MemoryLimit: 32 << 20,
		Importer: &moejs.Importer{Resolve: resolve},
	})
	before, err := common.Marshal(request)
	if err != nil {
		return nil, nil, err
	}
	var result struct {
		Request     map[string]json.RawMessage `json:"request"`
		Diagnostics []string                   `json:"diagnostics"`
	}
	if err := session.call("start", map[string]any{
		"request": json.RawMessage(before), "scripts": entries, "user": macro.user, "char": macro.char,
		"chatID": macro.chatID, "variables": macro.vars, "globalVariables": macro.globalVars,
	}, &result); err != nil {
		return nil, nil, err
	}
	if err := applyPresetRequestChanges(before, result.Request, request); err != nil {
		return nil, nil, err
	}
	warnings := []string{"Tavern data scripts use request-local messages and variables; browser, persistence, external IO and autonomous generation are unavailable"}
	if len(result.Diagnostics) > 0 {
		warnings = append(warnings, "Tavern scripts caught unavailable host operations; review the script compatibility requirements")
	}
	accepted = true
	return &PresetScriptResponse{tavern: session, fingerprint: presetScriptFingerprint(config)}, warnings, nil
}
