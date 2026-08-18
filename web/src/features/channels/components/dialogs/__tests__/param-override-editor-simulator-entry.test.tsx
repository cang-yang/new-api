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
import { fireEvent, render, screen } from '@testing-library/react'
import { afterAll, beforeAll, describe, expect, test } from 'vitest'

import { ParamOverrideEditorDialog } from '../param-override-editor-dialog'

const originalGetAnimations = Element.prototype.getAnimations

beforeAll(() => {
  Object.defineProperty(Element.prototype, 'getAnimations', {
    configurable: true,
    value: () => [],
  })
})

afterAll(() => {
  if (originalGetAnimations) {
    Object.defineProperty(Element.prototype, 'getAnimations', {
      configurable: true,
      value: originalGetAnimations,
    })
    return
  }
  Reflect.deleteProperty(Element.prototype, 'getAnimations')
})

describe('parameter override editor simulator entry', () => {
  test('opens the simulator with the unsaved override draft', async () => {
    const queryClient = new QueryClient()
    render(
      <QueryClientProvider client={queryClient}>
        <ParamOverrideEditorDialog
          open
          value={JSON.stringify({
            operations: [{ mode: 'set', path: 'temperature', value: 0.2 }],
          })}
          onOpenChange={() => undefined}
          onSave={() => undefined}
        />
      </QueryClientProvider>
    )

    fireEvent.click(screen.getByRole('button', { name: 'Simulate' }))

    expect(
      await screen.findByRole('dialog', {
        name: 'Parameter override simulator',
      })
    ).toBeVisible()
    expect(screen.getByLabelText('Sample upstream request JSON')).toBeVisible()
    queryClient.clear()
  })
})
