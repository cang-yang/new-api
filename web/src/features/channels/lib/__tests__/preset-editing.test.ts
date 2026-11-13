/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { expect, test } from 'vitest'

import {
  transformFormDataToCreatePayload,
  CHANNEL_FORM_DEFAULT_VALUES,
} from '../channel-form'
import {
  editPresetEntry,
  importPresetConfiguration,
  migratePresetPatches,
  movePresetEntry,
  parsePresetEditor,
  reorderPresetEntries,
} from '../sillytavern-editor'

const config = {
  future: true,
  preset: {
    custom: { keep: true },
    prompts: [
      {
        identifier: 'first',
        name: 'First',
        content: 'old old',
        role: 'system',
        extension: { keep: true },
      },
      { identifier: 'second', marker: true },
    ],
    prompt_order: [
      {
        character_id: 100001,
        order: [
          { identifier: 'first', enabled: true, extra: 'keep' },
          { identifier: 'second', enabled: false },
        ],
      },
    ],
  },
}

test('reimporting a preset preserves the complete channel delivery configuration', async () => {
  const previous = {
    ...config,
    parameter_policy: 'client',
    script_runtime: 'spreset-v1',
    reply_adapter: {
      version: 2,
      structured_output: true,
      require_container: true,
      formatted_output: 'client_format',
      containers: [],
    },
    failure_policy: { worldbook: 'skip', regex: 'preserve' },
    entry_overrides: { first: false },
    models: ['test-model'],
    user: 'Example',
  }
  const imported = await importPresetConfiguration(
    config.preset,
    JSON.stringify(previous),
    'updated.json'
  )
  expect(imported).toMatchObject(previous)
  expect(imported.source_file_name).toBe('updated.json')
  expect(imported.source_preset_id).toMatch(/^[a-f0-9]{64}$/)
  expect(previous).not.toHaveProperty('source_preset_id')
})

test('importing an adaptation configuration preserves its contract instead of previous settings', async () => {
  const bundle = { ...config, parameter_policy: 'client', script_runtime: '' }
  const imported = await importPresetConfiguration(
    bundle,
    JSON.stringify({ ...config, script_runtime: 'spreset-v1' }),
    'bundle.json'
  )
  expect(imported.script_runtime).toBe('')
  expect(imported.parameter_policy).toBe('client')
})

test('dragging an entry across multiple rows preserves unknown data and unrelated orders', () => {
  const original = {
    ...config,
    preset: {
      ...config.preset,
      prompts: [
        ...config.preset.prompts,
        { identifier: 'third', content: 'third' },
      ],
      prompt_order: [
        { character_id: 0, order: [{ identifier: 'first', enabled: false }] },
        ...config.preset.prompt_order,
      ],
    },
  }
  const result = reorderPresetEntries(original, 'first', 'third')
  expect(result).toMatchObject({
    future: true,
    preset: { custom: { keep: true } },
  })
  const ordered = result.preset as typeof original.preset
  expect(ordered.prompt_order[0]).toEqual(original.preset.prompt_order[0])
  expect(ordered.prompt_order[1].order).toEqual([
    { identifier: 'second', enabled: false },
    { identifier: 'third', enabled: false },
    { identifier: 'first', enabled: true, extra: 'keep' },
  ])
  expect(original.preset.prompt_order[1].order[0].identifier).toBe('first')
  expect(ordered.prompts).toEqual(original.preset.prompts)
})

test('edited content, role and order survive channel save and reopen with unknown fields intact', () => {
  const edited = editPresetEntry(JSON.stringify(config), 'first', {
    name: 'Revised',
    content: 'Saved content',
    role: 'user',
  })
  const reordered = movePresetEntry(edited, 'first', 1)
  const saved = JSON.parse(
    transformFormDataToCreatePayload({
      ...CHANNEL_FORM_DEFAULT_VALUES,
      sillytavern_preset: reordered,
    }).channel.settings || '{}'
  )
  const reopened = parsePresetEditor(JSON.stringify(saved.sillytavern_preset))
  expect(reopened).not.toBeNull()
  if (!reopened) throw new Error('Saved preset did not reopen')
  expect(reopened.entries.map((entry) => entry.identifier)).toEqual([
    'second',
    'first',
  ])
  expect(reopened.entries[1]).toMatchObject({
    name: 'Revised',
    content: 'Saved content',
    role: 'user',
    enabled: true,
  })
  expect(saved.sillytavern_preset).toMatchObject({
    future: true,
    preset: {
      custom: { keep: true },
      prompts: [{ extension: { keep: true } }, { marker: true }],
      prompt_order: [
        {
          order: [
            { identifier: 'second', enabled: false },
            { identifier: 'first', extra: 'keep' },
          ],
        },
      ],
    },
  })
})

test('legacy literal patches become editable content exactly once when saved', () => {
  const value = JSON.stringify({
    ...config,
    patches: [{ identifier: 'first', find: 'old', replace: 'old new' }],
  })
  const result = migratePresetPatches(value)
  expect(result.error).toBeUndefined()
  expect(parsePresetEditor(result.value)?.entries[0].content).toBe(
    'old new old new'
  )
  const saved = JSON.parse(
    transformFormDataToCreatePayload({
      ...CHANNEL_FORM_DEFAULT_VALUES,
      sillytavern_preset: result.value,
      sillytavern_preset_patches: JSON.stringify([
        { identifier: 'first', find: 'old', replace: 'old new' },
      ]),
    }).channel.settings || '{}'
  )
  expect(saved.sillytavern_preset.patches).toBeUndefined()
  expect(migratePresetPatches(result.value).value).toBe(result.value)
})

test.each([
  { identifier: 'missing', find: 'old', replace: '' },
  { identifier: 'second', find: 'old', replace: '' },
  { find: 'old', replace: '' },
  { identifier: 'first', find: 'not found', replace: '' },
])('unsafe legacy patch migration retains the original data: %j', (patch) => {
  const value = JSON.stringify({ ...config, patches: [patch] })
  const result = migratePresetPatches(value)
  expect(result.error).toBeTruthy()
  expect(result.value).toBe(value)
})
