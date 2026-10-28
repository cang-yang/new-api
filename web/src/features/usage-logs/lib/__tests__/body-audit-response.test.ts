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

import { parseBodyAuditResponse } from '../body-audit-response'

describe('body audit readable response parsing', () => {
  const eventStream = (...events: unknown[]) =>
    events.map((event) => `data: ${JSON.stringify(event)}\n\n`).join('')

  test('retains empty tool input when a stream closes without argument deltas', () => {
    const result = parseBodyAuditResponse({
      body: eventStream(
        {
          type: 'content_block_start',
          index: 0,
          content_block: {
            type: 'tool_use',
            id: 'call',
            name: 'get_time',
            input: {},
          },
        },
        { type: 'content_block_stop', index: 0 }
      ),
      encoding: 'utf-8',
      contentType: 'text/event-stream',
    })
    expect(result.toolCalls?.[0]).toMatchObject({
      arguments: '{}',
      valid: true,
    })
  })

  test('replaces placeholder tool input with actual JSON deltas', () => {
    const result = parseBodyAuditResponse({
      body: eventStream(
        {
          type: 'content_block_start',
          index: 0,
          content_block: {
            type: 'tool_use',
            id: 'call',
            name: 'lookup',
            input: {},
          },
        },
        {
          type: 'content_block_delta',
          index: 0,
          delta: { type: 'input_json_delta', partial_json: '{"city":' },
        },
        {
          type: 'content_block_delta',
          index: 0,
          delta: { type: 'input_json_delta', partial_json: '"Paris"}' },
        },
        { type: 'content_block_stop', index: 0 }
      ),
      encoding: 'utf-8',
      contentType: 'text/event-stream',
    })
    expect(result.toolCalls?.[0]).toMatchObject({
      arguments: '{"city":"Paris"}',
      valid: true,
    })
  })

  test('reads the response.done terminal snapshot used by compatible providers', () => {
    const result = parseBodyAuditResponse({
      body: eventStream({
        type: 'response.done',
        response: {
          output: [
            {
              type: 'message',
              content: [{ type: 'output_text', text: 'finished' }],
            },
          ],
        },
      }),
      encoding: 'utf-8',
      contentType: 'text/event-stream',
    })
    expect(result.text).toBe('finished')
  })

  test('does not mislabel a filtered incomplete response as an output limit', () => {
    const result = parseBodyAuditResponse({
      body: JSON.stringify({
        status: 'incomplete',
        incomplete_details: { reason: 'content_filter' },
        output: [],
      }),
      encoding: 'utf-8',
    })
    expect(result.warnings).not.toContain('length')
    expect(result.warnings).toContain('incomplete')
  })

  test('preserves unknown binary captures instead of presenting an empty result', () => {
    const result = parseBodyAuditResponse({
      body: '/w==',
      encoding: 'base64',
      contentType: 'application/octet-stream',
    })
    expect(result.kind).toBe('structured')
    expect(result.structured).toEqual({ encoding: 'base64', body: '/w==' })
  })

  test.each([
    {
      name: 'does not duplicate Responses done snapshots',
      body: eventStream(
        {
          type: 'response.output_text.delta',
          delta: 'Hello',
          output_index: 0,
          content_index: 0,
        },
        {
          type: 'response.output_text.done',
          text: 'Hello',
          output_index: 0,
          content_index: 0,
        }
      ),
      expected: 'Hello',
    },
    {
      name: 'reads a terminal-only Responses snapshot',
      body: eventStream({
        type: 'response.completed',
        response: {
          output: [
            {
              type: 'message',
              content: [{ type: 'output_text', text: 'Hello' }],
            },
          ],
        },
      }),
      expected: 'Hello',
    },
    {
      name: 'parses multiline SSE data',
      body: 'data: {"choices":\ndata: [{"delta":{"content":"Hello"}}]}\n\n',
      expected: 'Hello',
    },
    {
      name: 'parses CR-only SSE framing',
      body: 'data: {"choices":[{"delta":{"content":"Hello"}}]}\r\r',
      expected: 'Hello',
    },
    {
      name: 'keeps tool argument deltas out of prose',
      body: eventStream(
        {
          type: 'response.function_call_arguments.delta',
          delta: '{"city":"Paris"}',
          output_index: 1,
        },
        { type: 'response.output_text.delta', delta: 'Hello', output_index: 0 }
      ),
      expected: 'Hello',
    },
  ])('$name', ({ body, expected }) => {
    expect(
      parseBodyAuditResponse({
        body,
        encoding: 'utf-8',
        contentType: 'text/event-stream',
      }).text
    ).toBe(expected)
  })

  test('separates Gemini thoughts from answer parts', () => {
    const result = parseBodyAuditResponse({
      body: JSON.stringify({
        candidates: [
          {
            content: {
              parts: [{ thought: true, text: 'analysis' }, { text: 'answer' }],
            },
          },
        ],
      }),
      encoding: 'utf-8',
      contentType: 'application/json',
    })
    expect(result.text).toBe('answer')
    expect(result.reasoning).toBe('analysis')
  })

  test('reads Anthropic thinking-only content', () => {
    const result = parseBodyAuditResponse({
      body: JSON.stringify({
        content: [{ type: 'thinking', thinking: 'analysis' }],
      }),
      encoding: 'utf-8',
      contentType: 'application/json',
    })
    expect(result.text).toBe('analysis')
    expect(result.reasoningOnly).toBe(true)
  })

  test('preserves explicit plain text containing data lines and literal escapes', () => {
    const body = 'Example:\ndata: not SSE\nC:\\temp\\notes'
    expect(
      parseBodyAuditResponse({
        body,
        encoding: 'utf-8',
        contentType: 'text/plain',
      }).text
    ).toBe(body)
  })

  test('assembles tool arguments by index without hiding them behind reasoning', () => {
    const result = parseBodyAuditResponse({
      body: eventStream(
        {
          choices: [
            {
              index: 0,
              delta: {
                reasoning_content: 'analysis',
                tool_calls: [
                  {
                    index: 0,
                    id: 'a',
                    function: {
                      name: 'submit_image_plan',
                      arguments: '{"images":',
                    },
                  },
                ],
              },
            },
          ],
        },
        {
          choices: [
            {
              index: 0,
              delta: {
                tool_calls: [
                  {
                    index: 1,
                    id: 'b',
                    function: { name: 'other', arguments: '{"x":1}' },
                  },
                ],
              },
            },
          ],
        },
        {
          choices: [
            {
              index: 0,
              delta: {
                tool_calls: [{ index: 0, function: { arguments: '[]}' } }],
              },
              finish_reason: 'tool_calls',
            },
          ],
        }
      ),
      encoding: 'utf-8',
      contentType: 'text/event-stream',
    })
    expect(result.kind).toBe('tools')
    expect(result.text).toBe('')
    expect(result.reasoning).toBe('analysis')
    expect(result.toolCalls).toEqual([
      expect.objectContaining({
        id: 'a',
        name: 'submit_image_plan',
        arguments: '{"images":[]}',
        valid: true,
      }),
      expect.objectContaining({
        id: 'b',
        name: 'other',
        arguments: '{"x":1}',
        valid: true,
      }),
    ])
  })

  test('preserves malformed upstream arguments and reports invalid JSON', () => {
    const args = '{"images":[}'
    const result = parseBodyAuditResponse({
      body: JSON.stringify({
        choices: [
          {
            message: {
              tool_calls: [
                {
                  id: 'a',
                  function: { name: 'submit_image_plan', arguments: args },
                },
              ],
            },
          },
        ],
      }),
      encoding: 'utf-8',
    })
    expect(result.toolCalls?.[0]).toMatchObject({
      arguments: args,
      valid: false,
    })
  })

  test('reports output length limits and malformed stream events while keeping valid text', () => {
    const result = parseBodyAuditResponse({
      body:
        `${eventStream({
          choices: [{ delta: { content: 'partial' }, finish_reason: 'length' }],
        })  }data: {broken}\n\n`,
      encoding: 'utf-8',
      contentType: 'text/event-stream',
    })
    expect(result.text).toBe('partial')
    expect(result.warnings).toEqual(
      expect.arrayContaining(['length', 'malformed-event'])
    )
  })

  test('keeps independent choices separate instead of interleaving their tokens', () => {
    const result = parseBodyAuditResponse({
      body: eventStream(
        {
          choices: [
            { index: 1, delta: { content: 'B' } },
            { index: 0, delta: { content: 'A' } },
          ],
        },
        {
          choices: [
            { index: 0, delta: { content: '1' } },
            { index: 1, delta: { content: '2' } },
          ],
        }
      ),
      encoding: 'utf-8',
      contentType: 'text/event-stream',
    })
    expect(result.text).toBe('A1\n\nB2')
    expect(result.outputs?.map((output) => output.text)).toEqual(['A1', 'B2'])
  })

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

      expect(result.kind).toBe('text')
      expect(result.text).toBe(scenario.expected)
      expect(result.isStream).toBe(scenario.isStream ?? false)
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

    expect(result.kind).toBe('image')
    expect(result.media).toEqual([
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

    expect(result.kind).toBe('audio')
    expect(result.media).toEqual([
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

    expect(result.kind).toBe('video')
    expect(result.media).toEqual([
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

    expect(result.kind).toBe('structured')
    expect(result.structured).toEqual(body)
  })

  test('shows reasoning-only chat streams once instead of raw SSE', () => {
    const body = [
      'data: {"choices":[{"delta":{"role":"assistant"}}]}',
      '',
      'data: {"choices":[{"delta":{"reasoning":"我需要","reasoning_details":[{"type":"reasoning.text","text":"我需要"}]}}]}',
      '',
      'data: {"choices":[{"delta":{"reasoning":"先分析"}}]}',
      '',
      'data: [DONE]',
      '',
    ].join('\n')

    const result = parseBodyAuditResponse({
      body,
      encoding: 'utf-8',
      contentType: 'text/event-stream',
    })

    expect(result.kind).toBe('text')
    expect(result.text).toBe('我需要先分析')
    expect(result.reasoningOnly).toBe(true)
  })

  test('prefers final content over reasoning in a mixed chat stream', () => {
    const body = [
      'data: {"choices":[{"delta":{"reasoning_content":"思考"}}]}',
      '',
      'data: {"choices":[{"delta":{"content":"最终答复"}}]}',
      '',
      'data: [DONE]',
      '',
    ].join('\n')

    const result = parseBodyAuditResponse({
      body,
      encoding: 'utf-8',
      contentType: 'text/event-stream',
    })

    expect(result.kind).toBe('text')
    expect(result.text).toBe('最终答复')
    expect(result.reasoningOnly).toBe(false)
  })
})
