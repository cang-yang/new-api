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
import { useEffect, useId, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import {
  Field,
  FieldDescription,
  FieldLabel,
  FieldSet,
} from '@/components/ui/field'
import { Switch } from '@/components/ui/switch'

type Props = {
  config: Record<string, unknown>
  onChange: (config: Record<string, unknown>) => void
  disabled?: boolean
}

function record(value: unknown): Record<string, unknown> {
  return value !== null && typeof value === 'object' && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : {}
}

function hasEnabledScript(value: unknown, depth = 0): boolean {
  if (!Array.isArray(value) || depth > 8) return false
  return value.some((entry: unknown) => {
    const script = record(entry)
    if (script.enabled !== true) return false
    if (script.type === 'script') return true
    return (
      script.type === 'folder' && hasEnabledScript(script.scripts, depth + 1)
    )
  })
}

export function PresetScriptSettings(props: Props) {
  const { t } = useTranslation()
  const id = useId()
  const extensions = record(record(props.config.preset).extensions)
  const supported = Object.keys(record(extensions.SPreset)).length > 0
  const enabled = props.config.script_runtime === 'spreset-v1'
  const tavernEnabled = props.config.script_runtime === 'tavern-v1'
  const hasScripts = hasEnabledScript(record(extensions.tavern_helper).scripts)
  const bundleInput = useRef<HTMLInputElement>(null)
  const importRevision = useRef({ active: true, sequence: 0 })
  useEffect(() => {
    const revision = { active: true, sequence: 0 }
    importRevision.current = revision
    return () => {
      revision.active = false
    }
  }, [props.config, props.disabled])
  const [bundleError, setBundleError] = useState(false)
  const selectRuntime = (mode: string, checked: boolean) => {
    const next: Record<string, unknown> = {
      ...props.config,
      script_runtime: checked ? mode : '',
    }
    if (checked && record(props.config.tool_text).enabled === true) {
      next.tool_text = { ...record(props.config.tool_text), enabled: false }
    }
    props.onChange(next)
  }
  return (
    <section
      className='mx-auto flex w-full max-w-3xl flex-col gap-4'
      aria-label={t('Script compatibility')}
    >
      <div className='space-y-1'>
        <h4 className='text-sm font-semibold'>{t('Script compatibility')}</h4>
        <p className='text-muted-foreground text-sm'>
          {t(
            'Run supported preset data hooks inside New API. No separate service is required.'
          )}
        </p>
      </div>
      <FieldSet className='rounded-lg border p-4' disabled={props.disabled}>
        <Field orientation='horizontal'>
          <div className='min-w-0 flex-1'>
            <FieldLabel htmlFor={`${id}-tavern`}>
              {t('Enable Tavern Helper data scripts')}
            </FieldLabel>
            <FieldDescription>
              {t(
                'Execute enabled preset scripts with shared message, variable and event interfaces. No preset-specific rewriting.'
              )}
            </FieldDescription>
          </div>
          <Switch
            id={`${id}-tavern`}
            aria-label={t('Enable Tavern Helper data scripts')}
            checked={tavernEnabled}
            disabled={props.disabled || (!hasScripts && !tavernEnabled)}
            onCheckedChange={(checked) => selectRuntime('tavern-v1', checked)}
          />
        </Field>
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
        <div className='min-w-0 space-y-2 border-t pt-3'>
          <p className='text-sm font-medium'>
            {t('Pinned script dependencies')}
          </p>
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
              const revision = importRevision.current
              const sequence = ++revision.sequence
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
                if (revision.active && sequence === revision.sequence) setBundleError(true)
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
              onClick={() =>
                props.onChange({ ...props.config, script_modules: {} })
              }
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
        </div>
      </FieldSet>
      <FieldSet className='rounded-lg border p-4' disabled={props.disabled}>
        <Field orientation='horizontal'>
          <div className='min-w-0 flex-1'>
            <FieldLabel htmlFor={id}>
              {t('Enable SPreset data hooks')}
            </FieldLabel>
            <FieldDescription>
              {supported
                ? t(
                    'SPreset configuration detected. Only enable scripts from a trusted preset.'
                  )
                : t(
                    'No supported SPreset configuration was found. Browser loaders alone are not supported.'
                  )}
            </FieldDescription>
          </div>
          <Switch
            id={id}
            aria-label={t('Enable SPreset data hooks')}
            checked={enabled}
            disabled={props.disabled || (!supported && !enabled)}
            onCheckedChange={(checked) => selectRuntime('spreset-v1', checked)}
          />
        </Field>
      </FieldSet>
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
              'Browser pages, remote loaders, persistent state and tool actions are not executed. This is not full Tavern Helper emulation.'
            )}
          </p>
          <p>
            {t(
              'Only Chat Completions is supported. Unsupported options are reported in errors or the execution trace. Native tool-text transport is disabled when this mode is enabled.'
            )}
          </p>
        </AlertDescription>
      </Alert>
    </section>
  )
}
