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
import {
  cleanup,
  act,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { afterEach, expect, test, vi } from 'vitest'

import { Sheet, SheetContent, SheetTitle } from '@/components/ui/sheet'
import { api } from '@/lib/api'

import { applyScriptPreparation } from '../../lib/preset-script-preparation'
import { PresetScriptSettings } from '../preset-script-settings'
import { SillyTavernPresetEditor } from '../sillytavern-preset-editor'

async function chooseSelectOption(
  user: ReturnType<typeof userEvent.setup>,
  trigger: HTMLElement,
  option: string
) {
  await user.click(trigger)
  await user.click(await screen.findByRole('option', { name: option }))
}

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})

test('preparation issues display readable explanations and safely handle unknown codes', () => {
  const config = applyScriptPreparation(
    {
      preset: {
        extensions: {
          tavern_helper: {
            scripts: [{ type: 'script', id: 'issues', enabled: true }],
          },
        },
      },
    },
    {
      modules: {},
      scripts: [
        {
          id: 'issues',
          name: 'Issue hook',
          enabled: true,
          status: 'failed',
          issues: [
            { code: 'computed_import' },
            { code: 'download_failed' },
            { code: 'future_code' },
          ],
        },
      ],
    }
  )
  render(<PresetScriptSettings config={config} onChange={vi.fn()} />)
  expect(
    screen.getByText(
      'This script builds file names while running. Ask the author for a compatible version and the required files; retrying the download alone will not fix it.'
    )
  ).toBeVisible()
  expect(
    screen.getByText(
      'A required file could not be downloaded. Check the connection and try again, or ask the author for the file.'
    )
  ).toBeVisible()
  expect(
    screen.getByText(
      'This script could not be checked. Try again or ask the author for a compatible version.'
    )
  ).toBeVisible()
  expect(
    screen.queryByText(/future_code|download_failed|computed_import/)
  ).toBeNull()
})

test('script compatibility is explicit, preserves the preset and disables the conflicting transport', async () => {
  const user = userEvent.setup()
  const withHooks = {
    ...preset,
    extensions: {
      SPreset: {
        FinalRequestProcessing: {
          enabled: true,
          script: '({request}) => request',
        },
      },
    },
  }
  render(
    <Editor
      initialValue={JSON.stringify({
        preset: withHooks,
        tool_text: { enabled: true, name: 'reply', argument: 'text' },
      })}
    />
  )
  await user.click(screen.getByRole('button', { name: 'Edit preset' }))
  await user.click(screen.getByRole('tab', { name: 'Script compatibility' }))
  const toggle = screen.getByRole('radio', {
    name: 'Use preset settings (SPreset)',
  })
  expect(toggle).toHaveAttribute('aria-checked', 'false')
  await user.click(toggle)
  expect(screen.getByRole('radiogroup')).toBeVisible()
  expect(
    screen.getByRole('radio', { name: 'Do not use preset extras' })
  ).toHaveAttribute('aria-checked', 'false')
  expect(screen.queryByText('Compatibility boundaries')).toBeNull()
  await user.click(screen.getByRole('button', { name: 'Apply to channel' }))
  const saved = JSON.parse(
    screen.getByLabelText('Saved config').textContent || '{}'
  )
  expect(saved.script_runtime).toBe('spreset-v1')
  expect(saved.tool_text.enabled).toBe(false)
  expect(saved.preset).toEqual(withHooks)
})

test('script compatibility hides the legacy adapter when the preset has no supported adapter', async () => {
  const user = userEvent.setup()
  render(<Editor />)
  await user.click(screen.getByRole('button', { name: 'Edit preset' }))
  await user.click(screen.getByRole('tab', { name: 'Script compatibility' }))
  expect(
    screen.queryByRole('radio', { name: 'Use preset settings (SPreset)' })
  ).toBeNull()
})

test('script names and inherited disabled states appear without fetching existing presets and retry prepares explicitly', async () => {
  const user = userEvent.setup()
  const onChange = vi.fn()
  const post = vi.spyOn(api, 'post').mockResolvedValue({
    data: {
      success: true,
      data: {
        modules: {},
        scripts: [
          {
            id: 'local',
            name: 'Local hook',
            enabled: true,
            status: 'prepared',
            issues: [],
          },
          {
            id: 'off',
            name: 'Hidden hook',
            enabled: false,
            status: 'disabled',
            issues: [],
          },
        ],
      },
    },
  })
  render(
    <PresetScriptSettings
      onChange={onChange}
      config={{
        preset: {
          extensions: {
            tavern_helper: {
              scripts: [
                {
                  type: 'script',
                  id: 'local',
                  name: 'Local hook',
                  enabled: true,
                },
                {
                  type: 'folder',
                  enabled: false,
                  scripts: [
                    {
                      type: 'script',
                      id: 'off',
                      name: 'Hidden hook',
                      enabled: true,
                    },
                  ],
                },
              ],
            },
          },
        },
      }}
    />
  )
  expect(
    screen.getByRole('list', { name: 'Preset scripts' })
  ).toHaveTextContent('Local hook')
  expect(
    screen.getByRole('list', { name: 'Preset scripts' })
  ).toHaveTextContent('Hidden hook')
  expect(screen.getByText('Disabled by author')).toBeVisible()
  expect(post).not.toHaveBeenCalled()
  await user.click(
    screen.getByRole('button', { name: 'Check and download files' })
  )
  await waitFor(() => expect(onChange).toHaveBeenCalled())
  expect(onChange.mock.calls[0][0].script_runtime).toBeUndefined()
})

