import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useMemo, useState } from 'react'
import { useFormContext, useWatch } from 'react-hook-form'
import { useTranslation } from 'react-i18next'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import { handleServerError } from '@/lib/handle-server-error'

import { changeChannelAdaptation, getChannelAdaptation } from '../api'
import {
  adaptationBundleFromForm,
  adaptationBundleSignature,
  adaptationFormFields,
  adaptationKeys,
  type AdaptationAction,
  type AdaptationBundle,
} from '../lib/adaptation-lifecycle'
import type { ChannelFormValues } from '../lib/channel-form'
import type { Channel } from '../types'
import { AdaptationHealth } from './adaptation-health'
import { AdaptationPackageControls } from './adaptation-package-controls'

type Props = {
  channel: Channel
  disabled?: boolean
  onActivated: () => void
}

export function AdaptationLifecyclePanel(props: Props) {
  const { t } = useTranslation()
  const form = useFormContext<ChannelFormValues>()
  const values = useWatch({ control: form.control })
  const client = useQueryClient()
  const key = ['channel-adaptation', props.channel.id]
  const query = useQuery({
    queryKey: key,
    queryFn: () => getChannelAdaptation(props.channel.id),
    refetchOnWindowFocus: false,
  })
  const [confirmation, setConfirmation] = useState<
    AdaptationAction | 'load_draft' | null
  >(null)
  const bundle = useMemo(() => {
    try {
      return adaptationBundleFromForm(values as ChannelFormValues)
    } catch {
      // The editor owns the invalid field; do not save partial JSON.
      return undefined
    }
  }, [values])
  const state = query.data
  const changes = useMemo(() => {
    if (!bundle || !state) return []
    const labels = {
      adaptation_scope: t('When to use presets and reply rules'),
      sillytavern_preset: t('Preset and scripts'),
      worldbook: t('Worldbooks'),
      response_text_filter: t('Reply rules'),
    }
    const active = adaptationBundleFromForm({
      ...(values as ChannelFormValues),
      ...adaptationFormFields(state.active, props.channel),
    })
    return adaptationKeys
      .filter(
        (key) =>
          adaptationBundleSignature({ [key]: active[key] }) !==
          adaptationBundleSignature({ [key]: bundle[key] })
      )
      .map((key) => labels[key])
  }, [bundle, state, props.channel, values, t])
  const adaptationFields = useMemo(
    () => new Set(Object.keys(adaptationFormFields({}, props.channel))),
    [props.channel]
  )
  const hasOtherEdits = Object.keys(form.formState.dirtyFields).some(
    (field) => !adaptationFields.has(field)
  )
  let matchesDraft = false
  if (bundle && state?.draft) {
    try {
      const displayedDraft = adaptationBundleFromForm({
        ...(values as ChannelFormValues),
        ...adaptationFormFields(state.draft, props.channel),
      })
      matchesDraft =
        adaptationBundleSignature(bundle) ===
        adaptationBundleSignature(displayedDraft)
    } catch {
      // Invalid saved drafts remain editable but cannot become active.
    }
  }
  const load = (draft: AdaptationBundle) => {
    const fields = adaptationFormFields(draft, props.channel)
    for (const field of Object.keys(fields) as (keyof typeof fields)[]) {
      form.setValue(field, fields[field], {
        shouldDirty: true,
        shouldValidate: true,
      })
    }
  }
  const mutation = useMutation({
    mutationFn: async (action: AdaptationAction) => {
      if (!state) {
        throw new Error(t('Reload adaptation settings before continuing.'))
      }
      return changeChannelAdaptation(
        props.channel.id,
        state,
        action,
        action === 'save_draft' ? bundle : undefined
      )
    },
    onSuccess: (next, action) => {
      client.setQueryData(key, next)
      setConfirmation(null)
      if (action.startsWith('restore_') && next.draft) load(next.draft)
      if (action === 'activate') props.onActivated()
    },
    onError: (error) => handleServerError(error),
  })
  const disabled = props.disabled || mutation.isPending

  if (query.isPending) {
    return <LoadingState inline message={t('Loading adaptation versions...')} />
  }
  if (query.isError || !state) {
    return (
      <ErrorState
        title={t('Could not load adaptation versions')}
        description={t(
          'The running configuration is unchanged. Your edits are kept.'
        )}
        onRetry={() => void query.refetch()}
        className='min-h-0'
      />
    )
  }

  let confirmDescription = t(
    'This replaces the editor contents, not the running configuration. Check the recovered draft before activating it.'
  )
  if (confirmation === 'activate') {
    confirmDescription = t(
      'New requests will use this complete configuration. In-flight requests keep their existing configuration. This check does not replace real client testing; use a separate channel for incompatible upgrades.'
    )
  } else if (confirmation === 'mark_known_good') {
    confirmDescription = t(
      'Only mark this configuration after you have tested it with your real clients. This saves the complete preset, scripts, worldbooks and rules as a recovery point.'
    )
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>{t('Edit safely, then activate')}</CardTitle>
        <CardDescription>
          {t(
            'Save a draft here, check it, then activate it. The running configuration stays unchanged until activation. The bottom Save button only saves other channel settings.'
          )}
        </CardDescription>
      </CardHeader>
      <CardContent className='space-y-3'>
        <div className='flex flex-wrap gap-2' aria-live='polite'>
          <Badge variant='secondary'>
            {t('Active version: {{version}}', {
              version: state.active_revision,
            })}
          </Badge>
          {state.draft && (
            <Badge variant='outline'>
              {state.checked
                ? t('Draft checked; not activated')
                : t('Draft saved; not checked')}
            </Badge>
          )}
        </div>
        <p className='text-muted-foreground text-sm' aria-live='polite'>
          {changes.length > 0
            ? t('Changes from the active configuration: {{changes}}', {
                changes: changes.join(' · '),
              })
            : t('No adaptation changes from the active configuration.')}
        </p>
        <div className='flex flex-wrap gap-2'>
          <Button
            type='button'
            variant='outline'
            disabled={disabled || !bundle}
            onClick={() => mutation.mutate('save_draft')}
          >
            {t('Save adaptation draft')}
          </Button>
          <Button
            type='button'
            variant='outline'
            disabled={disabled || !matchesDraft}
            onClick={() => mutation.mutate('check')}
          >
            {t('Check saved draft')}
          </Button>
          <Button
            type='button'
            disabled={
              disabled || !matchesDraft || !state.checked || hasOtherEdits
            }
            onClick={() => setConfirmation('activate')}
          >
            {t('Activate checked draft')}
          </Button>
          {state.draft && !matchesDraft && (
            <Button
              type='button'
              variant='ghost'
              disabled={disabled}
              onClick={() => setConfirmation('load_draft')}
            >
              {t('Load saved draft')}
            </Button>
          )}
        </div>
        {hasOtherEdits && (
          <Alert>
            <AlertDescription>
              {t(
                'Other channel settings have unsaved changes. Save the adaptation draft, then use the bottom Save button and reopen this channel before activating.'
              )}
            </AlertDescription>
          </Alert>
        )}
        {state.draft && !matchesDraft && (
          <Alert>
            <AlertDescription>
              {t(
                'The editor differs from the saved draft. Save your edits or load the draft before checking or activating.'
              )}
            </AlertDescription>
          </Alert>
        )}
        <p className='text-muted-foreground text-sm'>
          {t(
            'Checks validate configuration locally. They do not call a model or prove that every client works.'
          )}
        </p>
        <AdaptationPackageControls
          active={state.active}
          disabled={disabled}
          onImport={load}
        />
        <details>
          <summary className='cursor-pointer text-sm font-medium'>
            {t('Recovery points')}
          </summary>
          <div className='mt-3 flex flex-wrap gap-2'>
            <Button
              type='button'
              variant='outline'
              disabled={disabled || !state.previous}
              onClick={() => setConfirmation('restore_previous')}
            >
              {t('Recover previous configuration')}
            </Button>
            <Button
              type='button'
              variant='outline'
              disabled={disabled || !state.known_good}
              onClick={() => setConfirmation('restore_known_good')}
            >
              {t('Recover tested configuration')}
            </Button>
            <Button
              type='button'
              variant='ghost'
              disabled={disabled}
              onClick={() => setConfirmation('mark_known_good')}
            >
              {t('Mark active configuration as tested')}
            </Button>
          </div>
        </details>
        <AdaptationHealth channelId={props.channel.id} />
        <ConfirmDialog
          open={confirmation !== null}
          onOpenChange={(open) => {
            if (!open) setConfirmation(null)
          }}
          title={t('Confirm configuration action')}
          desc={confirmDescription}
          isLoading={mutation.isPending}
          handleConfirm={() => {
            if (confirmation === 'load_draft' && state.draft) {
              load(state.draft)
              setConfirmation(null)
            } else if (confirmation && confirmation !== 'load_draft') {
              mutation.mutate(confirmation)
            }
          }}
        />
      </CardContent>
    </Card>
  )
}
