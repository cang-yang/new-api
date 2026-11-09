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
import { createInstance } from 'i18next'
import { I18nextProvider } from 'react-i18next'
import { beforeEach, expect, test } from 'vitest'

import { AuditDiagnostics } from '../audit-workspace/diagnostics'

const i18n = createInstance()

beforeEach(async () => {
  await i18n.init({
    lng: 'en',
    resources: { en: { translation: {} } },
    interpolation: { escapeValue: false },
  })
})

test('renders the adaptation timeline with decision metadata', () => {
  render(
    <I18nextProvider i18n={i18n}>
      <AuditDiagnostics
        value={{
          schema_version: 1,
          stages: [
            {
              stage: 'worldbook',
              status: 'completed',
              summary: 'worldbook matching completed',
              details: { matched: 1, entries: [{ id: 'city' }] },
            },
            {
              stage: 'tool_resolution',
              status: 'fallback',
              summary: 'tool visibility and response target resolved',
            },
          ],
        }}
      />
    </I18nextProvider>
  )

  expect(screen.getByText('Adaptation diagnostics')).toBeVisible()
  expect(screen.getByText('Worldbook')).toBeVisible()
  expect(screen.getByText('Reply source')).toBeVisible()
  expect(screen.getByText('2 stages')).toBeVisible()
  expect(screen.getAllByText('Decision details')).toHaveLength(2)
})

test('an unknown failed stage never appears as completed', () => {
  render(
    <I18nextProvider i18n={i18n}>
      <AuditDiagnostics
        value={{
          stages: [
            {
              stage: 'new-stage',
              status: 'failed',
              summary: 'safe failure code',
            },
          ],
        }}
      />
    </I18nextProvider>
  )
  expect(screen.getByText('Processing stopped at this step')).toBeVisible()
  expect(screen.queryByText('Stage completed')).toBeNull()
})

test('renders an explicit empty state for legacy attempts', () => {
  render(
    <I18nextProvider i18n={i18n}>
      <AuditDiagnostics value={undefined} />
    </I18nextProvider>
  )

  expect(
    screen.getByText('No adaptation diagnostics were recorded for this attempt')
  ).toBeVisible()
})