test('preparation completing after a preset switch does not overwrite the new draft', async () => {
  const user = userEvent.setup()
  const onChange = vi.fn()
  let resolve!: (value: unknown) => void
  vi.spyOn(api, 'post').mockImplementation(
    () =>
      new Promise((finish) => {
        resolve = finish
      })
  )
  const config = {
    preset: {
      extensions: {
        tavern_helper: {
          scripts: [
            { type: 'script', id: 'old', name: 'Old hook', enabled: true },
          ],
        },
      },
    },
  }
  const { rerender } = render(
    <PresetScriptSettings config={config} onChange={onChange} />
  )
  await user.click(
    screen.getByRole('button', { name: 'Check and download files' })
  )
  rerender(<PresetScriptSettings config={{ preset: {} }} onChange={onChange} />)
  resolve({
    data: { success: true, data: { modules: { stale: '1' }, scripts: [] } },
  })
  await waitFor(() =>
    expect(screen.queryByText('Checking and downloading files…')).toBeNull()
  )
  expect(onChange).not.toHaveBeenCalled()
})

test('generic Tavern scripts select a distinct runtime and preserve pinned dependencies', async () => {
  const user = userEvent.setup()
  const modules = {
    'https://example.invalid/helper.js': 'export const value = 1',
  }
  render(
    <Editor
      initialValue={JSON.stringify({
        preset: {
          ...preset,
          extensions: {
            tavern_helper: {
              scripts: [
                {
                  type: 'script',
                  id: 'generic',
                  enabled: true,
                  content: 'eventOn("local",()=>{})',
                },
              ],
            },
          },
        },
        script_runtime: 'spreset-v1',
        script_modules: modules,
        tool_text: { enabled: true, name: 'reply', argument: 'text' },
      })}
    />
  )
  await user.click(screen.getByRole('button', { name: 'Edit preset' }))
  await user.click(screen.getByRole('tab', { name: 'Script compatibility' }))
  await user.click(screen.getByRole('radio', { name: 'Run bundled scripts' }))
  expect(
    screen.queryByRole('radio', { name: 'Use preset settings (SPreset)' })
  ).toBeNull()
  await user.click(screen.getByRole('button', { name: 'Apply to channel' }))
  const saved = JSON.parse(
    screen.getByLabelText('Saved config').textContent || '{}'
  )
  expect(saved.script_runtime).toBe('tavern-v1')
  expect(saved.script_modules).toEqual(modules)
  expect(saved.tool_text.enabled).toBe(false)
})

test.each([
  'invalid JSON',
  'invalid shape',
  'oversized file',
  'oversized source',
])(
  'an advanced bundle with %s cancels preparation, clears loading and preserves the existing modules',
  async (kind) => {
    const user = userEvent.setup()
    const onChange = vi.fn()
    let resolve!: (value: unknown) => void
    const post = vi.spyOn(api, 'post').mockImplementation(
      () =>
        new Promise((finish) => {
          resolve = finish
        })
    )
    render(
      <PresetScriptSettings
        config={{
          preset: {
            extensions: {
              tavern_helper: {
                scripts: [
                  { type: 'script', id: 'pending-bundle', enabled: true },
                ],
              },
            },
          },
          script_modules: { saved: 'export const a=1' },
        }}
        onChange={onChange}
      />
    )
    await user.click(
      screen.getByRole('button', { name: 'Check and download files' })
    )
    expect(
      screen.getByRole('button', { name: 'Checking and downloading files…' })
    ).toBeDisabled()
    await user.click(
      screen.getByRole('button', { name: 'Advanced details and manual files' })
    )
    const file = new File(['{}'], 'invalid.json', { type: 'application/json' })
    let source = '{'
    if (kind === 'invalid shape') source = '[]'
    if (kind === 'oversized file') {
      Object.defineProperty(file, 'size', { value: 2 * 1024 * 1024 + 1 })
    }
    if (kind === 'oversized source') {
      source = JSON.stringify({ example: 'a'.repeat(128 * 1024 + 1) })
    }
    Object.defineProperty(file, 'text', { value: async () => source })
    await user.upload(
      screen.getByLabelText('Import module bundle', { selector: 'input' }),
      file
    )
    expect(await screen.findByText(/^Invalid module bundle/)).toBeVisible()
    expect(
      screen.getByRole('button', { name: 'Check and download files' })
    ).toBeEnabled()
    expect(screen.getByText('saved')).toBeVisible()
    await act(async () => {
      resolve({
        data: { success: true, data: { modules: { stale: '1' }, scripts: [] } },
      })
    })
    expect(onChange).not.toHaveBeenCalled()
    const signal = (post.mock.calls[0][2] as { signal: AbortSignal }).signal
    expect(signal.aborted).toBe(true)
  }
)

