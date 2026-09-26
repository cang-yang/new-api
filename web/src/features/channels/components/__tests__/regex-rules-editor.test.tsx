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
import { useState } from 'react'
import { afterEach, expect, test, vi } from 'vitest'

import { RegexRulesEditor } from '../regex-rules-editor'

afterEach(cleanup)

test('stored rules inherit strict behavior until explicitly changed and keep unknown options', async () => {
  const user = userEvent.setup()
  render(
    <Editor
      initialValue={JSON.stringify({
        mode: 'rules',
        rules: [],
        custom_option: { keep: true },
      })}
    />
  )
  expect(screen.getByLabelText('Regex failure policy')).toHaveValue('legacy')
  await user.click(screen.getByRole('button', { name: 'Add regex rule' }))
  let saved = JSON.parse(
    screen.getByLabelText('Saved rules').textContent || '{}'
  )
  expect(saved.failure_policy).toBeUndefined()
  await user.selectOptions(
    screen.getByLabelText('Regex failure policy'),
    'passthrough'
  )
  expect(
    screen.getByText(
      'On processing errors or limits, retain the entire original response or request. Not suitable for privacy redaction.'
    )
  ).toBeVisible()
  saved = JSON.parse(screen.getByLabelText('Saved rules').textContent || '{}')
  expect(saved).toMatchObject({
    failure_policy: 'passthrough',
    custom_option: { keep: true },
  })
  await user.selectOptions(
    screen.getByLabelText('Regex failure policy'),
    'error'
  )
  expect(
    screen.getByText(
      'Processing errors stop the request. An upstream-generated response may be lost and may still be billed.'
    )
  ).toBeVisible()
  await user.selectOptions(
    screen.getByLabelText('Regex failure policy'),
    'legacy'
  )
  expect(
    JSON.parse(screen.getByLabelText('Saved rules').textContent || '{}')
      .failure_policy
  ).toBeUndefined()
})

test('oversized imports are rejected before reading the file', async () => {
  const user = userEvent.setup()
  render(<Editor />)
  const file = new File(['{}'], 'regex.json', { type: 'application/json' })
  const read = vi.fn().mockResolvedValue('{}')
  Object.defineProperty(file, 'size', { value: 6 * 1024 * 1024 + 1 })
  Object.defineProperty(file, 'text', { value: read })
  await user.upload(
    screen.getByLabelText('Import SillyTavern regex JSON'),
    file
  )
  expect(
    screen.getByText('Regex import must be 6 MiB or smaller.')
  ).toBeVisible()
  expect(read).not.toHaveBeenCalled()
})

test('pending import locks editing and never overwrites newer external configuration', async () => {
  const user = userEvent.setup()
  let finish: (value: string) => void = () => {}
  const pending = new Promise<string>((resolve) => {
    finish = resolve
  })
  const onChange = vi.fn()
  const view = render(<RegexRulesEditor value='' onChange={onChange} />)
  const file = new File(['{}'], 'regex.json', { type: 'application/json' })
  Object.defineProperty(file, 'text', { value: () => pending })
  await user.upload(
    screen.getByLabelText('Import SillyTavern regex JSON'),
    file
  )
  expect(screen.getByRole('button', { name: 'Add regex rule' })).toBeDisabled()
  view.rerender(
    <RegexRulesEditor
      value={JSON.stringify({ mode: 'rules', rules: [], models: ['latest'] })}
      onChange={onChange}
    />
  )
  await act(async () => {
    finish(JSON.stringify({ findRegex: '/x/g', placement: [2] }))
    await pending
  })
  expect(onChange).not.toHaveBeenCalled()
  expect(
    screen.getByText(
      'Configuration changed while importing. Import again to keep the latest edits.'
    )
  ).toBeVisible()
  expect(
    screen.getByRole('button', { name: 'Add regex rule' })
  ).not.toBeDisabled()
})

