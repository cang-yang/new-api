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
import { act, cleanup, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'

import { RegexPlayground } from '../regex-playground'

afterEach(cleanup)

test('fallback shows the original text and never labels reverted steps as applied', async () => {
  const user = userEvent.setup()
  vi.spyOn(api, 'post').mockResolvedValue({
    data: {
      success: true,
      data: {
        output: 'original',
        rolled_back: true,
        warnings: ['execution limit'],
        steps: [
          {
            name: 'earlier rule',
            before: 'original',
            after: 'partial',
            changed: true,
          },
        ],
      },
    },
  })
  render(<Playground />)
  await user.click(screen.getByRole('button', { name: 'Run regex test' }))
  expect(await screen.findByLabelText('Original text (fallback)')).toHaveValue(
    'original'
  )
  expect(
    screen.getByText(
      'Processing failed. All changes were reverted according to the failure policy.'
    )
  ).toBeVisible()
  expect(screen.queryByText('Applied rules, in order')).toBeNull()
  expect(screen.getByText('Reverted')).toBeVisible()
})

function Playground(props: { scopeKey?: string; value?: string }) {
  return (
    <QueryClientProvider
      client={
        new QueryClient({ defaultOptions: { mutations: { retry: false } } })
      }
    >
      <RegexPlayground
        scopeKey={props.scopeKey || 'a'}
        value={props.value || '{"mode":"rules","rules":[]}'}
        presetValue='{"enable_embedded_regex":true}'
      />
    </QueryClientProvider>
  )
}

test('preview sends unsaved channel and preset rules and shows server output and applied rules', async () => {
  const user = userEvent.setup()
  const post = vi.spyOn(api, 'post').mockResolvedValue({
    data: {
      success: true,
      data: {
        output: 'hello',
        steps: [
          {
            name: 'Extract body',
            before: '<body>hello</body>',
            after: 'hello',
            changed: true,
          },
        ],
      },
    },
  })
  render(<Playground />)
  await user.type(screen.getByLabelText('Test text'), '<body>hello</body>')
  await user.click(screen.getByRole('button', { name: 'Send' }))
  await user.type(screen.getByLabelText('Test model'), 'model-a')
  await user.click(screen.getByRole('button', { name: 'Run regex test' }))
  expect(await screen.findByLabelText('Transformed text')).toHaveValue('hello')
  expect(screen.getByText(/Extract body/)).toBeVisible()
  expect(post).toHaveBeenCalledWith(
    '/api/channel/regex/preview',
    expect.objectContaining({
      config: { mode: 'rules', rules: [] },
      preset: { enable_embedded_regex: true },
      text: '<body>hello</body>',
      stage: 'send',
      role: 'user',
      depth: 0,
      model: 'model-a',
    })
  )
})

test('preview business failures show server details and never display a successful result', async () => {
  const user = userEvent.setup()
  vi.spyOn(api, 'post').mockResolvedValue({
    data: { success: false, message: 'Invalid regular expression: [' },
  })
  render(<Playground />)
  await user.click(screen.getByRole('button', { name: 'Run regex test' }))
  expect(await screen.findByRole('alert')).toHaveTextContent(
    'Invalid regular expression: ['
  )
  expect(screen.queryByLabelText('Transformed text')).toBeNull()
})

test('invalid advanced JSON is reported locally before a test request is sent', async () => {
  const user = userEvent.setup()
  const post = vi.spyOn(api, 'post')
  render(<Playground value='{' />)
  await user.click(screen.getByRole('button', { name: 'Run regex test' }))
  expect(await screen.findByRole('alert')).toHaveTextContent(
    'Fix the regex or preset JSON before testing.'
  )
  expect(post).not.toHaveBeenCalled()
})

test('changing drafts clears stale output and switching channels drops pending results and sample text', async () => {
  const user = userEvent.setup()
  let finish!: (value: unknown) => void
  const pending = new Promise((resolve) => {
    finish = resolve
  })
  vi.spyOn(api, 'post').mockImplementation(
    () => pending as ReturnType<typeof api.post>
  )
  const view = render(<Playground />)
  await user.type(screen.getByLabelText('Test text'), 'private sample')
  await user.click(screen.getByRole('button', { name: 'Run regex test' }))
  expect(screen.getByRole('button', { name: 'Testing…' })).toBeDisabled()
  view.rerender(<Playground scopeKey='b' />)
  await act(async () => {
    finish({
      data: { success: true, data: { output: 'old result', steps: [] } },
    })
    await pending
  })
  expect(screen.getByLabelText('Test text')).toHaveValue('')
  expect(screen.queryByLabelText('Transformed text')).toBeNull()
})

test('successful empty output remains visible and editing text marks the preview stale', async () => {
  const user = userEvent.setup()
  vi.spyOn(api, 'post').mockResolvedValue({
    data: { success: true, data: { output: '', steps: [] } },
  })
  render(<Playground />)
  await user.click(screen.getByRole('button', { name: 'Run regex test' }))
  expect(await screen.findByLabelText('Transformed text')).toHaveValue('')
  expect(screen.getByText('No applicable rules')).toBeVisible()
  await user.type(screen.getByLabelText('Test text'), 'changed')
  expect(screen.queryByLabelText('Transformed text')).toBeNull()
})
