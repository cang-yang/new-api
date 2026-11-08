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

export const worldbookNormalizedRuleSchema = z.object({
  field: z.string(),
  action: z.enum(['mapped', 'ignored', 'risk']),
  detail: z.string().optional(),
})

export const worldbookEntrySchema = z
  .object({
    id: z.string().min(1),
    name: z.string(),
    content: z.string().max(64000),
    enabled: z.boolean(),
    constant: z.boolean(),
    keys: z.array(z.string().max(256)).max(64),
    secondary_keys: z.array(z.string().max(256)).max(64),
    secondary_logic: z.enum(['none', 'any', 'all', 'not_any', 'not_all']),
    case_sensitive: z.boolean(),
    whole_words: z.boolean(),
    priority: z.number().int(),
    prevent_recursion: z.boolean().optional(),
    injection_mode: z
      .enum([
        'follow_global',
        'inherit',
        'system_prelude',
        'before_last_user',
        'at_depth',
      ])
      .optional(),
    injection_depth: z.number().int().min(0).max(20).optional(),
    normalized_rules: z.array(worldbookNormalizedRuleSchema).optional(),
    issues: z.array(z.string()).optional(),
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
    injection_mode: z
      .enum(['inherit', 'system_prelude', 'before_last_user', 'at_depth'])
      .default('inherit'),
    injection_depth: z.number().int().min(0).max(20).default(0),
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

// Imported behavior the gateway cannot reproduce is marked "risk" and the
// entry arrives disabled. The user reviews it once, then confirms the
// supported subset.
export function worldbookRiskFields(entry: WorldbookEntry): string[] {
  return (entry.normalized_rules ?? [])
    .filter((rule) => rule.action === 'risk')
    .map((rule) => rule.field)
}

export function worldbookNeedsReview(entry: WorldbookEntry): boolean {
  return !!entry.issues?.length || worldbookRiskFields(entry).length > 0
}

// An entry with unresolved validation issues can never run. It must not be
// enabled or "confirmed" until the source data is corrected; the switch stays
// interactive so the operator can see why it is blocked.
export function worldbookEntryBlocked(entry: WorldbookEntry): boolean {
  return !!entry.issues?.length
}

export function worldbookConfirmEntry(entry: WorldbookEntry): WorldbookEntry {
  if (worldbookEntryBlocked(entry)) {
    return entry
  }
  return {
    ...entry,
    enabled: true,
    normalized_rules: (entry.normalized_rules ?? []).filter(
      (rule) => rule.action !== 'risk'
    ),
  }
}

export function worldbookFieldLabel(field: string): string {
  const extension = field.startsWith('extensions.')
    ? field.slice('extensions.'.length)
    : ''
  switch (extension || field) {
    case 'position':
      return 'Tavern insertion position'
    case 'depth':
      return 'Tavern insertion depth'
    case 'display_index':
      return 'Tavern display order'
    case 'probability':
    case 'useProbability':
      return 'Random trigger probability'
    case 'characterFilter':
      return 'Character or scenario filter'
    case 'group':
    case 'groupWeight':
    case 'groupScoring':
      return 'Entry grouping'
    case 'sticky':
      return 'Sticky activation'
    case 'cooldown':
      return 'Cooldown'
    case 'delay':
      return 'Activation delay'
    case 'role':
      return 'Tavern role override'
    default:
      return 'Advanced Tavern option'
  }
}

export function worldbookActionLabel(action: string): string {
  switch (action) {
    case 'mapped':
      return 'Mapped'
    case 'risk':
      return 'Not supported'
    case 'ignored':
    default:
      return 'Ignored'
  }
}

export function worldbookIssueLabel(issue: string): string {
  switch (issue) {
    case 'regex':
      return 'Invalid regular expression'
    case 'selectiveLogic':
      return 'Invalid secondary-key logic'
    default:
      return 'Invalid entry data'
  }
}

export function worldbookWarningLabel(warning: string): string {
  switch (warning) {
    case 'Worldbook scan text was limited to 256 KiB':
      return 'The scan was limited to the most recent 256 KiB of conversation text.'
    case 'Worldbook matching stopped after its work limit':
      return 'Worldbook matching stopped at the safety limit.'
    default:
      return 'The worldbook reported a compatibility issue.'
  }
}
export type WorldbookPlacement = {
  mode: 'inherit' | 'system_prelude' | 'before_last_user' | 'at_depth'
  index: number
  role: string
  fallback: boolean
}
export type WorldbookPreviewResult = {
  text: string
  entries: {
    book_id: string
    id: string
    name: string
    status: 'included' | 'skipped'
    reason: string
    order?: number
    placement?: WorldbookPlacement
  }[]
  warnings: string[]
  placement?: WorldbookPlacement
}

export function createWorldbookConfig(): WorldbookConfig {
  return {
    enabled: false,
    scan_depth: 4,
    max_chars: 12000,
    recursive: false,
    injection_mode: 'inherit',
    injection_depth: 0,
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
