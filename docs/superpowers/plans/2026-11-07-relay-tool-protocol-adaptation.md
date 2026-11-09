# Relay Tool Protocol Adaptation Implementation Plan

> **Historical plan — do not execute (2026-11-09).**
> Superseded by the review and proposed design in
> `../specs/2026-11-08-relay-adaptation-final-spec.md` and
> `../specs/2026-11-09-relay-adaptation-review.md`.
> The tasks and agent instructions below are retained only as historical context.
> The current authorization covers review and documentation, not implementation.

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a conditional, config-driven protocol adaptation layer that keeps one final reply protocol per model call and preserves the client's external text, JSON, or tool contract.

**Architecture:** Resolve tool roles from explicit configuration, the original client contract, and preset-script ownership. Project only the current turn's `tools` and `tool_choice` before relay, then project a preset terminal tool result into the client's text or JSON content contract. All disabled or ambiguous cases retain the existing relay path.

**Tech Stack:** Go 1.26 root module, `relaykit` DTO module, Gin, existing `spreset-v1`/`tavern-v1` script runtime, existing response text filter and SSE pipeline.

## Revision 2 (authoritative corrections)

The formal spec `docs/superpowers/specs/2026-11-07-relay-tool-protocol-adaptation-design.md` is authoritative. The original task text below remains as historical scaffolding, but implementation must apply these corrections:

- Add `Enabled bool` to `SillyTavernToolProtocol`; a missing protocol object or `enabled=false` is a complete no-op.
- Infer the client final tool from the original client contract and the compiled request together. One original client tool plus preset-added tools and compiled `tool_choice=required` must still treat the original client tool as final, even when the original choice was not function-level.
- Treat script ownership (`ownedTools`) as the source of preset tool identity. Do not exclude a runtime solely because it is `tavern-v1`; if ownership is unavailable, remain inactive.
- Determine the phase from compiled messages and the latest user boundary. A completed auxiliary tool result after that boundary selects the final phase.
- Add audit fields for `enabled`, `eligible`, `reason`, and the existing safe identity fields.
- `response_target=tool` requires a client final tool. The first version supports `json_object`; `json_schema` does not activate automatic projection.
- Add the preset editor switch, persistence behavior, and retry fingerprint coverage described by the spec.
- Any unsafe or ambiguous case must leave request and response unchanged and record a diagnostic.

## Global Constraints

- Use `common.Marshal` / `common.Unmarshal` / `common.UnmarshalPreservingNumbers` in the root module; never call `encoding/json.Marshal` or `Unmarshal` from business code.
- Keep `relaykit` independently buildable with `GOWORK=off`.
- Preserve explicit optional scalar zero values in relay DTOs.
- Do not modify routing, billing, authentication, quota, or provider-private fields.
- Do not create frontend or documentation files unless the user explicitly requests them; the formal spec and this plan are explicitly requested.
- Format every modified Go file with the repository Go toolchain.
- Keep unrelated worktree changes untouched: `web/scripts/*.mjs`, `.playwright-cli/`, `PRODUCT.md`, and `output/`.

---

### Task 1: Add the protocol configuration contract

**Files:**
- Modify: `relaykit/dto/sillytavern_preset.go`
- Test: `relaykit/dto/sillytavern_preset_test.go`

**Interfaces:**
- Consumes: existing `SillyTavernPresetConfig`.
- Produces:
  - `type SillyTavernToolProtocol struct`
  - `SillyTavernPresetConfig.ToolProtocol *SillyTavernToolProtocol`
  - validation of optional protocol names and response target.

- [ ] **Step 1: Write the failing validation test**