test('module imports validate input and cannot overwrite a newer channel draft', async () => {
  const user = userEvent.setup()
  const onChange = vi.fn()
  const initial = { preset, script_runtime: '' }
  const { rerender } = render(
    <PresetScriptSettings config={initial} onChange={onChange} />
  )
  await user.click(screen.getByText('Advanced details and manual files'))
  const input = screen.getByLabelText('Import module bundle', {
    selector: 'input',
  })
  const invalid = new File(['[]'], 'invalid.json', { type: 'application/json' })
  Object.defineProperty(invalid, 'text', { value: async () => '[]' })
  await user.upload(input, invalid)
  expect(await screen.findByText(/^Invalid module bundle/)).toBeVisible()
  expect(onChange).not.toHaveBeenCalled()
  let resolveRead!: (value: string) => void
  const pending = new File(['{}'], 'modules.json', { type: 'application/json' })
  Object.defineProperty(pending, 'text', {
    value: () =>
      new Promise<string>((resolve) => {
        resolveRead = resolve
      }),
  })
  await user.upload(input, pending)
  const newer = { ...initial, script_runtime: 'tavern-v1' }
  rerender(<PresetScriptSettings config={newer} onChange={onChange} />)
  resolveRead('{"example":"export const a=1"}')
  await waitFor(() =>
    expect(screen.getByText(/^Invalid module bundle/)).toBeVisible()
  )
  expect(onChange).not.toHaveBeenCalled()
  const valid = new File(['{}'], 'valid.json', { type: 'application/json' })
  Object.defineProperty(valid, 'text', {
    value: async () => '{"example":"export const a=1"}',
  })
  await user.upload(input, valid)
  await waitFor(() =>
    expect(onChange).toHaveBeenCalledWith({
      ...newer,
      script_modules: { example: 'export const a=1' },
    })
  )
})

test('editing an entry applies the draft and preserves marker content protection', async () => {
  const user = userEvent.setup()
  render(<Editor />)
  await user.click(screen.getByRole('button', { name: 'Edit preset' }))
  await user.click(screen.getByRole('button', { name: /^Main instruction/ }))
  await user.clear(screen.getByRole('textbox', { name: 'Prompt content' }))
  await user.type(
    screen.getByRole('textbox', { name: 'Prompt content' }),
    'Revised content'
  )
  await chooseSelectOption(user, screen.getByLabelText('Message role'), 'user')
  expect(screen.getByLabelText('Saved config').textContent).not.toContain(
    'Revised content'
  )
  await user.click(screen.getByRole('button', { name: 'Apply to channel' }))
  const saved = JSON.parse(
    screen.getByLabelText('Saved config').textContent || '{}'
  )
  expect(saved.preset.prompts[0]).toMatchObject({
    content: 'Revised content',
    role: 'user',
  })
  await user.click(screen.getByRole('button', { name: 'Edit preset' }))
  await user.click(screen.getByRole('button', { name: /^Conversation/ }))
  expect(screen.queryByRole('textbox', { name: 'Prompt content' })).toBeNull()
  expect(
    screen.getByText('This marker is filled from the request context.')
  ).toBeVisible()
})

test('preset failure policy remains inherited on unrelated changes and explicit changes preserve preset options', async () => {
  const user = userEvent.setup()
  render(
    <Editor initialValue={JSON.stringify({ preset, custom_option: 'keep' })} />
  )
  await user.click(screen.getByRole('button', { name: 'Edit preset' }))
  await user.click(screen.getByRole('tab', { name: 'Embedded regex scripts' }))
  expect(
    screen.getByLabelText('Embedded regex failure policy')
  ).toHaveTextContent(
    'Inherited / legacy behavior (strict)'
  )
  await user.click(
    screen.getByRole('switch', { name: 'Enable embedded send-side regex' })
  )
  expect(
    JSON.parse(screen.getByLabelText('Saved config').textContent || '{}')
      .regex_failure_policy
  ).toBeUndefined()
  await chooseSelectOption(
    user,
    screen.getByLabelText('Embedded regex failure policy'),
    'Keep original on failure (recommended for availability)'
  )
  await user.click(screen.getByRole('button', { name: 'Apply to channel' }))
  expect(
    JSON.parse(screen.getByLabelText('Saved config').textContent || '{}')
  ).toMatchObject({
    preset,
    custom_option: 'keep',
    regex_failure_policy: 'passthrough',
  })
})

