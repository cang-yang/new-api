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
export type PresetEntry = {
  identifier: string
  name: string
  content: string
  role: string
  marker: boolean
  enabled: boolean
  originalEnabled: boolean
  injection_position: number
  injection_depth: number
  injection_order: number
}

export type PresetRegex = {
  id: string
  name: string
  enabled: boolean
  promptOnly: boolean
  markdownOnly: boolean
  responseSide: boolean
  sendSide: boolean
  generatesHtml: boolean
}

export function parsePresetEditor(value: string | Record<string, unknown>): {
  config: Record<string, unknown>
  entries: PresetEntry[]
  regexScripts: PresetRegex[]
} | null {
  try {
    const config =
      typeof value === 'string'
        ? (JSON.parse(value) as Record<string, unknown>)
        : value
    const preset = config.preset as {
      prompts: Array<{
        identifier: string
        name?: string
        content?: string
        role?: string
        marker?: boolean
        injection_position?: number
        injection_depth?: number
        injection_order?: number
      }>
      prompt_order: Array<{
        character_id: number
        order: Array<{ identifier: string; enabled: boolean }>
      }>
      extensions?: {
        regex_scripts?: Array<{
          id?: string
          scriptName?: string
          disabled?: boolean
          promptOnly?: boolean
          markdownOnly?: boolean
          placement?: number[]
          replaceString?: string
        }>
      }
    }
    if (
      !Array.isArray(preset?.prompts) ||
      !Array.isArray(preset.prompt_order)
    ) {
      return null
    }
    const order =
      (
        preset.prompt_order.find((item) => item.character_id === 100001) ||
        preset.prompt_order[0]
      )?.order || []
    const overrides = (config.entry_overrides || {}) as Record<string, boolean>
    const prompts = new Map(
      preset.prompts.map((prompt) => [prompt.identifier, prompt])
    )
    const orderedIdentifiers = new Set(order.map((item) => item.identifier))
    const allEntries = [
      ...order,
      ...preset.prompts
        .filter((prompt) => !orderedIdentifiers.has(prompt.identifier))
        .map((prompt) => ({ identifier: prompt.identifier, enabled: false })),
    ]
    const entries = allEntries.flatMap((item) => {
      const prompt = prompts.get(item.identifier)
      if (!prompt) return []
      return [
        {
          identifier: item.identifier,
          name: prompt.name ?? item.identifier,
          content: prompt.content || '',
          role: prompt.role || 'system',
          marker: prompt.marker === true,
          enabled: overrides[item.identifier] ?? item.enabled,
          originalEnabled: item.enabled,
          injection_position: prompt.injection_position ?? 0,
          injection_depth: prompt.injection_depth ?? 0,
          injection_order: prompt.injection_order ?? 0,
        },
      ]
    })
    const regexOverrides = (config.regex_overrides || {}) as Record<
      string,
      boolean
    >
    const regexScripts = (preset.extensions?.regex_scripts || []).map(
      (script) => ({
        id: script.id || '',
        name: script.scriptName || script.id || 'Regex',
        enabled: script.id
          ? (regexOverrides[script.id] ?? !script.disabled)
          : !script.disabled,
        promptOnly: script.promptOnly === true,
        markdownOnly: script.markdownOnly === true,
        sendSide:
          Array.isArray(script.placement) &&
          script.placement.some(
            (placement) => placement === 1 || placement === 2
          ) &&
          !(script.markdownOnly && !script.promptOnly),
        generatesHtml:
          typeof script.replaceString === 'string' &&
          /<!doctype\s+html|<\/?(?:html|head|body|script|style|div|span|iframe|svg|table|details|button|input|img|a|p|link|meta|section|article|canvas|video|audio)(?:\s|\/?>)/i.test(
            script.replaceString
          ),
        responseSide:
          Array.isArray(script.placement) &&
          script.placement.includes(2) &&
          !(script.promptOnly && !script.markdownOnly),
      })
    )
    return { config, entries, regexScripts }
  } catch {
    return null
  }
}

type EditablePreset = {
  prompts: Array<Record<string, unknown> & { identifier: string }>
  prompt_order: Array<{
    character_id: number
    order: Array<{
      identifier: string
      enabled: boolean
      [key: string]: unknown
    }>
  }>
}

export type PresetEntryChanges = Partial<
  Pick<
    PresetEntry,
    | 'name'
    | 'role'
    | 'content'
    | 'injection_position'
    | 'injection_depth'
    | 'injection_order'
  >
>

export function editPresetConfigEntry(
  config: Record<string, unknown>,
  identifier: string,
  changes: PresetEntryChanges
): Record<string, unknown> {
  const preset = config.preset as EditablePreset
  const prompt = preset.prompts.find((item) => item.identifier === identifier)
  if (
    !prompt ||
    (prompt.marker && Object.keys(changes).some((key) => key !== 'name'))
  ) {
    return config
  }
  return {
    ...config,
    preset: {
      ...preset,
      prompts: preset.prompts.map((item) =>
        item === prompt ? { ...item, ...changes } : item
      ),
    },
  }
}

