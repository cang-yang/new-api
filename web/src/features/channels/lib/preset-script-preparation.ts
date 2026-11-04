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
import { api } from '@/lib/api'
import { requireServerSuccess } from '@/lib/server-error-message'

export type PreparedScript = {
  id: string
  name: string
  enabled: boolean
  status: 'prepared' | 'disabled' | 'manual' | 'failed'
  issues: Array<{ code: string; module?: string }>
}
export type ScriptPreparation = {
  modules: Record<string, string>
  scripts: PreparedScript[]
}

export function scriptPreparationIssueMessage(
  code: string,
  t: (key: string) => string
): string {
  switch (code) {
    case 'computed_import':
      return t(
        'This script builds file names while running. Ask the author for a compatible version and the required files; retrying the download alone will not fix it.'
      )
    case 'missing_module':
      return t(
        'A required file is missing. Check and download files, or ask the author for a file bundle.'
      )
    case 'invalid_javascript':
      return t(
        'This script contains an error. Ask the author for a corrected version.'
      )
    case 'unsupported_javascript':
      return t(
        'This script uses features that are unavailable here. Ask the author for a compatible version.'
      )
    case 'source_limit':
      return t(
        'This script is too large to check. Ask the author for a smaller version.'
      )
    case 'dependency_limit':
      return t(
        'This script needs too many linked files. Ask the author for a simplified version.'
      )
    case 'download_failed':
      return t(
        'A required file could not be downloaded. Check the connection and try again, or ask the author for the file.'
      )
    case 'preparation_cancelled':
      return t(
        'The file check was cancelled or took too long. Try checking the files again.'
      )
    case 'unsupported_entry':
      return t(
        'This script is saved in an unsupported format. Ask the author for a compatible preset.'
      )
    case 'invalid_script_id':
      return t(
        'This script has a missing or duplicate identifier. Ask the author to correct the preset.'
      )
    case 'script_limit':
      return t(
        'This preset has too many scripts. Ask the author for a version with fewer scripts.'
      )
    default:
      return t(
        'This script could not be checked. Try again or ask the author for a compatible version.'
      )
  }
}

export function presetRecord(value: unknown): Record<string, unknown> {
  return value !== null && typeof value === 'object' && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : {}
}

export function hasSPreset(config: Record<string, unknown>): boolean {
  return (
    Object.keys(
      presetRecord(presetRecord(presetRecord(config.preset).extensions).SPreset)
    ).length > 0
  )
}

// Ephemeral diagnostics are never saved into the channel configuration. Bound
// the cache; snapshots remain the only persisted preparation output.
const reports = new Map<string, PreparedScript[]>()
function reportKey(config: Record<string, unknown>): string {
  return JSON.stringify([
    presetRecord(presetRecord(config.preset).extensions).tavern_helper,
    config.script_modules,
  ])
}

export function discoverPresetScripts(
  config: Record<string, unknown>
): PreparedScript[] {
  const cached = reports.get(reportKey(config))
  if (cached) return cached
  const scripts: PreparedScript[] = []
  function visit(value: unknown, parentEnabled: boolean, depth: number): void {
    if (!Array.isArray(value) || depth > 8) return
    for (const entry of value) {
      const script = presetRecord(entry)
      const enabled = parentEnabled && script.enabled === true
      if (script.type === 'folder') visit(script.scripts, enabled, depth + 1)
      else if (script.type === 'script') {
        scripts.push({
          id: typeof script.id === 'string' ? script.id : '',
          name:
            typeof script.name === 'string' && script.name
              ? script.name
              : String(script.id || ''),
          enabled,
          status: enabled ? 'manual' : 'disabled',
          issues: [],
        })
      }
    }
  }
  visit(
    presetRecord(
      presetRecord(presetRecord(config.preset).extensions).tavern_helper
    ).scripts,
    true,
    0
  )
  return scripts
}

export function applyScriptPreparation(
  config: Record<string, unknown>,
  result: ScriptPreparation,
  automatic = false
): Record<string, unknown> {
  const next: Record<string, unknown> = {
    ...config,
    script_modules: {
      ...presetRecord(config.script_modules),
      ...result.modules,
    },
  }
  const enabled = result.scripts.filter((script) => script.enabled)
  if (
    automatic &&
    !Object.hasOwn(config, 'script_runtime') &&
    !hasSPreset(config) &&
    enabled.length > 0 &&
    enabled.every((script) => script.status === 'prepared')
  ) {
    next.script_runtime = 'tavern-v1'
    if (presetRecord(config.tool_text).enabled === true) {
      next.tool_text = { ...presetRecord(config.tool_text), enabled: false }
    }
  }
  const oldestKey = reports.keys().next().value
  if (reports.size >= 8 && oldestKey !== undefined) reports.delete(oldestKey)
  reports.set(reportKey(next), result.scripts)
  return next
}

export async function preparePresetScripts(
  config: Record<string, unknown>,
  signal?: AbortSignal
): Promise<ScriptPreparation> {
  const response = await api.post<{
    success: boolean
    data: ScriptPreparation
  }>('/api/channel/preset/scripts/prepare', { config }, { signal })
  return requireServerSuccess(response.data).data
}
