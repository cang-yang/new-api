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

export interface AuditReadingField {
  path: string
  text: string
}

// Values have already been JSON-decoded. Never unescape string contents again:
// literal backslashes, embedded source code and model tags must remain intact.
export function auditReadingFields(value: unknown): AuditReadingField[] {
  const fields: AuditReadingField[] = []
  function visit(item: unknown, path: string, depth: number) {
    if (item && typeof item === 'object' && depth < 20) {
      const entries = Object.entries(item)
      if (entries.length) {
        for (const [key, nested] of entries) {
          const segment = Array.isArray(item)
            ? `[${key}]`
            : `[${JSON.stringify(key)}]`
          visit(nested, path + segment, depth + 1)
        }
        return
      }
    }
    fields.push({
      path,
      text:
        typeof item === 'string' ? item : (JSON.stringify(item, null, 2) ?? ''),
    })
  }
  visit(value, '$', 0)
  return fields
}

export function auditReadableCopy(result: ParsedBodyAuditResponse): string {
  const sections: string[] = []
  if (result.text && !result.reasoningOnly) sections.push(result.text)
  for (const tool of result.toolCalls ?? []) {
    const args = tool.valid
      ? auditReadingFields(JSON.parse(tool.arguments))
          .map((field) => `${field.path}\n${field.text}`)
          .join('\n\n')
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
