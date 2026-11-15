package service

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Calcium-Ion/moejs"
	"github.com/QuantumNous/new-api/common"
)

// This is deliberately separate from task plugins: it exposes no Go objects,
// credentials, network, filesystem, timers, or reusable module globals.
// Only immutable compiled code may eventually be cached, never a loaded realm.
const maxPresetScriptSource = 128 << 10

var presetScriptSlots = make(chan struct{}, 2)

// Only these host-defined fields may leave the isolated VM. Exception messages,
// thrown values, function names and complete stacks may contain client data.
type presetScriptFailure struct {
	Kind         string `json:"kind"`
	Hook         string `json:"hook,omitempty"`
	Reason       string `json:"reason,omitempty"`
	ModuleLine   int    `json:"module_line,omitempty"`
	ModuleColumn int    `json:"module_column,omitempty"`
}

func (e *presetScriptFailure) Error() string {
	if e.Reason == "metadata_limit" {
		return "preset request metadata exceeded its request-scoped limit"
	}
	return "preset script raised " + e.Kind
}

func PresetScriptFailureDetails(err error) *presetScriptFailure {
	var detail *presetScriptFailure
	if !errors.As(err, &detail) {
		return nil
	}
	return detail
}

func presetScriptHookError(hook string, err error) error {
	if detail := PresetScriptFailureDetails(err); detail != nil {
		copy := *detail
		copy.Hook = hook
		err = &copy
	}
	return fmt.Errorf("%s: %w", hook, err)
}

var presetScriptFrame = regexp.MustCompile(`\(preset-hook\.js:([0-9]+):([0-9]+)\)`)

//go:embed preset_data_host.js
var presetDataHostSource string

type presetDataHost struct {
	Settings      json.RawMessage `json:"settings"`
	Metadata      json.RawMessage `json:"metadata"`
	MetadataLimit int             `json:"metadata_limit"`
	Client        json.RawMessage `json:"-"`
}

func runPresetScript(source string, input any, target any, deadline time.Time) error {
	return runPresetScriptWithHost(source, input, target, deadline, nil)
}

