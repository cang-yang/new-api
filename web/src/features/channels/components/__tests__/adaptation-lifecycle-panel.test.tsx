import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  cleanup,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { FormProvider, useForm } from 'react-hook-form'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'

import {
  adaptationFormFields,
  type AdaptationState,
} from '../../lib/adaptation-lifecycle'
import {
  transformChannelToFormDefaults,
  type ChannelFormValues,
} from '../../lib/channel-form'
import { channelSchema } from '../../types'
import { AdaptationLifecyclePanel } from '../adaptation-lifecycle-panel'

const channel = channelSchema.parse({
  id: 7,
  name: 'Test channel',
  type: 1,
  key: '',
  status: 1,
  created_time: 0,
  test_time: 0,
  response_time: 0,
  balance_updated_time: 0,
  models: 'test-model',
  settings: '{"adaptation_scope":"all_supported"}',
})
let state: AdaptationState
let client: QueryClient
const activated = vi.fn()

function Harness() {
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
      <AdaptationLifecyclePanel channel={channel} onActivated={activated} />
    </FormProvider>
  )
}

beforeEach(() => {
  state = {
    revision: 0,
    active_revision: 0,
    active: { adaptation_scope: 'all_supported' },
    checked: false,
    base_digest: 'initial',
  }
  client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  activated.mockReset()
  vi.spyOn(api, 'get').mockImplementation(async () => ({
    data: { success: true, data: structuredClone(state) },
  }))
})
afterEach(() => {
  cleanup()
  client.clear()
  vi.restoreAllMocks()
})

function mount() {
  render(
    <QueryClientProvider client={client}>
      <Harness />
    </QueryClientProvider>
  )
}

test('saving and checking a draft never activate it; activation requires explicit confirmation', async () => {
  const user = userEvent.setup()
  const post = vi.spyOn(api, 'post').mockImplementation(async (_url, data) => {
    const change = data as {
      action: string
      bundle?: AdaptationState['draft']
      revision: number
    }
    expect(change.revision).toBe(state.revision)
    state = { ...state, revision: state.revision + 1 }
    if (change.action === 'save_draft') {
      state = { ...state, draft: change.bundle, checked: false }
    }
    if (change.action === 'check') state = { ...state, checked: true }
    if (change.action === 'activate') {
      if (!state.draft) throw new Error('No draft to activate')
      state = {
        ...state,
        active: state.draft,
        draft: undefined,
        checked: false,
        active_revision: state.revision,
      }
    }
    return { data: { success: true, data: structuredClone(state) } }
  })
  mount()
  const save = await screen.findByRole('button', {
    name: 'Save adaptation draft',
  })
  expect(
    screen.getByRole('button', { name: 'Activate checked draft' })
  ).toBeDisabled()
  await user.click(save)
  expect(await screen.findByText('Draft saved; not checked')).toBeVisible()
  expect(activated).not.toHaveBeenCalled()
  await user.click(screen.getByRole('button', { name: 'Check saved draft' }))
  expect(await screen.findByText('Draft checked; not activated')).toBeVisible()
  expect(activated).not.toHaveBeenCalled()
  await user.click(
    screen.getByRole('button', { name: 'Activate checked draft' })
  )
  expect(post).toHaveBeenCalledTimes(2)
  await user.click(
    within(screen.getByRole('alertdialog')).getByRole('button', {
      name: 'Continue',
    })
  )
  await waitFor(() => expect(activated).toHaveBeenCalledOnce())
})

test('editing after a check disables activation until the edited draft is saved and checked again', async () => {
  const user = userEvent.setup()
  state.draft = state.active
  state.checked = true
  mount()
  const activate = await screen.findByRole('button', {
    name: 'Activate checked draft',
  })
  expect(activate).toBeEnabled()
  const input = screen.getByRole('textbox', { name: 'Scope' })
  await user.clear(input)
  await user.type(input, 'plain_chat_only')
  expect(activate).toBeDisabled()
  expect(
    screen.getByText(/Changes from the active configuration:/)
  ).toHaveTextContent('When to use presets and reply rules')
  expect(
    screen.getByRole('button', { name: 'Check saved draft' })
  ).toBeDisabled()
  expect(
    screen.getByText(
      'The editor differs from the saved draft. Save your edits or load the draft before checking or activating.'
    )
  ).toBeVisible()
})

test('activation cannot discard unsaved non-adaptation channel edits', async () => {
  const user = userEvent.setup()
  state.draft = state.active
  state.checked = true
  mount()
  const activate = await screen.findByRole('button', {
    name: 'Activate checked draft',
  })
  expect(activate).toBeEnabled()
  await user.type(
    screen.getByRole('textbox', { name: 'Channel name' }),
    ' edited'
  )
  expect(activate).toBeDisabled()
  expect(
    screen.getByText(
      'Other channel settings have unsaved changes. Save the adaptation draft, then use the bottom Save button and reopen this channel before activating.'
    )
  ).toBeVisible()
  expect(activated).not.toHaveBeenCalled()
})

