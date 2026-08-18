import { describe, expect, it } from 'vitest'

import { getErrorIncidentSummary } from '../error-incident'

describe('getErrorIncidentSummary', () => {
  it('returns a compact validated incident summary', () => {
    expect(
      getErrorIncidentSummary({
        error_fingerprint: 'cef1_0123456789abcdef01234567',
        error_incident_count: 7,
      })
    ).toEqual({
      fingerprint: 'cef1_0123456789abcdef01234567',
      count: 7,
    })
  })

  it('rejects malformed fingerprints and non-aggregated error logs', () => {
    expect(
      getErrorIncidentSummary({ error_fingerprint: 'secret error' })
    ).toBeNull()
    expect(
      getErrorIncidentSummary({
        error_fingerprint: 'cef1_aaaaaaaaaaaaaaaaaaaaaaaa',
      })
    ).toBeNull()
  })
})
