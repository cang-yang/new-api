import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
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

import { parseBodyAuditResponse } from '../../lib/body-audit-response'
import { BodyAuditResultContent } from '../body-audit-result'

describe('body audit result content', () => {
  test('shows decoded tool text with its field name without executing HTML', () => {
    const result = parseBodyAuditResponse({
      encoding: 'utf-8',
      body: JSON.stringify({
        choices: [
          {
            message: {
              content: 'Answer',
              tool_calls: [
                {
                  function: {
                    name: 'game_content',
                    arguments: JSON.stringify({
                      content:
                        'First line\nSecond line\n<script>alert(1)</script>',
                      path: String.raw`C:\new\file`,
                    }),
                  },
                },
              ],
            },
          },
        ],
      }),
    })
    const markup = renderToStaticMarkup(
      createElement(BodyAuditResultContent, {
        result,
        emptyLabel: 'Empty',
        imageAlt: 'Image',
      })
    )
    expect(markup).toContain('First line\nSecond line\n&lt;script&gt;')
    expect(markup).toContain('content')
    expect(markup).toContain(String.raw`C:\new\file`)
    expect(markup).not.toContain('<script>')
  })

  test('renders image results with an accessible preview and caption', () => {
    const markup = renderToStaticMarkup(
      createElement(BodyAuditResultContent, {
        emptyLabel: 'No readable result',
        imageAlt: 'Generated image',
        result: {
          kind: 'image',
          text: '',
          media: [
            {
              kind: 'image',
              source: 'https://cdn.example/generated.png',
              label: 'a blue mountain',
            },
          ],
          structured: null,
          isStream: false,
        },
      })
    )

    expect(markup).toMatch(/<img/)
    expect(markup).toMatch(/alt="a blue mountain"/)
    expect(markup).toMatch(/loading="lazy"/)
    expect(markup).toMatch(/<figcaption[^>]*>a blue mountain<\/figcaption>/)
  })

  test('renders playable audio and video results', () => {
    const audioMarkup = renderToStaticMarkup(
      createElement(BodyAuditResultContent, {
        emptyLabel: 'No readable result',
        imageAlt: 'Generated image',
        result: {
          kind: 'audio',
          text: '',
          media: [
            {
              kind: 'audio',
              source: 'https://cdn.example/generated.mp3',
            },
          ],
          structured: null,
          isStream: false,
        },
      })
    )
    const videoMarkup = renderToStaticMarkup(
      createElement(BodyAuditResultContent, {
        emptyLabel: 'No readable result',
        imageAlt: 'Generated image',
        result: {
          kind: 'video',
          text: '',
          media: [
            {
              kind: 'video',
              source: 'https://cdn.example/generated.mp4',
            },
          ],
          structured: null,
          isStream: false,
        },
      })
    )

    expect(audioMarkup).toMatch(/<audio[^>]*controls=""/)
    expect(videoMarkup).toMatch(/<video[^>]*controls=""/)
  })

  test('renders unknown JSON as a readable structured result', () => {
    const markup = renderToStaticMarkup(
      createElement(BodyAuditResultContent, {
        emptyLabel: 'No readable result',
        imageAlt: 'Generated image',
        result: {
          kind: 'structured',
          text: '',
          media: [],
          structured: { status: 'completed', result: [1, 2] },
          isStream: false,
        },
      })
    )

    expect(markup).toMatch(/<pre/)
    expect(markup).toMatch(/&quot;status&quot;: &quot;completed&quot;/)
    expect(markup).toMatch(/&quot;result&quot;: \[/)
  })
})
