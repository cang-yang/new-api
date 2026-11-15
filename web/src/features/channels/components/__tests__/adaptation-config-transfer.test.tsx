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
import { render, screen, within, cleanup } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { FormProvider, useForm } from 'react-hook-form'
import { afterEach, expect, test, vi } from 'vitest'

import {
  transformChannelToFormDefaults,
  type ChannelFormValues,
} from '../../lib/channel-form'
import { channelSchema } from '../../types'
import { AdaptationConfigTransfer } from '../adaptation-config-transfer'

const channel = channelSchema.parse({
  id: 7,
  name: 'Test channel',
  type: 1,
  key: 'not-for-export',
  status: 1,
  created_time: 0,
  test_time: 0,
  response_time: 0,
  balance_updated_time: 0,
  models: 'test-model',
  settings: '{"adaptation_scope":"all_supported"}',
})
function Harness(props: { disabled?: boolean }) {
  const form = useForm<ChannelFormValues>({
    defaultValues: transformChannelToFormDefaults(channel),
  })
  return (
    <FormProvider {...form}>
      <label>
        Scope
        <input {...form.register('adaptation_scope')} />
      </label>
      <label>
        Channel name
        <input {...form.register('name')} />
      </label>
      <AdaptationConfigTransfer channel={channel} disabled={props.disabled} />
    </FormProvider>
  )
}
afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})

test('complete package import replaces only adaptation fields after confirmation without server activation', async () => {
  const user = userEvent.setup()
  render(<Harness />)
  const pack = {
    format: 'new_api_adaptation',
    version: 1,
    bundle: { adaptation_scope: 'plain_chat_only' },
  }
  const file = new File([JSON.stringify(pack)], 'package.json', {
    type: 'application/json',
  })
  file.text = async () => JSON.stringify(pack)
  await user.upload(screen.getByLabelText('Adaptation package file'), file)
  const dialog = await screen.findByRole('alertdialog')
  expect(screen.getByLabelText('Scope')).toHaveValue('all_supported')
  await user.click(within(dialog).getByRole('button', { name: 'Continue' }))
  expect(screen.getByLabelText('Scope')).toHaveValue('plain_chat_only')
  expect(screen.getByLabelText('Channel name')).toHaveValue('Test channel')
  expect(
    screen.queryByRole('button', { name: /draft/i })
  ).not.toBeInTheDocument()
})

test('export includes unsaved editor changes but excludes channel credentials', async () => {
  const user = userEvent.setup()
  let blob: Blob | undefined
  vi.spyOn(URL, 'createObjectURL').mockImplementation((value) => {
    blob = value as Blob
    return 'blob:export'
  })
  const click = vi
    .spyOn(HTMLAnchorElement.prototype, 'click')
    .mockImplementation(() => {})
  render(<Harness />)
  await user.clear(screen.getByLabelText('Scope'))
  await user.type(screen.getByLabelText('Scope'), 'plain_chat_only')
  await user.click(
    screen.getByRole('button', { name: 'Export complete adaptation package' })
  )
  expect(click).toHaveBeenCalledOnce()
  const text = await new Promise<string>((resolve) => {
    const reader = new FileReader()
    reader.addEventListener('load', () => resolve(String(reader.result)), {
      once: true,
    })
    if (!blob) throw new Error('Expected a configuration download')
    reader.readAsText(blob)
  })
  expect(JSON.parse(text)).toMatchObject({
    format: 'new_api_adaptation',
    version: 1,
    bundle: { adaptation_scope: 'plain_chat_only' },
  })
  expect(text).not.toContain('not-for-export')
  expect(text).not.toContain('Test channel')
})

test('read-only configuration disables import and export actions', () => {
  render(<Harness disabled />)
  expect(
    screen.getByRole('button', { name: 'Import complete adaptation package' })
  ).toBeDisabled()
  expect(
    screen.getByRole('button', { name: 'Export complete adaptation package' })
  ).toBeDisabled()
})
