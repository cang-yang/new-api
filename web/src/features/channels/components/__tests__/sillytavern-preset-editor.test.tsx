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
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { afterEach, expect, test, vi } from 'vitest'

import { Sheet, SheetContent, SheetTitle } from '@/components/ui/sheet'

import { SillyTavernPresetEditor } from '../sillytavern-preset-editor'

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
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
  await user.selectOptions(screen.getByLabelText('Message role'), 'user')
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
  await user.selectOptions(screen.getByLabelText('Injection placement'), '1')
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
