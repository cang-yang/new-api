import { expect, test } from 'vitest'

import {
  adoptDetectedReplySources,
  replySampleFields,
  replyAdapterSchema,
  migrateReplyAdapter,
  validReplyAdapter,
  validReplyPointer,
} from '../reply-adapter'

test('refreshing the same pinned source preserves approval, but a changed source does not inherit it', () => {
  const source = {
    name: 'reply',
    formatter: true,
    output_kind: 'text' as const,
    source_prompt_id: 'binding',
    source_fingerprint: 'a'.repeat(64),
  }
  const adapter = {
    version: 2 as const,
    structured_output: true,
    require_container: true,
    formatted_output: 'client_format' as const,
    containers: [source],
  }
  expect(
    adoptDetectedReplySources({ reply_adapter: adapter }, [source])
  ).toEqual(adapter)
  const changed = { ...source, source_fingerprint: 'b'.repeat(64) }
  expect(
    adoptDetectedReplySources({ reply_adapter: adapter }, [changed])
  ).toEqual({ version: 2, containers: [changed] })
  expect(adoptDetectedReplySources({ reply_adapter: adapter }, [])).toEqual({
    version: 2,
    containers: [],
  })
})

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

test('required reply transport and formatted JSON policy survive validation without accepting incomplete declarations', () => {
  const adapter = {
    version: 2,
    containers: [{ name: 'reply', formatter: true, output_kind: 'text' }],
    structured_output: true,
    require_container: true,
    formatted_output: 'client_format',
  }
  expect(replyAdapterSchema.parse(adapter)).toEqual(adapter)
  expect(migrateReplyAdapter({ reply_adapter: adapter }).adapter).toEqual(
    adapter
  )
  expect(
    validReplyAdapter({ reply_adapter: { ...adapter, containers: [] } })
  ).toBe(false)
  expect(
    validReplyAdapter({
      reply_adapter: { ...adapter, structured_output: false },
    })
  ).toBe(true)
  expect(
    validReplyAdapter({
      reply_adapter: { ...adapter, formatted_output: 'guess' },
    })
  ).toBe(false)
})
