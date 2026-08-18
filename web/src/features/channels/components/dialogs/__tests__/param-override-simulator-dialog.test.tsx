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
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'

import { ParamOverrideSimulatorDialog } from '../param-override-simulator-dialog'

type ApiPost = typeof api.post
const originalPost = api.post

function renderDialog(paramOverride: string): QueryClient {
  const queryClient = new QueryClient({
    defaultOptions: { mutations: { retry: false } },
  })
  render(
    <QueryClientProvider client={queryClient}>
      <ParamOverrideSimulatorDialog
        open
        paramOverride={paramOverride}
        onOpenChange={() => undefined}
      />
    </QueryClientProvider>
  )
  return queryClient
}

afterEach(() => {
  api.post = originalPost
})

describe('parameter override simulator dialog', () => {
  test('sends the current override and shows operation outcomes and JSON diff', async () => {
    api.post = vi.fn(async (url: string, payload: unknown) => {
      expect(url).toBe('/api/param-override/simulate')
      expect(payload).toEqual({
        upstream_request: { model: 'old', temperature: 1 },
        param_override: {
          operations: [{ mode: 'set', path: 'model', value: 'new' }],
        },
        context: {},
      })
      return {
        data: {
          success: true,
          data: {
            before: { model: 'old', temperature: 1 },
            after: { model: 'new', temperature: 1 },
            headers: {},
            diagnostics: [],
            operations: [
              {
                index: 0,
                mode: 'set',
                path: 'model',
                status: 'applied',
                changed: true,
              },
            ],
          },
        },
      }
    }) as ApiPost

    const queryClient = renderDialog(
      JSON.stringify({
        operations: [{ mode: 'set', path: 'model', value: 'new' }],
      })
    )

    fireEvent.change(screen.getByLabelText('Sample upstream request JSON'), {
      target: { value: '{"model":"old","temperature":1}' },
    })
    fireEvent.click(screen.getByRole('button', { name: 'Run simulation' }))

    await waitFor(() => expect(screen.getByText('Changed')).toBeVisible())
    expect(screen.getAllByText('model')).toHaveLength(2)
    expect(screen.getByText('old')).toBeVisible()
    expect(screen.getByText('new')).toBeVisible()
    expect(screen.getByText('Applied · Changed')).toBeVisible()
    queryClient.clear()
  })

  test('shows compile diagnostics returned with an unsuccessful response', async () => {
    api.post = vi.fn(async () => {
      throw {
        response: {
          status: 400,
          data: {
            success: false,
            message: 'parameter override compilation failed',
            data: {
              diagnostics: [
                {
                  severity: 'error',
                  code: 'path_required',
                  operation_index: 0,
                  field: 'path',
                  message: 'set operation requires path',
                },
              ],
            },
          },
        },
      }
    }) as ApiPost

    const queryClient = renderDialog(
      JSON.stringify({ operations: [{ mode: 'set', value: 1 }] })
    )
    fireEvent.change(screen.getByLabelText('Sample upstream request JSON'), {
      target: { value: '{"model":"demo"}' },
    })
    fireEvent.click(screen.getByRole('button', { name: 'Run simulation' }))

    await waitFor(() =>
      expect(screen.getByText('set operation requires path')).toBeVisible()
    )
    expect(screen.getByText('Operation 1 · path')).toBeVisible()
    queryClient.clear()
  })

  test('blocks invalid sample JSON without sending a request', async () => {
    api.post = vi.fn() as ApiPost
    const queryClient = renderDialog('{}')

    fireEvent.change(screen.getByLabelText('Sample upstream request JSON'), {
      target: { value: '[' },
    })
    fireEvent.click(screen.getByRole('button', { name: 'Run simulation' }))

    expect(
      await screen.findByText('Sample request must be a JSON object')
    ).toBeVisible()
    expect(api.post).not.toHaveBeenCalled()
    queryClient.clear()
  })
})
