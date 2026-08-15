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
import assert from 'node:assert/strict'
import { describe, test } from 'node:test'

import { parseBodyAuditResponse } from '../body-audit-response'

describe('body audit readable response parsing', () => {
  const textCases: Array<{
    name: string
    body: string
    expected: string
    isStream?: boolean
  }> = [
    {
      name: 'OpenAI Chat',
      body: JSON.stringify({
        choices: [{ message: { role: 'assistant', content: 'chat text' } }],
      }),
      expected: 'chat text',
    },
    {
      name: 'OpenAI Responses',
      body: JSON.stringify({
        output: [
          {
            type: 'message',
            content: [{ type: 'output_text', text: 'responses text' }],
          },
        ],
      }),
      expected: 'responses text',
    },
    {
      name: 'Anthropic Messages',
      body: JSON.stringify({
        type: 'message',
        content: [{ type: 'text', text: 'claude text' }],
      }),
      expected: 'claude text',
    },
    {
      name: 'Gemini',
      body: JSON.stringify({
        candidates: [
          { content: { role: 'model', parts: [{ text: 'gemini text' }] } },
        ],
      }),
      expected: 'gemini text',
    },
    {
      name: 'legacy data wrapper',
      body: JSON.stringify({
        data: {
          choices: [{ message: { content: 'wrapped text' } }],
        },
        success: true,
      }),
      expected: 'wrapped text',
    },
    {
      name: 'OpenAI Chat SSE',
      body: [
        'data: {"choices":[{"delta":{"content":"stream "}}]}',
        '',
        'data: {"choices":[{"delta":{"content":"chat"}}]}',
        '',
        'data: [DONE]',
        '',
      ].join('\n'),
      expected: 'stream chat',
      isStream: true,
    },
    {
      name: 'OpenAI Responses SSE',
      body: [
        'event: response.output_text.delta',
        'data: {"type":"response.output_text.delta","delta":"response "}',
        '',
        'event: response.output_text.delta',
        'data: {"type":"response.output_text.delta","delta":"stream"}',
        '',
        'event: response.completed',
        'data: {"type":"response.completed"}',
        '',
      ].join('\n'),
      expected: 'response stream',
      isStream: true,
    },
    {
      name: 'Anthropic SSE',
      body: [
        'event: content_block_delta',
        'data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"claude "}}',
        '',
        'event: content_block_delta',
        'data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"stream"}}',
        '',
        'event: message_stop',
        'data: {"type":"message_stop"}',
        '',
      ].join('\n'),
      expected: 'claude stream',
      isStream: true,
    },
  ]

  for (const scenario of textCases) {
    test(`extracts ${scenario.name} text`, () => {
      const result = parseBodyAuditResponse({
        body: scenario.body,
        encoding: 'utf-8',
        contentType: scenario.isStream
          ? 'text/event-stream'
          : 'application/json',
      })

      assert.equal(result.kind, 'text')
      assert.equal(result.text, scenario.expected)
      assert.equal(result.isStream, scenario.isStream ?? false)
    })
  }

  test('returns image items from OpenAI image responses', () => {
    const result = parseBodyAuditResponse({
      body: JSON.stringify({
        created: 1,
        data: [
          {
            url: 'https://cdn.example/generated.png',
            revised_prompt: 'a blue mountain',
          },
        ],
      }),
      encoding: 'utf-8',
      contentType: 'application/json',
      requestPath: '/v1/images/generations',
    })

    assert.equal(result.kind, 'image')
    assert.deepEqual(result.media, [
      {
        kind: 'image',
        source: 'https://cdn.example/generated.png',
        label: 'a blue mountain',
      },
    ])
  })

  test('returns playable audio from a binary client response', () => {
    const result = parseBodyAuditResponse({
      body: 'SUQzBAAAAAA=',
      encoding: 'base64',
      contentType: 'audio/mpeg',
      requestPath: '/v1/audio/speech',
    })

    assert.equal(result.kind, 'audio')
    assert.deepEqual(result.media, [
      {
        kind: 'audio',
        source: 'data:audio/mpeg;base64,SUQzBAAAAAA=',
      },
    ])
  })

  test('returns video items from task-style URL responses', () => {
    const result = parseBodyAuditResponse({
      body: JSON.stringify({
        status: 'succeeded',
        output: { url: 'https://cdn.example/generated.mp4' },
      }),
      encoding: 'utf-8',
      contentType: 'application/json',
      requestPath: '/v1/videos/generations',
    })

    assert.equal(result.kind, 'video')
    assert.deepEqual(result.media, [
      {
        kind: 'video',
        source: 'https://cdn.example/generated.mp4',
      },
    ])
  })

  test('keeps embeddings as a structured result', () => {
    const body = {
      object: 'list',
      data: [{ object: 'embedding', index: 0, embedding: [0.1, 0.2] }],
    }
    const result = parseBodyAuditResponse({
      body: JSON.stringify(body),
      encoding: 'utf-8',
      contentType: 'application/json',
      requestPath: '/v1/embeddings',
    })

    assert.equal(result.kind, 'structured')
    assert.deepEqual(result.structured, body)
  })
})