test('embedded sending stays off by default and independently enables prompt-only scripts', async () => {
  const user = userEvent.setup()
  render(<Editor />)
  await user.click(screen.getByRole('button', { name: 'Edit preset' }))
  await user.click(screen.getByRole('tab', { name: 'Embedded regex scripts' }))
  const master = screen.getByRole('switch', {
    name: 'Enable embedded send-side regex',
  })
  const script = screen.getByRole('switch', {
    name: 'Enable regex script Render panel',
  })
  expect(master).toHaveAttribute('aria-checked', 'false')
  expect(script).toHaveAttribute('aria-disabled', 'true')
  await user.click(master)
  expect(script).not.toHaveAttribute('aria-disabled', 'true')
  await user.click(screen.getByRole('button', { name: 'Apply to channel' }))
  const saved = JSON.parse(
    screen.getByLabelText('Saved config').textContent || '{}'
  )
  expect(saved.enable_send_regex).toBe(true)
  expect(saved.enable_embedded_regex).toBeUndefined()
})

test('native tool transport is opt-in and preserves embedded browser scripts', async () => {
  const user = userEvent.setup()
  const scriptedPreset = {
    ...preset,
    extensions: {
      ...preset.extensions,
      tavern_helper: {
        scripts: [{ type: 'script', enabled: true, content: 'window.fetch' }],
      },
    },
  }
  render(<Editor initialValue={JSON.stringify({ preset: scriptedPreset })} />)
  expect(
    screen.getByText(
      'Browser scripts are preserved but not executed. Configure native compatibility in the preset editor.'
    )
  ).toBeVisible()
  await user.click(screen.getByRole('button', { name: 'Edit preset' }))
  await user.click(screen.getByRole('tab', { name: 'Macro values and time' }))
  expect(screen.getByRole('note')).toHaveTextContent(
    'Built-in macros run automatically'
  )
  expect(screen.getByRole('note')).toHaveTextContent('{{roll 1999999}}')
  const toggle = screen.getByRole('switch', {
    name: 'Enable native tool-text transport',
  })
  expect(toggle).toHaveAttribute('aria-checked', 'false')
  await user.click(toggle)
  expect(screen.getByLabelText('Transport tool name')).toHaveValue(
    'newapi_text'
  )
  expect(screen.getByLabelText('Transport text argument')).toHaveValue(
    'display_stream'
  )
  await user.clear(screen.getByLabelText('Transport tool name'))
  await user.type(screen.getByLabelText('Transport tool name'), 'custom_text')
  await user.click(screen.getByRole('button', { name: 'Apply to channel' }))
  const saved = JSON.parse(
    screen.getByLabelText('Saved config').textContent || '{}'
  )
  expect(saved.tool_text).toEqual({
    enabled: true,
    name: 'custom_text',
    argument: 'display_stream',
  })
  expect(saved.preset).toEqual(scriptedPreset)
})

const preset = {
  prompts: [
    {
      identifier: 'main',
      name: 'Main instruction',
      content: 'Full prompt text',
      role: 'system',
    },
    { identifier: 'history', name: 'Conversation', marker: true },
    {
      identifier: 'orphan',
      name: 'Unordered instruction',
      content: 'Optional',
    },
  ],
  prompt_order: [
    { character_id: 0, order: [{ identifier: 'main', enabled: false }] },
    {
      character_id: 100001,
      order: [
        { identifier: 'main', enabled: true },
        { identifier: 'history', enabled: false },
      ],
    },
  ],
  extensions: {
    regex_scripts: [
      {
        id: 'send',
        scriptName: 'Clean text',
        markdownOnly: true,
        placement: [2],
      },
      {
        id: 'display',
        scriptName: 'Render panel',
        promptOnly: true,
        placement: [1],
      },
      {
        id: 'html',
        scriptName: 'HTML status panel',
        markdownOnly: true,
        placement: [2],
        replaceString: '<div><style>.status{color:red}</style>$1</div>',
      },
    ],
  },
}

function Editor(props: { disabled?: boolean; initialValue?: string }) {
  const [value, setValue] = useState(
    props.initialValue ?? JSON.stringify({ preset })
  )
  return (
    <>
      <SillyTavernPresetEditor
        value={value}
        onChange={setValue}
        disabled={props.disabled}
      />
      <output aria-label='Saved config'>{value}</output>
    </>
  )
}

