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

export type ParamOverrideDiagnostic = {
  severity: string
  code: string
  operation_index?: number
  field?: string
  message: string
}

export type ParamOverrideOperationTrace = {
  index: number
  mode: string
  path?: string
  from?: string
  to?: string
  status: string
  reason?: string
  changed: boolean
}

export type ParamOverrideSimulation = {
  before: unknown
  after: unknown
  operations: ParamOverrideOperationTrace[]
  headers: Record<string, unknown>
  diagnostics: ParamOverrideDiagnostic[]
}

export type ParamOverrideSimulationResponse = {
  success: boolean
  message?: string
  data?: Partial<ParamOverrideSimulation> & {
    diagnostics?: ParamOverrideDiagnostic[]
  }
}

export type JsonDiffEntry = {
  path: string
  kind: 'added' | 'removed' | 'changed'
  before: unknown
  after: unknown
}

const REDACTED = '[REDACTED]'

const normalizeSensitiveKey = (key: string): string =>
  key.trim().toLowerCase().replaceAll('-', '_').replaceAll('.', '_')

const isSensitiveKey = (key: string): boolean => {
  const normalized = normalizeSensitiveKey(key)
  if (
    [
      'authorization',
      'proxy_authorization',
      'api_key',
      'apikey',
      'x_api_key',
      'key',
      'access_token',
      'refresh_token',
      'id_token',
      'token',
      'cookie',
      'set_cookie',
      'password',
      'passwd',
      'client_secret',
      'secret',
      'credential',
      'credentials',
    ].includes(normalized)
  ) {
    return true
  }
  return [
    '_api_key',
    '_access_token',
    '_refresh_token',
    '_auth_token',
    '_secret',
    '_password',
    '_credential',
  ].some((suffix) => normalized.endsWith(suffix))
}

const looksLikeSecret = (value: string): boolean => {
  const normalized = value.trim().toLowerCase()
  return (
    normalized.startsWith('bearer ') ||
    normalized.startsWith('basic ') ||
    normalized.startsWith('sk-')
  )
}

export function redactSimulationValue(value: unknown, key = ''): unknown {
  if (key && isSensitiveKey(key)) return REDACTED
  if (Array.isArray(value)) {
    return value.map((item) => redactSimulationValue(item))
  }
  if (value && typeof value === 'object') {
    const redacted: Record<string, unknown> = {}
    for (const [childKey, childValue] of Object.entries(value)) {
      redacted[childKey] = redactSimulationValue(childValue, childKey)
    }
    return redacted
  }
  if (typeof value === 'string' && looksLikeSecret(value)) return REDACTED
  return value
}

const isRecord = (value: unknown): value is Record<string, unknown> =>
  Boolean(value) && typeof value === 'object' && !Array.isArray(value)

const valuesEqual = (left: unknown, right: unknown): boolean =>
  JSON.stringify(left) === JSON.stringify(right)

export function buildJsonDiff(
  before: unknown,
  after: unknown,
  parentPath = ''
): JsonDiffEntry[] {
  if (isRecord(before) && isRecord(after)) {
    const keys = [...new Set([...Object.keys(before), ...Object.keys(after)])]
    keys.sort((left, right) => left.localeCompare(right))
    const entries: JsonDiffEntry[] = []
    for (const key of keys) {
      const path = parentPath ? `${parentPath}.${key}` : key
      const hasBefore = Object.hasOwn(before, key)
      const hasAfter = Object.hasOwn(after, key)
      if (!hasBefore) {
        entries.push({
          path,
          kind: 'added',
          before: undefined,
          after: after[key],
        })
        continue
      }
      if (!hasAfter) {
        entries.push({
          path,
          kind: 'removed',
          before: before[key],
          after: undefined,
        })
        continue
      }
      entries.push(...buildJsonDiff(before[key], after[key], path))
    }
    return entries
  }
  if (valuesEqual(before, after)) return []
  return [
    {
      path: parentPath || '$',
      kind: 'changed',
      before,
      after,
    },
  ]
}

export function normalizeSimulation(
  value: Partial<ParamOverrideSimulation>
): ParamOverrideSimulation {
  const before = redactSimulationValue(value.before ?? {})
  const after = redactSimulationValue(value.after ?? {})
  const headers = redactSimulationValue(value.headers ?? {})
  return {
    before,
    after,
    operations: Array.isArray(value.operations) ? value.operations : [],
    headers: isRecord(headers) ? headers : {},
    diagnostics: Array.isArray(value.diagnostics) ? value.diagnostics : [],
  }
}

export function getSimulationErrorResponse(
  error: unknown
): ParamOverrideSimulationResponse | null {
  if (!isRecord(error) || !isRecord(error.response)) return null
  const responseData = error.response.data
  if (!isRecord(responseData) || typeof responseData.success !== 'boolean') {
    return null
  }
  return responseData as ParamOverrideSimulationResponse
}

export function formatSimulationValue(value: unknown): string {
  if (value === undefined) return '—'
  if (typeof value === 'string') return value
  return JSON.stringify(value, null, 2)
}