func runPresetScriptWithHost(source string, input any, target any, deadline time.Time, host *presetDataHost) error {
	source = strings.TrimRight(strings.TrimSpace(source), "; \r\n\t")
	if source == "" || len(source) > maxPresetScriptSource {
		return fmt.Errorf("preset script requires 1 to %d bytes of source", maxPresetScriptSource)
	}
	budget := 300 * time.Millisecond
	if !deadline.IsZero() {
		budget = min(budget, time.Until(deadline))
	}
	if budget <= 0 {
		return fmt.Errorf("preset script execution deadline exceeded")
	}
	expires := time.Now().Add(budget)
	admission := time.NewTimer(budget)
	defer admission.Stop()
	select {
	case presetScriptSlots <- struct{}{}:
		defer func() { <-presetScriptSlots }()
	case <-admission.C:
		return fmt.Errorf("preset script runtime is busy")
	}
	inputData, err := common.Marshal(input)
	if err != nil {
		return fmt.Errorf("preset script input is invalid or exceeds 8 MiB")
	}
	callHost := host
	if host != nil {
		copy := *host
		// Metadata may legitimately hold a request-local context snapshot.
		// Scale to the serialized input, never beyond the existing 8 MiB
		// boundary. Preserve the allowance across this request's hooks only.
		copy.MetadataLimit = min(maxFilteredResponseBytes, max((256<<10)+len(inputData), host.MetadataLimit))
		callHost = &copy
	}
	inputJSON, err := common.Marshal(map[string]any{"input": json.RawMessage(inputData), "host": callHost})
	if err != nil || len(inputJSON) > maxFilteredResponseBytes {
		return fmt.Errorf("preset script input is invalid or exceeds 8 MiB")
	}
	// JSON is the boundary in both directions: no mutable Go values cross it.
	// Console calls are inert; logging arbitrary messages would expose chat data.
	module, err := moejs.Compile("preset-hook.js", presetDataHostSource+`
const console = Object.freeze({log(){}, warn(){}, error(){}, debug(){}, info(){}});
export function run(serialized) {
  const envelope = JSON.parse(serialized);
  const input = envelope.input;
  const metadata = envelope.host ? createPresetDataHost(envelope.host.settings, envelope.host.metadata, {}, envelope.host.metadata_limit) : null;
  const result = (`+source+`)(input);
  if (result && typeof result.then === 'function')
    throw new TypeError('Asynchronous hooks are unsupported');
  const output = JSON.stringify({result, metadata: metadata ? metadata() : null});
  if (typeof output !== 'string' || output.length > 8388608)
    throw new TypeError('Invalid hook result');
  return output;
}`)
	if err != nil || len(module.Requests()) != 0 {
		return fmt.Errorf("preset script could not compile as an isolated function expression")
	}
	runtime := moejs.NewRuntime(moejs.Options{DisableDynamicCode: true, MemoryLimit: 64 << 20})
	budget = time.Until(expires)
	if budget <= 0 {
		return fmt.Errorf("preset script preparation exceeded its deadline")
	}
	interrupted := make(chan struct{})
	executionTimeout := errors.New("preset execution timeout")
	timer := time.AfterFunc(budget, func() {
		runtime.Interrupt(executionTimeout)
		close(interrupted)
	})
	defer func() {
		if !timer.Stop() {
			<-interrupted
		}
	}()
	if err := runtime.Load(module); err != nil {
		return fmt.Errorf("preset script initialization failed")
	}
	hook, err := module.Hook("run")
	if err != nil {
		return fmt.Errorf("preset script entry point is unavailable")
	}
	argument, err := runtime.FromGo(string(inputJSON))
	if err != nil {
		return fmt.Errorf("preset script input conversion failed")
	}
	result, err := runtime.Call(hook, argument)
	if err != nil {
		if errors.Is(err, executionTimeout) {
			return fmt.Errorf("preset script execution exceeded its deadline")
		}
		if errors.Is(err, moejs.ErrMemoryLimit) {
			return fmt.Errorf("preset script execution exceeded its memory limit")
		}
		detail := &presetScriptFailure{Kind: "an unclassified exception"}
		var exception *moejs.Exception
		if errors.As(err, &exception) {
			// StackTrace does not run getters or other user code. Whitelist
			// the class and numeric location, never the error message or stack.
			stack := runtime.StackTrace(exception)
			// Only exact host-defined messages are classified; never publish
			// arbitrary exception text or guesses based on a partial match.
			if exception.Name() == "RangeError" && exception.Message() == "Request metadata limit" {
				detail.Reason = "metadata_limit"
			}
			kind, _, _ := strings.Cut(stack, ":")
			switch kind {
			case "Error", "TypeError", "ReferenceError", "RangeError", "SyntaxError", "URIError", "EvalError", "AggregateError":
				detail.Kind = kind
			}
			if _, frames, ok := strings.Cut(stack, "\n"); ok {
				if frame := presetScriptFrame.FindStringSubmatch(frames); len(frame) == 3 {
					detail.ModuleLine, _ = strconv.Atoi(frame[1])
					detail.ModuleColumn, _ = strconv.Atoi(frame[2])
				}
			}
		}
		return detail
	}
	value, err := runtime.ToGo(result)
	if err != nil {
		return fmt.Errorf("preset script output conversion failed")
	}
	output, ok := value.(string)
	if !ok || len(output) > maxFilteredResponseBytes {
		return fmt.Errorf("preset script output is invalid or exceeds 8 MiB")
	}
	var envelope struct {
		Result   json.RawMessage `json:"result"`
		Metadata json.RawMessage `json:"metadata"`
	}
	if err := common.Unmarshal([]byte(output), &envelope); err != nil || len(envelope.Result) == 0 {
		return fmt.Errorf("preset script returned an incompatible result")
	}
	if host != nil {
		if len(envelope.Metadata) > callHost.MetadataLimit || common.GetJsonType(envelope.Metadata) != "object" {
			return fmt.Errorf("preset request metadata exceeds its request-scoped limit or is not an object")
		}
	}
	if err := common.Unmarshal(envelope.Result, target); err != nil {
		return fmt.Errorf("preset script returned an incompatible result")
	}
	if host != nil {
		host.Metadata = envelope.Metadata
		host.MetadataLimit = callHost.MetadataLimit
	}
	return nil
}