test('entry switches override the selected order without altering imported preset and can restore defaults', async () => {
  const user = userEvent.setup()
  render(<Editor />)
  await user.click(screen.getByRole('button', { name: 'Edit preset' }))
  const toggle = screen.getByRole('switch', {
    name: 'Enable preset entry Main instruction',
  })
  expect(toggle.getAttribute('aria-checked')).toBe('true')
  await user.click(toggle)
  await user.click(screen.getByRole('button', { name: 'Apply to channel' }))
  const saved = JSON.parse(
    screen.getByLabelText('Saved config').textContent || '{}'
  )
  expect(saved.entry_overrides).toEqual({ main: false })
  expect(saved.preset).toEqual(preset)
  await user.click(screen.getByRole('button', { name: 'Edit preset' }))
  await user.click(
    screen.getByRole('button', { name: 'Restore entry switches' })
  )
  expect(
    screen.getByRole('switch', { name: 'Enable preset entry Main instruction' })
  ).toHaveAttribute('aria-checked', 'true')
})

test('unordered prompts are visible disabled and can be enabled without changing the imported order', async () => {
  const user = userEvent.setup()
  render(<Editor />)
  await user.click(screen.getByRole('button', { name: 'Edit preset' }))
  const toggle = screen.getByRole('switch', {
    name: 'Enable preset entry Unordered instruction',
  })
  expect(toggle.getAttribute('aria-checked')).toBe('false')
  await user.click(toggle)
  await user.click(screen.getByRole('button', { name: 'Apply to channel' }))
  const saved = JSON.parse(
    screen.getByLabelText('Saved config').textContent || '{}'
  )
  expect(saved.entry_overrides).toEqual({ orphan: true })
  expect(saved.preset).toEqual(preset)
})

test('filtering limits bulk toggles to visible entries and disables sorting', async () => {
  const user = userEvent.setup()
  render(<Editor />)
  await user.click(screen.getByRole('button', { name: 'Edit preset' }))
  await user.click(screen.getByRole('button', { name: /^Main instruction/ }))
  expect(screen.getByText('Full prompt text')).toBeTruthy()
  await user.type(
    screen.getByRole('textbox', { name: 'Search preset entries' }),
    'Conversation'
  )
  await user.click(
    screen.getByRole('button', { name: 'Enable visible entries' })
  )
  expect(
    screen.getByRole('button', { name: 'Reorder Conversation' })
  ).toBeDisabled()
  expect(screen.getByText('Clear search to reorder entries.')).toBeVisible()
  await user.click(screen.getByRole('button', { name: 'Apply to channel' }))
  expect(
    JSON.parse(screen.getByLabelText('Saved config').textContent || '{}')
      .entry_overrides
  ).toEqual({ history: true })
})

test('legacy saved presets keep regex disabled until explicitly enabled and sending scripts remain disabled', async () => {
  const user = userEvent.setup()
  render(<Editor />)
  await user.click(screen.getByRole('button', { name: 'Edit preset' }))
  await user.click(screen.getByRole('tab', { name: 'Embedded regex scripts' }))
  expect(
    screen
      .getByRole('switch', { name: 'Enable regex script Clean text' })
      .getAttribute('aria-disabled')
  ).toBe('true')
  await user.click(
    screen.getByRole('switch', { name: 'Enable embedded regex scripts' })
  )
  expect(
    screen
      .getByRole('switch', { name: 'Enable regex script Clean text' })
      .getAttribute('aria-disabled')
  ).not.toBe('true')
  expect(
    screen
      .getByRole('switch', { name: 'Enable regex script Render panel' })
      .getAttribute('aria-disabled')
  ).toBe('true')
  await user.click(
    screen.getByRole('switch', { name: 'Enable regex script Clean text' })
  )
  await user.click(screen.getByRole('button', { name: 'Apply to channel' }))
  expect(
    JSON.parse(screen.getByLabelText('Saved config').textContent || '{}')
      .regex_overrides
  ).toEqual({ send: false })
})

test('editing entries preserves saved regex opt-out and unknown configuration', async () => {
  const user = userEvent.setup()
  render(
    <Editor
      initialValue={JSON.stringify({
        preset,
        enable_embedded_regex: false,
        future_option: { mode: 'preserve' },
      })}
    />
  )
  await user.click(screen.getByRole('button', { name: 'Edit preset' }))
  await user.click(
    screen.getByRole('switch', { name: 'Enable preset entry Main instruction' })
  )
  await user.click(screen.getByRole('button', { name: 'Apply to channel' }))
  const saved = JSON.parse(
    screen.getByLabelText('Saved config').textContent || '{}'
  )
  expect(saved.enable_embedded_regex).toBe(false)
  expect(saved.future_option).toEqual({ mode: 'preserve' })
  await user.click(screen.getByRole('button', { name: 'Edit preset' }))
  await user.click(screen.getByRole('tab', { name: 'Embedded regex scripts' }))
  expect(
    screen
      .getByRole('switch', { name: 'Enable embedded regex scripts' })
      .getAttribute('aria-checked')
  ).toBe('false')
})

test('locked channel prevents preset and regex mutations', () => {
  render(<Editor disabled />)
  expect(screen.getByRole('button', { name: 'Edit preset' })).toBeDisabled()
})

