import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';
import vm from 'node:vm';
import { adaptMeowssiahChannelSettings, adaptMeowssiahPreset } from './meowssiah-relay.mjs';

const hook = vm.runInNewContext('(' + await readFile(new URL('./meowssiah-relay-request.js', import.meta.url), 'utf8') + ')');

test('native hook retains real history, media, tools, schema and literal escapes without synthetic calls', () => {
  const request = {
    messages: [
      { role: 'system', content: 'Worldbook: a blue door.' },
      { role: 'user', content: [{ type: 'text', text: 'Use C:\\notes, not a real newline: \\n' }, { type: 'image_url', image_url: { url: 'data:image/png;base64,AA==' } }] },
      { role: 'assistant', tool_calls: [{ id: 'lookup_1', type: 'function', function: { name: 'lookup', arguments: '{"q":"a"}' } }] },
      { role: 'tool', tool_call_id: 'lookup_1', content: '{"nested":"{\\"ok\\":true}"}' },
      { role: 'user', content: 'Only update variables.' },
    ],
    tools: [{ type: 'function', function: { name: 'lookup', parameters: { type: 'object' } } }],
    tool_choice: 'none',
    response_format: { type: 'json_schema', json_schema: { name: 'patch', schema: { type: 'object' } } },
  };
  const before = structuredClone(request);
  const result = hook({ request, client: structuredClone(request) });
  assert.deepEqual(result.messages.slice(0, -1), before.messages);
  assert.deepEqual(result.tools, before.tools);
  assert.deepEqual(result.response_format, before.response_format);
  assert.equal(result.tool_choice, 'none');
  assert.match(result.messages.at(-1).content, /原生 response_format/);
});

const source = () => ({
  prompts: ['main', 'a00fb1fe-9907-4709-b230-43ae0d236529', 'nsfw', 'jailbreak', '3df19ba7-b44d-4d46-b17a-de514d86e5d5', 'enhanceDefinitions', 'creative', 'SPresetSettings'].map(id => ({ identifier: id, content: id === 'enhanceDefinitions' ? '本喵给他们的聊天偷偷加个`soliumbra_reply`工具，然后在content里面先用 `<think_nya>...</think_nya>` 好好想想怎么回复。' : 'author text' })),
  prompt_order: [{ character_id: 100001, order: [{ identifier: 'creative', enabled: false }] }],
  extensions: { SPreset: { ChatSquash: { enabled: true }, ToolBindings: { old: {} } } },
});

test('derivative preserves attribution, creative text and entry selection; changes only the relay contract', () => {
  const original = source();
  const before = structuredClone(original);
  const result = adaptMeowssiahPreset(original);
  assert.deepEqual(original, before);
  assert.equal(result.prompts.find(p => p.identifier === 'main').content, 'author text');
  assert.ok(result.prompts.find(p => p.identifier === 'creative').content.endsWith('author text'));
  assert.deepEqual(result.prompt_order, before.prompt_order);
  assert.deepEqual(result.extensions.SPreset.ToolBindings, {});
  assert.equal(result.extensions.SPreset.OutputPreprocessing.consumeToolCalls, false);
  assert.deepEqual(JSON.parse(result.prompts.find(p => p.identifier === 'SPresetSettings').content), result.extensions.SPreset);
  assert.deepEqual(adaptMeowssiahPreset(result), result);
});

test('channel conversion removes internal reply conversion without deleting worldbooks, regexes or overrides', () => {
  const settings = {
    sillytavern_preset: { preset: source(), reply_adapter: { version: 2 }, tool_text: { enabled: true }, entry_overrides: { creative: false } },
    worldbook: { books: [{ name: 'Book' }] }, response_text_filter: { mode: 'rules', rules: [] },
  };
  const result = adaptMeowssiahChannelSettings(settings);
  assert.equal(result.sillytavern_preset.reply_adapter, undefined);
  assert.equal(result.sillytavern_preset.tool_text, undefined);
  assert.equal(result.sillytavern_preset.script_runtime, 'spreset-v1');
  assert.deepEqual(result.sillytavern_preset.entry_overrides, settings.sillytavern_preset.entry_overrides);
  assert.deepEqual(result.worldbook, settings.worldbook);
  assert.deepEqual(result.response_text_filter, settings.response_text_filter);
});

test('unreviewed source fails before any channel changes', () => {
  const changed = source();
  changed.prompts.find(p => p.identifier === 'enhanceDefinitions').content = 'new source';
  assert.throws(() => adaptMeowssiahPreset(changed), /review before upgrading/);
  changed.prompts.find(p => p.identifier === 'enhanceDefinitions').content = 'unreviewed soliumbra_reply instructions';
  assert.throws(() => adaptMeowssiahPreset(changed), /review before upgrading/);
});

test('obsolete envelope cleanup is removed without removing unrelated administrator rules', () => {
  const settings = { sillytavern_preset: { preset: source() }, response_text_filter: { rules: [
    { stage: 'receive', pattern: '/</?game>/g', replacement: '' },
    { stage: 'receive', pattern: '/custom/g', replacement: 'wanted' },
    { stage: 'send', pattern: '/</?game>/g', replacement: '' },
  ] } };
  const result = adaptMeowssiahChannelSettings(settings);
  assert.deepEqual(result.response_text_filter.rules, settings.response_text_filter.rules.slice(1));
});
