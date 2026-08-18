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
import {
  AlertCircle,
  CheckCircle2,
  FlaskConical,
  GitCompare,
} from 'lucide-react'
import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Textarea } from '@/components/ui/textarea'
import { handleServerError } from '@/lib/handle-server-error'
import { cn } from '@/lib/utils'

import { simulateParamOverride } from '../../api'
import {
  buildJsonDiff,
  formatSimulationValue,
  getSimulationErrorResponse,
  normalizeSimulation,
  type ParamOverrideDiagnostic,
  type ParamOverrideSimulation,
} from '../../lib/param-override-simulator'

type ParamOverrideSimulatorDialogProps = {
  open: boolean
  paramOverride: string
  onOpenChange: (open: boolean) => void
}

const DEFAULT_SAMPLE = JSON.stringify(
  {
    model: 'gpt-4o-mini',
    messages: [{ role: 'user', content: 'Hello' }],
    stream: false,
  },
  null,
  2
)

const diagnosticLocation = (
  diagnostic: ParamOverrideDiagnostic,
  operationLabel: string
): string => {
  const parts: string[] = []
  if (diagnostic.operation_index !== undefined) {
    parts.push(operationLabel)
  }
  if (diagnostic.field) parts.push(diagnostic.field)
  return parts.join(' · ')
}

