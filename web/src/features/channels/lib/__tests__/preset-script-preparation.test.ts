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
import { afterEach, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'

import {
  applyScriptPreparation,
  discoverPresetScripts,
  preparePresetScripts,
} from '../preset-script-preparation'

afterEach(() => vi.restoreAllMocks())

const config = {
  preset: {
    extensions: {
      tavern_helper: {
        scripts: [
          {
            type: 'script',
            id: 'active',
            name: 'Active script',
            enabled: true,
            content: '1',
          },
          {
            type: 'folder',
            enabled: false,
            scripts: [
              {
                type: 'script',
                id: 'nested',
                name: 'Nested script',
                enabled: true,
                content: '2',
              },
            ],
          },
        ],
      },
    },
  },
}
const result = {
  modules: { helper: 'export const a = 1' },
  scripts: [
    {
      id: 'active',
      name: 'Active script',
      enabled: true,
      status: 'prepared' as const,
      issues: [],
    },
    {
      id: 'nested',
      name: 'Nested script',
      enabled: false,
      status: 'disabled' as const,
      issues: [],
    },
  ],
}

test('discovering scripts retains names and inherited folder disablement', () => {
  expect(discoverPresetScripts(config)).toMatchObject([
    { id: 'active', name: 'Active script', enabled: true, status: 'manual' },
    { id: 'nested', name: 'Nested script', enabled: false, status: 'disabled' },
  ])
})

test('fresh prepared import enables Tavern and disables conflicting transport without changing script flags', () => {
  const next = applyScriptPreparation(
    { ...config, tool_text: { enabled: true } },
    result,
    true
  )
  expect(next).toMatchObject({
    script_runtime: 'tavern-v1',
    script_modules: result.modules,
    tool_text: { enabled: false },
  })
  expect(next.preset).toEqual(config.preset)
  expect(discoverPresetScripts(next)).toEqual(result.scripts)
})

test.each([
  { ...config, script_runtime: '' },
  { ...config, script_runtime: 'spreset-v1' },
  {
    preset: {
      extensions: { ...config.preset.extensions, SPreset: { enabled: true } },
    },
  },
])(
  'preparation preserves explicit runtime choices and leaves mixed families for selection',
  (input) => {
    expect(applyScriptPreparation(input, result, true).script_runtime).toBe(
      (input as Record<string, unknown>).script_runtime
    )
  }
)

test('failed enabled scripts do not autoenable and failed snapshots preserve good sources', () => {
  const next = applyScriptPreparation(
    { ...config, script_modules: { saved: 'good' } },
    {
      modules: {},
      scripts: [
        {
          ...result.scripts[0],
          status: 'failed',
          issues: [{ code: 'download_failed' }],
        },
      ],
    },
    true
  )
  expect(next.script_runtime).toBeUndefined()
  expect(next.script_modules).toEqual({ saved: 'good' })
})

test('preparation uses the shared API and rejects business failures', async () => {
  const post = vi
    .spyOn(api, 'post')
    .mockResolvedValueOnce({ data: { success: true, data: result } })
  expect(await preparePresetScripts(config)).toEqual(result)
  expect(post).toHaveBeenCalledWith(
    '/api/channel/preset/scripts/prepare',
    { config },
    expect.any(Object)
  )
  post.mockResolvedValueOnce({
    data: { success: false, message: 'Preparation failed' },
  })
  await expect(preparePresetScripts(config)).rejects.toThrow(
    'Preparation failed'
  )
})
