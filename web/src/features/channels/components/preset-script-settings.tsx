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
import { useLayoutEffect, useId, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from '@/components/ui/collapsible'
import {
  Field,
  FieldDescription,
  FieldLegend,
  FieldLabel,
  FieldSet,
} from '@/components/ui/field'
import { RadioGroup, RadioGroupItem } from '@/components/ui/radio-group'
import { handleServerError } from '@/lib/handle-server-error'

import {
  applyScriptPreparation,
  discoverPresetScripts,
  hasSPreset,
  preparePresetScripts,
  scriptPreparationIssueMessage,
  presetRecord as record,
} from '../lib/preset-script-preparation'

type Props = {
  config: Record<string, unknown>
  onChange: (config: Record<string, unknown>) => void
  disabled?: boolean
}

export function PresetScriptSettings(props: Props) {
  const { t } = useTranslation()
  const id = useId()
  const supported = hasSPreset(props.config)
  const enabled = props.config.script_runtime === 'spreset-v1'
  const tavernEnabled = props.config.script_runtime === 'tavern-v1'
  const scripts = discoverPresetScripts(props.config)
  const hasScripts = scripts.some((script) => script.enabled)
  const [preparing, setPreparing] = useState(false)
  const bundleInput = useRef<HTMLInputElement>(null)
  const preparationAbort = useRef<AbortController | null>(null)
  const importRevision = useRef({ active: true, sequence: 0 })
  useLayoutEffect(() => {
    const revision = { active: true, sequence: 0 }
    importRevision.current = revision
    setPreparing(false)
    return () => {
      revision.active = false
      preparationAbort.current?.abort()
    }
  }, [props.config, props.disabled])
  const [bundleError, setBundleError] = useState(false)
  const supersedeScriptOperation = () => {
    preparationAbort.current?.abort()
    preparationAbort.current = null
    setPreparing(false)
    const revision = importRevision.current
    return { revision, sequence: ++revision.sequence }
  }
  const selectRuntime = (mode: string) => {
    if (
      props.disabled ||
      (mode === 'spreset-v1' && !supported) ||
      (mode === 'tavern-v1' && !hasScripts)
    ) {
      return
    }
    supersedeScriptOperation()
    const next: Record<string, unknown> = {
      ...props.config,
      script_runtime: mode,
    }
    if (mode && record(props.config.tool_text).enabled === true) {
      next.tool_text = { ...record(props.config.tool_text), enabled: false }
    }
    props.onChange(next)
  }
  return (
    <section
      className='mx-auto flex w-full max-w-3xl flex-col gap-4'
      aria-label={t('Script compatibility')}
    >
      <FieldSet disabled={props.disabled}>
        <FieldLegend id={`${id}-mode`} variant='label'>
          {t('How should this preset be processed?')}
        </FieldLegend>
        <FieldDescription>
          {t(
            'Choose how this preset handles messages and replies. Only run extras from authors you trust.'
          )}
        </FieldDescription>
        <p role='status' className='text-sm font-medium'>
          {!enabled && !tavernEnabled && t('Current draft: preset extras off')}
          {enabled && t('Current draft: use preset settings (SPreset)')}
          {tavernEnabled && t('Current draft: run bundled scripts')}
        </p>
        <RadioGroup
          aria-labelledby={`${id}-mode`}
          value={
            typeof props.config.script_runtime === 'string'
              ? props.config.script_runtime
              : ''
          }
          disabled={props.disabled}
          onValueChange={(value) => {
            if (
              value === '' ||
              value === 'spreset-v1' ||
              value === 'tavern-v1'
            ) {
              selectRuntime(value)
            }
          }}
        >
          <FieldLabel htmlFor={`${id}-off`} className='min-w-0'>
            <Field orientation='horizontal'>
              <RadioGroupItem
                id={`${id}-off`}
                value=''
                aria-labelledby={`${id}-off-label`}
                aria-describedby={`${id}-off-hint`}
              />
              <div className='min-w-0'>
                <span id={`${id}-off-label`}>
                  {t('Do not use preset extras')}
                </span>
                <FieldDescription id={`${id}-off-hint`}>
                  {t('The normal preset text still applies.')}
                </FieldDescription>
              </div>
            </Field>
          </FieldLabel>
          {(supported || enabled) && (
            <FieldLabel htmlFor={`${id}-spreset`} className='min-w-0'>
              <Field
                orientation='horizontal'
                data-disabled={props.disabled || !supported}
              >
                <RadioGroupItem
                  id={`${id}-spreset`}
                  value='spreset-v1'
                  disabled={props.disabled || !supported}
                  aria-labelledby={`${id}-spreset-label`}
                  aria-describedby={`${id}-spreset-hint`}
                />
                <div className='min-w-0'>
                  <span id={`${id}-spreset-label`}>
                    {t('Use preset settings (SPreset)')}
                  </span>
                  <FieldDescription id={`${id}-spreset-hint`}>
                    {t(
                      'Use SPreset settings to organize sent messages, attach preset tools and process replies. Choose this when the author requires SPreset.'
                    )}
                  </FieldDescription>
                </div>
              </Field>
            </FieldLabel>
          )}
          {(scripts.length > 0 || tavernEnabled) && (
            <FieldLabel htmlFor={`${id}-tavern`} className='min-w-0'>
              <Field
                orientation='horizontal'
                data-disabled={props.disabled || !hasScripts}
              >
                <RadioGroupItem
                  id={`${id}-tavern`}
                  value='tavern-v1'
                  disabled={props.disabled || !hasScripts}
                  aria-labelledby={`${id}-tavern-label`}
                  aria-describedby={`${id}-tavern-hint`}
                />
                <div className='min-w-0'>
                  <span id={`${id}-tavern-label`}>
                    {t('Run bundled scripts')}
                  </span>
                  <FieldDescription id={`${id}-tavern-hint`}>
                    {t(
                      'Run the scripts below that the author enabled. Choose this when the author asks you to enable Tavern Helper scripts.'
                    )}
                  </FieldDescription>
                </div>
              </Field>
            </FieldLabel>
          )}
        </RadioGroup>
        {supported && scripts.length > 0 && (
          <p className='text-muted-foreground text-sm'>
            {t(
              'Both options were found. Only one can run at a time; follow the preset author’s instructions.'
            )}
          </p>
        )}
        {!supported && scripts.length === 0 && !enabled && !tavernEnabled && (
          <p className='text-muted-foreground text-sm'>
            {t(
              'No supported preset extras were found. The normal preset text can still be used.'
            )}
          </p>
        )}
        {((enabled && !supported) || (tavernEnabled && !hasScripts)) && (
          <Alert>
            <AlertTitle>{t('The selected option is unavailable')}</AlertTitle>
            <AlertDescription>
              {enabled
                ? t('Choose Off or import a preset with SPreset settings.')
                : t(
                    'No author-enabled scripts are available. Choose Off or import a preset with enabled scripts.'
                  )}
            </AlertDescription>
          </Alert>
        )}
        <p className='text-muted-foreground text-sm'>
          {t(
            'Apply these changes, then save the channel for them to take effect.'
          )}
        </p>
      </FieldSet>
      <p className='text-muted-foreground text-sm'>
        {t(
          'Some Tavern functions are unavailable. See advanced details for limits.'
        )}
      </p>
      {scripts.length > 0 && (
        <section
          className='min-w-0 space-y-3 border-t pt-4'
          aria-label={t('Bundled script files')}
        >
          <h5 className='text-sm font-medium'>{t('Bundled script files')}</h5>
          <p className='text-muted-foreground text-sm'>
            {t(
              'Checking and downloading files does not enable scripts or change your choice above.'
            )}
          </p>
          <ul aria-label={t('Preset scripts')} className='min-w-0 space-y-2'>
            {scripts.map((script) => (
              <li
                key={`${script.id}-${script.name}`}
                className='flex min-w-0 flex-wrap items-center justify-between gap-2 rounded-lg border px-3 py-2'
              >
                <span className='min-w-0 text-sm break-words'>
                  {script.name || t('Unnamed script')}
                </span>
                <Badge
                  variant={
                    script.status === 'failed' ? 'destructive' : 'secondary'
                  }
                >
                  {script.status === 'prepared' && t('Files checked')}
                  {script.status === 'disabled' && t('Disabled by author')}
                  {script.status === 'manual' && t('Not checked')}
                  {script.status === 'failed' && t('Check failed')}
                </Badge>
                {script.issues.map((issue) => (
                  <p
                    key={`${issue.code}-${issue.module || ''}`}
                    className='text-muted-foreground w-full text-xs break-words'
                  >
                    {scriptPreparationIssueMessage(issue.code, t)}
                  </p>
                ))}
              </li>
            ))}
          </ul>
          <p className='text-muted-foreground text-sm'>
            {t(
              'Passing the file check does not guarantee every script function works.'
            )}
          </p>
          <Button
            type='button'
            variant='outline'
            disabled={props.disabled || preparing || !hasScripts}
            onClick={async () => {
              const { revision, sequence } = supersedeScriptOperation()
              const controller = new AbortController()
              preparationAbort.current = controller
              setPreparing(true)
              try {
                const result = await preparePresetScripts(
                  props.config,
                  controller.signal
                )
                if (!revision.active || sequence !== revision.sequence) return
                props.onChange(applyScriptPreparation(props.config, result))
              } catch (error) {
                if (revision.active && sequence === revision.sequence) {
                  handleServerError(error)
                }
              } finally {
                if (revision.active && sequence === revision.sequence) {
                  setPreparing(false)
                }
              }
            }}
          >
            {preparing
              ? t('Checking and downloading files…')
              : t('Check and download files')}
          </Button>
        </section>
      )}
      <Collapsible className='min-w-0 space-y-2'>
        <CollapsibleTrigger
          render={
            <Button type='button' variant='ghost' disabled={props.disabled} />
          }
        >
          {t('Advanced details and manual files')}
        </CollapsibleTrigger>
        <CollapsibleContent className='space-y-3'>
          <p className='text-muted-foreground text-sm'>
            {t(
              'Import a JSON object mapping exact module names or URLs to JavaScript source. Imports use these saved snapshots only, never the network.'
            )}
          </p>
          <input
            ref={bundleInput}
            type='file'
            accept='.json,application/json'
            className='hidden'
            aria-label={t('Import module bundle')}
            disabled={props.disabled}
            onChange={async (event) => {
              const file = event.currentTarget.files?.[0]
              event.currentTarget.value = ''
              if (!file) return
              const { revision, sequence } = supersedeScriptOperation()
              try {
                if (file.size > 2 * 1024 * 1024) throw new Error('size')
                const value: unknown = JSON.parse(await file.text())
                if (!revision.active || sequence !== revision.sequence) return
                const entries = Object.entries(record(value))
                if (
                  !value ||
                  typeof value !== 'object' ||
                  Array.isArray(value) ||
                  entries.length > 64
                ) {
                  throw new Error('shape')
                }
                const encoder = new TextEncoder()
                let bytes = 0
                for (const [name, source] of entries) {
                  if (
                    !name ||
                    name.startsWith('preset:') ||
                    typeof source !== 'string'
                  ) {
                    throw new Error('entry')
                  }
                  const length = encoder.encode(source).length
                  if (length > 128 * 1024) throw new Error('source')
                  bytes += length
                }
                if (bytes > 1024 * 1024) throw new Error('total')
                props.onChange({ ...props.config, script_modules: value })
                setBundleError(false)
              } catch {
                if (revision.active && sequence === revision.sequence) {
                  setBundleError(true)
                }
              }
            }}
          />
          <div className='flex flex-wrap gap-2'>
            <Button
              type='button'
              variant='outline'
              disabled={props.disabled}
              onClick={() => bundleInput.current?.click()}
            >
              {t('Import module bundle')}
            </Button>
            <Button
              type='button'
              variant='ghost'
              disabled={
                props.disabled ||
                Object.keys(record(props.config.script_modules)).length === 0
              }
              onClick={() => {
                supersedeScriptOperation()
                props.onChange({ ...props.config, script_modules: {} })
              }}
            >
              {t('Clear')}
            </Button>
          </div>
          {Object.keys(record(props.config.script_modules)).map((name) => (
            <p
              key={name}
              className='text-muted-foreground font-mono text-xs break-all'
            >
              {name}
            </p>
          ))}
          {bundleError && (
            <p role='alert' className='text-destructive text-sm'>
              {t(
                'Invalid module bundle. Use at most 64 string entries, 128 KiB per module and 1 MiB of total source.'
              )}
            </p>
          )}
          <p className='text-muted-foreground text-sm'>
            {t(
              'Variables and listeners belong to this request only. Supports one text response choice; browser rendering and persistent chat storage are unavailable.'
            )}
          </p>
          <p className='text-muted-foreground text-sm'>
            {t(
              'Await custom events sequentially. Concurrent or nested event dispatch is not supported.'
            )}
          </p>
          {(supported || enabled) && (
            <div className='space-y-3 rounded-lg border p-4 text-sm'>
              <h5 className='font-medium'>
                SPreset · {t('Supported data pipeline')}
              </h5>
              <p className='text-muted-foreground text-sm leading-relaxed'>
                {t(
                  'ChatSquash text merging, active ToolBindings, FinalRequestProcessing, tool-call formatting and final OutputPreprocessing run before existing response regex rules.'
                )}
              </p>
            </div>
          )}
          <p className='text-muted-foreground text-sm leading-relaxed'>
            {t(
              'Each request is isolated. Script time and memory are limited. Streamed replies are buffered; script failures do not trigger another generation.'
            )}
          </p>
          <Alert>
            <AlertTitle>{t('Compatibility boundaries')}</AlertTitle>
            <AlertDescription className='space-y-2'>
              <p>
                {t(
                  'Dependencies are downloaded only during preparation, never during script execution. Browser pages, persistent state and tool execution are unavailable. This is not full Tavern Helper emulation.'
                )}
              </p>
              <p>
                {t(
                  'Only Chat Completions is supported. Unsupported options are reported in errors or the execution trace. Native tool-text transport is disabled when this mode is enabled.'
                )}
              </p>
            </AlertDescription>
          </Alert>
        </CollapsibleContent>
      </Collapsible>
    </section>
  )
}
