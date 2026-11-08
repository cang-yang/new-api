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
import { expect, test } from 'vitest'

import {
  CHANNEL_FORM_DEFAULT_VALUES,
  transformFormDataToCreatePayload,
} from '../channel-form'
import {
  parseWorldbook,
  worldbookConfirmEntry,
  worldbookEntryBlocked,
  worldbookNeedsReview,
  worldbookRiskFields,
  worldbookSchema,
  type WorldbookEntry,
} from '../worldbook'

test('channel save preserves worldbook separately from preset and other settings', () => {
  const config = {
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
            content: 'A walled city.',
            enabled: true,
            constant: false,
            keys: ['city'],
            secondary_keys: [],
            secondary_logic: 'none',
            priority: 100,
            case_sensitive: false,
            whole_words: false,
            source: { original: true },
          },
        ],
      },
    ],
  }
  const payload = transformFormDataToCreatePayload({
    ...CHANNEL_FORM_DEFAULT_VALUES,
    settings: '{"future":{"keep":true}}',
    worldbook: JSON.stringify(config),
  })
  const stored = JSON.parse(payload.channel.settings || '{}')
  expect(stored.worldbook).toEqual(config)
  expect(stored.future).toEqual({ keep: true })
  expect(stored.sillytavern_preset).toBeUndefined()
  expect(parseWorldbook(JSON.stringify(stored.worldbook))).toEqual(config)
})

test('invalid worldbook settings remain invalid rather than silently clearing books', () => {
  expect(parseWorldbook('{broken')).toBeNull()
  expect(worldbookSchema.safeParse({ enabled: true, books: [] }).success).toBe(
    false
  )
  expect(
    worldbookSchema.safeParse({
      enabled: true,
      books: [],
      scan_depth: 0,
      max_chars: 12000,
      recursive: false,
    }).success
  ).toBe(false)
})

test('normalized and invalid entry metadata survive parsing', () => {
  const parsed = parseWorldbook(
    JSON.stringify({
      enabled: true,
      scan_depth: 4,
      max_chars: 12000,
      recursive: false,
      injection_mode: 'before_last_user',
      injection_depth: 0,
      books: [
        {
          id: 'book',
          name: 'Book',
          enabled: true,
          entries: [
            {
              id: 'entry',
              name: 'Entry',
              content: 'Lore',
              enabled: true,
              constant: false,
              keys: ['moon'],
              secondary_keys: [],
              secondary_logic: 'none',
              case_sensitive: false,
              whole_words: false,
              priority: 0,
              normalized_rules: [
                { field: 'probability', action: 'ignored' },
                { field: 'sticky', action: 'risk' },
              ],
              issues: ['regex'],
            },
          ],
        },
      ],
    })
  )
  expect(parsed?.books[0].entries[0].normalized_rules).toHaveLength(2)
  expect(parsed?.books[0].entries[0].issues).toEqual(['regex'])
})

test('risky imports stay disabled until the supported subset is confirmed', () => {
  const entry: WorldbookEntry = {
    id: 'entry',
    name: 'Entry',
    content: 'Lore',
    enabled: false,
    constant: false,
    keys: ['moon'],
    secondary_keys: [],
    secondary_logic: 'none',
    case_sensitive: false,
    whole_words: false,
    priority: 0,
    normalized_rules: [
      { field: 'sticky', action: 'risk' },
      { field: 'position', action: 'ignored' },
    ],
  }
  expect(worldbookRiskFields(entry)).toEqual(['sticky'])
  expect(worldbookNeedsReview(entry)).toBe(true)
  const confirmed = worldbookConfirmEntry(entry)
  expect(confirmed.enabled).toBe(true)
  expect(worldbookRiskFields(confirmed)).toEqual([])
  expect(confirmed.normalized_rules).toEqual([
    { field: 'position', action: 'ignored' },
  ])
  expect(worldbookNeedsReview(confirmed)).toBe(false)
})

test('an entry with unresolved issues cannot be confirmed or enabled', () => {
  const entry: WorldbookEntry = {
    id: 'entry',
    name: 'Broken',
    content: 'Lore',
    enabled: false,
    constant: false,
    keys: ['moon'],
    secondary_keys: [],
    secondary_logic: 'none',
    case_sensitive: false,
    whole_words: false,
    priority: 0,
    normalized_rules: [{ field: 'sticky', action: 'risk' }],
    issues: ['invalid regular expression'],
  }
  expect(worldbookEntryBlocked(entry)).toBe(true)
  const confirmed = worldbookConfirmEntry(entry)
  expect(confirmed.enabled).toBe(false)
  expect(confirmed.issues).toEqual(['invalid regular expression'])
  expect(worldbookRiskFields(confirmed)).toEqual(['sticky'])
  expect(worldbookNeedsReview(confirmed)).toBe(true)
})