test('HTML-generating receive rules are marked and remain independently controllable', async () => {
  const user = userEvent.setup()
  render(<Editor />)
  await user.click(screen.getByRole('button', { name: 'Edit preset' }))
  await user.click(screen.getByRole('tab', { name: 'Embedded regex scripts' }))
  expect(screen.getByRole('alert').textContent).toContain(
    'Some response rules generate HTML'
  )
  expect(screen.getByText('May generate HTML')).toBeTruthy()
  await user.click(
    screen.getByRole('switch', { name: 'Enable embedded regex scripts' })
  )
  const htmlRule = screen.getByRole('switch', {
    name: 'Enable regex script HTML status panel',
  })
  expect(htmlRule.getAttribute('aria-disabled')).not.toBe('true')
  expect(htmlRule.getAttribute('aria-checked')).toBe('true')
  await user.click(htmlRule)
  await user.click(screen.getByRole('button', { name: 'Apply to channel' }))
  expect(
    JSON.parse(screen.getByLabelText('Saved config').textContent || '{}')
      .regex_overrides
  ).toEqual({ html: false })
})

test('custom macro values and time zone persist without changing the preset', async () => {
  const user = userEvent.setup()
  render(<Editor />)
  await user.click(screen.getByRole('button', { name: 'Edit preset' }))
  await user.click(screen.getByRole('tab', { name: 'Macro values and time' }))
  await user.type(
    screen.getByRole('textbox', { name: 'New macro name' }),
    'storyTime'
  )
  await user.click(screen.getByRole('button', { name: 'Add macro value' }))
  await user.type(
    screen.getByRole('textbox', { name: 'Value for macro storyTime' }),
    'Spring morning'
  )
  await user.type(
    screen.getByRole('textbox', { name: 'Preset time zone' }),
    'Asia/Shanghai'
  )
  await user.click(screen.getByRole('button', { name: 'Apply to channel' }))
  const config = JSON.parse(
    screen.getByLabelText('Saved config').textContent || '{}'
  )
  expect(config.macro_values).toEqual({ storyTime: 'Spring morning' })
  expect(config.time_zone).toBe('Asia/Shanghai')
  expect(config.preset).toEqual(preset)
  await user.click(screen.getByRole('button', { name: 'Edit preset' }))
  await user.click(screen.getByRole('tab', { name: 'Macro values and time' }))
  await user.click(
    screen.getByRole('button', { name: 'Remove macro storyTime' })
  )
  expect(
    screen.queryByRole('textbox', { name: 'Value for macro storyTime' })
  ).toBeNull()
})

test('opening selects the first entry and edits survive switching entries and tabs before one apply', async () => {
  const user = userEvent.setup()
  const onChange = vi.fn()
  render(
    <SillyTavernPresetEditor
      value={JSON.stringify({ preset })}
      onChange={onChange}
    />
  )
  expect(screen.queryByRole('textbox', { name: 'Prompt content' })).toBeNull()
  await user.click(screen.getByRole('button', { name: 'Edit preset' }))
  expect(screen.getByRole('textbox', { name: 'Prompt content' })).toHaveValue(
    'Full prompt text'
  )
  await user.type(
    screen.getByRole('textbox', { name: 'Prompt content' }),
    ' draft'
  )
  await user.click(screen.getByRole('button', { name: /^Conversation/ }))
  await user.click(screen.getByRole('tab', { name: 'Macro values and time' }))
  await user.click(screen.getByRole('tab', { name: 'Preset entries' }))
  await user.click(screen.getByRole('button', { name: /^Main instruction/ }))
  expect(screen.getByRole('textbox', { name: 'Prompt content' })).toHaveValue(
    'Full prompt text draft'
  )
  expect(onChange).not.toHaveBeenCalled()
  await user.click(screen.getByRole('button', { name: 'Apply to channel' }))
  expect(onChange).toHaveBeenCalledTimes(1)
})

test('cancel confirms dirty drafts and reopening restores the parent value', async () => {
  const user = userEvent.setup()
  render(<Editor />)
  await user.click(screen.getByRole('button', { name: 'Edit preset' }))
  await user.type(
    screen.getByRole('textbox', { name: 'Prompt content' }),
    ' discarded'
  )
  await user.click(screen.getByRole('button', { name: 'Cancel' }))
  expect(screen.getByRole('alertdialog')).toBeVisible()
  await user.click(screen.getByRole('button', { name: 'Keep editing' }))
  expect(screen.getByRole('textbox', { name: 'Prompt content' })).toHaveValue(
    'Full prompt text discarded'
  )
  await user.keyboard('{Escape}')
  await user.click(screen.getByRole('button', { name: 'Discard changes' }))
  await user.click(screen.getByRole('button', { name: 'Edit preset' }))
  expect(screen.getByRole('textbox', { name: 'Prompt content' })).toHaveValue(
    'Full prompt text'
  )
  await user.click(screen.getByRole('button', { name: 'Cancel' }))
  expect(screen.queryByRole('alertdialog')).toBeNull()
})

