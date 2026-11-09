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
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Field, FieldGroup, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { handleServerError } from '@/lib/handle-server-error'

import {
  inspectPresetCapability,
  inspectAdaptationScope,
  type AdaptationScopeReport,
  type PresetCapabilityReport as CapabilityReport,
} from '../api'
import { presetRecord as record } from '../lib/preset-script-preparation'
import { migrateReplyAdapter } from '../lib/reply-adapter'

const scenarioKeys: Record<string, string> = {
  text: 'Plain text chat',
  tools: 'Client tool call',
  tool_history: 'Continue after a tool result',
  json: 'JSON object reply',
  schema: 'JSON schema reply',
  stream: 'Streaming text reply',
  other_protocol: 'Other API endpoints',
}
const scopeReasonKeys: Record<string, string> = {
  internal_transport_with_client_tools:
    'Preset reply tools conflict with client tools. Use plain-chat scope or a preset without reply tools.',
  schema_transformation:
    'Schema replies cannot use reply conversion or selected JSON text-field processing.',
  text_transport_with_json:
    'This preset returns text, but the client requires a JSON object.',
  chat_completions_only:
    'This processing setup supports Chat Completions only.',
}

export function AdaptationScopeCheck(props: {
  scope?: string
  defaultModel?: string
  presetValue?: string
  worldbookValue?: string
  filterValue?: string
  disabled?: boolean
}) {
  const { t } = useTranslation()
  const [sampleModel, setSampleModel] = useState('')
  const model = sampleModel || props.defaultModel || ''
  const revision = JSON.stringify([
    props.scope,
    model,
    props.presetValue,
    props.worldbookValue,
    props.filterValue,
  ])
  const [snapshot, setSnapshot] = useState<{
    revision: string
    report: AdaptationScopeReport
  } | null>(null)
  const [checking, setChecking] = useState(false)
  const report = snapshot?.revision === revision ? snapshot.report : null
  return (
    <section
      aria-label={t('Check request types')}
      className='min-w-0 space-y-3 rounded-lg border p-3'
    >
      <div className='space-y-1'>
        <p className='text-sm font-medium'>{t('Check request types')}</p>
        <p className='text-muted-foreground text-xs'>
          {t(
            'Checks which requests use this configuration without running scripts or calling a model. Actual model replies still need a real request test.'
          )}
        </p>
      </div>
      <FieldGroup className='flex flex-wrap items-end gap-2 sm:flex-row'>
        <Field className='min-w-0 flex-1'>
          <FieldLabel htmlFor='adaptation-check-model'>
            {t('Model to check')}
          </FieldLabel>
          <Input
            id='adaptation-check-model'
            value={model}
            onChange={(event) => setSampleModel(event.target.value)}
            disabled={props.disabled || checking}
          />
        </Field>
        <Button
          type='button'
          variant='outline'
          size='sm'
          disabled={props.disabled || checking || !model.trim()}
          onClick={async () => {
            setChecking(true)
            try {
              const next = await inspectAdaptationScope({
                adaptation_scope: props.scope ?? 'all_supported',
                model: model.trim(),
                sillytavern_preset: props.presetValue?.trim()
                  ? JSON.parse(props.presetValue)
                  : undefined,
                worldbook: props.worldbookValue?.trim()
                  ? JSON.parse(props.worldbookValue)
                  : undefined,
                response_text_filter: props.filterValue?.trim()
                  ? JSON.parse(props.filterValue)
                  : undefined,
              })
              setSnapshot({ revision, report: next })
            } catch (error) {
              handleServerError(error)
            } finally {
              setChecking(false)
            }
          }}
        >
          {checking ? t('Checking…') : t('Check request types')}
        </Button>
      </FieldGroup>
      {report && (
        <div aria-live='polite' className='space-y-2'>
          <ul className='divide-y rounded-md border text-sm'>
            {report.scenarios.map((scenario) => {
              let status = t('Uses processing; model not verified')
              if (scenario.status === 'native') {
                status = t('No preset processing')
              }
              if (scenario.status === 'conflict') {
                status = t('Configuration conflict')
              }
              return (
                <li
                  key={scenario.kind}
                  className='flex flex-wrap items-start justify-between gap-2 p-2'
                >
                  <span>
                    {t(scenarioKeys[scenario.kind] ?? 'Other API endpoints')}
                  </span>
                  <Badge
                    variant={
                      scenario.status === 'conflict' ? 'destructive' : 'outline'
                    }
                  >
                    {status}
                  </Badge>
                  {scenario.status === 'conflict' && (
                    <p className='text-muted-foreground basis-full text-xs'>
                      {t(
                        scopeReasonKeys[scenario.reason] ??
                          'Check the preset and client output requirements.'
                      )}
                    </p>
                  )}
                </li>
              )
            })}
          </ul>
          <p className='text-muted-foreground text-xs'>
            {report.buffered
              ? t(
                  'Streaming replies wait for the complete upstream reply before local processing.'
                )
              : t(
                  'This check found no full-reply buffering for streaming text.'
                )}
          </p>
        </div>
      )}
    </section>
  )
}