```go
func TestSillyTavernToolProtocolValidation(t *testing.T) {
	valid := &SillyTavernPresetConfig{
		Preset: []byte(`{"prompts":[{"identifier":"main","content":"x"}],"prompt_order":[{"order":[{"identifier":"main","enabled":true}]}]}`),
		ToolProtocol: &SillyTavernToolProtocol{
			ClientFinal: "client_reply",
			Auxiliary:   []string{"context"},
		},
	}
	_, err := valid.ParseAndValidate()
	require.NoError(t, err)

	invalid := *valid
	invalid.ToolProtocol = &SillyTavernToolProtocol{ResponseTarget: "xml"}
	_, err = invalid.ParseAndValidate()
	require.ErrorContains(t, err, "tool_protocol response_target")
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run:

```powershell
& 'D:\JAVA\XM\new-api-audit\.tools\go\bin\go.exe' test ./relaykit/dto -run TestSillyTavernToolProtocolValidation -count=1
```

Expected: FAIL because `SillyTavernToolProtocol` and the field do not exist.

- [ ] **Step 3: Add the contract and validation**

```go
type SillyTavernToolProtocol struct {
	ClientFinal    string   `json:"client_final,omitempty"`
	PresetFinal    string   `json:"preset_final,omitempty"`
	Auxiliary      []string `json:"auxiliary,omitempty"`
	ResponseTarget string   `json:"response_target,omitempty"` // auto, text, json, tool
}
```

Add `ToolProtocol *SillyTavernToolProtocol` to `SillyTavernPresetConfig`.

Validate:

- `ResponseTarget` is empty or one of `auto`, `text`, `json`, `tool`.
- each explicit tool name is 1 to 64 characters from the existing tool-name alphabet.
- `Auxiliary` has at most 64 unique names.
- `ClientFinal` and `PresetFinal` cannot both be non-empty.

- [ ] **Step 4: Run the test to verify it passes**

Run the command from Step 2. Expected: PASS.

- [ ] **Step 5: Run the DTO package tests**

```powershell
Push-Location relaykit
$env:GOWORK='off'
& 'D:\JAVA\XM\new-api-audit\.tools\go\bin\go.exe' test ./dto -count=1
Pop-Location
```

Expected: PASS.

---

### Task 2: Build the capability plan and role resolver

**Files:**
- Create: `service/preset_protocol.go`
- Modify: `service/sillytavern_preset.go:47-57`
- Test: `service/sillytavern_preset_test.go`

**Interfaces:**

- Consumes:
  - original `*dto.GeneralOpenAIRequest`
  - preset-compiled `*dto.GeneralOpenAIRequest`
  - `*dto.SillyTavernPresetConfig`
  - `*PresetScriptResponse`
- Produces:
  - `type PresetProtocolAudit`
  - `type presetProtocolPlan`
  - `func buildPresetProtocolPlan(...) (*presetProtocolPlan, int, error)`
  - `func (p *presetProtocolPlan) fingerprint() string`

The integer return is the request-side validation mode:

- `0`: strict original client contract
- `1`: protocol projection owns the upstream `tools`/`tool_choice`, while client history remains strict

- [ ] **Step 1: Write failing role-resolution tests**

Add tests covering:

```go
func TestPresetProtocolPlanUsesClientFinalAndOwnedAuxiliary(t *testing.T) {}

func TestPresetProtocolPlanUsesExplicitConfiguration(t *testing.T) {}

func TestPresetProtocolPlanLeavesAmbiguousOwnedToolsUntouched(t *testing.T) {}
```

The first test must use one client tool named `reply`, one script-owned tool named `context`, `tool_choice=required`, and assert:

- plan is active
- `FinalTool == "reply"`
- `Auxiliary == []string{"context"}`
- strict history validation mode is preserved

The second test must set `ToolProtocol.ClientFinal` and `ToolProtocol.Auxiliary` explicitly and assert those names win.

The third test must use two owned tools, `tool_choice=required`, no client tool, and assert:

- plan is not active
- audit contains a diagnostic naming `ambiguous_preset_final_tool`
- original compiled tools remain unchanged

- [ ] **Step 2: Run the failing tests**

```powershell
& 'D:\JAVA\XM\new-api-audit\.tools\go\bin\go.exe' test ./service -run 'TestPresetProtocolPlan' -count=1
```

Expected: FAIL because the plan types and resolver do not exist.

- [ ] **Step 3: Implement the plan structure**

Create `service/preset_protocol.go` with:

```go
type PresetProtocolAudit struct {
	Activated   bool     `json:"activated"`
	Phase       string   `json:"phase,omitempty"`
	Contract    string   `json:"contract,omitempty"` // text, json, tool
	ClientFinal string   `json:"client_final,omitempty"`
	PresetFinal string   `json:"preset_final,omitempty"`
	FinalTool   string   `json:"final_tool,omitempty"`
	Auxiliary   []string `json:"auxiliary,omitempty"`
	Diagnostics []string `json:"diagnostics,omitempty"`
	Fingerprint string   `json:"fingerprint,omitempty"`
}

