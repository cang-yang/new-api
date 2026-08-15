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

export interface BodyAuditMediaItem {
  kind: 'image' | 'audio' | 'video'
  source: string
  label?: string
}

export interface ParsedBodyAuditResponse {
  kind: 'text' | 'image' | 'audio' | 'video' | 'structured' | 'empty'
  text: string
  media: BodyAuditMediaItem[]
  structured: unknown | null
  isStream: boolean
}

interface ParseBodyAuditResponseInput {
  body: string
  encoding: 'utf-8' | 'base64'
  contentType?: string
  requestPath?: string
}

function stringContent(value: unknown): string {
  if (typeof value === 'string') return value
  if (!Array.isArray(value)) return ''

  return value
    .map((item) => {
      if (typeof item === 'string') return item
      if (!item || typeof item !== 'object') return ''
      const part = item as Record<string, unknown>
      return (
        stringContent(part.text) ||
        stringContent(part.content) ||
        stringContent(part.output_text)
      )
    })
    .join('')
}

function extractResponseText(value: unknown, depth = 0): string {
  if (!value || typeof value !== 'object' || depth > 12) return ''
  const data = value as Record<string, unknown>

  if (typeof data.delta === 'string') return data.delta
  if (typeof data.text === 'string') return data.text
  if (typeof data.completion === 'string') return data.completion
  if (typeof data.output_text === 'string') return data.output_text

  if (data.delta && typeof data.delta === 'object') {
    const deltaText = extractResponseText(data.delta, depth + 1)
    if (deltaText) return deltaText
  }

  if (Array.isArray(data.choices)) {
    return data.choices
      .map((choice) => {
        if (!choice || typeof choice !== 'object') return ''
        const item = choice as Record<string, unknown>
        if (item.delta && typeof item.delta === 'object') {
          return extractResponseText(item.delta, depth + 1)
        }
        if (item.message && typeof item.message === 'object') {
          return extractResponseText(item.message, depth + 1)
        }
        return stringContent(item.text)
      })
      .join('')
  }

  if (Array.isArray(data.output)) {
    return data.output
      .map((item) => extractResponseText(item, depth + 1))
      .join('')
  }

  if (Array.isArray(data.candidates)) {
    return data.candidates
      .map((candidate) => extractResponseText(candidate, depth + 1))
      .join('')
  }

  const contentText = stringContent(data.content)
  if (contentText) return contentText

  const partsText = stringContent(data.parts)
  if (partsText) return partsText

  for (const key of ['message', 'content', 'data', 'response']) {
    const nested = data[key]
    if (nested && typeof nested === 'object') {
      const nestedText = extractResponseText(nested, depth + 1)
      if (nestedText) return nestedText
    }
  }
  return ''
}

function streamEventText(value: unknown): string {
  if (!value || typeof value !== 'object') return ''
  const data = value as Record<string, unknown>
  if (
    data.type === 'response.completed' ||
    data.type === 'message_stop' ||
    data.type === 'content_block_stop'
  ) {
    return ''
  }
  return extractResponseText(value)
}

function parsedResult(
  kind: ParsedBodyAuditResponse['kind'],
  options?: Partial<Omit<ParsedBodyAuditResponse, 'kind'>>
): ParsedBodyAuditResponse {
  return {
    kind,
    text: options?.text ?? '',
    media: options?.media ?? [],
    structured: options?.structured ?? null,
    isStream: options?.isStream ?? false,
  }
}

function requestedMediaKind(
  contentType: string,
  requestPath: string
): BodyAuditMediaItem['kind'] | null {
  if (contentType.startsWith('image/') || requestPath.includes('/image')) {
    return 'image'
  }
  if (contentType.startsWith('audio/') || requestPath.includes('/audio')) {
    return 'audio'
  }
  if (contentType.startsWith('video/') || requestPath.includes('/video')) {
    return 'video'
  }
  return null
}

function safeMediaSource(value: unknown): string {
  if (typeof value !== 'string') return ''
  if (/^https?:\/\//i.test(value)) return value
  if (/^data:(image|audio|video)\//i.test(value)) return value
  return ''
}

function collectMediaItems(
  value: unknown,
  kind: BodyAuditMediaItem['kind'],
  items: BodyAuditMediaItem[],
  depth = 0
): void {
  if (!value || depth > 12) return
  if (Array.isArray(value)) {
    for (const item of value) {
      collectMediaItems(item, kind, items, depth + 1)
    }
    return
  }
  if (typeof value !== 'object') return

  const data = value as Record<string, unknown>
  const source =
    safeMediaSource(data.url) ||
    safeMediaSource(data.uri) ||
    safeMediaSource(data.download_url)
  if (source && !items.some((item) => item.source === source)) {
    const item: BodyAuditMediaItem = { kind, source }
    if (typeof data.revised_prompt === 'string') {
      item.label = data.revised_prompt
    } else if (typeof data.name === 'string') {
      item.label = data.name
    }
    items.push(item)
  }

  if (kind === 'image' && typeof data.b64_json === 'string') {
    const base64Source = `data:image/png;base64,${data.b64_json}`
    if (!items.some((item) => item.source === base64Source)) {
      items.push({ kind, source: base64Source })
    }
  }

  for (const nested of Object.values(data)) {
    if (nested && typeof nested === 'object') {
      collectMediaItems(nested, kind, items, depth + 1)
    }
  }
}

export function parseBodyAuditResponse(
  input: ParseBodyAuditResponseInput
): ParsedBodyAuditResponse {
  if (!input.body) return parsedResult('empty')

  const contentType = input.contentType?.toLowerCase() ?? ''
  const requestPath = input.requestPath?.toLowerCase() ?? ''
  const mediaKind = requestedMediaKind(contentType, requestPath)
  if (input.encoding === 'base64') {
    if (!mediaKind || !contentType) return parsedResult('empty')
    return parsedResult(mediaKind, {
      media: [
        {
          kind: mediaKind,
          source: `data:${contentType};base64,${input.body}`,
        },
      ],
    })
  }

  const dataLines = input.body
    .split(/\r?\n/)
    .filter((line) => line.startsWith('data:'))
    .map((line) => line.slice(5).trim())
    .filter((line) => line && line !== '[DONE]')
  const isStream =
    contentType.includes('text/event-stream') || dataLines.length > 0

  if (isStream) {
    let text = ''
    for (const line of dataLines) {
      try {
        text += streamEventText(JSON.parse(line))
      } catch {
        // Malformed events remain available in the raw response tabs.
      }
    }
    if (text) return parsedResult('text', { text, isStream: true })
    return parsedResult('structured', {
      structured: input.body,
      isStream: true,
    })
  }

  const trimmed = input.body.trim()
  try {
    const structured: unknown = JSON.parse(trimmed)
    if (mediaKind) {
      const media: BodyAuditMediaItem[] = []
      collectMediaItems(structured, mediaKind, media)
      if (media.length > 0) {
        return parsedResult(mediaKind, { media, structured })
      }
    }
    const text = extractResponseText(structured)
    if (text) return parsedResult('text', { text, structured })
    return parsedResult('structured', { structured })
  } catch {
    return parsedResult('text', { text: input.body })
  }
}
