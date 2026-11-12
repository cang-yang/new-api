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
import { act, cleanup, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'

import {
  AdaptationScopeCheck,
  PresetCapabilityReportSettings,
} from '../preset-capability-report'
import { PresetScriptSettings } from '../preset-script-settings'
import { ReplyAdapterSettings } from '../reply-adapter-settings'

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})

test('JSON conversion is explicit and retained when editing a reply source', async () => {
  const user = userEvent.setup()
  const onChange = vi.fn()
  const draft = {
    reply_adapter: {
      version: 2,
      structured_output: true,
      require_container: true,
      formatted_output: 'client_format',
      containers: [{ name: 'reply', formatter: true, output_kind: 'text' }],
    },
  }
  const view = render(
    <ReplyAdapterSettings config={draft} onChange={onChange} />
  )
  const toggle = screen.getByRole('switch', {
    name: "Convert preset replies to the client's JSON format",
  })
  expect(toggle).toBeChecked()
  await user.click(toggle)
  expect(onChange).toHaveBeenLastCalledWith({
    reply_adapter: { ...draft.reply_adapter, structured_output: false },
  })
  await user.type(screen.getByRole('textbox', { name: 'Reply tool name' }), '2')
  expect(onChange).toHaveBeenLastCalledWith({
    reply_adapter: {
      ...draft.reply_adapter,
      containers: [{ name: 'reply2', formatter: true, output_kind: 'text' }],
    },
  })
  view.rerender(
    <ReplyAdapterSettings config={draft} onChange={onChange} disabled />
  )
  expect(toggle).toHaveAttribute('aria-disabled', 'true')
  onChange.mockClear()
  await user.click(toggle)
  expect(onChange).not.toHaveBeenCalled()
})

test('reply-source suggestions require a local check and apply only to the unchanged draft', async () => {
  const user = userEvent.setup()
  const source = {
    name: 'narrative_packet',
    formatter: true,
    output_kind: 'text',
    source_prompt_id: 'reply',
    source_fingerprint: 'a'.repeat(64),
  }
  const post = vi.spyOn(api, 'post').mockResolvedValue({
    data: {
      success: true,
      data: {
        name: 'Generic preset',
        probe_status: 'completed',
        tool_names: ['narrative_packet', 'history_record'],
        capabilities: [],
        warnings: [],
        entries: [],
        reply_sources: [source],
        history_tools: ['history_record'],
      },
    },
  })
  const onChange = vi.fn()
  const draft = {
    script_runtime: 'spreset-v1',
    preset: { name: 'Generic preset' },
  }
  const view = render(
    <PresetCapabilityReportSettings config={draft} onChange={onChange} />
  )
  expect(post).not.toHaveBeenCalled()
  expect(
    screen.queryByRole('button', { name: 'Use detected reply source' })
  ).toBeNull()
  await user.click(screen.getByRole('button', { name: 'Check capabilities' }))
  expect(
    await screen.findByText(
      'Local check completed. No model was called; test a real request before using this channel.'
    )
  ).toBeVisible()
  await user.click(
    screen.getByRole('button', { name: 'Use detected reply source' })
  )
  expect(onChange).toHaveBeenCalledWith({
    ...draft,
    reply_adapter: { version: 2, containers: [source] },
  })
  view.rerender(
    <PresetCapabilityReportSettings
      config={{ ...draft, entry_overrides: { reply: false } }}
      onChange={onChange}
    />
  )
  expect(
    screen.queryByRole('button', { name: 'Use detected reply source' })
  ).toBeNull()
})

test('request-type checks are local previews and become stale when processing scope changes', async () => {
  const user = userEvent.setup()
  const post = vi.spyOn(api, 'post').mockResolvedValue({
    data: {
      success: true,
      data: {
        fingerprint: 'version',
        model: 'example',
        buffered: false,
        scenarios: [
          {
            kind: 'text',
            status: 'adapted_unverified',
            reason: 'model_output_not_checked',
          },
          { kind: 'tools', status: 'native', reason: 'client_tools' },
        ],
      },
    },
  })
  const view = render(
    <AdaptationScopeCheck scope='plain_chat_only' defaultModel='example' />
  )
  await user.click(screen.getByRole('button', { name: 'Check request types' }))
  expect(
    await screen.findByText('Uses processing; model not verified')
  ).toBeVisible()
  expect(screen.getByText('No preset processing')).toBeVisible()
  expect(post).toHaveBeenCalledWith(
    '/api/channel/sillytavern/inspect',
    expect.objectContaining({
      check_scope: true,
      adaptation_scope: 'plain_chat_only',
      model: 'example',
    })
  )
  view.rerender(
    <AdaptationScopeCheck scope='all_supported' defaultModel='example' />
  )
  expect(screen.queryByText('No preset processing')).toBeNull()
})
const config = {
  preset: {
    extensions: {
      SPreset: { enabled: true },
      tavern_helper: {
        scripts: [
          {
            type: 'script',
            id: 'selection',
            name: 'Example script',
            enabled: true,
          },
        ],
      },
    },
  },
  script_runtime: 'spreset-v1',
  script_modules: { saved: 'source' },
}

