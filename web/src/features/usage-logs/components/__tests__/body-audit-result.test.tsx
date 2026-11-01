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
import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { createInstance } from 'i18next'
import type { ReactNode } from 'react'
import { I18nextProvider } from 'react-i18next'
import { beforeEach, expect, test, vi } from 'vitest'

import { requestDocuments } from '../../lib/audit-workspace'
import { DocumentBrowser } from '../audit-workspace/document-browser'
import { AuditPayload } from '../audit-workspace/payload'
import { TextBlock, ValueExplorer } from '../audit-workspace/reader'

const i18n = createInstance()
beforeEach(async () => {
  await i18n.init({
    lng: 'en',
    resources: { en: { translation: {} } },
    interpolation: { escapeValue: false },
  })
})
function mount(element: ReactNode) {
  return render(<I18nextProvider i18n={i18n}>{element}</I18nextProvider>)
}
test('unknown tool schemas open one level at a time instead of flattening paths', async () => {
  const user = userEvent.setup()
  mount(
    <ValueExplorer
      value={{
        changes: [{ fields: { body: 'Readable content' } }],
        enabled: false,
      }}
    />
  )
  expect(screen.queryByText('Readable content')).not.toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: 'Open changes' }))
  await user.click(screen.getByRole('button', { name: 'Open #1' }))
  await user.click(screen.getByRole('button', { name: 'Open fields' }))
  expect(screen.getByText('Readable content')).toBeVisible()
  expect(
    screen.queryByText('$["changes"][0]["fields"]["body"]')
  ).not.toBeInTheDocument()
  await user.selectOptions(
    screen.getByRole('combobox', { name: 'Data location' }),
    '0'
  )
  expect(screen.getByText('false')).toBeVisible()
})
test('own special keys are navigable without interpreting prototypes or dotted paths', async () => {
  const user = userEvent.setup()
  mount(
    <ValueExplorer
      value={JSON.parse('{"__proto__":{"a.b":"literal"},"a":{"b":"nested"}}')}
    />
  )
  await user.click(screen.getByRole('button', { name: 'Open __proto__' }))
  expect(screen.getByText('a.b')).toBeVisible()
  expect(screen.getByText('literal')).toBeVisible()
  await user.click(screen.getByRole('button', { name: 'Back' }))
  expect(screen.getByRole('button', { name: 'Open a' })).toBeVisible()
})
test('large arrays provide bounded pages without losing later entries', async () => {
  const user = userEvent.setup()
  mount(
    <ValueExplorer
      value={Array.from({ length: 41 }, (_, index) => `item-${index}`)}
    />
  )
  expect(screen.queryByText('item-40')).not.toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: 'Next' }))
  expect(screen.getByText('item-40')).toBeVisible()
  await user.click(screen.getByRole('button', { name: 'Previous' }))
  expect(screen.getByText('item-0')).toBeVisible()
})
test('long text reveals its tail on demand without unescaping literals', async () => {
  const user = userEvent.setup()
  mount(<TextBlock text={'x'.repeat(32768) + String.raw`\n literal tail`} />)
  expect(screen.queryByText(/literal tail/)).not.toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: 'Load more' }))
  expect(screen.getByText(/literal tail/)).toHaveTextContent(
    String.raw`\n literal tail`
  )
})
test('mixed output keeps answers, tools and reasoning selectable without treating parameters as execution', async () => {
  const user = userEvent.setup()
  mount(
    <AuditPayload
      capture={{
        encoding: 'utf-8',
        body: JSON.stringify({
          choices: [
            {
              message: {
                content: 'Answer text',
                reasoning_content: 'Separate analysis',
                tool_calls: [
                  {
                    id: 'call',
                    function: {
                      name: 'unknown_tool',
                      arguments: '{"content":"<script>safe text</script>"}',
                    },
                  },
                ],
              },
            },
          ],
        }),
      }}
      final
    />
  )
  expect(screen.getByText('Answer text')).toBeVisible()
  await user.selectOptions(
    screen.getByRole('combobox', { name: 'Reading section' }),
    screen.getByRole('option', { name: /unknown_tool/ })
  )
  expect(
    screen.getByText('Tool call arguments, not an execution result.')
  ).toBeVisible()
  expect(screen.getByText('<script>safe text</script>')).toBeVisible()
  expect(document.querySelector('script')).toBeNull()
  await user.selectOptions(
    screen.getByRole('combobox', { name: 'Reading section' }),
    screen.getByRole('option', { name: /Reasoning/ })
  )
  expect(screen.getByText('Separate analysis')).toBeVisible()
})
test('invalid tool JSON is preserved with a warning instead of disappearing', () => {
  mount(
    <AuditPayload
      capture={{
        encoding: 'utf-8',
        body: '{"choices":[{"message":{"tool_calls":[{"function":{"name":"bad","arguments":"{broken"}}]}}]}',
      }}
    />
  )
  expect(screen.getByText('Tool arguments are not valid JSON')).toBeVisible()
  expect(screen.getByText('{broken')).toBeVisible()
})
test('base64 audio keeps a media player and the original payload accessible', async () => {
  const user = userEvent.setup()
  const { container } = mount(
    <AuditPayload
      capture={{
        encoding: 'base64',
        contentType: 'audio/wav',
        body: 'UklGRg==',
      }}
    />
  )
  expect(container.querySelector('audio')).toHaveAttribute(
    'src',
    'data:audio/wav;base64,UklGRg=='
  )
  await user.click(screen.getByRole('tab', { name: 'Raw data' }))
  expect(screen.getByText('UklGRg==')).toBeVisible()
})
test('reasoning-only responses are never labeled as answers', () => {
  mount(
    <AuditPayload
      capture={{
        encoding: 'utf-8',
        body: '{"choices":[{"message":{"reasoning_content":"Analysis only"}}]}',
      }}
    />
  )
  expect(
    screen.getByText(
      'Only reasoning was returned; no answer or tool call was received.'
    )
  ).toBeVisible()
  expect(
    screen.queryByRole('option', { name: /Answer/ })
  ).not.toBeInTheDocument()
})

