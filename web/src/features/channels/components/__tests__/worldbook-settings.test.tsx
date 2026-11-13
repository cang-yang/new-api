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
  await user.click(
    within(dialog).getByRole('switch', { name: 'Required background' })
  )
  await user.click(within(dialog).getByRole('button', { name: 'Edit City' }))
  await user.clear(within(dialog).getByLabelText('Entry content'))
  await user.type(
    within(dialog).getByLabelText('Entry content'),
    'Edited background'
  )
  expect(onChange).not.toHaveBeenCalled()
  await user.click(
    within(dialog).getByRole('button', { name: 'Apply changes' })
  )
  expect(JSON.parse(onChange.mock.lastCall?.[0]).books[0].required).toBe(true)
  expect(
    JSON.parse(onChange.mock.lastCall?.[0]).books[0].entries[0]
  ).toMatchObject({ content: 'Edited background', keys: ['city'] })
})

test('preview uses backend results and disappears when input changes', async () => {
  const user = userEvent.setup()
  const post = vi
    .spyOn(api, 'post')
    .mockImplementation(async (_url, payload) => ({
      data: {
        success: true,
        data: {
          draft_revision: (payload as { draft_revision: string })
            .draft_revision,
          text: 'Selected background',
          entries: [
            {
              book_id: 'book',
              id: 'city',
              name: 'City',
              status: 'included',
              reason: 'included',
              order: 1,
              placement: {
                mode: 'system_prelude',
                index: 2,
                role: 'system',
                fallback: true,
              },
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
    }))
  mount()
  await user.click(screen.getByRole('button', { name: 'Manage worldbooks' }))
  await user.click(screen.getByRole('tab', { name: 'Test matching' }))
  await user.type(screen.getByLabelText('Conversation or request JSON'), 'city')
  await user.click(screen.getByRole('button', { name: 'Run matching test' }))
  expect(await screen.findByText('Selected background')).toBeVisible()
  expect(screen.getByText('Insertion position')).toBeVisible()
  expect(screen.getByText('System prelude')).toBeVisible()
  expect(screen.getByText(/Message position 3 \(System\)/)).toBeVisible()
  expect(screen.getByText(/Fallback applied/)).toBeVisible()
  expect(screen.getByText(/#1 City/)).toBeVisible()
  expect(screen.queryByText(/system_prelude/)).not.toBeInTheDocument()
  expect(post).toHaveBeenCalledWith(
    '/api/channel/worldbook/preview',
    expect.objectContaining({
      config,
      request: { messages: [{ role: 'user', content: 'city' }] },
      draft_revision: expect.any(String),
    })
  )
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
  const toggle = screen.getByRole('switch', { name: 'Enable City' })
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
  await user.click(screen.getByRole('button', { name: 'Edit City' }))
  expect(screen.getByText(/Invalid data/)).toBeVisible()
  expect(screen.getByText('regex')).toBeVisible()
  expect(screen.getByRole('switch', { name: 'Enable entry' })).toBeEnabled()
})

test('an invalid worldbook entry cannot be enabled from the editor', async () => {
  const user = userEvent.setup()
  const invalid = structuredClone(config)
  invalid.books[0].entries[0].enabled = false
  invalid.books[0].entries[0].issues = ['invalid regular expression']
  const onChange = mount(JSON.stringify(invalid))
  await user.click(screen.getByRole('button', { name: 'Manage worldbooks' }))
  await user.click(screen.getByRole('button', { name: 'Edit City' }))
  const toggle = screen.getByRole('switch', { name: 'Enable entry' })
  expect(toggle).toBeEnabled()
  await user.click(toggle)
  expect(toggle).not.toBeChecked()
  await user.click(screen.getByRole('button', { name: 'Apply changes' }))
  const saved = JSON.parse(onChange.mock.lastCall?.[0] ?? '{}')
  expect(saved.books?.[0]?.entries?.[0]?.enabled).toBe(false)
})

test('searches the overview and opens an entry with the keyboard', async () => {
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

  await user.type(
    screen.getByRole('textbox', { name: 'Search entries...' }),
    'tower'
  )
  expect(
    screen.queryByRole('button', { name: 'Edit City' })
  ).not.toBeInTheDocument()
  screen.getByRole('button', { name: 'Edit Tower district' }).focus()
  await user.keyboard('{Enter}')

  await waitFor(() =>
    expect(screen.getByLabelText('Entry content')).toHaveValue(
      'Tower background'
    )
  )
})

test('overview shows enabled and disabled entries and toggles without opening the editor', async () => {
  const user = userEvent.setup()
  const multiple = structuredClone(config)
  multiple.books[0].entries.push({
    ...multiple.books[0].entries[0],
    id: 'tower',
    name: 'Tower',
    enabled: false,
  })
  const onChange = mount(JSON.stringify(multiple))
  await user.click(screen.getByRole('button', { name: 'Manage worldbooks' }))
  expect(screen.getByRole('switch', { name: 'Enable City' })).toBeChecked()
  expect(screen.getByRole('switch', { name: 'Enable Tower' })).not.toBeChecked()
  await user.click(screen.getByRole('switch', { name: 'Enable Tower' }))
  await user.click(screen.getByRole('button', { name: 'Apply changes' }))
  expect(
    JSON.parse(onChange.mock.lastCall?.[0]).books[0].entries[1].enabled
  ).toBe(true)
})

test('entry position and search survive returning from the editor', async () => {
  const user = userEvent.setup()
  const onChange = mount()
  await user.click(screen.getByRole('button', { name: 'Manage worldbooks' }))
  await user.type(
    screen.getByRole('textbox', { name: 'Search entries...' }),
    'City'
  )
  await user.click(screen.getByRole('button', { name: 'Edit City' }))
  await user.click(
    screen.getByRole('combobox', { name: 'Entry insertion position' })
  )
  await user.click(
    screen.getByRole('option', { name: 'Before the latest user message' })
  )
  await user.click(screen.getByRole('button', { name: 'Back to all entries' }))
  expect(
    screen.getByRole('textbox', { name: 'Search entries...' })
  ).toHaveValue('City')
  expect(screen.getByText('Before the latest user message')).toBeVisible()
  await user.click(screen.getByRole('button', { name: 'Apply changes' }))
  expect(
    JSON.parse(onChange.mock.lastCall?.[0]).books[0].entries[0].injection_mode
  ).toBe('before_last_user')
})

test('import summary uses current issues rather than legacy blocking warning prose', async () => {
  const user = userEvent.setup()
  const old = structuredClone(config)
  old.books[0].warnings = [
    'Entry 0 is blocked by unsupported rules: depth, groupWeight',
  ]
  old.books[0].entries[0].normalized_rules = [
    {
      field: 'groupWeight',
      action: 'ignored',
      detail: 'field is not used by the gateway',
    },
  ]
  mount(JSON.stringify(old))
  await user.click(screen.getByRole('button', { name: 'Manage worldbooks' }))
  expect(screen.queryByText(/blocked by unsupported/)).not.toBeInTheDocument()
  expect(
    screen.getByText(
      'Some advanced Tavern options are not used. Keyword matching remains available.'
    )
  ).toBeVisible()
})

test('import summary reports attention entries and filters to them', async () => {
  const user = userEvent.setup()
  const multiple = structuredClone(config)
  multiple.books[0].entries.push({
    ...multiple.books[0].entries[0],
    id: 'broken',
    name: 'Broken entry',
    issues: ['regex'],
  })
  mount(JSON.stringify(multiple))
  await user.click(screen.getByRole('button', { name: 'Manage worldbooks' }))

  expect(screen.getByText('Imported 2 entries.')).toBeVisible()
  expect(screen.getByText('1 need attention.')).toBeVisible()
  await user.click(
    screen.getByRole('button', { name: 'Review entries needing attention' })
  )
  expect(
    screen.queryByRole('button', { name: 'Edit City' })
  ).not.toBeInTheDocument()
  expect(
    screen.getByRole('button', { name: 'Edit Broken entry' })
  ).toBeVisible()
})

test('enabling an entry with unsupported behavior asks once and keeps the supported parts', async () => {
  const user = userEvent.setup()
  const risky = structuredClone(config)
  const entry = risky.books[0].entries[0]
  entry.enabled = false
  entry.normalized_rules = [
    {
      field: 'sticky',
      action: 'risk',
      detail: 'activation timing cannot be reproduced',
      fingerprint: 'source-revision',
    },
  ]
  const onChange = mount(JSON.stringify(risky))
  await user.click(screen.getByRole('button', { name: 'Manage worldbooks' }))
  expect(screen.getByText('1 need review before they can run.')).toBeVisible()
  const toggle = screen.getByRole('switch', { name: 'Enable City' })
  expect(toggle).toHaveAttribute('aria-checked', 'false')
  await user.click(toggle)
  expect(screen.getByRole('alertdialog')).toBeVisible()
  expect(screen.getByText(/Sticky activation/)).toBeVisible()
  await user.click(screen.getByRole('button', { name: 'Confirm and enable' }))
  await user.click(screen.getByRole('button', { name: 'Apply changes' }))
  expect(
    JSON.parse(onChange.mock.lastCall?.[0]).books[0].entries[0]
  ).toMatchObject({
    enabled: true,
    normalized_rules: entry.normalized_rules,
    accepted_risks: { sticky: 'source-revision|inherit|0|1' },
  })
})

test('import summary confirms every risky entry at once', async () => {
  const user = userEvent.setup()
  const risky = structuredClone(config)
  const entry = risky.books[0].entries[0]
  entry.enabled = false
  entry.normalized_rules = [
    {
      field: 'probability',
      action: 'risk',
      detail: 'random activation cannot be reproduced',
      fingerprint: 'source-revision',
    },
  ]
  const onChange = mount(JSON.stringify(risky))
  await user.click(screen.getByRole('button', { name: 'Manage worldbooks' }))
  await user.click(
    screen.getByRole('button', { name: 'Confirm and enable 1 entries' })
  )
  await user.click(screen.getByRole('button', { name: 'Apply changes' }))
  expect(
    JSON.parse(onChange.mock.lastCall?.[0]).books[0].entries[0]
  ).toMatchObject({
    enabled: true,
    normalized_rules: entry.normalized_rules,
    accepted_risks: { probability: 'source-revision|inherit|0|1' },
  })
})

test('overview clearly reports when the book cannot run', async () => {
  const user = userEvent.setup()
  const disabled = structuredClone(config)
  disabled.enabled = false
  mount(JSON.stringify(disabled))
  await user.click(screen.getByRole('button', { name: 'Manage worldbooks' }))

  expect(
    screen.getByText(
      'Worldbooks or this book are turned off. Entry switches are preserved but no entries will run.'
    )
  ).toBeVisible()
})
