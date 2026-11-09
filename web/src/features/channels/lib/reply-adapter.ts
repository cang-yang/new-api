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
import { z } from 'zod'

import { presetRecord as record } from './preset-script-preparation'

export function validReplyPointer(pointer: string): boolean {
  if (new TextEncoder().encode(pointer).length > 2048) return false
  if (pointer === '') return true
  if (!pointer.startsWith('/') || pointer.split('/').length > 33) return false
  return pointer
    .slice(1)
    .split('/')
    .every(
      (part) =>
        !/~(?![01])/.test(part) &&
        !['__proto__', 'prototype', 'constructor', '*', '-'].includes(
          part.replaceAll('~1', '/').replaceAll('~0', '~')
        )
    )
}
export const replyAdapterSchema = z
  .object({
    version: z.literal(2),
    containers: z
      .array(
        z
          .object({
            name: z.string().regex(/^[A-Za-z0-9_-]{1,64}$/),
            argument_pointer: z.string().refine(validReplyPointer).optional(),
            formatter: z.boolean().optional(),
            output_kind: z.enum(['text', 'json_object']),
            source_prompt_id: z.string().min(1).max(256).optional(),
            source_fingerprint: z
              .string()
              .regex(/^[a-f0-9]{64}$/)
              .optional(),
          })
          .refine(
            (item) =>
              (item.argument_pointer !== undefined) !==
                (item.formatter === true) &&
              (item.source_prompt_id !== undefined) ===
                (item.source_fingerprint !== undefined)
          )
      )
      .max(32),
  })
  .refine(
    (adapter) =>
      new Set(adapter.containers.map((item) => item.name)).size ===
      adapter.containers.length
  )

export function validReplyAdapter(config: Record<string, unknown>): boolean {
  return (
    config.reply_adapter === undefined ||
    replyAdapterSchema.safeParse(config.reply_adapter).success
  )
}

export type ReplyContainer = {
  name: string
  argument_pointer?: string
  formatter?: boolean
  output_kind: 'text' | 'json_object'
  source_prompt_id?: string
  source_fingerprint?: string
}
export type ReplyAdapter = { version: 2; containers: ReplyContainer[] }
export type ReplySampleField = {
  pointer: string
  kind: 'text' | 'json_object'
  preview: string
}

export function replySampleFields(sample: string): ReplySampleField[] {
  if (sample.length > 262144) return []
  let root: unknown
  try {
    root = JSON.parse(sample)
  } catch {
    return []
  }
  if (!root || typeof root !== 'object' || Array.isArray(root)) return []
  const fields: ReplySampleField[] = []
  const pending: { value: unknown; pointer: string; depth: number }[] = [
    { value: root, pointer: '', depth: 0 },
  ]
  while (pending.length && fields.length < 256) {
    const current = pending.pop()
    if (!current) break
    const value = current.value
    if (typeof value === 'string') {
      fields.push({
        pointer: current.pointer,
        kind: 'text',
        preview: value.slice(0, 120),
      })
    } else if (value && typeof value === 'object') {
      if (!Array.isArray(value)) {
        fields.push({
          pointer: current.pointer,
          kind: 'json_object',
          preview: '{…}',
        })
      }
      if (current.depth < 32) {
        const entries = Object.entries(value).slice(0, 256)
        for (let i = entries.length - 1; i >= 0; i--) {
          const [key, child] = entries[i]
          if (
            ['__proto__', 'constructor', 'prototype', '*', '-'].includes(key)
          ) {
            continue
          }
          pending.push({
            value: child,
            pointer: `${current.pointer}/${key
              .replaceAll('~', '~0')
              .replaceAll('/', '~1')}`,
            depth: current.depth + 1,
          })
        }
      }
    }
  }
  return fields
}

export function migrateReplyAdapter(config: Record<string, unknown>): {
  status: 'none' | 'ready' | 'requires_review'
  adapter?: ReplyAdapter
} {
  if (record(config.reply_adapter).version === 2) {
    return replyAdapterSchema.safeParse(config.reply_adapter).success
      ? { status: 'ready', adapter: config.reply_adapter as ReplyAdapter }
      : { status: 'requires_review' }
  }
  const legacy = record(config.tool_protocol)
  if (legacy.enabled !== true) return { status: 'none' }
  if (
    legacy.client_final ||
    (Array.isArray(legacy.auxiliary) && legacy.auxiliary.length) ||
    (Array.isArray(legacy.action) && legacy.action.length) ||
    (legacy.phase_policy && legacy.phase_policy !== 'disabled')
  ) {
    return { status: 'requires_review' }
  }
  const names = [
    ...new Set(
      [
        ...(Array.isArray(legacy.content) ? legacy.content : []),
        legacy.preset_final,
      ].filter((x): x is string => typeof x === 'string' && !!x)
    ),
  ]
  const argument =
    typeof legacy.response_argument === 'string' ? legacy.response_argument : ''
  if (
    !names.length ||
    !/^[A-Za-z_][A-Za-z0-9_]*(\.[A-Za-z_][A-Za-z0-9_]*)*$/.test(argument)
  ) {
    return { status: 'requires_review' }
  }
  return {
    status: 'ready',
    adapter: {
      version: 2,
      containers: names.map((name) => ({
        name,
        argument_pointer: `/${argument.replaceAll('.', '/')}`,
        output_kind: legacy.response_target === 'json' ? 'json_object' : 'text',
      })),
    },
  }
}
