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
import userEvent from '@testing-library/user-event'
import { expect, test } from 'vitest'

import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '../select'

test('renders the selected item label when no items prop is provided', async () => {
  const user = userEvent.setup()
  render(
    <Select defaultValue='inherit'>
      <SelectTrigger aria-label='Insertion position'>
        <SelectValue />
      </SelectTrigger>
      <SelectContent>
        <SelectItem value='inherit'>Use preset marker</SelectItem>
        <SelectItem value='system_prelude'>System prelude</SelectItem>
      </SelectContent>
    </Select>
  )

  const trigger = screen.getByRole('combobox', { name: 'Insertion position' })
  expect(trigger).toHaveTextContent('Use preset marker')
  expect(trigger).not.toHaveTextContent('inherit')

  await user.click(trigger)
  await user.click(screen.getByRole('option', { name: 'System prelude' }))

  expect(trigger).toHaveTextContent('System prelude')
  expect(trigger).not.toHaveTextContent('system_prelude')
})
