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
import { describe, expect, test } from 'vitest'

import {
  buildJsonDiff,
  redactSimulationValue,
} from '../param-override-simulator'

describe('parameter override simulator result protection', () => {
  test('redacts nested credentials before a simulator result reaches the UI', () => {
    const value = {
      model: 'demo',
      api_key: 'sk-private',
      nested: {
        Authorization: 'Bearer private-token',
        safe: 'visible',
      },
      messages: [{ role: 'user', content: 'hello' }],
    }

    expect(redactSimulationValue(value)).toEqual({
      model: 'demo',
      api_key: '[REDACTED]',
      nested: {
        Authorization: '[REDACTED]',
        safe: 'visible',
      },
      messages: [{ role: 'user', content: 'hello' }],
    })
  })
})

describe('parameter override simulator JSON diff', () => {
  test('reports added, removed, and changed paths with exact values', () => {
    const before = {
      model: 'old',
      temperature: 0.7,
      metadata: { remove_me: true },
    }
    const after = {
      model: 'new',
      temperature: 0.7,
      metadata: { added: 'yes' },
    }

    expect(buildJsonDiff(before, after)).toEqual([
      {
        path: 'metadata.added',
        kind: 'added',
        before: undefined,
        after: 'yes',
      },
      {
        path: 'metadata.remove_me',
        kind: 'removed',
        before: true,
        after: undefined,
      },
      {
        path: 'model',
        kind: 'changed',
        before: 'old',
        after: 'new',
      },
    ])
  })

  test('returns an empty diff when object key order is the only difference', () => {
    expect(buildJsonDiff({ b: 2, a: 1 }, { a: 1, b: 2 })).toEqual([])
  })
})
