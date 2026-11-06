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

export const worldbookEntrySchema = z
  .object({
    id: z.string().min(1),
    name: z.string(),
    content: z.string().max(128000),
    enabled: z.boolean(),
    constant: z.boolean(),
    keys: z.array(z.string().max(512)).max(64),
    secondary_keys: z.array(z.string().max(512)).max(64),
    secondary_logic: z.enum(['none', 'any', 'all', 'not_any', 'not_all']),
    case_sensitive: z.boolean(),
    whole_words: z.boolean(),
    priority: z.number().int(),
    unsupported: z.array(z.string()).optional(),
    source: z.unknown().optional(),
  })
  .passthrough()

export const worldbookBookSchema = z
  .object({
    id: z.string().min(1),
    name: z.string(),
    enabled: z.boolean(),
    entries: z.array(worldbookEntrySchema).max(1000),
    warnings: z.array(z.string()).optional(),
  })
  .passthrough()

export const worldbookSchema = z
  .object({
    enabled: z.boolean(),
    scan_depth: z.number().int().min(1).max(50),
    max_chars: z.number().int().min(256).max(64000),
    recursive: z.boolean(),
    books: z.array(worldbookBookSchema).max(16),
  })
  .passthrough()
  .refine(
    (value) =>
      value.books.reduce((sum, book) => sum + book.entries.length, 0) <= 1000
  )

export type WorldbookConfig = z.infer<typeof worldbookSchema>
export type Worldbook = z.infer<typeof worldbookBookSchema>
export type WorldbookEntry = z.infer<typeof worldbookEntrySchema>
export type WorldbookPreviewResult = {
  text: string
  entries: {
    book_id: string
    id: string
    name: string
    status: 'included' | 'skipped'
    reason: string
  }[]
  warnings: string[]
}

export function createWorldbookConfig(): WorldbookConfig {
  return {
    enabled: false,
    scan_depth: 4,
    max_chars: 12000,
    recursive: false,
    books: [],
  }
}

export function parseWorldbook(
  value: string | undefined
): WorldbookConfig | null {
  if (!value?.trim()) {
    return createWorldbookConfig()
  }
  try {
    const parsed = worldbookSchema.safeParse(JSON.parse(value))
    return parsed.success ? parsed.data : null
  } catch {
    return null
  }
}
