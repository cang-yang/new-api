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
import { useTranslation } from 'react-i18next'

import { StatusBadge, type StatusVariant } from '@/components/status-badge'

import { ReadingViewport, TextBlock } from './reader'

interface DiagnosticStage {
  key: string
  stage: string
  status: string
  summary?: string
  details?: unknown
}

const stageLabels: Record<string, string> = {
  preflight: 'Preflight',
  worldbook: 'Worldbook',
  preset: 'Preset',
  tool_resolution: 'Reply source',
  plan: 'Request plan',
  response_validation: 'Response validation',
  response_restore: 'Response restore',
  output_processing: 'Output processing',
  regex: 'Receive regex',
  response_started: 'Client writeback',
  writeback: 'Client writeback',
  cross_channel_retry: 'Cross-channel retry',
}

const statusVariants: Record<string, StatusVariant> = {
  completed: 'green',
  frozen: 'blue',
  skipped: 'grey',
  fallback: 'yellow',
  failed: 'red',
}

const stageSummaryKeys: Record<string, Record<string, string>> = {
  preflight: {
    completed: 'Request contract classified and frozen',
  },
  worldbook: {
    completed: 'Worldbook matching and insertion positions were calculated',
    skipped: 'Worldbook is not active for this request',
  },
  preset: {
    completed: 'The preset was assembled before the upstream request',
    skipped: 'No preset was selected for this request',
  },
  tool_resolution: {
    completed: 'Reply sources were validated against client requirements',
    fallback: 'Reply source settings fell back safely',
    skipped: 'No internal reply source is active',
  },
  plan: {
    frozen: 'The request plan was frozen after tool resolution',
  },
  response_validation: {
    completed: 'The upstream response passed the frozen request plan',
    failed: 'The upstream response did not match the frozen request plan',
  },
  response_restore: {
    completed: 'The response was restored according to the request plan',
    fallback: 'The raw response was preserved by the safe fallback policy',
    failed: 'The response could not be restored safely',
  },
  output_processing: {
    completed: 'Preset output processing completed',
    failed: 'Preset output processing failed',
  },
  regex: {
    completed: 'Receive-side regex processing completed',
    fallback: 'The receive-side regex failure policy was applied',
    skipped: 'No receive-side regex rule is active',
    failed: 'Receive-side regex processing failed',
  },
  response_started: {
    completed: 'The final client response was written',
    failed: 'Client response writing failed',
  },
  writeback: {
    completed: 'The final client response was written',
    failed: 'The request ended before a successful client response',
  },
  cross_channel_retry: {
    failed: 'Cross-channel retry was rejected because the request plan changed',
  },
}

function diagnosticSummary(stage: DiagnosticStage, t: (key: string) => string) {
  const key = stageSummaryKeys[stage.stage]?.[stage.status]
  if (key) return t(key)
  if (stage.status === 'failed') return t('Processing stopped at this step')
  if (stage.status === 'skipped') return t('This step was not run')
  if (stage.status === 'fallback') return t('A fallback was used at this step')
  return t('See the recorded details for this step')
}

function asRecord(value: unknown): Record<string, unknown> | undefined {
  return value !== null && typeof value === 'object' && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : undefined
}

function diagnosticStages(value: unknown): DiagnosticStage[] {
  const record = asRecord(value)
  if (!record || !Array.isArray(record.stages)) return []
  const occurrences = new Map<string, number>()
  return record.stages.flatMap((item) => {
    const stage = asRecord(item)
    if (!stage || typeof stage.stage !== 'string') return []
    const occurrence = (occurrences.get(stage.stage) ?? 0) + 1
    occurrences.set(stage.stage, occurrence)
    return [
      {
        key: `${stage.stage}:${occurrence}`,
        stage: stage.stage,
        status: typeof stage.status === 'string' ? stage.status : 'unknown',
        summary: typeof stage.summary === 'string' ? stage.summary : undefined,
        details: stage.details,
      },
    ]
  })
}

export function AuditDiagnostics(props: { value: unknown }) {
  const { t } = useTranslation()
  const stages = diagnosticStages(props.value)
  const schemaVersion = asRecord(props.value)?.schema_version

  if (!stages.length) {
    return (
      <ReadingViewport>
        <p className='text-muted-foreground text-sm'>
          {t('No adaptation diagnostics were recorded for this attempt')}
        </p>
      </ReadingViewport>
    )
  }

  return (
    <ReadingViewport>
      <section data-slot='audit-diagnostics' className='space-y-5'>
        <header className='flex min-w-0 flex-wrap items-center gap-2 border-b pb-4'>
          <div className='min-w-0 flex-1'>
            <h3 className='text-sm font-medium'>
              {t('Adaptation diagnostics')}
            </h3>
            <p className='text-muted-foreground mt-1 text-xs'>
              {t(
                'Decision metadata only; prompt and response bodies stay in their captured sections.'
              )}
            </p>
          </div>
          <StatusBadge
            label={t('{{count}} stages', { count: stages.length })}
            size='sm'
            variant='neutral'
            copyable={false}
          />
          {typeof schemaVersion === 'number' && (
            <StatusBadge
              label={t('Schema version {{version}}', {
                version: schemaVersion,
              })}
              size='sm'
              variant='neutral'
              copyable={false}
            />
          )}
        </header>

        <ol className='space-y-3'>
          {stages.map((stage, index) => (
            <li
              key={stage.key}
              className='grid min-w-0 gap-3 border-b pb-4 last:border-b-0 last:pb-0 sm:grid-cols-[minmax(0,10rem)_minmax(0,1fr)]'
            >
              <div className='min-w-0'>
                <p className='text-muted-foreground text-xs tabular-nums'>
                  {t('Step {{number}}', { number: index + 1 })}
                </p>
                <p className='mt-1 text-sm font-medium [overflow-wrap:anywhere]'>
                  {t(stageLabels[stage.stage] ?? stage.stage)}
                </p>
              </div>
              <div className='min-w-0 space-y-2'>
                <StatusBadge
                  label={t(stage.status)}
                  size='sm'
                  variant={statusVariants[stage.status] ?? 'neutral'}
                  copyable={false}
                />
                <p className='text-muted-foreground text-xs leading-5 [overflow-wrap:anywhere]'>
                  {diagnosticSummary(stage, t)}
                </p>
                {(stage.details != null || stage.summary) && (
                  <details className='min-w-0'>
                    <summary className='text-muted-foreground cursor-pointer text-xs'>
                      {t('Decision details')}
                    </summary>
                    <div className='bg-muted/35 mt-2 min-w-0 rounded-md p-3'>
                      <TextBlock
                        code
                        compact
                        text={JSON.stringify(
                          { summary: stage.summary, details: stage.details },
                          null,
                          2
                        )}
                      />
                    </div>
                  </details>
                )}
              </div>
            </li>
          ))}
        </ol>
      </section>
    </ReadingViewport>
  )
}
