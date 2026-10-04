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
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { afterEach, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'

import { RegexSettings } from '../regex-settings'

const config = JSON.stringify({
  mode: 'rules',
  enable_send: true,
  rules: [
    {
      id: 'one',
      name: 'First rule',
      stage: 'receive',
      action: 'replace',
      pattern: '/x/g',
      replacement: 'y',
    },
  ],
})
afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})

function Harness(props: {
  value?: string
  disabled?: boolean
  scopeKey?: string
}) {
  const [value, setValue] = useState(props.value ?? config)
  const [client] = useState(
    () => new QueryClient({ defaultOptions: { mutations: { retry: false } } })
  )
  return (
    <QueryClientProvider client={client}>
      <RegexSettings
        scopeKey={props.scopeKey ?? 'one'}
        value={value}
        onChange={setValue}
        disabled={props.disabled}
      />
      <output aria-label='Saved configuration'>{value}</output>
    </QueryClientProvider>
  )
}

test('closed settings show a compact summary and open rules only on demand', async () => {
  const user = userEvent.setup()
  render(<Harness />)
  expect(screen.getByText('1 of 1 rules enabled')).toBeVisible()
  expect(screen.queryByRole('button', { name: 'Add regex rule' })).toBeNull()
  await user.click(screen.getByRole('button', { name: 'Edit regex' }))
  expect(screen.getByRole('dialog', { name: 'Edit regex' })).toBeVisible()
  expect(screen.getByRole('tabpanel', { name: 'Regex rules' })).toHaveClass(
    'relative',
    'min-h-0',
    'overflow-y-auto'
  )
  expect(
    screen.getByRole('button', { name: 'Execution settings' })
  ).toHaveAttribute('aria-expanded', 'false')
  expect(screen.queryByLabelText('Regex failure policy')).toBeNull()
  expect(screen.getByRole('button', { name: 'Edit rule 1' })).toHaveAttribute(
    'aria-expanded',
    'false'
  )
  expect(screen.queryByLabelText('Find pattern')).toBeNull()
  await user.click(screen.getByRole('button', { name: 'Edit rule 1' }))
  expect(screen.getByLabelText('Find pattern')).toHaveValue('/x/g')
})

test('rule edits are staged until applied and applying returns focus to the trigger', async () => {
  const user = userEvent.setup()
  render(<Harness />)
  await user.click(screen.getByRole('button', { name: 'Edit regex' }))
  await user.click(screen.getByRole('button', { name: 'Edit rule 1' }))
  await user.clear(screen.getByLabelText('Rule name'))
  await user.type(screen.getByLabelText('Rule name'), 'Edited')
  expect(screen.getByLabelText('Saved configuration')).toHaveTextContent(
    'First rule'
  )
  await user.click(screen.getByRole('button', { name: 'Apply to channel' }))
  expect(screen.getByLabelText('Saved configuration')).toHaveTextContent(
    'Edited'
  )
  await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
  await waitFor(() =>
    expect(screen.getByRole('button', { name: 'Edit regex' })).toHaveFocus()
  )
})

test('Escape with unapplied changes confirms discard without touching the channel', async () => {
  const user = userEvent.setup()
  render(<Harness value='' />)
  await user.click(screen.getByRole('button', { name: 'Edit regex' }))
  await user.click(screen.getByRole('button', { name: 'Add regex rule' }))
  expect(screen.getByLabelText('Find pattern')).toBeVisible()
  await user.keyboard('{Escape}')
  expect(
    screen.getByRole('alertdialog', { name: 'Discard regex changes?' })
  ).toBeVisible()
  await user.click(screen.getByRole('button', { name: 'Keep editing' }))
  expect(screen.getByRole('dialog', { name: 'Edit regex' })).toBeVisible()
  await user.click(screen.getByRole('button', { name: 'Cancel' }))
  await user.click(screen.getByRole('button', { name: 'Discard changes' }))
  await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
  expect(screen.getByLabelText('Saved configuration')).toHaveTextContent('')
})

