import { expect, test } from 'vitest'

import {
  replySampleFields,
  migrateReplyAdapter,
  validReplyAdapter,
  validReplyPointer,
} from '../reply-adapter'

test('sample fields preserve literal keys and array indices without evaluating code', () => {
  expect(
    replySampleFields(
      '{"payload":{"a/b~c":"hello"},"items":[{"text":"world"}]}'
    )
  ).toEqual([
    { pointer: '', kind: 'json_object', preview: '{…}' },
    { pointer: '/payload', kind: 'json_object', preview: '{…}' },
    { pointer: '/payload/a~1b~0c', kind: 'text', preview: 'hello' },
    { pointer: '/items/0', kind: 'json_object', preview: '{…}' },
    { pointer: '/items/0/text', kind: 'text', preview: 'world' },
  ])
  expect(replySampleFields('null')).toEqual([])
  expect(replySampleFields('{broken')).toEqual([])
})

test('reply field validation rejects ambiguous or unsafe declarations before applying', () => {
  for (const path of [
    'payload.text',
    '/bad~escape',
    '/__proto__/x',
    '/items/*',
    `/${Array(33).fill('x').join('/')}`,
  ]) {
    expect(validReplyPointer(path)).toBe(false)
  }
  expect(validReplyPointer('/payload/a~1b/0/~0text')).toBe(true)
  const row = {
    name: 'reply',
    argument_pointer: '/content',
    output_kind: 'text',
  }
  expect(
    validReplyAdapter({ reply_adapter: { version: 2, containers: [row] } })
  ).toBe(true)
  expect(
    validReplyAdapter({
      reply_adapter: { version: 2, containers: [row], structured_output: true },
    })
  ).toBe(true)
  expect(
    validReplyAdapter({
      reply_adapter: { version: 2, containers: [], structured_output: true },
    })
  ).toBe(false)
  expect(
    validReplyAdapter({ reply_adapter: { version: 2, containers: [row, row] } })
  ).toBe(false)
  expect(
    validReplyAdapter({
      reply_adapter: { version: 2, containers: [{ ...row, formatter: true }] },
    })
  ).toBe(false)
})

test('only explicit reply fields migrate and business-tool orchestration requires review', () => {
  const config = {
    tool_protocol: {
      enabled: true,
      preset_final: 'reply',
      response_argument: 'payload.text',
      response_target: 'text',
    },
  }
  expect(migrateReplyAdapter(config)).toMatchObject({
    status: 'ready',
    adapter: {
      version: 2,
      containers: [
        {
          name: 'reply',
          argument_pointer: '/payload/text',
          output_kind: 'text',
        },
      ],
    },
  })
  expect(
    migrateReplyAdapter({
      tool_protocol: { enabled: true, auxiliary: ['lookup'] },
    }).status
  ).toBe('requires_review')
  expect(
    migrateReplyAdapter({
      tool_protocol: {
        enabled: true,
        preset_final: 'reply',
        response_argument: 'items.0',
      },
    }).status
  ).toBe('requires_review')
})
