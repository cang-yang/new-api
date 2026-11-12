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
import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'

import { PolicyDecisionRecord } from '../decision-record'

describe('request decision outcome labels', () => {
  it('does not describe an upstream response followed by local failure as a completed request', () => {
    render(
      <PolicyDecisionRecord
        events={[
          {
            attempt: 1,
            elapsed_ms: 100,
            status: 200,
            decision: {
              action: 'success',
              reason: 'request_completed',
              source: 'upstream',
            },
          },
          {
            attempt: 1,
            elapsed_ms: 120,
            status: 500,
            decision: {
              action: 'failure',
              reason: 'request_failed',
              source: 'local',
            },
          },
        ]}
      />
    )

    expect(screen.getByText('Upstream response received')).toBeVisible()
    expect(screen.getByText('Request failed · HTTP 500')).toBeVisible()
    expect(screen.queryByText('Request completed')).not.toBeInTheDocument()
  })

  it('keeps the completed label for an actual system completion', () => {
    render(
      <PolicyDecisionRecord
        events={[
          {
            attempt: 1,
            elapsed_ms: 120,
            decision: {
              action: 'success',
              reason: 'request_completed',
              source: 'system',
            },
          },
        ]}
      />
    )

    expect(screen.getByText('Request completed')).toBeVisible()
    expect(
      screen.queryByText('Upstream response received')
    ).not.toBeInTheDocument()
  })
})