function capabilityEntryVariant(
  status: string
): 'secondary' | 'destructive' | 'outline' {
  if (status === 'enabled') return 'secondary'
  if (status === 'missing') return 'destructive'
  return 'outline'
}

function capabilityEntryLabel(
  entry: { kind: string; status: string },
  t: (key: string) => string
): string {
  if (entry.kind === 'marker') return t('Slot marker')
  if (entry.status === 'enabled') return t('Runs')
  if (entry.status === 'missing') return t('Missing')
  if (entry.status === 'not_probed') return t('Not checked for this model')
  return t('Will not run')
}

const capabilityKeys: Record<string, string> = {
  'prompt assembly': 'Prompt assembly',
  'script runtime': 'Script runtime',
  'tool text transport': 'Tool text transport',
  'tool protocol coordination': 'Tool protocol coordination',
  'reply transport': 'Reply source',
  'output preprocessing': 'Output preprocessing',
  'tool call formatter': 'Tool call formatter',
  passthrough: 'Passthrough',
}

const scriptRuntimeKeys: Record<string, string> = {
  'spreset-v1': 'Preset settings runtime',
  'tavern-v1': 'Bundled script runtime',
}

const noticeKeys: Record<string, string> = {
  request_local_scripts:
    'Scripts can process this request and its reply. They cannot access other chats, browser pages or external services.',
  log_upload_skipped:
    'The preset’s external log upload is not run. This does not prevent normal message processing.',
  model_scope_skipped:
    'The sample model is outside this preset’s model scope. Scripts were not run.',
  browser_scripts_unavailable:
    'Browser scripts are preserved but not executed.',
  unsupported_macro_or_parameter:
    'Some preset macros or parameters have no equivalent mapping.',
  exact_mode_unresolved:
    'Exact mode cannot resolve every preset macro; the request may need a compatible mode.',
  worldbook_slot_unavailable:
    'The preset has no worldbook slot, so worldbook content will use the fallback position.',
  chat_history_slot_missing:
    'The preset has no chat-history slot; client history is appended for compatibility.',
  incoming_system_history:
    'Incoming system messages remain ordinary chat history.',
  prompt_not_applicable:
    'Some preset entries do not apply to normal generation.',
  receive_regex_compatibility:
    'Some receive-side regex rules are not compatible with the gateway.',
  preset_warning: 'The preset reported a compatibility warning.',
}

// PresetAdapterOverview is the first layer from the spec: it answers whether
// the adaptation actually runs and what the client will finally receive.
export function PresetAdapterOverview(props: {
  config: Record<string, unknown>
}) {
  const { t } = useTranslation()
  const runtime =
    typeof props.config.script_runtime === 'string'
      ? props.config.script_runtime
      : ''
  const migration = migrateReplyAdapter(props.config)
  const containers = record(props.config.reply_adapter).containers
  const count = Array.isArray(containers) ? containers.length : 0
  let runtimeLabel = t('Do not use preset extras')
  if (runtime === 'spreset-v1') {
    runtimeLabel = t('Use preset settings (SPreset)')
  } else if (runtime === 'tavern-v1') {
    runtimeLabel = t('Run bundled scripts')
  }
  return (
    <section
      aria-label={t('Adaptation overview')}
      className='bg-muted/20 flex flex-wrap items-center gap-x-4 gap-y-2 rounded-lg border p-3 text-sm'
    >
      <span className='font-medium'>{t('Adaptation overview')}</span>
      <Badge variant={runtime ? 'secondary' : 'outline'}>{runtimeLabel}</Badge>
      <Badge
        variant={
          migration.status === 'requires_review' ? 'destructive' : 'outline'
        }
      >
        {migration.status === 'requires_review'
          ? t('Old tool settings need review')
          : t('Client format stays protected')}
      </Badge>
      <span className='text-muted-foreground'>
        {count
          ? t('{{count}} reply sources', { count })
          : t('Text replies need no additional setup')}
      </span>
    </section>
  )
}