export function reorderPresetEntries(
  config: Record<string, unknown>,
  identifier: string,
  targetIdentifier: string
): Record<string, unknown> {
  const parsed = parsePresetEditor(config)
  if (!parsed || identifier === targetIdentifier) return config
  const preset = config.preset as EditablePreset
  const active =
    preset.prompt_order.find((item) => item.character_id === 100001) ||
    preset.prompt_order[0]
  const order = [...(active?.order || [])]
  const known = new Set(order.map((item) => item.identifier))
  order.push(
    ...parsed.entries
      .filter((entry) => !known.has(entry.identifier))
      .map((entry) => ({
        identifier: entry.identifier,
        enabled: entry.originalEnabled,
      }))
  )
  const from = order.findIndex((item) => item.identifier === identifier)
  const to = order.findIndex((item) => item.identifier === targetIdentifier)
  if (from < 0 || to < 0) return config
  order.splice(to, 0, order.splice(from, 1)[0])
  const prompt_order = active
    ? preset.prompt_order.map((item) =>
        item === active ? { ...item, order } : item
      )
    : [{ character_id: 100001, order }]
  return { ...config, preset: { ...preset, prompt_order } }
}

export function editPresetEntry(
  value: string,
  identifier: string,
  changes: Partial<
    Pick<
      PresetEntry,
      | 'name'
      | 'role'
      | 'content'
      | 'injection_position'
      | 'injection_depth'
      | 'injection_order'
    >
  >
): string {
  const parsed = parsePresetEditor(value)
  if (!parsed) return value
  const preset = parsed.config.preset as EditablePreset
  const prompt = preset.prompts.find((item) => item.identifier === identifier)
  if (!prompt) return value
  if (prompt.marker && Object.keys(changes).some((key) => key !== 'name')) {
    return value
  }
  Object.assign(prompt, changes)
  return JSON.stringify(parsed.config)
}

export function movePresetEntry(
  value: string,
  identifier: string,
  direction: -1 | 1
): string {
  const parsed = parsePresetEditor(value)
  if (!parsed) return value
  const preset = parsed.config.preset as EditablePreset
  let active =
    preset.prompt_order.find((item) => item.character_id === 100001) ||
    preset.prompt_order[0]
  if (!active) {
    active = { character_id: 100001, order: [] }
    preset.prompt_order.push(active)
  }
  const known = new Set(active.order.map((item) => item.identifier))
  active.order.push(
    ...parsed.entries
      .filter((entry) => !known.has(entry.identifier))
      .map((entry) => ({
        identifier: entry.identifier,
        enabled: entry.originalEnabled,
      }))
  )
  const index = active.order.findIndex((item) => item.identifier === identifier)
  const target = index + direction
  if (index < 0 || target < 0 || target >= active.order.length) return value
  ;[active.order[index], active.order[target]] = [
    active.order[target],
    active.order[index],
  ]
  return JSON.stringify(parsed.config)
}

export function migratePresetPatches(value: string): {
  value: string
  error?: string
} {
  const parsed = parsePresetEditor(value)
  if (!parsed) return { value, error: 'Invalid preset' }
  const patches = parsed.config.patches
  if (patches === undefined) return { value }
  if (!Array.isArray(patches)) return { value, error: 'Invalid legacy patches' }
  const preset = parsed.config.preset as EditablePreset
  const encoder = new TextEncoder()
  let totalBytes = preset.prompts.reduce(
    (sum, prompt) =>
      sum +
      (typeof prompt.content === 'string'
        ? encoder.encode(prompt.content).length
        : 0),
    0
  )
  for (const patch of patches) {
    if (
      !patch ||
      typeof patch.find !== 'string' ||
      !patch.find ||
      typeof patch.replace !== 'string'
    ) {
      return { value, error: 'Invalid legacy patches' }
    }
    const targets = preset.prompts.filter(
      (prompt) => !patch.identifier || prompt.identifier === patch.identifier
    )
    if (!targets.length || targets.some((prompt) => prompt.marker)) {
      return {
        value,
        error:
          'Legacy patches target missing entries or dynamic markers. They are preserved; import a corrected preset to replace them.',
      }
    }
    if (
      !targets.some(
        (prompt) =>
          typeof prompt.content === 'string' &&
          prompt.content.includes(patch.find)
      )
    ) {
      return {
        value,
        error:
          'Legacy patch text was not found. The saved patches are preserved to avoid losing changes.',
      }
    }
    for (const target of targets) {
      if (typeof target.content === 'string') {
        let occurrences = 0
        let cursor = target.content.indexOf(patch.find)
        while (cursor !== -1) {
          occurrences++
          cursor = target.content.indexOf(
            patch.find,
            cursor + patch.find.length
          )
        }
        const bytes =
          encoder.encode(target.content).length +
          occurrences *
            (encoder.encode(patch.replace).length -
              encoder.encode(patch.find).length)
        totalBytes += bytes - encoder.encode(target.content).length
        if (bytes > 8 * 1024 * 1024 || totalBytes > 6 * 1024 * 1024) {
          return {
            value,
            error:
              'Converted preset exceeds the size limit. The original changes are preserved.',
          }
        }
        target.content = target.content.split(patch.find).join(patch.replace)
      }
    }
  }
  delete parsed.config.patches
  const result = JSON.stringify(parsed.config)
  if (new TextEncoder().encode(result).length > 6 * 1024 * 1024) {
    return {
      value,
      error:
        'Converted preset exceeds the size limit. The original changes are preserved.',
    }
  }
  return { value: result }
}

export function updatePresetEntries(
  value: string,
  identifiers: string[],
  enabled: boolean
): string {
  const parsed = parsePresetEditor(value)
  if (!parsed) return value
  const overrides = {
    ...((parsed.config.entry_overrides || {}) as Record<string, boolean>),
  }
  for (const identifier of identifiers) overrides[identifier] = enabled
  return JSON.stringify({ ...parsed.config, entry_overrides: overrides })
}
