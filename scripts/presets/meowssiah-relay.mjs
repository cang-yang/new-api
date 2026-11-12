import { readFile, writeFile } from 'node:fs/promises';
import { resolve } from 'node:path';
import { pathToFileURL } from 'node:url';

export const relayRevision = 'relay-native-1';
const requestHook = await readFile(new URL('./meowssiah-relay-request.js', import.meta.url), 'utf8');

// This is an explicit derivative of the administrator's preset, not gateway
// detection by preset/tool name. The original file remains the source/backup.
export function adaptMeowssiahPreset(source) {
  const preset = structuredClone(source);
  const settings = preset.extensions?.SPreset;
  if (!settings || !Array.isArray(preset.prompts) || !Array.isArray(preset.prompt_order)) {
    throw new Error('Expected a Meowssiah preset with SPreset settings and prompt order');
  }
  if (preset.extensions.new_api_relay?.revision === relayRevision) return preset;
  const byId = new Map(preset.prompts.map(prompt => [prompt.identifier, prompt]));
  const replace = (id, content) => {
    const prompt = byId.get(id);
    if (!prompt) throw new Error(`Missing reviewed prompt: ${id}`);
    prompt.content = content;
  };
  replace('a00fb1fe-9907-4709-b230-43ae0d236529',
    '{{trim}}你是小猫之神 SoliUmbra。按照本次请求的规则、上下文和输出要求完成任务。创作时保留已启用的角色与文风；其他任务按实际要求回答，不强制剧情、工具包装或思考标签。');
  replace('nsfw', '以下保留真实客户端的历史消息与当前请求，请按原有角色、顺序和工具关联理解，不将其改写成模拟剧情。');
  replace('jailbreak', '根据上述上下文，按照本次请求的规则和格式输出结果。后续预设条目只提供适用任务的补充规则，不改变本次任务。');
  replace('3df19ba7-b44d-4d46-b17a-de514d86e5d5', '');
  const introduction = byId.get('enhanceDefinitions');
  const oldIntroduction = '本喵给他们的聊天偷偷加个`soliumbra_reply`工具，然后在content里面先用 `<think_nya>...</think_nya>` 好好想想怎么回复。';
  if (typeof introduction?.content !== 'string' || introduction.content.split(oldIntroduction).length !== 2) {
    throw new Error('The reviewed persona introduction has changed; review before upgrading');
  }
  introduction.content = introduction.content.replace(
    oldIntroduction,
    '本喵会按照人类本次请求的规则完成任务，使用客户端要求的格式回复。'
  );
  introduction.content = '以下是角色与语气的背景示例，不是真实对话，不代表本轮要开始游戏，也不要求输出示例中的工具或思考标签。\n' + introduction.content;

  const structural = new Set([
    'main', 'version_info', 'SPresetSettings', 'enhanceDefinitions',
    'a00fb1fe-9907-4709-b230-43ae0d236529', 'nsfw', 'jailbreak',
    '3df19ba7-b44d-4d46-b17a-de514d86e5d5', '9c015364-9395-46a6-b6dc-fca5637e9f06',
    '7a077e05-2616-4b58-b725-53c80ecf4931', 'ab29af7b-4f0e-402f-8236-80b15800e554',
    '34aca678-07d4-4aeb-96ca-37f78617c525',
  ]);
  for (const prompt of preset.prompts) {
    if (prompt.marker || structural.has(prompt.identifier) || !prompt.content) continue;
    // Preserve the author's actual rules, including disabled alternatives.
    prompt.content = '【可选创作规则：仅适用于本轮要求的叙事创作；不得改变其他任务或覆盖客户端指定的输出格式。】\n' + prompt.content;
  }
  // These are plugin control markers, not world or client text. Only mutate
  // preset-created strings; the request hook leaves all client strings intact.
  for (const prompt of preset.prompts) {
    if (prompt.identifier === 'SPresetSettings' || typeof prompt.content !== 'string') continue;
    prompt.content = prompt.content
      .replace(/<<<\|\s*catgodml\s*\|[^\n]*?>>>?/gi, '')
      .replace(/<run_tool_replacement>|<\|sep\|>|<\|merge\|>/g, '');
  }
  settings.ChatSquash.enabled = false;
  settings.ChatSquash.conditional_enabled = false;
  settings.ChatSquash.squashed_post_script_enable = false;
  settings.ToolBindings = {};
  settings.FinalRequestProcessing = { enabled: true, script: requestHook };
  settings.OutputPreprocessing = {
    enabled: true,
    script: '({ buffer, state = {} }) => ({ output: buffer, hold: "", state: { ...state, accepted: true } })',
    consumeToolCalls: false,
    toolCallFormatter: { enabled: false, script: '' },
  };
  settings.ForcedPostProcessing = { enabled: false, mode: 'none' };
  settings.LogUpload = { enabled: false, url: '' };
  const embedded = byId.get('SPresetSettings');
  if (embedded) embedded.content = JSON.stringify(settings);
  preset.extensions.new_api_relay = { revision: relayRevision, transport: 'native', source: 'Meowssiah' };
  return preset;
}

