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
import { useMutation } from '@tanstack/react-query'
import { FlaskConical, Play } from 'lucide-react'
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { CopyButton } from '@/components/copy-button'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Field, FieldGroup, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Textarea } from '@/components/ui/textarea'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'
import { api } from '@/lib/api'
import { markServerErrorHandled } from '@/lib/handle-server-error'
import {
  getServerErrorMessage,
  requireServerSuccess,
} from '@/lib/server-error-message'

type PreviewResult = {
  output: string
  rolled_back?: boolean
  steps: Array<{
    key: string
    name: string
    before: string
    after: string
    changed: boolean
  }>
  warnings?: string[]
}
type RegexPlaygroundProps = {
  scopeKey: string | number
  value: string
  presetValue?: string
  presetModels?: string
  disabled?: boolean
}

export function RegexPlayground(props: RegexPlaygroundProps) {
  return <ScopedRegexPlayground key={props.scopeKey} {...props} />
}

function ScopedRegexPlayground(props: RegexPlaygroundProps) {
  const { t } = useTranslation()
  const id = useId()
  const [text, setText] = useState('')
  const [stage, setStage] = useState<'send' | 'receive'>('receive')
  const [role, setRole] = useState('user')
  const [model, setModel] = useState('')
  const [depth, setDepth] = useState('0')
  const snapshot = JSON.stringify([
    props.value,
    props.presetValue,
    props.presetModels,
    text,
    stage,
    role,
    model,
    depth,
  ])
  const preview = useMutation<PreviewResult, Error, string>({
    mutationFn: async (_snapshot: string): Promise<PreviewResult> => {
      let config: unknown
      let preset: unknown
      try {
        config = props.value.trim() ? JSON.parse(props.value) : null
        preset = props.presetValue?.trim()
          ? JSON.parse(props.presetValue)
          : undefined
        if (
          preset &&
          typeof preset === 'object' &&
          !Array.isArray(preset) &&
          props.presetModels !== undefined
        ) {
          preset = {
            ...preset,
            models: props.presetModels
              .split(',')
              .map((name) => name.trim())
              .filter(Boolean),
          }
        }
      } catch {
        throw new Error(t('Fix the regex or preset JSON before testing.'))
      }
      const response = await api.post<{
        success: boolean
        message?: string
        data: PreviewResult
      }>('/api/channel/regex/preview', {
        config,
        preset,
        model,
        stage,
        role: stage === 'receive' ? 'assistant' : role,
        depth: Number(depth),
        text,
      })
      const result = requireServerSuccess(response.data).data
      return {
        ...result,
        steps: result.steps.map((step) => ({
          ...step,
          key: crypto.randomUUID(),
        })),
      }
    },
    meta: { errorToast: false },
    onError: markServerErrorHandled,
  })
  const current = preview.variables === snapshot
  const result = current ? preview.data : undefined
  const error = current ? preview.error : null
  const disabled = props.disabled || preview.isPending
  const validDepth =
    depth.trim() !== '' && Number.isInteger(Number(depth)) && Number(depth) >= 0
  let stepsLabel = t('No applicable rules')
  if (result?.rolled_back) stepsLabel = t('Intermediate steps (reverted)')
  else if (result?.steps.length) stepsLabel = t('Applied rules, in order')

  return (
    <section
      aria-label={t('Regex playground')}
      className='bg-muted/20 flex max-w-full min-w-0 flex-col gap-4 rounded-xl border p-4'
    >
      <div className='flex flex-wrap items-center gap-3'>
        <FlaskConical
          className='text-muted-foreground size-5'
          aria-hidden='true'
        />
        <h4 className='text-sm font-semibold'>{t('Regex playground')}</h4>
        <Badge variant='outline'>{t('Current channel draft')}</Badge>
      </div>
      <p className='text-muted-foreground text-xs'>
        {t(
          'Test unsaved rules with the server regex engine. Channel rules run before enabled embedded preset rules. No upstream request is sent.'
        )}
      </p>
      {props.presetValue?.trim() && (
        <p className='text-muted-foreground text-xs'>
          {t(
            'Imported rules linked by script ID replace their embedded copy in the same direction, even when disabled. Older unlinked copies may still run twice.'
          )}
        </p>
      )}
      <FieldGroup className='gap-4'>
        <Field>
          <FieldLabel id={`${id}-direction`}>{t('Test direction')}</FieldLabel>
          <ToggleGroup
            aria-labelledby={`${id}-direction`}
            variant='outline'
            value={[stage]}
            disabled={disabled}
            onValueChange={(value) => {
              if (value[0] === 'send' || value[0] === 'receive') {
                setStage(value[0])
              }
            }}
          >
            <ToggleGroupItem value='receive'>{t('Receive')}</ToggleGroupItem>
            <ToggleGroupItem value='send'>{t('Send')}</ToggleGroupItem>
          </ToggleGroup>
        </Field>
        <div className='grid min-w-0 gap-3 sm:grid-cols-3'>
          <Field>
            <FieldLabel htmlFor={`${id}-model`}>{t('Test model')}</FieldLabel>
            <Input
              id={`${id}-model`}
              value={model}
              disabled={disabled}
              onChange={(event) => setModel(event.target.value)}
              placeholder={t('Optional model name')}
            />
          </Field>
          <Field>
            <FieldLabel htmlFor={`${id}-role`}>{t('Message role')}</FieldLabel>
            <NativeSelect
              id={`${id}-role`}
              className='w-full'
              value={stage === 'receive' ? 'assistant' : role}
              disabled={disabled || stage === 'receive'}
              onChange={(event) => setRole(event.target.value)}
            >
              <NativeSelectOption value='user'>{t('User')}</NativeSelectOption>
              <NativeSelectOption value='assistant'>
                {t('Assistant')}
              </NativeSelectOption>
              <NativeSelectOption value='system'>
                {t('System')}
              </NativeSelectOption>
              <NativeSelectOption value='developer'>
                {t('Developer')}
              </NativeSelectOption>
            </NativeSelect>
          </Field>
          <Field data-invalid={!validDepth}>
            <FieldLabel htmlFor={`${id}-depth`}>
              {t('Message depth')}
            </FieldLabel>
            <Input
              id={`${id}-depth`}
              type='number'
              min={0}
              step={1}
              aria-invalid={!validDepth}
              value={depth}
              disabled={disabled}
              onChange={(event) => setDepth(event.target.value)}
            />
          </Field>
        </div>
        <Field>
          <FieldLabel htmlFor={`${id}-input`}>{t('Test text')}</FieldLabel>
          <Textarea
            id={`${id}-input`}
            value={text}
            disabled={disabled}
            onChange={(event) => setText(event.target.value)}
            className='min-h-32 max-w-full font-mono text-xs'
            placeholder={t('Paste message text to test your rules…')}
          />
        </Field>
      </FieldGroup>
      <Button
        type='button'
        className='self-start'
        disabled={disabled || !validDepth}
        onClick={() => preview.mutate(snapshot)}
      >
        <Play className='size-4' aria-hidden='true' />
        {preview.isPending ? t('Testing…') : t('Run regex test')}
      </Button>
      <div aria-live='polite' className='flex min-w-0 flex-col gap-3'>
        {error && (
          <Alert variant='destructive'>
            <AlertDescription className='break-all'>
              {getServerErrorMessage(error, t('Regex test failed'))}
            </AlertDescription>
          </Alert>
        )}
        {result && (
          <>
            {result.rolled_back && (
              <Alert variant='destructive'>
                <AlertDescription>
                  {t(
                    'Processing failed. All changes were reverted according to the failure policy.'
                  )}
                </AlertDescription>
              </Alert>
            )}
            {[...new Set(result.warnings)].map((warning) => (
              <Alert key={warning}>
                <AlertDescription className='break-all'>
                  {warning}
                </AlertDescription>
              </Alert>
            ))}
            <Field>
              <div className='flex items-center justify-between gap-2'>
                <FieldLabel htmlFor={`${id}-output`}>
                  {result.rolled_back
                    ? t('Original text (fallback)')
                    : t('Transformed text')}
                </FieldLabel>
                <CopyButton
                  value={result.output}
                  size='sm'
                  aria-label={t('Copy transformed text')}
                />
              </div>
              <Textarea
                id={`${id}-output`}
                value={result.output}
                readOnly
                className='bg-background min-h-32 max-w-full font-mono text-xs'
              />
            </Field>
            <div className='flex min-w-0 flex-col gap-2'>
              <p className='text-muted-foreground text-xs'>{stepsLabel}</p>
              {result.steps.map((step, index) => (
                <div
                  key={step.key}
                  className='flex min-w-0 flex-wrap items-center gap-2 text-xs'
                >
                  <span className='min-w-0 break-all'>
                    {index + 1}. {step.name}
                  </span>
                  <Badge variant={step.changed ? 'secondary' : 'outline'}>
                    {result.rolled_back
                      ? t('Reverted')
                      : t(step.changed ? 'Changed' : 'Unchanged')}
                  </Badge>
                </div>
              ))}
            </div>
          </>
        )}
      </div>
    </section>
  )
}
