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

import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'

import { BodyAuditResultContent } from '../body-audit-result'

describe('body audit result content', () => {
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

    assert.match(markup, /<img/)
    assert.match(markup, /alt="a blue mountain"/)
    assert.match(markup, /loading="lazy"/)
    assert.match(markup, /<figcaption[^>]*>a blue mountain<\/figcaption>/)
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

    assert.match(audioMarkup, /<audio[^>]*controls=""/)
    assert.match(videoMarkup, /<video[^>]*controls=""/)
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

    assert.match(markup, /<pre/)
    assert.match(markup, /&quot;status&quot;: &quot;completed&quot;/)
    assert.match(markup, /&quot;result&quot;: \[/)
  })
})
