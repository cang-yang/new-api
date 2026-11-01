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
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { createInstance } from 'i18next'
import { I18nextProvider } from 'react-i18next'
import { beforeEach, expect, test, vi } from 'vitest'

import { usageLogSchema } from '../../data/schema'
import type { BodyAudit } from '../../types'
import { BodyAuditSection } from '../dialogs/body-audit-section'
import { DetailsDialog } from '../dialogs/details-dialog'

const { getAudit, executeReplay, previewReplay } = vi.hoisted(() => ({
  getAudit: vi.fn(),
  executeReplay: vi.fn(),
  previewReplay: vi.fn(),
}))
vi.mock('../../api', () => ({
  getBodyAudit: getAudit,
  executeBodyAuditReplay: executeReplay,
  previewBodyAuditReplay: previewReplay,
}))
const i18n = createInstance()
let audit: BodyAudit
beforeEach(async () => {
  vi.clearAllMocks()
  await i18n.init({
    lng: 'en',
    resources: { en: { translation: {} } },
    interpolation: { escapeValue: false },
  })
  const body = JSON.stringify({
    choices: [{ message: { content: 'Delivered answer' } }],
  })
  audit = {
    request_id: 'request-1',
    created_at: 1,
    updated_at: 1,
    model_name: 'fixture',
    channel_id: 1,
    request_body: JSON.stringify({
      messages: [{ role: 'user', content: 'Question' }],
      temperature: 0,
    }),
    request_body_encoding: 'utf-8',
    request_body_size: 90,
    request_body_truncated: false,
    response_body: JSON.stringify({
      choices: [{ message: { content: 'Upstream answer' } }],
    }),
    response_body_encoding: 'utf-8',
    response_body_size: 90,
    response_body_truncated: false,
    response_content_type: 'application/json',
    response_status: 200,
    response_complete: true,
    client_response_body: body,
    client_response_body_encoding: 'utf-8',
    client_response_body_size: body.length,
    client_response_body_truncated: false,
    client_response_content_type: 'application/json',
    client_response_status: 200,
    client_response_complete: true,
  }
  getAudit.mockImplementation(async () => audit)
})
function mount(enabled = true) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return render(
    <I18nextProvider i18n={i18n}>
      <QueryClientProvider client={client}>
        <BodyAuditSection requestId='request-1' enabled={enabled} />
      </QueryClientProvider>
    </I18nextProvider>
  )
}
test('opens the final result directly with flat source navigation', async () => {
  mount()
  expect(
    await screen.findByRole('tab', { name: 'Final Result' })
  ).toHaveAttribute('aria-selected', 'true')
  expect(screen.getByRole('tab', { name: 'Client Response' })).toBeVisible()
  expect(screen.getByRole('tab', { name: 'Upstream Response' })).toBeVisible()
  expect(screen.getByText('Delivered answer')).toBeVisible()
  expect(
    screen.queryByRole('tab', { name: 'Response' })
  ).not.toBeInTheDocument()
})
test('keeps all navigation outside the single reading viewport', async () => {
  mount()
  const viewport = await screen.findByRole('region', {
    name: 'Reading content',
  })
  expect(
    screen.getAllByRole('region', { name: 'Reading content' })
  ).toHaveLength(1)
  for (const tab of screen.getAllByRole('tab')) {
    expect(viewport).not.toContainElement(tab)
  }
  expect(viewport).not.toContainElement(
    screen.getByRole('button', { name: 'Copy raw body' })
  )
  expect(viewport).toHaveClass('overflow-y-auto', 'min-h-0')
})
test('copies captured bytes even while displaying a decoded final answer', async () => {
  const user = userEvent.setup()
  mount()
  await user.click(await screen.findByRole('button', { name: 'Copy raw body' }))
  expect(await navigator.clipboard.readText()).toBe(audit.client_response_body)
})
test('switching upstream does not reuse client text and raw mode retains the payload', async () => {
  const user = userEvent.setup()
  mount()
  await user.click(
    await screen.findByRole('tab', { name: 'Upstream Response' })
  )
  expect(screen.getByText('Upstream answer')).toBeVisible()
  expect(screen.queryByText('Delivered answer')).not.toBeInTheDocument()
  await user.click(screen.getByRole('tab', { name: 'Raw data' }))
  expect(
    screen.getByRole('region', { name: 'Reading content' })
  ).toHaveTextContent(audit.response_body)
})
test('missing client capture explicitly marks the upstream fallback', async () => {
  audit.client_response_status = 0
  audit.client_response_body_size = 0
  audit.client_response_complete = false
  const user = userEvent.setup()
  mount()
  expect(
    await screen.findByText(/delivery to the client is unverified/)
  ).toBeVisible()
  await user.click(screen.getByRole('tab', { name: 'Client Response' }))
  expect(screen.getByText(/legacy record/)).toBeVisible()
})
test('truncated and interrupted captures show separate warnings', async () => {
  audit.client_response_complete = false
  audit.client_response_body_truncated = true
  mount()
  expect(await screen.findByText(/Partial capture/)).toBeVisible()
  expect(screen.getByText(/delivery may have been interrupted/)).toBeVisible()
})
test('request messages have roles while unknown parameters remain accessible', async () => {
  const user = userEvent.setup()
  mount()
  await user.click(await screen.findByRole('tab', { name: 'Sent Request' }))
  expect(screen.getByText('Question')).toBeVisible()
  await user.selectOptions(
    screen.getByRole('combobox', { name: 'Reading section' }),
    'parameters'
  )
  expect(screen.getByText('temperature')).toBeVisible()
  expect(screen.getByText('0')).toBeVisible()
})
test('disabled audit never fetches or displays content', () => {
  mount(false)
  expect(getAudit).not.toHaveBeenCalled()
  expect(screen.queryByRole('region')).not.toBeInTheDocument()
})
test('failed retrieval can be retried without enabling replay', async () => {
  getAudit.mockRejectedValueOnce(new Error('unavailable'))
  const user = userEvent.setup()
  mount()
  await user.click(await screen.findByRole('button', { name: /Retry/ }))
  expect(await screen.findByText('Delivered answer')).toBeVisible()
  expect(executeReplay).not.toHaveBeenCalled()
})
test('execution loads attempt bodies on demand and preserves configuration provenance', async () => {
  const first = {
    id: 1,
    attempt_no: 0,
    routing_retry_index: 0,
    channel_id: 1,
    channel_type: 1,
    channel_name: 'one',
    request_model: 'a',
    upstream_model: 'b',
    request_format: 'openai',
    upstream_format: 'openai',
    target: '/chat',
    state: 'succeeded',
    outcome: 'complete',
    http_status: 200,
    error_code: '',
    error_message: '',
    retry_action: '',
    terminal_kind: 'stop',
    complete: true,
    duration_ms: 3,
    request_body: '{"x":1}',
    request_body_encoding: 'utf-8' as const,
    request_body_size: 7,
    request_body_truncated: false,
    response_body: '{}',
    response_body_encoding: 'utf-8' as const,
    response_body_size: 2,
    response_body_truncated: false,
    config_snapshot: {
      id: 1,
      digest: 'test-digest',
      schema_version: 2,
      canonical_json: { limit: 4 },
    },
  }
  audit.attempts = [
    first,
    { ...first, id: 2, attempt_no: 1, request_body: '{"x":2}' },
  ]
  const user = userEvent.setup()
  mount()
  await screen.findByText('Delivered answer')
  expect(getAudit).toHaveBeenCalledTimes(1)
  await user.click(screen.getByRole('tab', { name: 'Execution details' }))
  await waitFor(() => expect(getAudit).toHaveBeenCalledWith('request-1', true))
  await user.selectOptions(
    screen.getByRole('combobox', { name: 'Attempt content' }),
    'config'
  )
  expect(await screen.findByText(/test-digest/)).toBeVisible()
  expect(screen.getByRole('button', { name: 'Copy data' })).toBeVisible()
  expect(
    screen.queryByRole('button', { name: 'Copy raw body' })
  ).not.toBeInTheDocument()
  expect(executeReplay).not.toHaveBeenCalled()
  expect(previewReplay).not.toHaveBeenCalled()
})

test('the real log dialog enters one workspace and returns focus to its entry', async () => {
  const user = userEvent.setup()
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  const log = usageLogSchema.parse({
    id: 1,
    user_id: 1,
    created_at: 1,
    type: 2,
    content: '',
    request_id: 'request-1',
  })
  render(
    <I18nextProvider i18n={i18n}>
      <QueryClientProvider client={client}>
        <DetailsDialog log={log} open onOpenChange={() => {}} isAdmin isRoot />
      </QueryClientProvider>
    </I18nextProvider>
  )
  const entry = screen.getByRole('button', { name: 'Open audit workspace' })
  expect(getAudit).not.toHaveBeenCalled()
  await user.click(entry)
  expect(await screen.findByText('Delivered answer')).toBeVisible()
  expect(screen.getAllByRole('dialog')).toHaveLength(1)
  expect(screen.getByRole('button', { name: 'Log Details' })).toHaveFocus()
  expect(
    screen.queryByRole('button', { name: 'Open audit workspace' })
  ).not.toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: 'Log Details' }))
  expect(entry).toHaveFocus()
  expect(
    screen.queryByRole('region', { name: 'Body Audit' })
  ).not.toBeInTheDocument()
})