export function PresetCapabilityReportSettings(props: {
  config: Record<string, unknown>
  onChange?: (config: Record<string, unknown>) => void
  disabled?: boolean
}) {
  const { t } = useTranslation()
  const revision = JSON.stringify(props.config)
  const [snapshot, setReport] = useState<{
    revision: string
    report: CapabilityReport
  } | null>(null)
  const report = snapshot?.revision === revision ? snapshot.report : null
  const [checking, setChecking] = useState(false)
  return (
    <section
      aria-label={t('Capability report')}
      className='flex min-w-0 flex-col gap-3 rounded-lg border p-4'
    >
      <div className='flex flex-wrap items-center justify-between gap-2'>
        <div className='min-w-0'>
          <p className='text-sm font-medium'>{t('Capability report')}</p>
          <p className='text-muted-foreground text-xs'>
            {t(
              'Runs one bounded probe against the current draft. The result describes what the gateway can execute now, not a permanent property of the file.'
            )}
          </p>
        </div>
        <Button
          type='button'
          variant='outline'
          size='sm'
          disabled={props.disabled || checking}
          onClick={async () => {
            setChecking(true)
            try {
              const next = await inspectPresetCapability(props.config)
              setReport({ revision, report: next })
            } catch (error) {
              handleServerError(error)
            } finally {
              setChecking(false)
            }
          }}
        >
          {checking ? t('Checking…') : t('Check capabilities')}
        </Button>
      </div>
      {report && (
        <div className='flex min-w-0 flex-col gap-3'>
          {report.probe_status === 'completed' && (
            <p className='text-muted-foreground text-xs'>
              {t(
                'Local check completed. No model was called; test a real request before using this channel.'
              )}
            </p>
          )}
          {report.reply_sources?.length === 1 && (
            <div className='bg-muted/30 flex min-w-0 flex-wrap items-center justify-between gap-3 rounded-md border p-3'>
              <div className='min-w-0 flex-1 text-sm'>
                <p className='font-medium'>
                  {t('Detected reply source')}:{' '}
                  <span className='break-all'>
                    {report.reply_sources[0].name}
                  </span>
                </p>
                <p className='text-muted-foreground mt-1 text-xs'>
                  {t(
                    'The preset’s own script turns this tool’s arguments into your reply. History tools are not used as reply sources.'
                  )}
                </p>
              </div>
              {props.onChange && (
                <Button
                  type='button'
                  variant='outline'
                  size='sm'
                  disabled={props.disabled}
                  onClick={() => {
                    props.onChange?.({
                      ...props.config,
                      reply_adapter: {
                        version: 2,
                        containers: report.reply_sources,
                      },
                    })
                  }}
                >
                  {t('Use detected reply source')}
                </Button>
              )}
            </div>
          )}
          {!!report.history_tools?.length && (
            <p className='text-muted-foreground text-xs break-words'>
              {t(
                'Prepared history tools: {{tools}}. These only carry earlier messages; New API does not execute them.',
                { tools: report.history_tools.join(', ') }
              )}
            </p>
          )}
          <div className='flex flex-wrap items-center gap-2 text-sm'>
            <span className='font-medium'>{report.name}</span>
            {report.script_runtime ? (
              <Badge variant='secondary'>
                {t(
                  scriptRuntimeKeys[report.script_runtime] ?? 'Script runtime'
                )}
              </Badge>
            ) : (
              <Badge variant='outline'>{t('No script runtime')}</Badge>
            )}
            {report.capabilities.map((capability) => (
              <Badge key={capability} variant='outline'>
                {t(capabilityKeys[capability] ?? 'Other preset capability')}
              </Badge>
            ))}
          </div>
          {report.tool_names.length > 0 && (
            <p className='text-muted-foreground text-xs break-words'>
              {t('Observed preset tools: {{tools}}', {
                tools: report.tool_names.join(', '),
              })}
            </p>
          )}
          {report.warnings.length > 0 && (
            <Alert
              variant={
                report.notices?.every((notice) => notice.severity === 'info')
                  ? 'default'
                  : 'destructive'
              }
            >
              <AlertTitle>
                {report.notices?.every((notice) => notice.severity === 'info')
                  ? t('Processing limits')
                  : t('Needs attention')}
              </AlertTitle>
              <AlertDescription>
                <ul className='list-disc space-y-1 pl-4'>
                  {(report.notices?.length
                    ? report.notices
                    : report.warnings.map((warning) => ({
                        code: 'preset_warning',
                        severity: 'warning',
                        detail: warning,
                      }))
                  ).map((notice) => (
                    <li
                      key={`${notice.code}:${notice.detail ?? ''}`}
                      className='break-words'
                    >
                      {t(noticeKeys[notice.code] ?? 'preset_warning')}
                      {notice.detail && (
                        <details className='mt-1'>
                          <summary className='cursor-pointer text-xs opacity-80'>
                            {t('Technical details')}
                          </summary>
                          <code className='mt-1 block text-xs break-all'>
                            {notice.detail}
                          </code>
                        </details>
                      )}
                    </li>
                  ))}
                </ul>
              </AlertDescription>
            </Alert>
          )}
          <ul className='max-h-64 divide-y overflow-y-auto rounded-md border text-sm'>
            {report.entries.map((entry) => (
              <li
                key={entry.identifier}
                className='flex min-w-0 flex-wrap items-center justify-between gap-2 px-3 py-2'
              >
                <span className='min-w-0 break-words'>
                  {entry.name || entry.identifier}
                </span>
                <Badge variant={capabilityEntryVariant(entry.status)}>
                  {capabilityEntryLabel(entry, t)}
                </Badge>
              </li>
            ))}
          </ul>
        </div>
      )}
    </section>
  )
}
