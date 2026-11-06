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
import { parseWorldbook, worldbookSchema } from '../worldbook'

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
              ],
              issues: ['regex'],
            },
          ],
        },
      ],
    })
  )
  expect(parsed?.books[0].entries[0].normalized_rules).toHaveLength(1)
  expect(parsed?.books[0].entries[0].issues).toEqual(['regex'])
})
