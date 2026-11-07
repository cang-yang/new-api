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
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'

import type { WorldbookConfig } from '../../lib/worldbook'
import { WorldbookSettings } from '../worldbook-settings'

afterEach(() => vi.restoreAllMocks())

const config: WorldbookConfig = {
  enabled: true,
  scan_depth: 4,
  max_chars: 12000,
  recursive: false,
  injection_mode: 'inherit',
  injection_depth: 0,
  books: [
    {
      id: 'book',
      name: 'Places',
      enabled: true,
      entries: [
        {
          id: 'city',
          name: 'City',
          content: 'City background',
          keys: ['city'],
          secondary_keys: [],
          secondary_logic: 'none',
          constant: false,
          enabled: true,
          priority: 100,
          whole_words: false,
          case_sensitive: false,
        },
      ],
    },
  ],
}
function mount(value = JSON.stringify(config)) {
  const onChange = vi.fn()
  render(
    <QueryClientProvider
      client={
        new QueryClient({ defaultOptions: { mutations: { retry: false } } })
      }
    >
      <WorldbookSettings
        scopeKey='channel-1'
        value={value}
        onChange={onChange}
      />
    </QueryClientProvider>
  )
  return onChange
}

test('worldbook edits apply only on confirmation and preserve other entries', async () => {
  const user = userEvent.setup()
  const onChange = mount()
  await user.click(screen.getByRole('button', { name: 'Manage worldbooks' }))
  const dialog = screen.getByRole('dialog', { name: 'Manage worldbooks' })
  await user.clear(within(dialog).getByLabelText('Entry content'))
  await user.type(
    within(dialog).getByLabelText('Entry content'),
    'Edited background'
  )
  expect(onChange).not.toHaveBeenCalled()
  await user.click(
    within(dialog).getByRole('button', { name: 'Apply changes' })
  )
  expect(
    JSON.parse(onChange.mock.lastCall?.[0]).books[0].entries[0]
  ).toMatchObject({ content: 'Edited background', keys: ['city'] })
})

test('preview uses backend results and disappears when input changes', async () => {
  const user = userEvent.setup()
  const post = vi.spyOn(api, 'post').mockResolvedValue({
    data: {
      success: true,
      data: {
        text: 'Selected background',
        entries: [
          {
            book_id: 'book',
            id: 'city',
            name: 'City',
            status: 'included',
            reason: 'included',
          },
        ],
        warnings: [],
        placement: {
          mode: 'system_prelude',
          index: 0,
          role: 'system',
          fallback: false,
        },
      },
    },
  })
  mount()
  await user.click(screen.getByRole('button', { name: 'Manage worldbooks' }))
  await user.click(screen.getByRole('tab', { name: 'Test matching' }))
  await user.type(screen.getByLabelText('Conversation or request JSON'), 'city')
  await user.click(screen.getByRole('button', { name: 'Run matching test' }))
  expect(await screen.findByText('Selected background')).toBeVisible()
  expect(screen.getByText('Insertion position')).toBeVisible()
  expect(screen.getByText('System prelude')).toBeVisible()
  expect(screen.queryByText(/system_prelude/)).not.toBeInTheDocument()
  expect(post).toHaveBeenCalledWith('/api/channel/worldbook/preview', {
    config,
    request: { messages: [{ role: 'user', content: 'city' }] },
  })
  await user.type(
    screen.getByLabelText('Conversation or request JSON'),
    ' changed'
  )
  await waitFor(() =>
    expect(screen.queryByText('Selected background')).toBeNull()
  )
  post.mockRestore()
})

test('normalized worldbook entries remain directly toggleable', async () => {
  const user = userEvent.setup()
  const normalized = structuredClone(config)
  normalized.books[0].entries[0].normalized_rules = [
    { field: 'probability', action: 'ignored', detail: 'always eligible' },
  ]
  const onChange = mount(JSON.stringify(normalized))
  await user.click(screen.getByRole('button', { name: 'Manage worldbooks' }))
  const toggle = screen.getByRole('switch', { name: 'Enable entry' })
  expect(toggle).toBeEnabled()
  await user.click(toggle)
  await user.click(screen.getByRole('button', { name: 'Apply changes' }))
  expect(
    JSON.parse(onChange.mock.lastCall?.[0]).books[0].entries[0].enabled
  ).toBe(false)
})

test('invalid worldbook entries show the issue and keep the toggle editable', async () => {
  const user = userEvent.setup()
  const invalid = structuredClone(config)
  invalid.books[0].entries[0].issues = ['regex']
  mount(JSON.stringify(invalid))
  await user.click(screen.getByRole('button', { name: 'Manage worldbooks' }))
  expect(screen.getByText(/Invalid data/)).toBeVisible()
  expect(screen.getByText('regex')).toBeVisible()
  expect(screen.getByRole('switch', { name: 'Enable entry' })).toBeEnabled()
})

test('searches worldbook entries and selects one with the keyboard', async () => {
  const user = userEvent.setup()
  const multiple = structuredClone(config)
  multiple.books[0].entries.push({
    ...multiple.books[0].entries[0],
    id: 'tower',
    name: 'Tower district',
    content: 'Tower background',
    keys: ['tower'],
  })
  mount(JSON.stringify(multiple))
  await user.click(screen.getByRole('button', { name: 'Manage worldbooks' }))

  const entry = screen.getByRole('combobox', { name: 'Entry' })
  await user.click(entry)
  await user.type(entry, 'tower')
  expect(screen.getByRole('option', { name: 'Tower district' })).toBeVisible()
  await user.keyboard('{ArrowDown}{Enter}')

  await waitFor(() =>
    expect(screen.getByLabelText('Entry content')).toHaveValue(
      'Tower background'
    )
  )
})
