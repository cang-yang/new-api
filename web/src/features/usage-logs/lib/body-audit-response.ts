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
import {
  readAuditContent,
  readAuditStream,
  type AuditContent,
  type AuditOutput,
  type AuditToolCall,
  type AuditWarning,
} from './body-audit-content'

export interface BodyAuditMediaItem {
  kind: 'image' | 'audio' | 'video'
  source: string
  label?: string
}

export interface ParsedBodyAuditResponse {
  kind: 'text' | 'tools' | 'image' | 'audio' | 'video' | 'structured' | 'empty'
  text: string
  media: BodyAuditMediaItem[]
  structured: unknown | null
  isStream: boolean
  reasoningOnly?: boolean
  reasoning?: string
  toolCalls?: AuditToolCall[]
  outputs?: AuditOutput[]
  warnings?: AuditWarning[]
}

interface ParseBodyAuditResponseInput {
  body: string
  encoding: 'utf-8' | 'base64'
  contentType?: string
  requestPath?: string
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
    reasoningOnly: options?.reasoningOnly ?? false,
    reasoning: options?.reasoning ?? '',
    toolCalls: options?.toolCalls ?? [],
    outputs: options?.outputs ?? [],
    warnings: options?.warnings ?? [],
  }
}

function contentResult(
  content: AuditContent,
  structured: unknown,
  isStream: boolean
): ParsedBodyAuditResponse {
  const answer = content.outputs
    .map((output) => output.text)
    .filter(Boolean)
    .join('\n\n')
  const reasoning = content.outputs
    .map((output) => output.reasoning)
    .filter(Boolean)
    .join('\n\n')
  const reasoningOnly = !answer && !content.toolCalls.length && !!reasoning
  let kind: ParsedBodyAuditResponse['kind'] = 'structured'
  if (answer || reasoningOnly) kind = 'text'
  else if (content.toolCalls.length) kind = 'tools'
  return parsedResult(kind, {
    ...content,
    text: reasoningOnly ? reasoning : answer,
    reasoning,
    reasoningOnly,
    structured,
    isStream,
  })
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
    if (!mediaKind || !contentType) {
      return parsedResult('structured', {
        structured: { encoding: input.encoding, body: input.body },
      })
    }
    return parsedResult(mediaKind, {
      media: [
        {
          kind: mediaKind,
          source: `data:${contentType};base64,${input.body}`,
        },
      ],
    })
  }

  const isStream =
    contentType.includes('text/event-stream') ||
    (!contentType && /^(?:\uFEFF)?(?:data:|event:|:)/.test(input.body))

  if (isStream) {
    return contentResult(readAuditStream(input.body), input.body, true)
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
    if (typeof structured === 'string') {
      return parsedResult('text', { text: structured, structured })
    }
    return contentResult(readAuditContent(structured), structured, false)
  } catch {
    if (contentType.includes('json')) {
      return parsedResult('structured', {
        structured: input.body,
        warnings: ['invalid-json'],
      })
    }
    return parsedResult('text', { text: input.body })
  }
}
