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
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { createInstance } from 'i18next'
import { I18nextProvider } from 'react-i18next'
import { beforeEach, expect, test, vi } from 'vitest'

import type { AuditCapture } from '../../lib/audit-workspace'
import { AuditPayload } from '../audit-workspace/payload'

const i18n = createInstance()
beforeEach(async () => {
  await i18n.init({
    lng: 'en',
    resources: { en: { translation: {} } },
    interpolation: { escapeValue: false },
  })
})

function mount(capture: Partial<AuditCapture>) {
  return render(
    <I18nextProvider i18n={i18n}>
      <AuditPayload
        capture={{ body: '', encoding: 'utf-8', request: true, ...capture }}
      />
    </I18nextProvider>
  )
}

test('sent requests show the entire formatted JSON without modes or message navigation', () => {
  const body =
    '{"model":"example","messages":[{"role":"system","content":""},{"role":"user","content":"hello"}],"tools":[]}'
  mount({ body })
  expect(screen.getByText('Formatted view')).toBeVisible()
  expect(screen.queryByRole('tablist')).not.toBeInTheDocument()
  expect(screen.queryByRole('combobox')).not.toBeInTheDocument()
  expect(
    screen.queryByRole('button', { name: 'Content index' })
  ).not.toBeInTheDocument()
  expect(
    screen.getByRole('region', { name: 'Reading content' }).querySelector('pre')
      ?.textContent
  ).toBe(JSON.stringify(JSON.parse(body), null, 2))
  expect(screen.getAllByRole('button')).toHaveLength(1)
})

test('copy raw body retains exact bytes and escaping rather than copying the formatted view', async () => {
  const user = userEvent.setup()
  const write = vi.spyOn(navigator.clipboard, 'writeText')
  const body = ' { "content": "\\u4f60\\u597d", "literal": "\\\\n" }\r\n'
  mount({ body })
  await user.click(screen.getByRole('button', { name: 'Copy raw body' }))
  expect(write).toHaveBeenCalledWith(body)
  write.mockRestore()
})

test.each(['{"messages": [', 'plain text request', ''])(
  'non-JSON or empty requests stay accessible without a view switch: %s',
  (body) => {
    mount({ body })
    expect(
      screen
        .getByRole('region', { name: 'Reading content' })
        .querySelector('pre')?.textContent
    ).toBe(body || '""')
    expect(screen.queryByRole('tab')).not.toBeInTheDocument()
  }
)

test('capture warnings survive the single request view', () => {
  mount({ body: 'AA==', encoding: 'base64', truncated: true, incomplete: true })
  expect(screen.getByRole('status')).toHaveTextContent(
    'Binary body is shown as Base64'
  )
  expect(screen.getByRole('status')).toHaveTextContent('Partial capture')
  expect(screen.getByRole('status')).toHaveTextContent('Capture incomplete')
  expect(screen.getByText('AA==')).toBeVisible()
})

test('invalid JSON requests explain the parse failure while preserving the original body', () => {
  const body = '{"messages":[{"content":"unfinished}'
  mount({ body, contentType: 'application/json' })
  expect(screen.getByRole('status')).toHaveTextContent(
    'Invalid or incomplete JSON request'
  )
  expect(
    screen.getByRole('region', { name: 'Reading content' }).querySelector('pre')
      ?.textContent
  ).toBe(body)
})

test('long request content remains loadable without introducing a second view', async () => {
  const user = userEvent.setup()
  mount({
    body: JSON.stringify({ content: `${'x'.repeat(33000)}END-OF-REQUEST` }),
  })
  expect(screen.queryByText(/END-OF-REQUEST/)).not.toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: 'Load more' }))
  expect(screen.getByText(/END-OF-REQUEST/)).toBeVisible()
  expect(screen.queryByRole('tablist')).not.toBeInTheDocument()
})