test('keyboard opens rule scope and depth changes stay editable while bounds are reversed', async () => {
  const user = userEvent.setup()
  render(
    <Editor
      initialValue={JSON.stringify({
        mode: 'rules',
        rules: [
          {
            id: 'a',
            stage: 'send',
            action: 'replace',
            pattern: 'x',
            replacement: '',
          },
        ],
      })}
    />
  )
  const scope = screen.getByRole('button', {
    name: 'Rule scope and unmatched text',
  })
  expect(scope).toHaveAttribute('aria-expanded', 'false')
  scope.focus()
  await user.keyboard('{Enter}')
  expect(scope).toHaveAttribute('aria-expanded', 'true')
  await user.type(screen.getByLabelText('Minimum depth'), '5')
  await user.type(screen.getByLabelText('Maximum depth'), '2')
  expect(screen.getByLabelText('Minimum depth')).toHaveValue(5)
  await user.clear(screen.getByLabelText('Maximum depth'))
  await user.type(screen.getByLabelText('Maximum depth'), '8')
  expect(
    JSON.parse(screen.getByLabelText('Saved rules').textContent || '{}')
      .rules[0]
  ).toMatchObject({ min_depth: 5, max_depth: 8 })
})
function Editor(props: { initialValue?: string }) {
  const [value, setValue] = useState(props.initialValue || '')
  return (
    <>
      <RegexRulesEditor value={value} onChange={setValue} />
      <output aria-label='Saved rules'>{value}</output>
    </>
  )
}
test('new rules allow deletion by empty replacement and sending stays off until explicitly enabled', async () => {
  const user = userEvent.setup()
  render(<Editor />)
  await user.click(screen.getByRole('button', { name: 'Add regex rule' }))
  await user.type(
    screen.getByRole('textbox', { name: 'Find pattern' }),
    'secret'
  )
  expect(
    JSON.parse(screen.getByLabelText('Saved rules').textContent || '{}')
  ).toMatchObject({
    mode: 'rules',
    failure_policy: 'passthrough',
    enable_send: false,
    rules: [{ pattern: 'secret', replacement: '', stage: 'receive' }],
  })
  await user.selectOptions(screen.getByLabelText('Direction'), 'send')
  expect(
    screen.queryByText('Applicable receive rules buffer streaming responses.')
  ).toBeNull()
  await user.click(
    screen.getByRole('switch', { name: 'Enable send-side regex' })
  )
  expect(
    JSON.parse(screen.getByLabelText('Saved rules').textContent || '{}')
      .enable_send
  ).toBe(true)
})
test('legacy extraction remains intact until explicit conversion preserves trim and missing-match behavior', async () => {
  const user = userEvent.setup()
  const legacy = {
    mode: 'regex_extract',
    pattern: '(?s)<body>(.*?)</body>',
    trim_capture: true,
    missing_match: 'empty',
    models: ['test'],
  }
  render(<Editor initialValue={JSON.stringify(legacy)} />)
  expect(
    JSON.parse(screen.getByLabelText('Saved rules').textContent || '{}')
  ).toEqual(legacy)
  await user.click(
    screen.getByRole('button', { name: 'Convert legacy filter to rules' })
  )
  expect(
    JSON.parse(screen.getByLabelText('Saved rules').textContent || '{}')
  ).toMatchObject({
    mode: 'rules',
    enable_send: false,
    models: ['test'],
    rules: [
      {
        stage: 'receive',
        action: 'extract',
        pattern: legacy.pattern,
        replacement: '$1',
        trim_capture: true,
        missing_match: 'empty',
      },
    ],
  })
})
test('ordered rules can move, disable and remove without discarding the other rule', async () => {
  const user = userEvent.setup()
  render(<Editor />)
  await user.click(screen.getByRole('button', { name: 'Add regex rule' }))
  await user.type(screen.getByLabelText('Rule name'), 'First')
  await user.click(screen.getByRole('button', { name: 'Add regex rule' }))
  await user.click(screen.getAllByRole('button', { name: 'Move rule up' })[1])
  await user.click(screen.getAllByRole('switch', { name: 'Enable rule' })[0])
  await user.click(screen.getAllByRole('button', { name: 'Remove rule' })[1])
  expect(
    JSON.parse(screen.getByLabelText('Saved rules').textContent || '{}').rules
  ).toMatchObject([{ disabled: true }])
  expect(screen.getAllByLabelText('Rule name')).toHaveLength(1)
})
