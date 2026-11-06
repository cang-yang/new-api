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

import { WorldbookSettings } from '../worldbook-settings'

afterEach(() => vi.restoreAllMocks())

const config = {
  enabled: true,
  scan_depth: 4,
  max_chars: 12000,
  recursive: false,
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
      },
    },
  })
  mount()
  await user.click(screen.getByRole('button', { name: 'Manage worldbooks' }))
  await user.click(screen.getByRole('tab', { name: 'Test matching' }))
  await user.type(screen.getByLabelText('Conversation or request JSON'), 'city')
  await user.click(screen.getByRole('button', { name: 'Run matching test' }))
  expect(await screen.findByText('Selected background')).toBeVisible()
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
