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
import { beforeEach, expect, test, vi } from 'vitest'

import { getBodyAudit } from '../api'

const { get } = vi.hoisted(() => ({ get: vi.fn() }))
vi.mock('@/lib/api', () => ({ api: { get } }))

beforeEach(() => vi.clearAllMocks())

test('only treats a missing audit record as absent', async () => {
  get.mockResolvedValueOnce({ status: 404 })
  expect(await getBodyAudit('missing')).toBeNull()
  get.mockResolvedValueOnce({
    status: 200,
    data: { success: false, message: 'denied' },
  })
  await expect(getBodyAudit('denied')).rejects.toThrow('denied')
  get.mockResolvedValueOnce({ status: 200, data: { success: true } })
  await expect(getBodyAudit('malformed')).rejects.toThrow()
})

test('encodes request identifiers and returns successful audit data unchanged', async () => {
  const data = { request_id: 'req/a', response_body: '{"text":"a\\nb"}' }
  get.mockResolvedValueOnce({ status: 200, data: { success: true, data } })
  expect(await getBodyAudit('req/a', true)).toBe(data)
  expect(get).toHaveBeenCalledWith(
    '/api/log/body-audit/req%2Fa',
    expect.objectContaining({
      params: { include_attempt_bodies: true },
    })
  )
})