type presetProtocolPlan struct {
	audit         PresetProtocolAudit
	clientFinal   string
	presetFinal   string
	finalTool     string
	auxiliary     []string
	contract      string
	validationMode int
	phase         string
}
```

Implement deterministic role resolution in this order:

1. explicit `ToolProtocol.ClientFinal`, `PresetFinal`, and `Auxiliary`;
2. client function-level `tool_choice`;
3. client `required` with exactly one original tool;
4. preset-owned tool named by a function-level `tool_choice`;
5. a single preset-owned tool when no client final exists;
6. otherwise do not activate the plan.

Build the auxiliary list from owned tools minus the selected preset final tool. If a client final exists, all owned tools are auxiliary.

`fingerprint` must hash only stable protocol identity:

```go
common.Marshal(struct {
	Contract     string
	ClientFinal  string
	PresetFinal  string
	FinalTool    string
	Auxiliary    []string
}{
	plan.contract,
	plan.clientFinal,
	plan.presetFinal,
	plan.finalTool,
	plan.auxiliary,
})
```

- [ ] **Step 4: Add the trace field**

Add `Protocol *PresetProtocolAudit` to `PresetCompileTrace` and keep it JSON-serializable.

- [ ] **Step 5: Run the role tests**

Run the command from Step 2. Expected: PASS.

---

### Task 3: Apply request projection and relax only the gateway-owned contract

**Files:**
- Modify: `service/preset_protocol.go`
- Modify: `service/sillytavern_preset.go:538-587`
- Modify: `service/preset_script_adapter.go:221-329`
- Test: `service/sillytavern_preset_test.go`

**Interfaces:**

- Consumes: `presetProtocolPlan` from Task 2.
- Produces:
  - `func (p *presetProtocolPlan) applyRequest(request *dto.GeneralOpenAIRequest) error`
  - `func (p *presetProtocolPlan) phaseForMessages(messages []dto.Message) string`
  - `func validatePresetClientToolContractWithMode(original, compiled *dto.GeneralOpenAIRequest, mode int) error`

- [ ] **Step 1: Write failing request projection tests**

Add a request with:

```go
request := &dto.GeneralOpenAIRequest{
	Model: "m",
	Tools: []dto.ToolCallRequest{{
		Type: "function",
		Function: dto.FunctionRequest{Name: "reply", Parameters: json.RawMessage(`{"type":"object"}`)},
	}},
	ToolChoice: "required",
	Messages: []dto.Message{
		{Role: "user", Content: "go"},
		{Role: "assistant", ToolCalls: json.RawMessage(`[{"id":"c1","type":"function","function":{"name":"context","arguments":"{}"}}]`)},
		{Role: "tool", ToolCallId: "c1", Content: "synced"},
	},
}
```

The bundled preset adds `context`. Assert the compiled upstream request has only `reply`, `tool_choice` is a function call to `reply`, and the client history is byte-for-byte unchanged.

Add a second test where the last message is a user message and no auxiliary result exists. Assert the upstream request exposes only `context` and forces it.

Add a third test where the final tool is a preset-owned tool and the client only requests JSON content. Assert the upstream request exposes only that preset final tool.

- [ ] **Step 2: Run the tests to verify they fail**

```powershell
& 'D:\JAVA\XM\new-api-audit\.tools\go\bin\go.exe' test ./service -run 'TestPresetProtocolRequestProjection' -count=1
```

Expected: FAIL because no projection is applied.

- [ ] **Step 3: Implement the phase resolver**

Implement:

```go
func (p *presetProtocolPlan) phaseForMessages(messages []dto.Message) string {
	if len(p.auxiliary) == 0 {
		return "final"
	}
	auxiliary := make(map[string]struct{}, len(p.auxiliary))
	for _, name := range p.auxiliary {
		auxiliary[name] = struct{}{}
	}
	callNames := map[string]string{}
	lastUser := -1
	for index, message := range messages {
		if message.Role == "user" {
			lastUser = index
		}
		for _, call := range parsePresetToolCalls(message.ToolCalls) {
			callNames[call.ID] = call.Function.Name
		}
	}
	for index := len(messages) - 1; index > lastUser; index-- {
		message := messages[index]
		if message.Role != "tool" || message.ToolCallId == "" {
			continue
		}
		if _, ok := auxiliary[callNames[message.ToolCallId]]; ok {
			return "final"
		}
	}
	return "auxiliary"
}
```

Use a small private parser for `json.RawMessage` tool calls. Do not expose it outside the service package.

- [ ] **Step 4: Implement request projection**

`applyRequest` must:

- leave `messages`, `functions`, `function_call`, model, stream, billing, and provider fields untouched;
- in auxiliary phase set `Tools` to the auxiliary definitions present in the current upstream request;
- in final phase set `Tools` to the selected final tool definition;
- set `ToolChoice` to:

```go
map[string]any{
	"type": "function",
	"function": map[string]any{"name": selectedName},
}
```

- preserve the original tool definition bytes by copying the matching `dto.ToolCallRequest` value;
- return an error if the selected tool cannot be found.

- [ ] **Step 5: Split validation**

Change `validatePresetClientToolContract` to delegate to:

```go
func validatePresetClientToolContractWithMode(original, compiled *dto.GeneralOpenAIRequest, mode int) error
```

Keep existing behavior for mode `0`.

For mode `1`:

- require every original client tool to remain unchanged in `compiled`;
- require original `Functions`, `FunctionCall`, and tool history to remain unchanged;
- allow `Tools` and `ToolChoice` to differ because projection owns them;
- still reject modified/removed/reordered history and reused IDs.

- [ ] **Step 6: Wire projection into preset compilation**

In `CompileSillyTavernPreset`:

1. build the plan after script tools and legacy `ToolText` handling;
2. validate the preset-compiled request with the mode returned by `buildPresetProtocolPlan`;
3. call `plan.applyRequest(compiled)` only when the plan is active;
4. store `trace.Protocol = &plan.audit`;
5. attach the plan to `trace.ScriptResponse`.

Do not perform projection when `config.ScriptRuntime == ""`.

- [ ] **Step 7: Run the request projection tests**

Run the command from Step 2. Expected: PASS.

---

### Task 4: Project terminal tool calls into text and JSON content

**Files:**
- Modify: `service/preset_protocol.go`
- Modify: `service/preset_script_response.go:11-49`
- Modify: `service/response_text_filter.go:180-225`
- Test: `service/sillytavern_preset_test.go`
- Test: `service/response_text_filter_test.go`

**Interfaces:**

- Consumes: `presetProtocolPlan` from Task 2.
- Produces:
  - `func (p *presetProtocolPlan) responseTarget(original *dto.GeneralOpenAIRequest) string`
  - `func (p *presetProtocolPlan) restoreTerminal(body []byte, stream bool, deadline time.Time, request *dto.GeneralOpenAIRequest) ([]byte, error)`

- [ ] **Step 1: Write failing text projection test**

Create a script preset with a final tool whose schema has:

```json
{"type":"object","properties":{"think_nya":{"type":"string"},"game_content":{"type":"string"}},"required":["think_nya","game_content"]}
```

Return a response containing only a terminal tool call with:

```json
{"think_nya":"reason","game_content":"visible reply"}
```

Assert non-stream output contains `"content":"visible reply"`, no `tool_calls`, and `"finish_reason":"stop"`.

- [ ] **Step 2: Write failing JSON projection test**

Send a client request with `response_format.type=json_object`, project a preset terminal tool whose arguments are:

```json
{"answer":"ok","score":7}
```

Assert `content` is a JSON string containing that object.

- [ ] **Step 3: Write failing auxiliary passthrough test**

Assert auxiliary-phase responses retain the auxiliary `tool_calls`, do not become text, and do not force `finish_reason=stop`.

- [ ] **Step 4: Run the tests to verify they fail**

```powershell
& 'D:\JAVA\XM\new-api-audit\.tools\go\bin\go.exe' test ./service -run 'TestPresetProtocol.*Projection|TestPresetProtocolAuxiliary' -count=1
```

Expected: FAIL because response projection is not wired.

- [ ] **Step 5: Implement response target selection**

`responseTarget` must return:

- `tool` when the client has a clear client final tool;
- `json` when the original request has `ResponseFormat.Type` equal to `json_object` or `json_schema`;
- `text` otherwise.

An explicit `ToolProtocol.ResponseTarget` overrides auto detection, except that `tool` is invalid when no client final exists.

- [ ] **Step 6: Implement terminal extraction**

Implement `restoreTerminal` with the existing bounded `restorePresetToolCalls` helper:

- only consume the selected final tool name;
- use `OutputPreprocessing.toolCallFormatter` when enabled;
- otherwise use the last required string property, then the schema/property order, then the complete JSON argument string;
- reject malformed JSON;
- enforce `maxFilteredResponseBytes`;
- preserve `usage`, reasoning, choice index, and non-tool frames.

- [ ] **Step 7: Wire the response pipeline**

In `PresetScriptResponse.restoreTools`:

1. if no protocol plan exists, keep the current behavior;
2. if the plan phase is `auxiliary`, return the body unchanged;
3. if the plan target is `tool`, return the body unchanged;
4. otherwise call `restoreTerminal`.

Keep legacy `tool_text` mutually exclusive with the new plan.

- [ ] **Step 8: Run projection tests**

Run the command from Step 4. Expected: PASS.

---

### Task 5: Add audit, preview, retry, and endpoint regression coverage

**Files:**
- Modify: `service/preset_protocol.go`
- Modify: `service/sillytavern_preset.go`
- Modify: `controller/relay.go:230-256`
- Modify: `controller/channel-test.go:250-300`
- Test: `service/sillytavern_preset_test.go`
- Test: `controller/relay_retry_test.go`

**Interfaces:**

- Consumes: `PresetProtocolAudit` from Task 2.
- Produces: protocol audit exposure through `PresetCompileTrace` and channel-test trace.

- [ ] **Step 1: Add failing audit tests**

Assert an activated plan exposes:

```go
trace.Protocol.Activated
trace.Protocol.Phase
trace.Protocol.FinalTool
trace.Protocol.Auxiliary
trace.Protocol.Fingerprint
```

Assert diagnostics do not contain any message content or tool description longer than 256 bytes.

- [ ] **Step 2: Add failing retry test**

In `controller/relay_retry_test.go`, add one case where the initial and selected channel have different `ToolProtocol.ClientFinal` values and assert the relay rejects the cross-channel retry before sending a second request.

- [ ] **Step 3: Add failing endpoint test**

Assert protocol configuration on a non-Chat-Completions endpoint is rejected by `validatePresetScriptEndpoint`, matching the existing script-runtime rule.

- [ ] **Step 4: Run the tests to verify they fail**

```powershell
& 'D:\JAVA\XM\new-api-audit\.tools\go\bin\go.exe' test ./service ./controller -run 'Protocol|ToolProtocol' -count=1
```

Expected: FAIL for the new retry and endpoint assertions until the configuration is included in the retry signature and endpoint guard.

- [ ] **Step 5: Implement the audit and retry propagation**

- Add `Protocol` to `PresetCompileTrace`.
- Include `ToolProtocol` in `presetRequestRetrySettings` by copying the value.
- In the cross-channel check, reject when the protocol fingerprints differ.
- Ensure the channel-test path receives the same `trace.Protocol`.

- [ ] **Step 6: Run the focused audit and controller tests**

Run the command from Step 4. Expected: PASS.

---

### Task 6: Verification and branch delivery

**Files:**
- No new source files unless a verification failure identifies a real gap.

- [ ] **Step 1: Format all modified Go files**

```powershell
& 'D:\JAVA\XM\new-api-audit\.tools\go\bin\gofmt.exe' -w service/preset_protocol.go service/sillytavern_preset.go service/preset_script_adapter.go service/preset_script_response.go service/response_text_filter.go relaykit/dto/sillytavern_preset.go relaykit/dto/sillytavern_preset_test.go service/sillytavern_preset_test.go service/response_text_filter_test.go controller/relay.go controller/relay_retry_test.go
```

- [ ] **Step 2: Run focused service tests**

```powershell
& 'D:\JAVA\XM\new-api-audit\.tools\go\bin\go.exe' test ./service -run 'Preset|Protocol|Worldbook|ResponseText' -count=1
```

Expected: PASS with zero failures.

- [ ] **Step 3: Run the full affected package tests**

```powershell
& 'D:\JAVA\XM\new-api-audit\.tools\go\bin\go.exe' test ./service ./controller -count=1
```

Expected: PASS with zero failures.

- [ ] **Step 4: Verify relaykit independently**

```powershell
Push-Location relaykit
$env:GOWORK='off'
& 'D:\JAVA\XM\new-api-audit\.tools\go\bin\go.exe' test ./...
& 'D:\JAVA\XM\new-api-audit\.tools\go\bin\go.exe' build ./...
Pop-Location
```

Expected: exit code 0 for both commands.

- [ ] **Step 5: Run the root build check**

```powershell
& 'D:\JAVA\XM\new-api-audit\.tools\go\bin\go.exe' build ./...
```

Expected: exit code 0.

- [ ] **Step 6: Inspect the final diff**

```powershell
git diff --stat
git diff --check
git status --short
```

Expected: only intended protocol and documentation files, no whitespace errors, and unrelated worktree changes remain untouched.

- [ ] **Step 7: Commit the feature**

```powershell
git add docs/superpowers relaykit/dto/sillytavern_preset.go relaykit/dto/sillytavern_preset_test.go service/preset_protocol.go service/sillytavern_preset.go service/preset_script_adapter.go service/preset_script_response.go service/response_text_filter.go service/sillytavern_preset_test.go service/response_text_filter_test.go controller/relay.go controller/relay_retry_test.go
git commit -m "feat(relay): adapt client and preset tool protocols"
```

- [ ] **Step 8: Push the feature branch**

```powershell
git push origin feature/personal-request-lab
```

Expected: the remote branch receives the commit and the configured automatic build is triggered.
