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

export function parsePresetEditor(value: string): {
  config: Record<string, unknown>
  entries: PresetEntry[]
  regexScripts: PresetRegex[]
} | null {
  try {
    const config = JSON.parse(value) as Record<string, unknown>
    const preset = config.preset as {
      prompts: Array<{
        identifier: string
        name?: string
        content?: string
        role?: string
        marker?: boolean
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
          name: prompt.name || item.identifier,
          content: prompt.content || '',
          role: prompt.role || 'system',
          marker: prompt.marker === true,
          enabled: overrides[item.identifier] ?? item.enabled,
          originalEnabled: item.enabled,
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
