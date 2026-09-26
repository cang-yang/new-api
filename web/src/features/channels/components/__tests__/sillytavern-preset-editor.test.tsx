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
import { cleanup, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { afterEach, expect, test } from 'vitest'

import { SillyTavernPresetEditor } from '../sillytavern-preset-editor'

afterEach(cleanup)

test('preset failure policy remains inherited on unrelated changes and explicit changes preserve preset options', async () => {
  const user = userEvent.setup()
  render(
    <Editor initialValue={JSON.stringify({ preset, custom_option: 'keep' })} />
  )
  expect(screen.getByLabelText('Embedded regex failure policy')).toHaveValue(
    'legacy'
  )
  await user.click(
    screen.getByRole('switch', { name: 'Enable embedded send-side regex' })
  )
  expect(
    JSON.parse(screen.getByLabelText('Saved config').textContent || '{}')
      .regex_failure_policy
  ).toBeUndefined()
  await user.selectOptions(
    screen.getByLabelText('Embedded regex failure policy'),
    'passthrough'
  )
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
  const saved = JSON.parse(
    screen.getByLabelText('Saved config').textContent || '{}'
  )
  expect(saved.enable_send_regex).toBe(true)
  expect(saved.enable_embedded_regex).toBeUndefined()
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
  const toggle = screen.getByRole('switch', {
    name: 'Enable preset entry Main instruction',
  })
  expect(toggle.getAttribute('aria-checked')).toBe('true')
  await user.click(toggle)
  const saved = JSON.parse(
    screen.getByLabelText('Saved config').textContent || '{}'
  )
  expect(saved.entry_overrides).toEqual({ main: false })
  expect(saved.preset).toEqual(preset)
  await user.click(
    screen.getByRole('button', { name: 'Restore preset defaults' })
  )
  expect(toggle.getAttribute('aria-checked')).toBe('true')
})

test('unordered prompts are visible disabled and can be enabled without changing the imported order', async () => {
  const user = userEvent.setup()
  render(<Editor />)
  const toggle = screen.getByRole('switch', {
    name: 'Enable preset entry Unordered instruction',
  })
  expect(toggle.getAttribute('aria-checked')).toBe('false')
  await user.click(toggle)
  const saved = JSON.parse(
    screen.getByLabelText('Saved config').textContent || '{}'
  )
  expect(saved.entry_overrides).toEqual({ orphan: true })
  expect(saved.preset).toEqual(preset)
})

test('filtering limits bulk toggles to visible entries and full prompt expands inline', async () => {
  const user = userEvent.setup()
  render(<Editor />)
  await user.click(screen.getByRole('button', { name: /Main instruction/ }))
  expect(screen.getByText('Full prompt text')).toBeTruthy()
  await user.type(
    screen.getByRole('textbox', { name: 'Search preset entries' }),
    'Conversation'
  )
  await user.click(
    screen.getByRole('button', { name: 'Enable visible entries' })
  )
  expect(
    JSON.parse(screen.getByLabelText('Saved config').textContent || '{}')
      .entry_overrides
  ).toEqual({ history: true })
})

test('legacy saved presets keep regex disabled until explicitly enabled and sending scripts remain disabled', async () => {
  const user = userEvent.setup()
  render(<Editor />)
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
  await user.click(
    screen.getByRole('switch', { name: 'Enable preset entry Main instruction' })
  )
  const saved = JSON.parse(
    screen.getByLabelText('Saved config').textContent || '{}'
  )
  expect(saved.enable_embedded_regex).toBe(false)
  expect(saved.future_option).toEqual({ mode: 'preserve' })
  expect(
    screen
      .getByRole('switch', { name: 'Enable embedded regex scripts' })
      .getAttribute('aria-checked')
  ).toBe('false')
})

test('locked channel prevents preset and regex mutations', () => {
  render(<Editor disabled />)
  for (const toggle of screen.getAllByRole('switch')) {
    expect(toggle.getAttribute('aria-disabled')).toBe('true')
  }
  expect(
    screen
      .getByRole('button', { name: 'Enable visible entries' })
      .hasAttribute('disabled')
  ).toBe(true)
})

test('HTML-generating receive rules are marked and remain independently controllable', async () => {
  const user = userEvent.setup()
  render(<Editor />)
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
  expect(
    JSON.parse(screen.getByLabelText('Saved config').textContent || '{}')
      .regex_overrides
  ).toEqual({ html: false })
})

test('custom macro values and time zone persist without changing the preset', async () => {
  const user = userEvent.setup()
  render(<Editor />)
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
  const config = JSON.parse(
    screen.getByLabelText('Saved config').textContent || '{}'
  )
  expect(config.macro_values).toEqual({ storyTime: 'Spring morning' })
  expect(config.time_zone).toBe('Asia/Shanghai')
  expect(config.preset).toEqual(preset)
  await user.click(
    screen.getByRole('button', { name: 'Remove macro storyTime' })
  )
  expect(
    screen.queryByRole('textbox', { name: 'Value for macro storyTime' })
  ).toBeNull()
})
