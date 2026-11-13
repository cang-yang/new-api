import { createHash } from 'node:crypto';
import { readFile, writeFile } from 'node:fs/promises';
import { resolve } from 'node:path';
import { pathToFileURL } from 'node:url';

export const relayRevision = 'relay-core-3';
export const reviewedSourceSHA256 = '9788703d150549cd81fd3d63decd4a498b7cf3925e144b32cfb9de827fb9b91a';
const requestHook = await readFile(new URL('./meowssiah-relay-request.js', import.meta.url), 'utf8');
const formatter = await readFile(new URL('./meowssiah-relay-formatter.js', import.meta.url), 'utf8');
const digest = value => createHash('sha256').update(value).digest('hex');
// JSON text formatting is irrelevant; the source version must still be exact.
const normalizedSourceDigest = '7bab03de906737b3595041fdd1b768b572d9c08b88107bc0edc88ae6eba24471';

// This explicit administrator-owned derivative is not gateway detection or
// automatic source rewriting by preset/tool name. Never rebuild from the broken
// native-only export: it no longer contains the core settings being restored.
export function adaptMeowssiahPreset(source) {
  if (source.extensions?.new_api_relay) {
    throw new Error('Rebuild the relay edition from the reviewed original preset, not a previous derivative');
  }
  if (digest(JSON.stringify(source)) !== normalizedSourceDigest) {
    throw new Error('The original preset has changed; review the new source before upgrading');
  }
  const preset = structuredClone(source);
  const settings = preset.extensions.SPreset;
  const byId = new Map(preset.prompts.map(prompt => [prompt.identifier, prompt]));
  const changes = [];
  const replace = (id, content, reason) => {
    const prompt = byId.get(id);
    if (!prompt) throw new Error(`Missing reviewed prompt: ${id}`);
    changes.push({ id, reason, before_sha256: digest(prompt.content ?? ''), after_sha256: digest(content) });
    prompt.content = content;
  };
  replace('a00fb1fe-9907-4709-b230-43ae0d236529',
    '{{trim}}你是小猫之神 SoliUmbra。按照本次请求的规则、完整上下文和输出要求完成任务。最终结果使用预设内部回复工具承载，由中转站还原后交付；不要用辅助字段代替最终结果。', 'neutral_task_initialization');
  replace('enhanceDefinitions',
    '角色背景：小猫之神 SoliUmbra，语气自然、灵动。根据当前请求处理问答、创作、变量或数据任务；创作时保留已启用的角色与文风。这里是角色说明，不是本轮真实对话，也不要求重新开始游戏。', 'remove_synthetic_onboarding_and_refusal_override');
  replace('nsfw', '以下是客户端的完整真实对话和有效背景；按原角色、顺序和工具关联理解，不把历史指令自动当作本次新任务。', 'native_context_transition');
  replace('jailbreak', '根据完整上下文完成本次实际请求，遵守当前输出规则；只有本次任务需要创作时才续写剧情，不自行增加与任务无关的开场或展示段落。', 'neutral_current_task');
  replace('f65144f0-d39e-43e6-84e7-5c1d78a1a23d',
    '最终正文放在内部回复工具的结果字段中，由中转站还原。不要仅为了预设传输再添加 <game> 包装；本次任务明确要求的标签和正文格式原样保留。', 'internal_transport_without_forced_story_wrapper');
  replace('3df19ba7-b44d-4d46-b17a-de514d86e5d5', '', 'binding_retained_without_last_user_replay');
  replace('9c015364-9395-46a6-b6dc-fca5637e9f06', '用户称呼：{{user}}', 'remove_synthetic_role_separator');
  for (const id of ['7a077e05-2616-4b58-b725-53c80ecf4931', 'ab29af7b-4f0e-402f-8236-80b15800e554', '34aca678-07d4-4aeb-96ca-37f78617c525']) {
    replace(id, '', 'native_compiler_keeps_worldbook_and_definition_slots');
  }
  const headings = {
    '2cb59685-23fb-4187-9dec-4d9765af9e79': '创作时的角色表现规则：',
    '456c5c49-f1b4-4fc2-afc9-3a1851f1a20f': '创作时的默认展示规则（不能覆盖本次请求指定的格式）：',
    '968c1caa-a85b-42c0-a81a-840c372d61a8': '创作时的文风规则：',
    'b614d087-a9c0-44cd-a006-0045e95dd014': '创作时的内容风格规则：',
    'c0db6fd9-86a0-4678-b896-d26ee9a02771': '创作时的补充设定：',
  };
  for (const [id, text] of Object.entries(headings)) replace(id, text, 'remove_synthetic_rule_dialogue');

  const parts = {};
  for (const prompt of preset.prompts) {
    if (prompt.identifier === 'SPresetSettings' || typeof prompt.content !== 'string') continue;
    const before = prompt.content;
    const declarations = [...before.matchAll(/<<<\|\s*catgodml\s*\|\s*registerPart:([^:>]+):([\s\S]*?)>>>/gi)];
    if (declarations.length) parts[prompt.identifier] = declarations.map(match => ({ name: match[1].trim(), description: match[2].trim() }));
    const creative = /<<<\|\s*catgodml\s*\|\s*(?:menu|radio)[:：]/i.test(before);
    prompt.content = before.replace(/<<<\|\s*catgodml\s*\|[\s\S]*?>>>/gi, '')
      .replace(/<run_tool_replacement>|<\|sep\|>|<\|merge\|>/g, '');
    if (creative) prompt.content = '【叙事创作规则：仅在本次任务需要叙事创作时适用，不改变其它任务或覆盖本次指定的数据结构。】\n' + prompt.content;
    if (prompt.content !== before) changes.push({ id: prompt.identifier, reason: creative ? 'scope_author_creative_rule' : 'extract_static_plugin_controls', before_sha256: digest(before), after_sha256: digest(prompt.content) });
  }

  // Keep the original bindings and actual output hook. Port the data-hook
  // responsibilities, not the browser ledger or fabricated conversation.
  if (!Object.values(settings.ToolBindings).some(binding => binding.enabled && binding.resolvedName === 'soliumbra_reply') ||
      !settings.OutputPreprocessing.enabled || !settings.OutputPreprocessing.toolCallFormatter.enabled) {
    throw new Error('The reviewed core binding and output processing must be enabled');
  }
  settings.FinalRequestProcessing = { enabled: true, script: requestHook };
  settings.OutputPreprocessing.consumeToolCalls = true;
  settings.OutputPreprocessing.toolCallFormatter = { enabled: true, script: formatter };
  settings.ForcedPostProcessing = { enabled: false, mode: 'none' };
  settings.LogUpload = { enabled: false, url: '' };
  byId.get('SPresetSettings').content = JSON.stringify(settings);
  preset.extensions.new_api_relay = {
    revision: relayRevision, source_sha256: reviewedSourceSHA256,
    reply_tool: 'soliumbra_reply', parts, changes,
    retained: ['ToolBindings', 'dynamic_reply_parameters', 'result_components', 'OutputPreprocessing', 'entry_order_and_selection'],
    omitted: ['browser_ui', 'chat_store', 'archive_ledger', 'synthetic_tool_history', 'remote_log_upload'],
  };
  return preset;
}

