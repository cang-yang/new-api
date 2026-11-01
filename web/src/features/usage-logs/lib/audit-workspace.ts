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
import type { TFunction } from 'i18next'

import type {
  BodyAuditMediaItem,
  ParsedBodyAuditResponse,
} from './body-audit-response'

export interface AuditDocument {
  id: string
  category:
    | 'message'
    | 'context'
    | 'parameters'
    | 'definitions'
    | 'answer'
    | 'reasoning'
    | 'tool'
    | 'data'
    | 'media'
  value: unknown
  name?: string
  ordinal?: number
  invalid?: boolean
  metadata?: Record<string, unknown>
  media?: BodyAuditMediaItem
}

export interface AuditCapture {
  body: string
  size?: number
  encoding: 'utf-8' | 'base64'
  contentType?: string
  truncated?: boolean
  incomplete?: boolean
  request?: boolean
  derived?: boolean
}

export function objectValue(value: unknown): Record<string, unknown> | null {
  return value !== null && typeof value === 'object' && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : null
}

export function displayValue(value: unknown): string {
  if (typeof value === 'string') return value
  return JSON.stringify(value, null, 2) ?? ''
}

export function requestDocuments(value: unknown): AuditDocument[] {
  const root = objectValue(value)
  if (!root) return [{ id: 'data', category: 'data', value }]
  const documents: AuditDocument[] = []
  const parameters: Record<string, unknown> = Object.create(null)
  for (const [field, item] of Object.entries(root)) {
    if (['messages', 'contents', 'input'].includes(field) && item !== null) {
      const messages = Array.isArray(item) ? item : [item]
      if (!messages.length) parameters[field] = item
      messages.forEach((message, index) => {
        const record = objectValue(message)
        let content: unknown = message
        let metadata: Record<string, unknown> | undefined
        if (
          record &&
          (Object.hasOwn(record, 'content') || Object.hasOwn(record, 'parts'))
        ) {
          const contentField = Object.hasOwn(record, 'content')
            ? 'content'
            : 'parts'
          content = record[contentField]
          metadata = Object.fromEntries(
            Object.entries(record).filter(
              ([key]) =>
                key !== contentField &&
                (key !== 'role' || typeof record.role !== 'string')
            )
          )
        }
        documents.push({
          id: `${field}:${index}`,
          category: 'message',
          value: content,
          name: typeof record?.role === 'string' ? record.role : undefined,
          ordinal: index + 1,
          metadata,
        })
      })
    } else if (
      ['system', 'instructions', 'systemInstruction'].includes(field)
    ) {
      documents.push({
        id: field,
        category: 'context',
        value: item,
        name: field,
      })
    } else if (['tools', 'functions'].includes(field)) {
      documents.push({
        id: field,
        category: 'definitions',
        value: item,
        name: field,
      })
    } else {
      parameters[field] = item
    }
  }
  if (Object.keys(parameters).length || !documents.length) {
    documents.push({
      id: 'parameters',
      category: 'parameters',
      value: parameters,
    })
  }
  return documents
}

export function responseDocuments(
  result: ParsedBodyAuditResponse
): AuditDocument[] {
  const documents: AuditDocument[] = []
  const outputs = result.outputs?.length
    ? result.outputs
    : [
        {
          index: 0,
          text: result.reasoningOnly ? '' : result.text,
          reasoning: result.reasoning || '',
        },
      ]
  for (const output of outputs) {
    if (output.text) {
      documents.push({
        id: `answer:${output.index}`,
        category: 'answer',
        ordinal: output.index + 1,
        value: output.text,
      })
    }
  }
  for (const tool of result.toolCalls ?? []) {
    let value: unknown = tool.arguments
    let invalid = !tool.valid
    if (tool.valid) {
      try {
        value = JSON.parse(tool.arguments)
      } catch {
        invalid = true
      }
    }
    documents.push({
      id: `tool:${tool.key}`,
      category: 'tool',
      name: tool.name,
      ordinal: tool.choiceIndex + 1,
      value,
      invalid,
      metadata: { id: tool.id, choice: tool.choiceIndex + 1 },
    })
  }
  for (const output of outputs) {
    if (output.reasoning) {
      documents.push({
        id: `reasoning:${output.index}`,
        category: 'reasoning',
        ordinal: output.index + 1,
        value: output.reasoning,
      })
    }
  }
  result.media.forEach((media, index) =>
    documents.push({
      id: `media:${index}`,
      category: 'media',
      ordinal: index + 1,
      name: media.kind,
      value: media.source,
      media,
    })
  )
  if (
    !documents.length &&
    (result.kind === 'structured' || result.structured !== null)
  ) {
    documents.push({ id: 'data', category: 'data', value: result.structured })
  }
  return documents
}

export function documentTitle(document: AuditDocument, t: TFunction): string {
  const labels = {
    message: t('Message'),
    context: t('Context'),
    parameters: t('Parameters'),
    definitions: t('Tool definitions'),
    answer: t('Answer'),
    reasoning: t('Reasoning'),
    tool: t('Tool call'),
    data: t('Structured content'),
    media: t('Media'),
  }
  if (document.category === 'message') {
    const roles: Record<string, string> = {
      system: t('System'),
      user: t('User'),
      assistant: t('Assistant'),
      developer: t('Developer'),
      tool: t('Tool'),
      model: t('Assistant'),
    }
    const role =
      document.name && Object.hasOwn(roles, document.name)
        ? roles[document.name]
        : document.name
    return `${document.ordinal} · ${role || labels.message}`
  }
  return [labels[document.category], document.name, document.ordinal]
    .filter(Boolean)
    .join(' · ')
}

// Formatting is a view only. Captured bytes never pass through this function
// when copied as raw data; strings are never recursively unescaped.
export function formatCapture(body: string): string {
  try {
    return JSON.stringify(JSON.parse(body), null, 2)
  } catch {
    /* not JSON */
  }
  if (!/^data:/m.test(body)) return body
  return body
    .replaceAll(/\r\n?/g, '\n')
    .split('\n\n')
    .map((block) => {
      const lines = block.split('\n')
      const data = lines.filter((line) => line.startsWith('data:'))
      if (!data.length) return block
      try {
        const formatted = JSON.stringify(
          JSON.parse(
            data.map((line) => line.slice(5).replace(/^ /, '')).join('\n')
          ),
          null,
          2
        )
        return [
          ...lines.filter((line) => !line.startsWith('data:')),
          `data: ${formatted}`,
        ].join('\n')
      } catch {
        return block
      }
    })
    .join('\n\n')
}

export function valueAtPath(root: unknown, path: string[]): unknown {
  return path.reduce<unknown>((value, key) => {
    if (
      value === null ||
      typeof value !== 'object' ||
      !Object.hasOwn(value, key)
    ) {
      return undefined
    }
    return (value as Record<string, unknown>)[key]
  }, root)
}