test('opening preserves the selected draft and exposes exactly one choice group', () => {
  const onChange = vi.fn()
  render(<PresetScriptSettings config={config} onChange={onChange} />)
  expect(screen.getAllByRole('radiogroup')).toHaveLength(1)
  expect(screen.getAllByRole('radio')).toHaveLength(3)
  expect(
    screen.getByRole('radio', { name: 'Use preset settings (SPreset)' })
  ).toHaveAttribute('aria-checked', 'true')
  expect(
    screen.getByText('Current draft: use preset settings (SPreset)')
  ).toBeVisible()
  expect(screen.queryByText(/Await custom events/)).toBeNull()
  expect(onChange).not.toHaveBeenCalled()
})

test('reply sources are explicit and never enable business-tool coordination', async () => {
  const user = userEvent.setup()
  const onChange = vi.fn()
  const view = render(
    <PresetScriptSettings config={config} onChange={onChange} />
  )
  expect(
    screen.queryByRole('switch', {
      name: 'Coordinate client and preset tool calls',
    })
  ).toBeNull()
  await user.click(screen.getByRole('button', { name: 'Add reply source' }))
  const next = {
    ...config,
    reply_adapter: {
      version: 2,
      containers: [
        { name: '', argument_pointer: '/content', output_kind: 'text' },
      ],
    },
  }
  expect(onChange).toHaveBeenLastCalledWith(next)
  view.rerender(<PresetScriptSettings config={next} onChange={onChange} />)
  expect(screen.getByRole('textbox', { name: 'Reply tool name' })).toBeVisible()
  expect(screen.getByRole('alert')).toHaveTextContent('Check reply sources')
})

test('old business-tool coordination clearly requires review without mutating the draft', () => {
  const onChange = vi.fn()
  render(
    <PresetScriptSettings
      config={{
        ...config,
        tool_protocol: { enabled: true, auxiliary: ['lookup'] },
      }}
      onChange={onChange}
    />
  )
  expect(
    screen.getAllByText('Old tool settings need review').length
  ).toBeGreaterThan(0)
  expect(
    screen.getByRole('button', { name: 'Remove old coordination' })
  ).toBeVisible()
  expect(onChange).not.toHaveBeenCalled()
})

test('switching choices and turning off preserves the preset and downloaded files', async () => {
  const user = userEvent.setup()
  const onChange = vi.fn()
  const view = render(
    <PresetScriptSettings config={config} onChange={onChange} />
  )
  await user.click(screen.getByRole('radio', { name: 'Run bundled scripts' }))
  const next = { ...config, script_runtime: 'tavern-v1' }
  expect(onChange).toHaveBeenLastCalledWith(next)
  view.rerender(<PresetScriptSettings config={next} onChange={onChange} />)
  await user.click(
    screen.getByRole('radio', { name: 'Do not use preset extras' })
  )
  expect(onChange).toHaveBeenLastCalledWith({ ...config, script_runtime: '' })
})

test('author-disabled scripts cannot be selected while an existing missing mode can be turned off', async () => {
  const user = userEvent.setup()
  const onChange = vi.fn()
  const view = render(
    <PresetScriptSettings
      config={{
        preset: {
          extensions: {
            tavern_helper: {
              scripts: [
                { type: 'script', id: 'off-selection', enabled: false },
              ],
            },
          },
        },
      }}
      onChange={onChange}
    />
  )
  const radio = screen.getByRole('radio', { name: 'Run bundled scripts' })
  expect(radio).toHaveAttribute('aria-disabled', 'true')
  await user.click(radio)
  expect(onChange).not.toHaveBeenCalled()
  view.rerender(
    <PresetScriptSettings
      config={{ script_runtime: 'spreset-v1' }}
      onChange={onChange}
    />
  )
  expect(screen.getByRole('alert')).toHaveTextContent(
    'Choose Off or import a preset with SPreset settings.'
  )
  await user.click(
    screen.getByRole('radio', { name: 'Do not use preset extras' })
  )
  expect(onChange).toHaveBeenLastCalledWith({ script_runtime: '' })
})