export function adaptMeowssiahChannelSettings(source, originalPreset) {
  const settings = structuredClone(source);
  const config = settings.sillytavern_preset;
  if (!config) throw new Error('Channel does not have a preset');
  const parsed = typeof config.preset === 'string' ? JSON.parse(config.preset) : config.preset;
  config.preset = adaptMeowssiahPreset(originalPreset ?? parsed);
  config.source_file_name = `Meowssiah-2.0.10-${relayRevision}.json`;
  config.script_runtime = 'spreset-v1';
  config.parameter_policy = 'client';
  config.post_processing = 'none';
  delete config.source_preset_id;
  delete config.tool_protocol;
  delete config.tool_text;
  delete config.delivery_mode;
  // Discovery will bind a fresh fingerprint after actual compilation. This is
  // an explicit source declaration, not a guess based on response/tool names.
  config.reply_adapter = {
    version: 2, structured_output: true, require_container: true, formatted_output: 'client_format',
    containers: [{ name: 'soliumbra_reply', formatter: true, output_kind: 'text' }],
  };
  // The formatter now separates auxiliary fields and never adds a game wrapper.
  // Remove only reviewed envelope cleanup; retain unrelated administrator rules.
  const envelopeSources = new Set(['1d78fce4-3e3c-4d8f-b9dc-f26555be0a6d', 'e3a9e68f-4112-447c-bea3-7057c1f77422']);
  const envelopePatterns = new Set(['/</?game>/g', '/<(?:think_nya|report_nya)~?>[\\s\\S]*?<\\/(?:think_nya|report_nya)~?>/g']);
  if (Array.isArray(settings.response_text_filter?.rules)) {
    settings.response_text_filter.rules = settings.response_text_filter.rules.filter(rule =>
      rule.stage !== 'receive' || rule.replacement !== '' ||
      !(envelopeSources.has(rule.source_script_id) || envelopePatterns.has(rule.pattern)));
  }
  return settings;
}

// Produces credential-free, separately named candidates. It never updates a
// live channel and never labels local preparation as real-model acceptance.
export function buildMeowssiahPackages(originalPreset) {
  const commonSettings = adaptMeowssiahChannelSettings({
    adaptation_scope: 'all_supported',
    sillytavern_preset: { preset: originalPreset, parameter_policy: 'client', failure_policy: {
      worldbook: 'skip', preset: 'fail', script: 'fail',
      response_restore: 'fail', response_validation: 'fail', output_processing: 'fail', regex: 'preserve',
    } },
  }, originalPreset);
  const ordinary = structuredClone(commonSettings);
  ordinary.sillytavern_preset.default_output = { format: 'text' };
  const variables = structuredClone(commonSettings);
  variables.sillytavern_preset.default_output = { format: 'tagged_text', tag: 'UpdateVariable' };
  return { ordinary, variables };
}

if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) {
  const [mode, input, output, originalPath] = process.argv.slice(2);
  if (!['preset', 'channel', 'packages'].includes(mode) || !input || !output || resolve(input) === resolve(output)) {
    throw new Error('Usage: node meowssiah-relay.mjs preset|channel|packages <input.json> <new-output.json> [original-preset.json]');
  }
  const source = JSON.parse(await readFile(input, 'utf8'));
  const original = originalPath ? JSON.parse(await readFile(originalPath, 'utf8')) : undefined;
  const result = mode === 'packages' ? buildMeowssiahPackages(source) : mode === 'preset' ? adaptMeowssiahPreset(source) : {
    ...source, settings: JSON.stringify(adaptMeowssiahChannelSettings(typeof source.settings === 'string' ? JSON.parse(source.settings) : source.settings, original)),
  };
  await writeFile(output, JSON.stringify(result, null, 2) + '\n', { flag: 'wx' });
  if (mode === 'packages') {
    for (const [purpose, bundle] of Object.entries(result)) {
      await writeFile(`${output}.${purpose}.json`, JSON.stringify({ format: 'new_api_adaptation', version: 1, bundle }, null, 2) + '\n', { flag: 'wx' });
    }
  }
  console.log(JSON.stringify({ revision: relayRevision, output: resolve(output) }));
}