test('a JSON null response is data rather than an absent response', () => {
  mount(
    <AuditPayload
      capture={{
        encoding: 'utf-8',
        contentType: 'application/json',
        body: 'null',
      }}
    />
  )
  expect(screen.getByText('null')).toBeVisible()
  expect(screen.queryByText('No readable content')).not.toBeInTheDocument()
})

test('structured scalar types remain distinct from lookalike strings', () => {
  mount(
    <ValueExplorer
      value={{
        boolean: false,
        text: 'false',
        nothing: null,
        nullText: 'null',
        number: 12,
        numberText: '12',
      }}
    />
  )
  expect(screen.getByText('"false"')).toBeVisible()
  expect(screen.getByText('"null"')).toBeVisible()
  expect(screen.getByText('"12"')).toBeVisible()
})

test('keyboard drill-down transfers focus to the stable data location control', async () => {
  const user = userEvent.setup()
  mount(<ValueExplorer value={{ nested: { text: 'target' } }} />)
  screen.getByRole('button', { name: 'Open nested' }).focus()
  await user.keyboard('{Enter}')
  expect(screen.getByRole('combobox', { name: 'Data location' })).toHaveFocus()
})

test('a single document does not reserve a redundant fixed navigation row', () => {
  const { container } = mount(
    <AuditPayload
      capture={{ encoding: 'utf-8', derived: true, body: '{"status":"ok"}' }}
    />
  )
  expect(container.querySelector('[data-audit-chrome="document"]')).toBeNull()
  expect(screen.getByRole('button', { name: 'Copy document' })).toBeVisible()
})

test('empty leading messages remain selectable but do not hide the first readable message', async () => {
  const user = userEvent.setup()
  const write = vi.spyOn(navigator.clipboard, 'writeText')
  mount(
    <DocumentBrowser
      documents={requestDocuments({
        messages: [
          { role: 'system', content: '' },
          { role: 'user', content: 'Read this request first' },
        ],
      })}
    />
  )
  expect(screen.getByText('Read this request first')).toBeVisible()
  await user.selectOptions(
    screen.getByRole('combobox', { name: 'Reading section' }),
    'messages:0'
  )
  expect(screen.getByText('This message has no text content.')).toBeVisible()
  await user.click(screen.getByRole('button', { name: 'Copy document' }))
  expect(write).toHaveBeenCalledWith('')
  write.mockRestore()
})

