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
import { handleServerError } from '@/lib/handle-server-error'

import {
  inspectPresetCapability,
  type PresetCapabilityReport as CapabilityReport,
} from '../api'
import { presetRecord as record } from '../lib/preset-script-preparation'

function stringList(value: unknown): string[] {
  return Array.isArray(value)
    ? value.filter((item): item is string => typeof item === 'string')
    : []
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
  return t('Will not run')
}

const capabilityKeys: Record<string, string> = {
  'prompt assembly': 'Prompt assembly',
  'script runtime': 'Script runtime',
  'tool text transport': 'Tool text transport',
  'tool protocol coordination': 'Tool protocol coordination',
  'output preprocessing': 'Output preprocessing',
  'tool call formatter': 'Tool call formatter',
  passthrough: 'Passthrough',
}

const scriptRuntimeKeys: Record<string, string> = {
  'spreset-v1': 'Preset settings runtime',
  'tavern-v1': 'Bundled script runtime',
}

const noticeKeys: Record<string, string> = {
  browser_scripts_unavailable: 'Browser scripts are preserved but not executed.',
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
  const toolProtocol = record(props.config.tool_protocol)
  const toolProtocolEnabled = toolProtocol.enabled === true
  const auxiliary = stringList(toolProtocol.auxiliary)
  const action = stringList(toolProtocol.action)
  const content = stringList(toolProtocol.content)
  const clientFinal =
    typeof toolProtocol.client_final === 'string'
      ? toolProtocol.client_final
      : ''
  const presetFinal =
    typeof toolProtocol.preset_final === 'string'
      ? toolProtocol.preset_final
      : ''
  const responseTarget =
    typeof toolProtocol.response_target === 'string'
      ? toolProtocol.response_target
      : 'auto'
  let runtimeLabel = t('Do not use preset extras')
  if (runtime === 'spreset-v1') {
    runtimeLabel = t('Use preset settings (SPreset)')
  } else if (runtime === 'tavern-v1') {
    runtimeLabel = t('Run bundled scripts')
  }
  const coordination = toolProtocolEnabled
    ? t('Tool coordination on')
    : t('Tool coordination off')
  let finalMode = t('Passthrough')
  if (clientFinal) finalMode = t('Tool call')
  else if (responseTarget === 'json') finalMode = t('JSON')
  else if (responseTarget === 'text' || presetFinal) finalMode = t('Text')
  const roleCount = auxiliary.length + action.length + content.length
  return (
    <section
      aria-label={t('Adaptation overview')}
      className='bg-muted/20 flex flex-wrap items-center gap-x-4 gap-y-2 rounded-lg border p-3 text-sm'
    >
      <span className='font-medium'>{t('Adaptation overview')}</span>
      <Badge variant={runtime ? 'secondary' : 'outline'}>{runtimeLabel}</Badge>
      <Badge variant={toolProtocolEnabled ? 'secondary' : 'outline'}>
        {coordination}
      </Badge>
      <span className='text-muted-foreground'>
        {t('Final reply: {{mode}}', { mode: finalMode })}
      </span>
      <span className='text-muted-foreground'>
        {t('{{count}} coordinated tools', { count: roleCount })}
      </span>
    </section>
  )
}

export function PresetCapabilityReportSettings(props: {
  config: Record<string, unknown>
  disabled?: boolean
}) {
  const { t } = useTranslation()
  const [report, setReport] = useState<CapabilityReport | null>(null)
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
              setReport(next)
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
            <Alert variant='destructive'>
              <AlertTitle>{t('Needs attention')}</AlertTitle>
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