test('recovering a complete configuration loads its companion preset controls without changing the active version', async () => {
  const user = userEvent.setup()
  state.previous = { adaptation_scope: 'plain_chat_only' }
  vi.spyOn(api, 'post').mockImplementation(async (_url, data) => {
    expect(data).toMatchObject({ action: 'restore_previous', revision: 0 })
    state = { ...state, revision: 1, draft: state.previous }
    return { data: { success: true, data: structuredClone(state) } }
  })
  mount()
  await screen.findByText('Recovery points')
  await user.click(screen.getByText('Recovery points'))
  await user.click(
    screen.getByRole('button', { name: 'Recover previous configuration' })
  )
  await user.click(
    within(screen.getByRole('alertdialog')).getByRole('button', {
      name: 'Continue',
    })
  )
  await waitFor(() =>
    expect(screen.getByRole('textbox', { name: 'Scope' })).toHaveValue(
      'plain_chat_only'
    )
  )
  expect(state.active.adaptation_scope).toBe('all_supported')
  expect(activated).not.toHaveBeenCalled()
  const fields = adaptationFormFields(
    {
      sillytavern_preset: {
        models: ['other'],
        user: 'User',
        char: 'Character',
        parameter_policy: 'client',
        post_processing: 'merge',
      },
    },
    channel
  )
  expect(fields).toMatchObject({
    sillytavern_models: 'other',
    sillytavern_user: 'User',
    sillytavern_char: 'Character',
    sillytavern_parameter_policy: 'client',
    sillytavern_post_processing: 'merge',
  })
})

test('a failed load keeps version controls unavailable and offers retry', async () => {
  const user = userEvent.setup()
  vi.mocked(api.get).mockRejectedValueOnce(new Error('offline'))
  mount()
  expect(
    await screen.findByText(
      'The running configuration is unchanged. Your edits are kept.'
    )
  ).toBeVisible()
  expect(
    screen.queryByRole('button', { name: 'Activate checked draft' })
  ).not.toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: 'Retry' }))
  expect(
    await screen.findByRole('button', { name: 'Save adaptation draft' })
  ).toBeEnabled()
})

test('a complete package replaces only the draft fields after confirmation, never the running configuration', async () => {
  const user = userEvent.setup()
  mount()
  await screen.findByRole('button', {
    name: 'Import complete adaptation package',
  })
  const scopeInput = screen.getByRole('textbox', { name: 'Scope' })
  const data = {
    format: 'new_api_adaptation',
    version: 1,
    bundle: { adaptation_scope: 'plain_chat_only' },
  }
  const file = new File([JSON.stringify(data)], 'package.json', {
    type: 'application/json',
  })
  file.text = async () => JSON.stringify(data)
  await user.upload(screen.getByLabelText('Adaptation package file'), file)
  const dialog = await screen.findByRole('alertdialog')
  expect(scopeInput).toHaveValue('all_supported')
  await user.click(within(dialog).getByRole('button', { name: 'Continue' }))
  expect(screen.getByRole('textbox', { name: 'Scope' })).toHaveValue(
    'plain_chat_only'
  )
  expect(screen.getByRole('textbox', { name: 'Channel name' })).toHaveValue(
    'Test channel'
  )
  expect(state.active.adaptation_scope).toBe('all_supported')
  expect(activated).not.toHaveBeenCalled()
})

test('processing results load on demand and keep degraded delivery separate from full completion', async () => {
  vi.mocked(api.get).mockImplementation(async (url) => ({
    data: {
      success: true,
      data: String(url).endsWith('/health')
        ? {
            audit_enabled: false,
            sample_size: 3,
            counts: { completed: 1, degraded: 1, interrupted: 1 },
            versions: [
              {
                profile: 'unknown',
                counts: { completed: 1, degraded: 1, interrupted: 1 },
              },
            ],
          }
        : structuredClone(state),
    },
  }))
  mount()
  const toggle = await screen.findByRole('button', {
    name: 'Recent processing results',
  })
  expect(api.get).not.toHaveBeenCalledWith(
    expect.stringContaining('/health'),
    expect.anything()
  )
  await userEvent.click(toggle)
  const section = screen.getByRole('region', {
    name: 'Recent processing results',
  })
  expect(await within(section).findByText('Recorded attempts: 3')).toBeVisible()
  expect(
    within(section).getByText('Written with optional steps skipped')
  ).toBeVisible()
  expect(within(section).getByText('Client write interrupted')).toBeVisible()
  expect(
    within(section).getByText(/This sample is not the success rate/)
  ).toBeVisible()
  expect(
    within(section).getByText(
      'Request recording is off. Only previously retained records can appear here.'
    )
  ).toBeVisible()
  await userEvent.click(
    within(section).getByText('Separate results by configuration')
  )
  expect(within(section).getByText('Unrecorded configuration')).toBeVisible()
})