test('document index is opt-in and closing it keeps the selected document', async () => {
  const user = userEvent.setup()
  mount(
    <AuditPayload
      capture={{
        encoding: 'utf-8',
        body: '{"choices":[{"message":{"content":"Answer","reasoning_content":"Analysis"}}]}',
      }}
    />
  )
  expect(screen.queryByRole('navigation', { name: 'Content index' })).toBeNull()
  const toggle = screen.getByRole('button', { name: 'Content index' })
  expect(toggle).toHaveAttribute('aria-expanded', 'false')
  await user.click(toggle)
  await user.click(
    within(screen.getByRole('navigation', { name: 'Content index' })).getByRole(
      'button',
      { name: /Reasoning/ }
    )
  )
  await user.click(toggle)
  expect(screen.getByText('Analysis')).toBeVisible()
  expect(toggle).toHaveAttribute('aria-expanded', 'false')
})

test('long and multiline fields use full-width reading blocks while short values stay compact', () => {
  const prose = 'A paragraph with room to read. '.repeat(10)
  mount(
    <ValueExplorer
      value={{ prose, multiline: 'First line\nSecond line', enabled: true }}
    />
  )
  expect(
    screen.getByText(prose.trim()).closest('[data-field-layout]')
  ).toHaveAttribute('data-field-layout', 'prose')
  expect(
    screen.getByText(/First line/).closest('[data-field-layout]')
  ).toHaveAttribute('data-field-layout', 'prose')
  expect(
    screen.getByText('true').closest('[data-field-layout]')
  ).toHaveAttribute('data-field-layout', 'compact')
})

test('unavailable derived data shows its explanation but preserves null in raw mode', async () => {
  const user = userEvent.setup()
  mount(
    <AuditPayload
      capture={{ encoding: 'utf-8', derived: true, body: 'null' }}
      emptyMessage='No comparison is available'
    />
  )
  expect(screen.getByText('No comparison is available')).toBeVisible()
  expect(screen.queryByText('null')).toBeNull()
  await user.click(screen.getByRole('tab', { name: 'Raw data' }))
  expect(screen.getByText('null')).toBeVisible()
})

test.each([false, true])(
  'a later document stays available in the paginated index (leading empty: %s)',
  async (leadingEmpty) => {
    const user = userEvent.setup()
    mount(
      <DocumentBrowser
        documents={requestDocuments({
          messages: Array.from({ length: 80 }, (_, index) => ({
            role: 'user',
            content: leadingEmpty && index < 79 ? '' : `message ${index + 1}`,
          })),
        })}
      />
    )
    await user.click(screen.getByRole('button', { name: 'Content index' }))
    if (leadingEmpty) {
      expect(
        within(
          screen.getByRole('navigation', { name: 'Content index' })
        ).getByRole('button', { name: /80 ·/ })
      ).toHaveAttribute('aria-current', 'true')
    }
    await user.selectOptions(
      screen.getByRole('combobox', { name: 'Reading section' }),
      'messages:79'
    )
    expect(
      within(
        screen.getByRole('navigation', { name: 'Content index' })
      ).getByRole('button', { name: /80 ·/ })
    ).toHaveAttribute('aria-current', 'true')
  }
)

test('tool metadata shares the explanation disclosure instead of adding another reading row', async () => {
  const user = userEvent.setup()
  const { container } = mount(
    <AuditPayload
      capture={{
        encoding: 'utf-8',
        body: '{"choices":[{"message":{"tool_calls":[{"id":"call-123","function":{"name":"tool","arguments":"{\\"content\\":\\"Readable\\"}"}}]}}]}',
      }}
    />
  )
  const summary = screen.getByText(
    'Tool call arguments, not an execution result.'
  )
  expect(summary.tagName).toBe('SUMMARY')
  expect(container.querySelector('details')).not.toHaveAttribute('open')
  await user.click(summary)
  expect(container.querySelector('details')).toHaveAttribute('open')
  expect(screen.getByText('Readable')).toBeVisible()
})
