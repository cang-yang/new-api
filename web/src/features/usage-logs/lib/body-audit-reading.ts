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
import type { ParsedBodyAuditResponse } from './body-audit-response'

// Inspection-only pretty printing. Invalid event data and unknown fields stay
// visible. The raw tab and raw copy never consume this transformed string.
export function formatAuditStream(body: string): string {
  if (!/^(?:data:|event:|id:|:)/m.test(body)) return body
  return body
    .replaceAll(/\r\n?/g, '\n')
    .split('\n\n')
    .map((block) => {
      const lines = block.split('\n')
      const data = lines.filter((line) => line.startsWith('data:'))
      if (!data.length) return block
      try {
        const value: unknown = JSON.parse(
          data.map((line) => line.slice(5).replace(/^ /, '')).join('\n')
        )
        let inserted = false
        return lines
          .flatMap((line) => {
            if (!line.startsWith('data:')) return [line]
            if (inserted) return []
            inserted = true
            return [`data: ${JSON.stringify(value, null, 2)}`]
          })
          .join('\n')
      } catch {
        return block
      }
    })
    .join('\n\n')
}

export function auditRecord(value: unknown): Record<string, unknown> | null {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return null
  return value as Record<string, unknown>
}

// Every property goes into a section, including unknown provider extensions.
export function auditRequestSections(value: unknown) {
  const data = auditRecord(value)
  if (!data) return null
  const messages: { key: string; role: string; value: unknown }[] = []
  const context: Record<string, unknown> = Object.create(null)
  const tools: Record<string, unknown> = Object.create(null)
  const parameters: Record<string, unknown> = Object.create(null)
  for (const [key, item] of Object.entries(data)) {
    if (['messages', 'contents', 'input'].includes(key)) {
      const items = Array.isArray(item) ? item : [item]
      if (!items.length) parameters[key] = item
      for (const [index, message] of items.entries()) {
        const record = auditRecord(message)
        let role = typeof message === 'string' ? 'user' : 'message'
        if (typeof record?.role === 'string') role = record.role
        messages.push({ key: `${key}:${index}`, role, value: message })
      }
    } else if (['system', 'instructions', 'systemInstruction'].includes(key)) {
      context[key] = item
    } else if (['tools', 'functions'].includes(key)) {
      tools[key] = item
    } else {
      parameters[key] = item
    }
  }
  return { messages, context, tools, parameters }
}

// A reconstruction for inspection, never a replacement for the capture.
export function auditResultDocument(result: ParsedBodyAuditResponse): unknown {
  if (result.kind === 'structured') return result.structured
  return {
    outputs: result.outputs?.length
      ? result.outputs
      : [
          {
            text: result.reasoningOnly ? '' : result.text,
            reasoning: result.reasoning || '',
          },
        ],
    tool_calls:
      result.toolCalls?.map((tool) => ({
        id: tool.id,
        name: tool.name,
        choice_index: tool.choiceIndex,
        arguments: tool.valid ? JSON.parse(tool.arguments) : tool.arguments,
      })) ?? [],
    media: result.media,
    warnings: result.warnings ?? [],
  }
}

export function auditReadableCopy(result: ParsedBodyAuditResponse): string {
  const sections: string[] = []
  if (result.text && !result.reasoningOnly) sections.push(result.text)
  for (const tool of result.toolCalls ?? []) {
    const args = tool.valid
      ? JSON.stringify(JSON.parse(tool.arguments), null, 2)
      : tool.arguments
    sections.push(`[${tool.name || 'tool'}]\n${args}`)
  }
  if (result.reasoning) sections.push(`[reasoning]\n${result.reasoning}`)
  if (sections.length) return sections.join('\n\n')
  if (result.media.length) {
    return result.media.map((item) => item.source).join('\n')
  }
  if (result.structured == null) return ''
  return typeof result.structured === 'string'
    ? result.structured
    : JSON.stringify(result.structured, null, 2)
}
