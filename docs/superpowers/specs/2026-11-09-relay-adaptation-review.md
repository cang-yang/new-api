# 渠道适配增量验收审查

日期：2026-11-09

代码基线：`315839842`

主要范围：`0151ca0fa..315839842`，54 个文件，约 10,373 行新增、347 行删除，含文档、测试和翻译。行数不代表质量。
本轮操作：代码和设计审查、运行已有必要测试、修订文档。**没有修改业务实现，没有提交、推送或部署。**

后续实施前设计已补入 [主设计第 12–18 节](2026-11-08-relay-adaptation-final-spec.md#12-实施边界与冲突决策确定规则)。本报告保留审查时的证据与验证结果；设计补齐不表示这些缺陷已修复，也不表示新验收案例已经运行。

随后增加的 [用户视角复审](2026-11-08-relay-adaptation-final-spec.md#19-用户视角复审配置一次之后能否长期使用) 指出：安全拦截冲突不足以证明多客户端低维护可用性；适配范围、必需正文整理、依赖与配置版本稳定、完整使用旅程也必须验收。本次补充仍仅修改文档，不能用于证明新增能力已经实现。

## 1. 总结

**不能通过“保留预设作用，同时满足本次客户端格式”的核心验收。**

这不是“所有基础功能完全不能运行”的结论。已有世界书匹配、预设拼装、文本恢复等基础测试通过。问题集中在模块交界：工具流程推断、客户端约束、失败回退、预览与真实状态、审计序列化。

建议：保留可验证的基础能力，替换工具编排和响应收尾核心，修正世界书锚点与状态，拆开运行计划和审计摘要。不建议全仓库推倒，也不建议继续靠新增开关掩盖边界矛盾。

当前代码不能证明线上全部请求都受影响。本轮没有读取线上新日志、复现 VPS 延迟或进行浏览器验收，不以本地静态结论代替线上证据。

## 2. 这段变更实际做了什么

只能从提交和文件重建工作范围，不能从共享 Git 作者推断哪个 AI 写了哪一行。

| 功能／链路 | 主要提交／文件 | 当前判断 |
| --- | --- | --- |
| 每条世界书独立位置、条目总览、位置预览 | `be26933eb`；`service/worldbook.go`、`worldbook-*-overview/fields/preview` | 基础可保留，排序与预览口径不统一 |
| 工具协议协调、辅助／最终阶段、工具集合投影 | `085952033`、`de95fdd19`、`6445e70a3`；`preset_protocol.go` | 主要设计错误所在 |
| 显式正文／JSON 字段恢复，去除部分自动猜字段路径 | `712c847ed`、`5faeef4f7`、`6445e70a3` | 显式转换方向合理，缺少最终客户端约束 |
| 请求原始快照、基础计划、解析后计划、重试指纹 | `b7cba658c`；`adaptation_plan.go`、`controller/relay.go` | 隔离思路可保留，计划未成为执行层唯一依据 |
| 分阶段错误策略、内部工具可见性、响应处理 | `b7cba658c`；`response_text_filter.go`、DTO 配置 | 有配置和部分保护，但执行存在反例 |
| 世界书高风险导入停用、确认启用与批量确认 | `6445e70a3`；`worldbook_import.go`、前端 worldbook 模块 | 有价值，但确认状态和详情入口不一致 |
| 能力探测 API、能力报告、适配概览 | `59f2279e8`、`315839842`；`preset_capability.go`、前端报告 | 观察与配置混淆，模型范围及报告过期处理不完整 |
| 实时适配诊断、响应后刷新审计快照 | `59f2279e8`、`315839842`；`adaptation_diagnostic.go`、`body_audit.go` | 状态统计和持久化边界有缺陷 |
| 有界能力缓存、计划元数据缓存 | `59f2279e8`；能力／计划服务 | 无跨请求正文对象复用证据；收益未测，非保存慢根因结论 |
| 配套测试、翻译、构建版本号 | 测试文件、locales、CI 工作流 | 测试有覆盖，但有把错误假设写进 fixture 的情况 |

继承基础另行区分：

- 世界书在预设前触发、预设前后保留世界书的基础修复，来自更早的 `61dce91a1`、`d2eff1af1` 等。
- 脚本沙箱、宏、审计阅读器、远程脚本准备等大部分能力早已存在；本轮审查它们与新增适配层的接点，不宣称重新验收了所有底层实现。
- 渠道保存延迟、全部下拉框改造、VPS 部署状态不在上述差异中全部体现，不能用这 54 个文件证明它们已经解决。

## 3. 现有执行链路

请求侧的主要调用：

```text
controller/relay.go
  原始请求副本
  → applySelectedRequestTextRegex
  → BuildBasePlan
  → compileSelectedSillyTavernPreset
      → CompileWorldbookFrom
          → MatchWorldbook
          → CompileSillyTavernPreset
              骨架／插入／合并
              → preparePresetScripts
              → buildPresetProtocolPlan
              → validatePresetClientToolContractWithMode
              → protocolPlan.applyRequest
  → AttachAdaptationPlan / Diagnostic
  → 上游转换与请求
```

响应侧的主要调用：

```text
ResponseTextFilterWriter.Finish
  → processResponse
      → restorePresetToolText
      → scriptResponse.restoreTools
      → filterJSON / filterSSE
          → processText
          → 正则
  → 错误策略／写回
  → RecordAdaptationWriteback
  → RefreshBodyAuditAdaptationSnapshot
```

关键事实：最终写回前没有统一执行所有客户端要求；`resolvedPlan` 被赋值，但没有被响应执行层读取；部分成功状态在实际验证之前写入。

## 4. 证据等级

- **实测**：此前同一基线的六个最小反例调用生产函数或完整 Writer，已经失败。记录在本地 `output/relay-adaptation-review-2026-11-09.md`，复现源归档为 `output/adaptation-acceptance-review_test.go.txt`；未留在业务测试包中。
- **代码确认**：能从生产调用链直接确认逻辑，不冒称已完成浏览器／线上复现。
- **待验证**：需要真实浏览器、数据库、VPS 或完整端点回放，尚不能下结果结论。

以下 P1 表示应阻止把新适配能力认定为可靠交付；P2 表示需要修正的功能／诊断问题。不等同于所有未启用适配的渠道都不可用。

## 5. 优先问题

### R1 / P1 / 实测：客户端禁止工具仍可激活强制工具阶段

条件：客户端声明一个工具但 `tool_choice=none`；预设增加工具并把选择改成 required。

`inferClientFinalTool` 通过编译后的 required 推断客户端工具为最终工具，激活计划；之后模式 1 放宽调用选择校验，再由 `applyRequest` 改写工具集合。

- 位置：`service/preset_protocol.go`，`inferClientFinalTool`、`applyRequest`。
- 处理建议：不要根据预设选择反推客户端要求；移除默认辅助／最终阶段编排。

### R2 / P1 / 实测：渠道文字默认覆盖明确 JSON 要求

`resolvePresetProtocolContract` 在处理客户端 json_object 之前返回渠道显式 text。

- 位置：`service/preset_protocol.go:592` 附近。
- 后果：同一渠道平常文字请求可用，偶发 JSON 请求被改成文字，直接违背核心需求。
- 处理建议：客户端明确要求优先，渠道设置只补空缺。

### R3 / P1 / 实测：恢复后的 JSON 被接收正则破坏仍成功写回

内部工具的 payload 为 `{"body":"hello"}`，恢复目标为 JSON；正则提取 body 后，Writer 成功写回 `hello`。

- 位置：`service/response_text_filter.go`，`processResponse`、`transform`、`Finish`。
- 处理建议：区分文本与结构化数据，保护结构，最终写回前重新验证。

### R4 / P1 / 实测：输出脚本失败的 preserve 回退暴露内部工具

内部调用本来已可恢复成文字；输出脚本报错；输出阶段策略为 preserve。Writer 回退到链路最初 body，重新发送内部工具。

- 位置：`service/response_text_filter.go:333` 附近。
- 注意：该反例明确配置 preserve，不表示所有默认配置均泄漏。
- 处理建议：保留失败阶段之前的有效结果，而不是上游最初结果；所有出口都执行内部工具和客户端要求检查。

### R5 / P1 / 实测：计划要求客户端工具，未计划的内部工具仍可返回

计划要求 client_reply，响应却是 internal_reply。`restoreTools` 在工具契约分支直接返回，成功路径没有使用可见工具集合拦截它。

- 位置：`service/preset_script_response.go:15`、`preset_protocol.go` 可见集合函数。
- 处理建议：可见性必须成为写回校验，不只是审计字段和异常分支条件。

### R6 / P2 / 实测：JSON 对象要求接受数组

`presetProtocolJSONArgument` 验证 JSON 语法，但不验证对象类型；格式化脚本路径也存在相同缺口。

- 位置：`service/preset_protocol.go:788`、`:827` 附近。
- 处理建议：区分 JSON 语法、对象类型和 Schema 合法性。

### A1 / P1 / 代码确认：运行计划整体进入审计配置快照

- `PresetCompileTrace.Plan` 可序列化；
- `AdaptationResolvedPlan.Base` 下有 `Original`、`Matched`、`Profile`，这些导出字段没有排除 JSON 序列化；
- `buildAttemptConfigSnapshot` 直接把 `info.PresetAudit` 放入 payload；
- `GetOrCreateAuditConfigSnapshot` 规范化并存储该 JSON，没有在这条路径移除请求正文。

因此配置快照会包含原始／加工消息及完整适配配置，而不只是文档承诺的摘要。响应后刷新又生成一份新的快照。

- 更早的 `WorldbookPreview.Text` 也会随 trace 序列化；本次新增 Plan 进一步带入完整请求和配置，不能把全部正文混入问题都归因于这次新增。
- 位置：`service/sillytavern_preset.go:52`、`service/adaptation_plan.go:61` 附近、`service/body_audit.go:529`、`model/audit_trace.go:439`。
- 后果：正文重复存储、快照变大、元数据边界失效，敏感聊天内容进入非预期字段。不是已经证明公开泄露或绕过鉴权。
- 处理建议：单独定义持久化审计 DTO，只允许安全摘要字段；已有数据是否需清理另行确认，不自动删除。

### A2 / P1 / 代码确认：错误没有阶段归属，回退可能抛弃世界书或绕过 fail

`CompileWorldbookFrom` 同时执行世界书匹配与预设编译。其任意错误都可能被 controller 当作世界书错误：去掉世界书重新编译。之后又用三个阶段的 request_passthrough 的 OR 决定回退。

- 位置：`controller/relay.go:498-511` 的控制分支。
- 后果：脚本错误可能导致预设再次执行；一阶段的默认回退可能压过另一阶段显式 fail。
- 衍生风险：回退请求已经过发送规则，却仍保留相关响应配置，不能称为完整原样中转。
- 处理建议：携带明确阶段的错误；只读该阶段策略，回退成对调整请求与响应处理上下文。

### A3 / P1 / 设计与代码确认：辅助工具没有执行者

旧规格一方面要求预设辅助工具交给客户端执行，另一方面承诺客户端不会收到示例中的 context。两者无法同时成立。

当前默认将其他预设工具列入辅助集合，第一阶段只暴露这些工具。客户端没有声明这些工具，并不能保证执行它们。

- 位置：旧规格第 7.6／8.1 节；`preset_protocol.go` 默认 auxiliary 构造、`phaseForMessages`、`applyRequest`。
- 处理建议：退役这套默认业务流程；工具调用不代表最终交付阶段，网关只处理可证明的协议边界。

### A4 / P2 / 代码确认：请求计划是摘要旁路，不是唯一执行依据

`BasePlan.Contract` 与协议层各自分类。`resolvedPlan` 当前只有定义和赋值，响应阶段未读取它。

此外 `ClassifyContract` 把存在 tools 的请求直接归为 tool，不能表达 tools 和 response_format 并存的要求。

- 位置：`service/adaptation_plan.go`，`ClassifyContract`、`AttachAdaptationPlan`；`service/preset_script_adapter.go:47`。
- 处理建议：保留快照与重试保护，将客户端约束作为真实执行输入；不要增加更多只用于日志的“冻结”字段。

### A5 / P2 / 代码确认：客户端 response_format 可被脚本修改，现有保护未覆盖

请求脚本允许修改 response_format；`validatePresetClientToolContractWithMode` 主要保护工具和历史，不保护输出格式。

- 位置：`service/preset_script_adapter.go:421`、`:466`；同文件客户端工具保护函数。
- 处理建议：在统一客户端要求中保护输出格式，不能把它当普通采样参数任意覆盖。

## 6. 世界书专项

### W1 / P2 / 代码确认：诊断把实际 included 统计为 skipped

匹配器产生 `status=included`；关闭或无效条目使用 `status=skipped` 配合 reason。诊断却按 matched、disabled、invalid 统计 status，导致真实命中数及关闭／无效计数不正确。

- 位置：`service/worldbook.go:94-150`、`service/adaptation_diagnostic.go:155-205`。
- 现有诊断测试手造 `status=matched`，没有接入真实匹配结果，因此掩盖问题。
- 影响：用户可能看到“没有触发”，即便内容已插入。不能据此断言此前线上未触发都由此引起。

### W2 / P2 / 代码确认：全局编号／拼接文本不等于最终插入顺序

例如两个条目：

- A：低优先级，插入末尾；
- B：高优先级，插入系统背景区。

匹配器给出 A #1、B #2，preview.text 为 A 后 B；插入器实际按位置放置 B 在前、A 在后。这是可直接从代码推演的确定案例，不是本轮另加测试后的实测声明。

- 位置：`worldbook.go` 的 orderedIncluded 与 `injectWorldbookGroups`。
- 处理建议：预算顺序、区块内优先级、最终消息顺序分开；预览按实际位置排列。

### W3 / P2 / 代码确认：预览的文本和位置属于不同阶段

preview.text 来自原始条目；宏随后展开，脚本还可改写或重排消息。Placement 在后处理／脚本之前计算；界面仍展示“将加入的背景”“消息位置”，没有清楚区分阶段。

- 位置：`service/worldbook.go` 的 Text 赋值、`injectWorldbookGroups`；`service/sillytavern_preset.go:472-546`；`worldbook-preview.tsx`。
- 处理建议：显示“匹配原文／插入时位置／处理后的请求”，不以一个 preview 对象声称全是最终结果。
- 注：脚本重排本身是允许的能力，不能把所有后处理变化都算作插入器错误。

### W4 / P2 / 代码确认：预览入口没有实发的完整上下文

预览 API 接收 config、request、preset，但没有发送规则；传给 CompileWorldbook 的 SillyTavernContext 为空。实发则会执行发送规则、解析显式上下文。普通文本预览还没有实际模型名。

- 位置：`controller/channel_worldbook.go:PreviewChannelWorldbook`、`service/worldbook.go:PreviewWorldbook`、`worldbook-preview.tsx`。
- 后果：发送规则改变触发词、显式 chat_history 替代 messages、预设限制模型时，预览和实发可能不同。
- 处理建议：共用请求准备入口，注明样本模型和有效配置，不只共用 MatchWorldbook 就声称一致。

### W5 / 设计待修订：深度和最后用户的锚点不明确

当前 resolveWorldbookPlacement 在组装后的全部消息上定位。尾部预设提示会计入深度；预设生成的 user 也可能成为 before_last_user 的目标。

- 位置：`service/worldbook.go:488`、`:576` 附近。
- 这符合旧文档“消息序列尾部”的一种解释，不能直接说违反现有明确协议；但与界面“聊天深度／最新用户输入”的理解不一致。
- 建议：采用稳定聊天锚点并版本化迁移，或明确保留“组装后深度”。新设计推荐前者，不静默改变旧配置。

### W6 / P2 / 代码确认：详情开关绕过总览的风险确认

总览启用时检查 risk 并弹确认；详情开关只拦截 issues，直接设置 enabled。后端匹配同样只看 enabled 与 issues。

- 位置：`worldbook-entry-overview.tsx` 开关回调、`worldbook-entry-fields.tsx` 开关回调、`worldbook.go` 候选筛选。
- 后果：同一条目从两个入口启用，确认语义不同。
- 处理建议：统一确认逻辑，服务端持久化确认状态。

### W7 / P2 / 代码确认：确认后删除简化原因

`worldbookConfirmEntry` 删除 risk 项，源 JSON 仍在，但常规界面不再完整显示已接受的差异。

- 位置：`web/src/features/channels/lib/worldbook.ts:109`。
- 处理建议：单独保存已确认状态，保留简化说明；不是数据全部丢失。

### W8 / 设计边界：同文不同位置被全局内容去重

选择阶段以 entry.Content 去重，发生在解析各条目的插入位置之前。相同正文但有意放在不同锚点，只会留下一个。

- 位置：`service/worldbook.go` 的 seenContent。
- 这是旧去重规则与新增 per-entry 位置的语义冲突，不应未经确认直接改掉所有去重行为。
- 建议：去重键至少考虑目标位置，并设计旧配置迁移。

### W9 / 边界说明：不是完整的酒馆位置系统

当前支持 inherit、system_prelude、before_last_user、at_depth 和条目跟随全局。原生 position/depth/role 的导入仍可能归为风险，inherit 选择先遇到的可用世界书 marker。

- 尚未提供独立定义前后、AN、EM、outlet 等完整定位。
- 工具边界回退只识别 role=tool，不覆盖所有 legacy function 情况。
- 不能把主动裁剪的范围写成“全部兼容”，也不应为了这些缺口把现有匹配器全部删除。

## 7. 报告、界面、诊断与性能

### U1 / P2：能力报告把配置存在当成实际探测

默认模型为 capability-probe。预设有模型白名单时编译器可能直接跳过，报告却仍添加 prompt assembly、script runtime 等配置能力。

- 位置：`service/preset_capability.go:107-146`。
- 修订：明确“已配置／样本已运行／未探测”，选择适用模型或显示跳过。

### U2 / P2：草稿变化后能力报告没有失效绑定

报告保存在本地 state，config 变化没有像世界书预览那样比对快照 key；异步结果也没有绑定发起时的配置。

- 位置：`preset-capability-report.tsx`，`PresetCapabilityReportSettings`。
- 修订：配置版本或摘要绑定结果；变化后标过期，丢弃过期异步回包。浏览器视觉和竞态尚未实测。

### U3 / P2：概览和失败策略展示超过真实保证

概览只凭渠道配置显示“最终回复”；没有当前客户端请求。前端恢复失败默认显示 safe_raw，而服务端对启用脚本的流水线默认 fail。

- 位置：`preset-capability-report.tsx:127`；`preset-script-settings.tsx:634` 附近；`response_text_filter.go:responseFailurePolicy`。
- 修订：展示有效默认与配置意图，不承诺尚未运行的结果。

### U4 / P2：诊断提前报成功，且部分失败摘要丢失

`processResponse` 在实际解析与约束校验前记录 response_validation completed；输出处理未启用也可能记录 completed。

前端 `diagnosticSummary` 只按 stage/status 取固定文案，不使用具体 summary；没有映射的失败组合默认显示 Stage completed，原始 summary 也未单独展示。

- 位置：`service/response_text_filter.go:processResponse`；`usage-logs/.../diagnostics.tsx:diagnosticSummary`。
- 修订：事件从真实执行点产生；未知状态不默认成功；给出可读原因及可折叠技术说明。

### PERF1 / 优化项：缓存成本未证明值得

请求计划元数据缓存为简单分类结果计算整段请求摘要，BasePlan 创建还进行深拷贝和多次序列化；收益未测量。

能力缓存有 TTL 与容量限制、返回深拷贝，这是正面设计。但样本结果依赖请求和脚本状态，不等于永久兼容证明。

- 修订：优先缓存稳定编译产物；保留边界，测量后决定是否保留请求分类缓存。
- 不认定它就是渠道保存慢的根因。

### RETRY1 / 待验证：重试冻结对象是否被后续转换修改

请求重试复用 initialCompiledRequest 和初始审计；指纹阻止不同出站配置的跨渠道重试，这一保护有价值。

但“复用同一指针”不是不可变证明，需核对各上游转换是否改写请求对象，以及真实 attempt 诊断是否准确分离。

本轮控制器相关测试通过，不等于所有 provider 和参数覆盖组合都验证。

## 8. 方案与代码质量结论

可保留：

- 请求副本隔离和工具历史保护的大部分基础；
- 世界书有限匹配、预算与基础插入；
- 复用原生表格、对话框和开关的总览；
- 显式字段转换与 JSON/SSE 基础解析；
- 容量限制、脚本截止时间、重试语义指纹。

需要替换或重构：

- 默认辅助／最终阶段编排；
- 多套客户端要求分类与真实执行脱节；
- 回退到最初 body 的响应收尾；
- 可见工具、JSON 要求不进入成功出口校验；
- 把运行计划当审计 DTO；
- 配置意图、样本探测、实际运行共用“完成”状态。

测试质量问题不是数量少，而是缺少对真实接点的反例。例如世界书诊断 fixture 使用生产者根本不产生的 matched，工具测试把预设 required 推断客户端 final 当作正确行为。

不要以“文件长／作者是 AI／使用了抽象”作为删除理由；以职责是否成立、边界是否被保护、结果是否可验证决定。

## 9. 本轮验证与未覆盖范围

本轮运行通过：

```text
go test ./service -run 'Worldbook|PresetCapability|Adaptation|PresetProtocol|ResponseWriterStage|ResponseInternal' -count=1
go test ./controller -run 'AdaptationRetry|SelectedChannelResponseRegex|Worldbook' -count=1
```

使用仓库旁的 Go 工具链，调用声明版本的 Go。测试只验证已有覆盖，不抵消 R1–R6 的同基线反例。

没有完成：

- 新设计的实现及回归验收；
- 前端浏览器测试、样式和交互验收；
- 当前 VPS 日志关联与真实客户端回放；
- 渠道保存的端到端耗时测量；
- 全部原生协议／provider／数据库组合；
- 远程脚本抓取与整个脚本运行时的重新安全验收。

旧的本地报告保留为历史证据。本文件与修订后的设计共同构成下一轮的输入，不授权自动开始开发。

## 10. 2026-11-09 后续授权实施记录

用户随后授权按最终设计实施、验证及推送。以上审查结论保留为实施前基线，以下为本次实际交付与验证边界。

已实施：统一协议要求与加工范围判定；显式回复来源 v2 与受限旧配置迁移；客户端工具、历史和输出格式保护；R/P/F/V 顺序及完整阶段回退；必需提取不可绕过；JSON 字段白名单；SSE 完整收集、16 个缓冲准入名额、8 MiB 单请求边界及取消释放；冻结完整配置和跨渠道重试语义检查；发送规则来源去重；世界书 v2 稳定历史锚点、完整工具块、顺序、展开后预算、风险确认与同链路预览；有界诊断；世界书与回复来源界面；七类请求静态检查；保存前检查其他窗口的配置变更。

实际运行通过：

- `go test ./service ./controller -count=1 -json`：两个包全量通过。
- 根模块 `go build ./...` 及 relaykit 内 `GOWORK=off go build ./...`：通过。
- 前端相关完整回归：47 个文件、530 项测试通过；另一次针对预设选择、渠道配置和审计诊断的回归为 3 个文件、128 项通过。
- `bun run typecheck`：1,483 个文件、2 个项目无类型错误。
- 31 个变更前端文件的定向格式化/lint、生产构建及 `git diff --check`：通过。
- Playwright 使用真实 Chrome 和隔离管理 API 数据检查了世界书总览、条目位置/深度、390px 手机布局、回复来源、七类请求检查及切换第二渠道后世界书隔离；截图存于本地 output/playwright。管理 API 为测试数据，不能据此证明真实模型效果。

尚未通过的线上放行条件：

- 当前环境 SSH 连接 VPS `23.238.7.190:22222` 超时，未读取线上全部配置、未执行线上只读迁移清单，也未实测真实客户端回放、渠道保存耗时和部署容量。自动部署现有备份机制不能替代配置级迁移检查。
- 保存冲突检测是前端保存前重新读取并比较配置；不是数据库级条件更新，检查与写入之间仍有短暂竞争窗口。本次没有修改数据库模式或事务。
- 并发上限是实现边界，尚未按 VPS 资源测定最合适的容量；准入在上游调用前执行，但不能据此宣称整个宿主脚本实例已经全局限流。
- 静态请求检查明确标为模型效果未验证；备用渠道语义判断已落在执行链中，界面尚未提供跨渠道可用备用的总览提示。
- 已有远程依赖准备流程继续沿用，本次没有重新验收所有外部脚本或所有 provider 的端到端行为。

因此可以确认上述实现通过本地回归，不能将本记录解读为第 19 节所有线上使用旅程均已通过，或任意第三方预设、模型、客户端都已获得完整兼容保证。