export function adaptMeowssiahChannelSettings(source) {
  const settings = structuredClone(source);
  const config = settings.sillytavern_preset;
  if (!config) throw new Error('Channel does not have a preset');
  const parsed = typeof config.preset === 'string' ? JSON.parse(config.preset) : config.preset;
  config.preset = adaptMeowssiahPreset(parsed);
  config.source_file_name = `Meowssiah-${relayRevision}.json`;
  config.script_runtime = 'spreset-v1';
  config.parameter_policy = 'client';
  config.post_processing = 'none';
  // No internal reply envelope exists in this derivative. Native tool and JSON
  // controls remain intact instead of approving prompt-only format conversion.
  for (const key of ['source_preset_id', 'reply_adapter', 'tool_protocol', 'tool_text', 'delivery_mode']) delete config[key];
  // Remove reviewed cleanup that existed only for the old reply envelope.
  // Leave unrelated administrator rules, send rules and worldbooks untouched.
  const envelopeSources = new Set([
    '1d78fce4-3e3c-4d8f-b9dc-f26555be0a6d',
    'e3a9e68f-4112-447c-bea3-7057c1f77422',
    '5f2f5e89-90cb-424c-868d-bf1c4036260c',
    '1eefb207-caf1-4c93-a5ee-bad981180bda',
  ]);
  const envelopePatterns = new Set([
    '/</?game>/g',
    '<logic_check>[\\s\\S]*?</logic_check>',
    '/<(?:think_nya|report_nya)~?>[\\s\\S]*?<\\/(?:think_nya|report_nya)~?>/g',
  ]);
  if (Array.isArray(settings.response_text_filter?.rules)) {
    settings.response_text_filter.rules = settings.response_text_filter.rules.filter(rule =>
      rule.stage !== 'receive' || rule.replacement !== '' ||
      !(envelopeSources.has(rule.source_script_id) || envelopePatterns.has(rule.pattern))
    );
  }
  settings.adaptation_scope = 'all_supported';
  return settings;
}

if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) {
  const [mode, input, output] = process.argv.slice(2);
  if (!['preset', 'channel'].includes(mode) || !input || !output || resolve(input) === resolve(output)) {
    throw new Error('Usage: node meowssiah-relay.mjs preset|channel <source.json> <new-output.json>');
  }
  const source = JSON.parse(await readFile(input, 'utf8'));
  let result;
  if (mode === 'preset') result = adaptMeowssiahPreset(source);
  else {
    const settings = typeof source.settings === 'string' ? JSON.parse(source.settings) : source.settings;
    result = { ...source, settings: JSON.stringify(adaptMeowssiahChannelSettings(settings)) };
  }
  // Never silently overwrite a backup or an administrator's source file.
  await writeFile(output, JSON.stringify(result, null, 2) + '\n', { flag: 'wx' });
  console.log(JSON.stringify({ revision: relayRevision, output: resolve(output) }));
}
