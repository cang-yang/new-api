import { describe, expect, test } from 'vitest'

import { diffJsonText, diffJsonValues } from '../json-diff'

describe('request lab JSON diff', () => {
  test('reports stable paths for added, removed and changed values', () => {
    expect(
      diffJsonValues(
        {
          model: 'a',
          messages: [{ role: 'user', content: 'old' }],
          removed: true,
        },
        { model: 'b', messages: [{ role: 'user', content: 'new' }], added: 1 }
      )
    ).toEqual([
      { path: '$.added', kind: 'added', after: 1 },
      {
        path: '$.messages[0].content',
        kind: 'changed',
        before: 'old',
        after: 'new',
      },
      { path: '$.model', kind: 'changed', before: 'a', after: 'b' },
      { path: '$.removed', kind: 'removed', before: true },
    ])
  })

  test('returns null instead of pretending non-JSON payloads are comparable', () => {
    expect(diffJsonText('{"ok":true}', 'not json')).toBeNull()
  })

  test('bounds pathological diffs', () => {
    const before = Array.from({ length: 800 }, (_, index) => index)
    const after = Array.from({ length: 800 }, (_, index) => index + 1)
    expect(diffJsonValues(before, after)).toHaveLength(500)
  })
})
