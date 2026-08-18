export interface JsonDiffEntry {
  path: string
  kind: 'added' | 'removed' | 'changed'
  before?: unknown
  after?: unknown
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return value != null && typeof value === 'object' && !Array.isArray(value)
}

export function diffJsonValues(
  before: unknown,
  after: unknown,
  path = '$',
  entries: JsonDiffEntry[] = [],
  limit = 500
): JsonDiffEntry[] {
  if (entries.length >= limit || Object.is(before, after)) return entries

  if (Array.isArray(before) && Array.isArray(after)) {
    const length = Math.max(before.length, after.length)
    for (let index = 0; index < length && entries.length < limit; index++) {
      if (index >= before.length) {
        entries.push({
          path: `${path}[${index}]`,
          kind: 'added',
          after: after[index],
        })
      } else if (index >= after.length) {
        entries.push({
          path: `${path}[${index}]`,
          kind: 'removed',
          before: before[index],
        })
      } else {
        diffJsonValues(
          before[index],
          after[index],
          `${path}[${index}]`,
          entries,
          limit
        )
      }
    }
    return entries
  }

  if (isRecord(before) && isRecord(after)) {
    const keys = [
      ...new Set([...Object.keys(before), ...Object.keys(after)]),
    ].sort()
    for (const key of keys) {
      if (entries.length >= limit) break
      const childPath = `${path}.${key}`
      if (!(key in before)) {
        entries.push({ path: childPath, kind: 'added', after: after[key] })
      } else if (!(key in after)) {
        entries.push({ path: childPath, kind: 'removed', before: before[key] })
      } else {
        diffJsonValues(before[key], after[key], childPath, entries, limit)
      }
    }
    return entries
  }

  entries.push({ path, kind: 'changed', before, after })
  return entries
}

export function diffJsonText(
  before: string,
  after: string
): JsonDiffEntry[] | null {
  try {
    return diffJsonValues(JSON.parse(before), JSON.parse(after))
  } catch {
    return null
  }
}