export function ParamOverrideSimulatorDialog(
  props: ParamOverrideSimulatorDialogProps
) {
  const { t } = useTranslation()
  const [sampleText, setSampleText] = useState(DEFAULT_SAMPLE)
  const [validationError, setValidationError] = useState('')
  const [diagnostics, setDiagnostics] = useState<ParamOverrideDiagnostic[]>([])
  const [result, setResult] = useState<ParamOverrideSimulation | null>(null)

  useEffect(() => {
    if (!props.open) return
    setValidationError('')
    setDiagnostics([])
    setResult(null)
  }, [props.open, props.paramOverride])

  const mutation = useMutation({
    mutationFn: simulateParamOverride,
    onSuccess: (response) => {
      const nextDiagnostics = response.data?.diagnostics ?? []
      setDiagnostics(nextDiagnostics)
      if (!response.success || !response.data) {
        setResult(null)
        setValidationError(
          response.message || t('Parameter override simulation failed')
        )
        return
      }
      setValidationError('')
      setResult(normalizeSimulation(response.data))
    },
    onError: (error: unknown) => {
      setResult(null)
      const response = getSimulationErrorResponse(error)
      if (response) {
        setDiagnostics(response.data?.diagnostics ?? [])
        setValidationError(
          response.message || t('Parameter override simulation failed')
        )
        return
      }
      handleServerError(error)
    },
  })

  const diff = useMemo(
    () => (result ? buildJsonDiff(result.before, result.after) : []),
    [result]
  )

  const handleRun = () => {
    setValidationError('')
    setDiagnostics([])
    setResult(null)
    let upstreamRequest: unknown
    let paramOverride: unknown
    try {
      upstreamRequest = JSON.parse(sampleText)
    } catch {
      setValidationError(t('Sample request must be a JSON object'))
      return
    }
    if (
      !upstreamRequest ||
      typeof upstreamRequest !== 'object' ||
      Array.isArray(upstreamRequest)
    ) {
      setValidationError(t('Sample request must be a JSON object'))
      return
    }
    try {
      paramOverride = props.paramOverride.trim()
        ? JSON.parse(props.paramOverride)
        : {}
    } catch {
      setValidationError(t('Parameter override must be valid JSON format'))
      return
    }
    if (
      !paramOverride ||
      typeof paramOverride !== 'object' ||
      Array.isArray(paramOverride)
    ) {
      setValidationError(t('Parameter override must be a valid JSON object'))
      return
    }
    mutation.mutate({
      upstream_request: upstreamRequest as Record<string, unknown>,
      param_override: paramOverride as Record<string, unknown>,
      context: {},
    })
  }

  return (
    <Dialog
      open={props.open}
      onOpenChange={props.onOpenChange}
      title={t('Parameter override simulator')}
      description={t(
        'Preview exactly how the current override changes a sample upstream request without sending it to a provider.'
      )}
      contentClassName='flex max-h-[92vh] flex-col sm:max-w-6xl'
      contentHeight='min(76vh, 780px)'
      bodyClassName='space-y-5'
      footer={
        <>
          <Button
            type='button'
            variant='outline'
            onClick={() => props.onOpenChange(false)}
          >
            {t('Close')}
          </Button>
          <Button
            type='button'
            onClick={handleRun}
            disabled={mutation.isPending}
          >
            <FlaskConical aria-hidden='true' />
            {mutation.isPending ? t('Simulating...') : t('Run simulation')}
          </Button>
        </>
      }
    >
      <section className='space-y-2'>
        <label className='text-sm font-medium' htmlFor='param-override-sample'>
          {t('Sample upstream request JSON')}
        </label>
        <Textarea
          id='param-override-sample'
          className='min-h-44 resize-y font-mono text-xs'
          spellCheck={false}
          value={sampleText}
          onChange={(event) => setSampleText(event.target.value)}
          aria-invalid={Boolean(validationError)}
        />
        <p className='text-muted-foreground text-xs'>
          {t(
            'The simulator runs locally on the server and never contacts the upstream provider. Sensitive result fields are redacted.'
          )}
        </p>
      </section>

      {validationError ? (
        <div
          className='border-destructive/40 bg-destructive/10 text-destructive flex items-start gap-2 rounded-md border p-3 text-sm'
          role='alert'
        >
          <AlertCircle className='mt-0.5 size-4 shrink-0' aria-hidden='true' />
          <span>{validationError}</span>
        </div>
      ) : null}

      {diagnostics.length > 0 ? (
        <section className='space-y-2' aria-label={t('Static diagnostics')}>
          <h3 className='text-sm font-semibold'>{t('Static diagnostics')}</h3>
          <div className='space-y-2'>
            {diagnostics.map((diagnostic) => (
              <div
                className='border-destructive/30 bg-destructive/5 rounded-md border p-3 text-sm'
                key={`${diagnostic.code}-${diagnostic.operation_index ?? 'root'}-${diagnostic.field ?? 'root'}-${diagnostic.message}`}
              >
                <div className='flex flex-wrap items-center gap-2'>
                  <Badge variant='destructive'>{diagnostic.severity}</Badge>
                  <code className='text-xs'>{diagnostic.code}</code>
                  {diagnosticLocation(
                    diagnostic,
                    t('Operation {{number}}', {
                      number: (diagnostic.operation_index ?? 0) + 1,
                    })
                  ) ? (
                    <span className='text-muted-foreground text-xs'>
                      {diagnosticLocation(
                        diagnostic,
                        t('Operation {{number}}', {
                          number: (diagnostic.operation_index ?? 0) + 1,
                        })
                      )}
                    </span>
                  ) : null}
                </div>
                <p className='mt-2'>{diagnostic.message}</p>
              </div>
            ))}
          </div>
        </section>
      ) : null}

      {result ? (
        <div className='space-y-5'>
          <section className='space-y-2'>
            <div className='flex flex-wrap items-center justify-between gap-2'>
              <h3 className='text-sm font-semibold'>{t('Operation trace')}</h3>
              <Badge variant='outline'>
                {t('{{count}} operations', { count: result.operations.length })}
              </Badge>
            </div>
            {result.operations.length === 0 ? (
              <p className='text-muted-foreground rounded-md border p-3 text-sm'>
                {t('No operation rules were evaluated.')}
              </p>
            ) : (
              <div className='grid gap-2 md:grid-cols-2'>
                {result.operations.map((operation) => {
                  const applied = operation.status === 'applied'
                  let outcome = t('Skipped')
                  if (applied && operation.changed) {
                    outcome = t('Applied · Changed')
                  } else if (applied) {
                    outcome = t('Applied · Unchanged')
                  }
                  let reason = operation.reason || ''
                  if (operation.reason === 'not_reached') {
                    reason = t('Not reached')
                  } else if (operation.reason === 'condition_false') {
                    reason = t('Condition did not match')
                  } else if (operation.reason === 'path_not_found') {
                    reason = t('Path not found')
                  } else if (operation.reason === 'would_return_error') {
                    reason = t('Would return an error')
                  }
                  return (
                    <div
                      className='flex items-start gap-3 rounded-md border p-3'
                      key={`${operation.index}-${operation.mode}`}
                    >
                      {applied ? (
                        <CheckCircle2
                          className='mt-0.5 size-4 shrink-0 text-emerald-600'
                          aria-hidden='true'
                        />
                      ) : (
                        <AlertCircle
                          className='text-muted-foreground mt-0.5 size-4 shrink-0'
                          aria-hidden='true'
                        />
                      )}
                      <div className='min-w-0 space-y-1'>
                        <div className='flex flex-wrap items-center gap-2'>
                          <Badge variant='secondary'>
                            #{operation.index + 1}
                          </Badge>
                          <code className='text-xs'>{operation.mode}</code>
                          <span className='text-xs font-medium'>{outcome}</span>
                        </div>
                        <p className='text-muted-foreground break-all text-xs'>
                          {operation.path ||
                            operation.from ||
                            operation.to ||
                            '—'}
                          {reason ? ` · ${reason}` : ''}
                        </p>
                      </div>
                    </div>
                  )
                })}
              </div>
            )}
          </section>

          <section className='space-y-2'>
            <div className='flex items-center gap-2'>
              <GitCompare className='size-4' aria-hidden='true' />
              <h3 className='text-sm font-semibold'>{t('JSON diff')}</h3>
              <Badge variant='outline'>{diff.length}</Badge>
            </div>
            {diff.length === 0 ? (
              <p className='text-muted-foreground rounded-md border p-3 text-sm'>
                {t('The override did not change the request body.')}
              </p>
            ) : (
              <div className='overflow-x-auto rounded-md border'>
                <table className='w-full min-w-[640px] text-left text-xs'>
                  <thead className='bg-muted/60'>
                    <tr>
                      <th className='px-3 py-2 font-medium'>{t('Path')}</th>
                      <th className='px-3 py-2 font-medium'>{t('Change')}</th>
                      <th className='px-3 py-2 font-medium'>{t('Before')}</th>
                      <th className='px-3 py-2 font-medium'>{t('After')}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {diff.map((entry) => {
                      let changeLabel = t('Removed')
                      if (entry.kind === 'changed') {
                        changeLabel = t('Changed')
                      } else if (entry.kind === 'added') {
                        changeLabel = t('Added')
                      }
                      return (
                        <tr
                          className='border-t align-top'
                          key={`${entry.kind}-${entry.path}`}
                        >
                          <td className='px-3 py-2 font-mono'>{entry.path}</td>
                          <td className='px-3 py-2'>
                            <Badge
                              variant='outline'
                              className={cn(
                                entry.kind === 'added' &&
                                  'border-emerald-500/40 text-emerald-700 dark:text-emerald-300',
                                entry.kind === 'removed' &&
                                  'border-red-500/40 text-red-700 dark:text-red-300'
                              )}
                            >
                              {changeLabel}
                            </Badge>
                          </td>
                          <td className='max-w-64 px-3 py-2'>
                            <pre className='whitespace-pre-wrap break-all font-mono'>
                              {formatSimulationValue(entry.before)}
                            </pre>
                          </td>
                          <td className='max-w-64 px-3 py-2'>
                            <pre className='whitespace-pre-wrap break-all font-mono'>
                              {formatSimulationValue(entry.after)}
                            </pre>
                          </td>
                        </tr>
                      )
                    })}
                  </tbody>
                </table>
              </div>
            )}
          </section>

          <section className='grid gap-3 lg:grid-cols-2'>
            {[
              { title: t('Before'), value: result.before },
              { title: t('After'), value: result.after },
            ].map((panel) => (
              <div className='min-w-0 space-y-2' key={panel.title}>
                <h3 className='text-sm font-semibold'>{panel.title}</h3>
                <pre className='bg-muted/40 max-h-80 overflow-auto rounded-md border p-3 font-mono text-xs whitespace-pre-wrap break-all'>
                  {JSON.stringify(panel.value, null, 2)}
                </pre>
              </div>
            ))}
          </section>

          {Object.keys(result.headers).length > 0 ? (
            <section className='space-y-2'>
              <h3 className='text-sm font-semibold'>{t('Header changes')}</h3>
              <pre className='bg-muted/40 max-h-56 overflow-auto rounded-md border p-3 font-mono text-xs whitespace-pre-wrap break-all'>
                {JSON.stringify(result.headers, null, 2)}
              </pre>
            </section>
          ) : null}
        </div>
      ) : null}
    </Dialog>
  )
}