test('background parent changes preserve the open draft and block stale application', async () => {
  const user = userEvent.setup()
  const onChange = vi.fn()
  const view = render(
    <SillyTavernPresetEditor
      value={JSON.stringify({ preset })}
      onChange={onChange}
    />
  )
  await user.click(screen.getByRole('button', { name: 'Edit preset' }))
  await user.type(
    screen.getByRole('textbox', { name: 'Prompt content' }),
    ' draft'
  )
  view.rerender(
    <SillyTavernPresetEditor
      value={JSON.stringify({ preset, external: true })}
      onChange={onChange}
    />
  )
  expect(screen.getByRole('textbox', { name: 'Prompt content' })).toHaveValue(
    'Full prompt text draft'
  )
  expect(
    screen.getByRole('button', { name: 'Apply to channel' })
  ).toBeDisabled()
  expect(screen.getByRole('alert')).toHaveTextContent(
    'The channel preset changed while this editor was open.'
  )
  expect(onChange).not.toHaveBeenCalled()
})

test('depth fields are conditional and focus mode retains edited content', async () => {
  const user = userEvent.setup()
  render(<Editor />)
  await user.click(screen.getByRole('button', { name: 'Edit preset' }))
  expect(screen.queryByLabelText('Chat depth')).toBeNull()
  await chooseSelectOption(
    user,
    screen.getByLabelText('Injection placement'),
    'At chat depth'
  )
  expect(screen.getByLabelText('Chat depth')).toHaveValue(0)
  expect(
    screen.getByText(
      'Chat depth controls injection into the conversation; list position does not.'
    )
  ).toBeVisible()
  await user.click(screen.getByRole('button', { name: 'Focus writing' }))
  expect(
    screen.queryByRole('textbox', { name: 'Search preset entries' })
  ).toBeNull()
  await user.type(
    screen.getByRole('textbox', { name: 'Prompt content' }),
    ' focused'
  )
  await user.click(screen.getByRole('button', { name: 'Show entry list' }))
  expect(screen.getByRole('textbox', { name: 'Prompt content' })).toHaveValue(
    'Full prompt text focused'
  )
})

test('empty preset and empty search retain usable tabs and actions', async () => {
  const user = userEvent.setup()
  render(
    <Editor
      initialValue={JSON.stringify({
        preset: { prompts: [], prompt_order: [] },
      })}
    />
  )
  await user.click(screen.getByRole('button', { name: 'Edit preset' }))
  expect(screen.getByText('No preset entries')).toBeVisible()
  expect(
    screen.getByRole('button', { name: 'Enable visible entries' })
  ).toBeDisabled()
  await user.click(screen.getByRole('tab', { name: 'Embedded regex scripts' }))
  expect(screen.getByText('No embedded regex scripts')).toBeVisible()
})

test('legacy patches stay untouched on open and require explicit conversion before content edits', async () => {
  const user = userEvent.setup()
  render(
    <Editor
      initialValue={JSON.stringify({
        preset,
        patches: [{ identifier: 'main', find: 'Full', replace: 'Converted' }],
      })}
    />
  )
  await user.click(screen.getByRole('button', { name: 'Edit preset' }))
  expect(screen.getByRole('textbox', { name: 'Prompt content' })).toBeDisabled()
  await user.click(
    screen.getByRole('button', { name: 'Convert to editable entries' })
  )
  expect(screen.getByRole('textbox', { name: 'Prompt content' })).toHaveValue(
    'Converted prompt text'
  )
  expect(screen.getByLabelText('Saved config').textContent).toContain('patches')
  await user.click(screen.getByRole('button', { name: 'Apply to channel' }))
  expect(
    JSON.parse(screen.getByLabelText('Saved config').textContent || '{}')
      .patches
  ).toBeUndefined()
})

