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

import { importRegexScripts, regexRulesSchema } from '../regex-rules'

test.each(['/x/v', '/x/y', '/x/gg'])(
  'unsupported or duplicate flags %s import disabled with warning',
  (pattern) => {
    const result = importRegexScripts({
      scriptName: 'Unsupported',
      findRegex: pattern,
      placement: [2],
      markdownOnly: true,
    })
    expect(result.warnings).toEqual(['Unsupported'])
    expect(result.rules).toMatchObject([{ disabled: true, pattern }])
    expect(
      regexRulesSchema.safeParse({ mode: 'rules', rules: result.rules }).success
    ).toBe(true)
  }
)

test('disabled empty pattern is preserved while oversized rules and payload fields are rejected', () => {
  const rule = {
    id: 'a',
    stage: 'receive',
    action: 'replace',
    pattern: '',
    replacement: '',
    disabled: true,
  }
  expect(
    regexRulesSchema.safeParse({ mode: 'rules', rules: [rule] }).success
  ).toBe(true)
  expect(
    regexRulesSchema.safeParse({
      mode: 'rules',
      rules: [{ ...rule, disabled: false }],
    }).success
  ).toBe(false)
  expect(
    regexRulesSchema.safeParse({
      mode: 'rules',
      rules: [{ ...rule, pattern: '体'.repeat(5462) }],
    }).success
  ).toBe(false)
  expect(
    regexRulesSchema.safeParse({
      mode: 'rules',
      rules: Array.from({ length: 101 }, (_, i) => ({
        ...rule,
        id: String(i),
      })),
    }).success
  ).toBe(false)
  expect(
    regexRulesSchema.safeParse({
      mode: 'rules',
      rules: [{ ...rule, replacement: 'x'.repeat(524289) }],
    }).success
  ).toBe(false)
})

test('standalone import maps prompt and display flags to independent directions without enabling sending', () => {
  const imported = importRegexScripts([
    {
      scriptName: 'Prompt',
      findRegex: '/x/g',
      replaceString: '',
      placement: [1, 2],
      promptOnly: true,
    },
    {
      scriptName: 'Display',
      findRegex: '/x/g',
      replaceString: '$1',
      placement: [2],
      markdownOnly: true,
    },
    {
      scriptName: 'Both',
      findRegex: 'x',
      placement: [2],
      promptOnly: true,
      markdownOnly: true,
    },
  ])
  expect(
    imported.rules.map((rule) => ({
      stage: rule.stage,
      roles: rule.roles,
      replacement: rule.replacement,
    }))
  ).toEqual([
    { stage: 'send', roles: ['user', 'assistant'], replacement: '' },
    { stage: 'receive', roles: undefined, replacement: '$1' },
    { stage: 'send', roles: ['assistant'], replacement: '' },
    { stage: 'receive', roles: undefined, replacement: '' },
  ])
  expect(imported.warnings).toEqual([])
})
test('unsupported world placement and trim behavior retain originals and disable converted rules', () => {
  const script = {
    scriptName: 'World',
    findRegex: 'x',
    placement: [2, 5],
    trimStrings: ['x'],
  }
  const imported = importRegexScripts(script)
  expect(imported.originals).toEqual([script])
  expect(imported.warnings).toEqual(['World'])
  expect(imported.rules.every((rule) => rule.disabled)).toBe(true)
})

test('plain capture trims and case-insensitive match macro import without losing behavior', () => {
  const imported = importRegexScripts({
    findRegex: '/(hello)/g',
    replaceString: '{{MATCH}} $1',
    placement: [2],
    markdownOnly: true,
    trimStrings: ['hello'],
  })
  expect(imported.warnings).toEqual([])
  expect(imported.rules).toMatchObject([
    {
      stage: 'receive',
      disabled: false,
      trim_strings: ['hello'],
      replacement: '{{MATCH}} $1',
    },
  ])
})
test('rule validation accepts empty replacement but rejects invalid direction, duplicate IDs and reversed depth', () => {
  const rule = {
    id: 'a',
    stage: 'receive',
    action: 'replace',
    pattern: 'secret',
    replacement: '',
  }
  expect(
    regexRulesSchema.safeParse({ mode: 'rules', rules: [rule] }).success
  ).toBe(true)
  expect(
    regexRulesSchema.safeParse({
      mode: 'rules',
      rules: [{ ...rule, stage: 'display' }],
    }).success
  ).toBe(false)
  expect(
    regexRulesSchema.safeParse({ mode: 'rules', rules: [rule, rule] }).success
  ).toBe(false)
  expect(
    regexRulesSchema.safeParse({
      mode: 'rules',
      rules: [{ ...rule, min_depth: 5, max_depth: 2 }],
    }).success
  ).toBe(false)
})
