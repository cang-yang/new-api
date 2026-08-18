import type { LogOtherData } from '../types'

export interface ErrorIncidentSummary {
  fingerprint: string
  count: number
}

export function getErrorIncidentSummary(
  other: LogOtherData | null | undefined
): ErrorIncidentSummary | null {
  const fingerprint = other?.error_fingerprint
  if (
    typeof fingerprint !== 'string' ||
    !/^cef1_[0-9a-f]{24}$/.test(fingerprint)
  ) {
    return null
  }
  const rawCount = other?.error_incident_count
  if (
    typeof rawCount !== 'number' ||
    !Number.isFinite(rawCount) ||
    rawCount <= 0
  ) {
    return null
  }
  const count = Math.floor(rawCount)
  return { fingerprint, count }
}
