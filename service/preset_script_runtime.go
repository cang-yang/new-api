package service

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Calcium-Ion/moejs"
	"github.com/QuantumNous/new-api/common"
)

// This is deliberately separate from task plugins: it exposes no host objects,
// credentials, network, filesystem, timers, or reusable module globals.
// Only immutable compiled code may eventually be cached, never a loaded realm.
const maxPresetScriptSource = 128 << 10

var presetScriptSlots = make(chan struct{}, 2)

func runPresetScript(source string, input any, target any, deadline time.Time) error {
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
	inputJSON, err := common.Marshal(input)
	if err != nil || len(inputJSON) > maxFilteredResponseBytes {
		return fmt.Errorf("preset script input is invalid or exceeds 8 MiB")
	}
	// JSON is the boundary in both directions: no mutable Go values cross it.
	// Console calls are inert; logging arbitrary messages would expose chat data.
	module, err := moejs.Compile("preset-hook.js", `
const console = Object.freeze({log(){}, warn(){}, error(){}, debug(){}, info(){}});
export function run(serialized) {
  const input = JSON.parse(serialized);
  const result = (`+source+`)(input);
  if (result && typeof result.then === 'function')
    throw new TypeError('Asynchronous hooks are unsupported');
  const output = JSON.stringify(result);
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
	timer := time.AfterFunc(budget, func() {
		runtime.Interrupt(errors.New("preset execution timeout"))
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
		// Never include the JS exception: a script can throw the whole prompt.
		return fmt.Errorf("preset script failed, exceeded its limits, or requires an unavailable API")
	}
	value, err := runtime.ToGo(result)
	if err != nil {
		return fmt.Errorf("preset script output conversion failed")
	}
	output, ok := value.(string)
	if !ok || len(output) > maxFilteredResponseBytes {
		return fmt.Errorf("preset script output is invalid or exceeds 8 MiB")
	}
	if err := common.Unmarshal([]byte(output), target); err != nil {
		return fmt.Errorf("preset script returned an incompatible result")
	}
	return nil
}