test('advanced JSON cannot be applied when invalid and untouched metadata survives applying', async () => {
  const user = userEvent.setup()
  const value = JSON.stringify({
    mode: 'rules',
    rules: [],
    future_metadata: { keep: true },
  })
  render(<Harness value={value} />)
  await user.click(screen.getByRole('button', { name: 'Edit regex' }))
  await user.click(screen.getByRole('button', { name: 'Advanced regex JSON' }))
  const json = screen.getByRole('textbox', { name: 'Response Text Filter' })
  await user.clear(json)
  await user.type(json, 'invalid')
  expect(
    screen.getByRole('button', { name: 'Apply to channel' })
  ).toBeDisabled()
  fireEvent.change(json, { target: { value } })
  await user.click(screen.getByRole('button', { name: 'Apply to channel' }))
  expect(
    JSON.parse(screen.getByLabelText('Saved configuration').textContent || '{}')
  ).toMatchObject({ future_metadata: { keep: true } })
})

test('testing a staged rule calls preview only and switching tabs retains the sample', async () => {
  const user = userEvent.setup()
  const post = vi.spyOn(api, 'post').mockResolvedValue({
    data: { success: true, data: { output: 'y', steps: [] } },
  })
  render(<Harness />)
  await user.click(screen.getByRole('button', { name: 'Edit regex' }))
  await user.click(screen.getByRole('tab', { name: 'Regex playground' }))
  await user.type(screen.getByLabelText('Test text'), 'x')
  await user.click(screen.getByRole('button', { name: 'Run regex test' }))
  expect(await screen.findByLabelText('Transformed text')).toHaveValue('y')
  expect(post).toHaveBeenCalledWith(
    '/api/channel/regex/preview',
    expect.objectContaining({ text: 'x', config: JSON.parse(config) })
  )
  await user.click(screen.getByRole('tab', { name: 'Regex rules' }))
  await user.click(screen.getByRole('tab', { name: 'Regex playground' }))
  expect(screen.getByLabelText('Test text')).toHaveValue('x')
  expect(screen.getByLabelText('Saved configuration').textContent).toBe(config)
})

test('scope changes close stale drafts and disabled settings cannot open', async () => {
  const user = userEvent.setup()
  const onChange = vi.fn()
  const client = new QueryClient()
  const view = render(
    <RegexSettings scopeKey='one' value={config} onChange={onChange} />,
    {
      wrapper: ({ children }) => (
        <QueryClientProvider client={client}>{children}</QueryClientProvider>
      ),
    }
  )
  await user.click(screen.getByRole('button', { name: 'Edit regex' }))
  await user.click(screen.getByRole('button', { name: 'Clear' }))
  view.rerender(
    <RegexSettings scopeKey='two' value='' onChange={onChange} disabled />
  )
  await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
  expect(screen.getByRole('button', { name: 'Edit regex' })).toBeDisabled()
  expect(onChange).not.toHaveBeenCalled()
})

test('external changes during editing lock the stale draft instead of overwriting them', async () => {
  const user = userEvent.setup()
  const onChange = vi.fn()
  const client = new QueryClient()
  const view = render(
    <RegexSettings scopeKey='one' value={config} onChange={onChange} />,
    {
      wrapper: ({ children }) => (
        <QueryClientProvider client={client}>{children}</QueryClientProvider>
      ),
    }
  )
  await user.click(screen.getByRole('button', { name: 'Edit regex' }))
  view.rerender(<RegexSettings scopeKey='one' value='' onChange={onChange} />)
  expect(
    screen.getByRole('button', { name: 'Apply to channel' })
  ).toBeDisabled()
  expect(screen.getByRole('button', { name: 'Add regex rule' })).toBeDisabled()
  expect(onChange).not.toHaveBeenCalled()
})