test('keyboard grab move drop updates ordering while Escape cancels without closing the editor', async () => {
  const user = userEvent.setup()
  const onDrawerChange = vi.fn()
  render(
    <Sheet open onOpenChange={onDrawerChange}>
      <SheetContent>
        <SheetTitle>Channel</SheetTitle>
        <Editor />
      </SheetContent>
    </Sheet>
  )
  await user.click(screen.getByRole('button', { name: 'Edit preset' }))
  const list = screen.getByRole('list', { name: 'Preset entries' })
  // jsdom has no layout. Supply only the browser geometry used by the real DnD sensor.
  vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockImplementation(
    function (this: HTMLElement) {
      const row = this.closest('li')
      const index = row ? [...list.children].indexOf(row) : -1
      const transform = this.style.transform.match(
        /translate3d\([^,]+,\s*([\d.-]+)px/
      )
      const offset = transform ? Number(transform[1]) : 0
      return new DOMRect(0, Math.max(index, 0) * 60 + offset, 280, 60)
    }
  )
  const handle = screen.getByRole('button', {
    name: 'Reorder Main instruction',
  })
  handle.focus()
  await user.keyboard(' ')
  expect(handle).toHaveAttribute('aria-pressed', 'true')
  await user.keyboard('{ArrowDown}')
  await user.keyboard('{Escape}')
  expect(handle).not.toHaveAttribute('aria-pressed', 'true')
  expect(screen.getByRole('dialog', { name: 'Edit preset' })).toBeVisible()
  expect(screen.queryByRole('alertdialog')).toBeNull()
  expect(onDrawerChange).not.toHaveBeenCalled()
  expect(within(list).getAllByRole('listitem')[0]).toHaveTextContent(
    'Main instruction'
  )
  handle.focus()
  await user.keyboard(' ')
  await user.keyboard('{ArrowDown}')
  await user.keyboard(' ')
  await waitFor(() =>
    expect(within(list).getAllByRole('listitem')[0]).toHaveTextContent(
      'Conversation'
    )
  )
  expect(screen.getByRole('textbox', { name: 'Prompt content' })).toHaveValue(
    'Full prompt text'
  )
  await user.click(screen.getByRole('button', { name: 'Apply to channel' }))
  const config = JSON.parse(
    screen.getByLabelText('Saved config').textContent || '{}'
  )
  expect(
    config.preset.prompt_order[1].order.map(
      (item: { identifier: string }) => item.identifier
    )
  ).toEqual(['history', 'main', 'orphan'])
  expect(config.preset.prompt_order[0]).toEqual(preset.prompt_order[0])
})

test('no matches leaves the selected draft intact and disables bulk actions', async () => {
  const user = userEvent.setup()
  render(<Editor />)
  await user.click(screen.getByRole('button', { name: 'Edit preset' }))
  await user.type(
    screen.getByRole('textbox', { name: 'Search preset entries' }),
    'no match'
  )
  expect(screen.getByText('No matching preset entries')).toBeVisible()
  expect(
    screen.getByRole('button', { name: 'Enable visible entries' })
  ).toBeDisabled()
  expect(screen.getByRole('textbox', { name: 'Prompt content' })).toHaveValue(
    'Full prompt text'
  )
})

test('replacing an entry name does not insert its identifier into the new name', async () => {
  const user = userEvent.setup()
  render(<Editor />)
  await user.click(screen.getByRole('button', { name: 'Edit preset' }))
  await user.clear(screen.getByRole('textbox', { name: 'Entry name' }))
  await user.type(
    screen.getByRole('textbox', { name: 'Entry name' }),
    'Revised name'
  )
  await user.click(screen.getByRole('button', { name: 'Apply to channel' }))
  expect(
    JSON.parse(screen.getByLabelText('Saved config').textContent || '{}').preset
      .prompts[0].name
  ).toBe('Revised name')
})

test('preset tabs constrain narrow layouts and scroll independently while retaining all navigation', async () => {
  const user = userEvent.setup()
  render(<Editor />)
  await user.click(screen.getByRole('button', { name: 'Edit preset' }))
  const tabs = screen.getByRole('tablist', { name: 'Preset settings' })
  expect(tabs.parentElement).toHaveClass(
    'min-w-0',
    'max-w-full',
    'overflow-x-auto'
  )
  expect(tabs.parentElement?.parentElement).toHaveClass('min-w-0', 'w-full')
  expect(tabs).toHaveClass('w-max')
  await user.click(screen.getByRole('tab', { name: 'Script compatibility' }))
  expect(
    screen.getByRole('tab', { name: 'Script compatibility' })
  ).toHaveAttribute('aria-selected', 'true')
  await user.click(screen.getByRole('tab', { name: 'Preset entries' }))
  expect(screen.getByRole('textbox', { name: 'Prompt content' })).toBeVisible()
})

test('entry editing keeps a wrapping expanding text area and returning to the list preserves the draft', async () => {
  const user = userEvent.setup()
  render(<Editor />)
  await user.click(screen.getByRole('button', { name: 'Edit preset' }))
  expect(screen.getByRole('dialog', { name: 'Edit preset' })).toHaveClass(
    'h-[85dvh]',
    'overflow-hidden'
  )
  await user.click(screen.getByRole('button', { name: /^Main instruction/ }))
  const content = screen.getByRole('textbox', { name: 'Prompt content' })
  expect(content).toHaveAttribute('wrap', 'soft')
  expect(content).toHaveClass('flex-1', 'max-h-none')
  await user.type(content, ' draft')
  await user.click(screen.getByRole('button', { name: 'Back to entries' }))
  await user.click(screen.getByRole('button', { name: /^Main instruction/ }))
  expect(screen.getByRole('textbox', { name: 'Prompt content' })).toHaveValue(
    'Full prompt text draft'
  )
})