test('changing the mode cancels pending file checks and discards their result', async () => {
  const user = userEvent.setup()
  let finish!: (value: unknown) => void
  const post = vi.spyOn(api, 'post').mockImplementation(
    () =>
      new Promise((resolve) => {
        finish = resolve
      })
  )
  const onChange = vi.fn()
  render(<PresetScriptSettings config={config} onChange={onChange} />)
  await user.click(
    screen.getByRole('button', { name: 'Check and download files' })
  )
  await user.click(
    screen.getByRole('radio', { name: 'Do not use preset extras' })
  )
  expect(
    (post.mock.calls[0][2] as { signal: AbortSignal }).signal.aborted
  ).toBe(true)
  await act(async () => {
    finish({
      data: { success: true, data: { modules: { stale: '1' }, scripts: [] } },
    })
  })
  expect(onChange).toHaveBeenCalledTimes(1)
  expect(onChange).toHaveBeenLastCalledWith({ ...config, script_runtime: '' })
})

test('locked settings disable all choices and file actions', () => {
  render(<PresetScriptSettings config={config} onChange={vi.fn()} disabled />)
  for (const radio of screen.getAllByRole('radio')) {
    expect(radio).toHaveAttribute('aria-disabled', 'true')
  }
  expect(
    screen.getByRole('button', { name: 'Check and download files' })
  ).toBeDisabled()
})

test('keyboard arrows select one processing mode at a time', async () => {
  const user = userEvent.setup()
  const onChange = vi.fn()
  render(<PresetScriptSettings config={config} onChange={onChange} />)
  screen.getByRole('radio', { name: 'Use preset settings (SPreset)' }).focus()
  await user.keyboard('{ArrowDown}')
  expect(onChange).toHaveBeenLastCalledWith({
    ...config,
    script_runtime: 'tavern-v1',
  })
})

test.each(['', 'spreset-v1', 'tavern-v1'])(
  'checking files preserves the selected mode %s and author flags',
  async (mode) => {
    const user = userEvent.setup()
    const onChange = vi.fn()
    vi.spyOn(api, 'post').mockResolvedValue({
      data: {
        success: true,
        data: {
          modules: { downloaded: 'source' },
          scripts: [
            {
              id: 'selection',
              name: 'Example script',
              enabled: true,
              status: 'prepared',
              issues: [],
            },
          ],
        },
      },
    })
    render(
      <PresetScriptSettings
        config={{ ...config, script_runtime: mode }}
        onChange={onChange}
      />
    )
    await user.click(
      screen.getByRole('button', { name: 'Check and download files' })
    )
    expect(onChange).toHaveBeenCalledWith({
      ...config,
      script_runtime: mode,
      script_modules: { saved: 'source', downloaded: 'source' },
    })
  }
)

test('advanced execution details are hidden initially and revealed by the accessible disclosure', async () => {
  const user = userEvent.setup()
  render(<PresetScriptSettings config={config} onChange={vi.fn()} />)
  const disclosure = screen.getByRole('button', {
    name: 'Advanced details and manual files',
  })
  expect(disclosure).toHaveAttribute('aria-expanded', 'false')
  expect(screen.queryByText('Compatibility boundaries')).toBeNull()
  await user.click(disclosure)
  expect(disclosure).toHaveAttribute('aria-expanded', 'true')
  expect(screen.getByText('Compatibility boundaries')).toBeVisible()
  expect(
    screen.getByRole('button', { name: 'Import module bundle' })
  ).toBeVisible()
})

test('a preset without extras keeps Off available and explains that the normal text still works', () => {
  const onChange = vi.fn()
  render(<PresetScriptSettings config={{ preset: {} }} onChange={onChange} />)
  expect(screen.getAllByRole('radio')).toHaveLength(1)
  expect(
    screen.getByRole('radio', { name: 'Do not use preset extras' })
  ).toHaveAttribute('aria-checked', 'true')
  expect(
    screen.getByText(
      'No supported preset extras were found. The normal preset text can still be used.'
    )
  ).toBeVisible()
  expect(onChange).not.toHaveBeenCalled()
})

test('long script names wrap and file status stays separate from the selected mode', () => {
  const name = 'Long-script-name-'.repeat(30)
  render(
    <PresetScriptSettings
      config={{
        ...config,
        preset: {
          extensions: {
            tavern_helper: {
              scripts: [
                { type: 'script', id: 'long-name', name, enabled: true },
              ],
            },
          },
        },
        script_runtime: 'tavern-v1',
      }}
      onChange={vi.fn()}
    />
  )
  expect(screen.getByText(name)).toHaveClass('min-w-0', 'break-words')
  expect(
    screen.getByRole('region', { name: 'Bundled script files' })
  ).toHaveTextContent('Not checked')
  expect(screen.getByRole('status')).toHaveTextContent(
    'Current draft: run bundled scripts'
  )
})
